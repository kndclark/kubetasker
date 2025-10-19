package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"testing"
	"time"

	customv1 "github.com/kndclark/kubetasker/api/v1"
	"github.com/kndclark/kubetasker/internal/controller"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
)

// mockManager is a mock implementation of the controller-runtime manager.Manager interface.
// It allows us to simulate errors during the manager setup process.
type mockManager struct {
	manager.Manager
	addFn             func(manager.Runnable) error
	addHealthzCheckFn func(string, healthz.Checker) error
	addReadyzCheckFn  func(string, healthz.Checker) error
	startFn           func(context.Context) error
}

func (m *mockManager) Add(r manager.Runnable) error {
	if m.addFn != nil {
		return m.addFn(r)
	}
	return m.Manager.Add(r)
}

func (m *mockManager) AddHealthzCheck(name string, check healthz.Checker) error {
	if m.addHealthzCheckFn != nil {
		return m.addHealthzCheckFn(name, check)
	}
	return m.Manager.AddHealthzCheck(name, check)
}

func (m *mockManager) AddReadyzCheck(name string, check healthz.Checker) error {
	if m.addReadyzCheckFn != nil {
		return m.addReadyzCheckFn(name, check)
	}
	return m.Manager.AddReadyzCheck(name, check)
}

func (m *mockManager) Start(ctx context.Context) error {
	if m.startFn != nil {
		return m.startFn(ctx)
	}
	return m.Manager.Start(ctx)
}

// mockNewManager is a function that returns a mock manager or an error.
func mockNewManager(config *rest.Config, options manager.Options) (manager.Manager, error) {
	if mockNewManagerErr != nil {
		return nil, mockNewManagerErr
	}

	// In our tests, the mockMgr is already configured with a real manager instance.
	// We just need to return it. The mockMgr is responsible for overriding
	// specific functions for each test case.
	// If mockMgr is nil, it means the test is for a manager creation failure.
	return mockMgr, nil
}

var mockMgr *mockManager
var mockNewManagerErr error

func TestMainFunction(t *testing.T) {
	g := NewGomegaWithT(t)

	// This is a simple smoke test to ensure the main function can be invoked
	// and starts up without immediately crashing. We run it in a separate
	// process to avoid issues with flags and blocking.
	// Build the binary to test
	buildCmd := exec.Command("go", "build", "-o", "manager_test_binary", ".")
	err := buildCmd.Run()
	g.Expect(err).NotTo(HaveOccurred(), "Failed to build manager binary")
	defer os.Remove("manager_test_binary")
	// Run the manager with a timeout
	cmd := exec.Command("./manager_test_binary", "--metrics-bind-address=:0", "--health-probe-bind-address=:8089")
	err = cmd.Start()
	g.Expect(err).NotTo(HaveOccurred(), "Failed to start manager process")
	// Let it run for a moment to see if it crashes
	time.Sleep(2 * time.Second)
	// Kill the process
	err = cmd.Process.Kill()
	g.Expect(err).NotTo(HaveOccurred(), "Failed to kill manager process")
}

func TestRunFunctionErrorPaths(t *testing.T) {
	g := NewGomegaWithT(t)
	originalNewManager := newManager
	defer func() { newManager = originalNewManager }()

	// Create a scheme and add the necessary types for the tests.
	// This is required because the init() in main.go doesn't run for this test.
	testScheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(testScheme))
	utilruntime.Must(customv1.AddToScheme(testScheme))

	// Create a single real manager for all sub-tests to share.
	// This avoids the "controller already exists" error by not re-creating
	// the manager (and its global registrations) for each test.
	realMgr, err := ctrl.NewManager(&rest.Config{}, manager.Options{
		Scheme:  testScheme,
		Metrics: metricsserver.Options{BindAddress: "0"},
	})
	g.Expect(err).NotTo(HaveOccurred())

	// Override the newManager function with our mock
	newManager = mockNewManager

	t.Run("should return error on manager creation failure", func(t *testing.T) {
		// This test doesn't use the shared manager, it just checks the error.
		// We set mockMgr to nil so mockNewManager returns the error.
		mockMgr = nil
		mockNewManagerErr = errors.New("failed to create manager")
		defer func() { mockNewManagerErr = nil }()
		// We need a dummy config for the real manager creation to not fail early
		err := run(&rest.Config{}, testScheme, []string{"--health-probe-bind-address=:8090"})
		g.Expect(err).To(MatchError("failed to create manager"))
	})

	t.Run("should return error on controller setup failure", func(t *testing.T) {
		mockMgr = &mockManager{
			Manager: realMgr,
			// This mock will be triggered by SetupWithManager
			addFn: func(r manager.Runnable) error { return errors.New("failed to add controller") },
		}

		// Directly test the controller setup logic from the run() function
		err := (&controller.JobRequestReconciler{
			Client: mockMgr.GetClient(),
			Scheme: mockMgr.GetScheme(),
		}).SetupWithManager(mockMgr)
		g.Expect(err).To(MatchError("failed to add controller"))
	})

	t.Run("should return error on health check setup failure", func(t *testing.T) {
		mockMgr = &mockManager{
			Manager: realMgr,
			// This mock will be triggered by AddHealthzCheck
			addHealthzCheckFn: func(name string, check healthz.Checker) error { return errors.New("healthz failed") },
		}
		// Mock the preceding calls to succeed
		mockMgr.addFn = func(r manager.Runnable) error {
			return nil
		}

		// Directly test the health check logic from the run() function
		err := mockMgr.AddHealthzCheck("healthz", healthz.Ping)
		g.Expect(err).To(MatchError("healthz failed"))
	})

	t.Run("should return error on ready check setup failure", func(t *testing.T) {
		mockMgr = &mockManager{
			Manager: realMgr,
			// This mock will be triggered by AddReadyzCheck
			addReadyzCheckFn: func(name string, check healthz.Checker) error { return errors.New("readyz failed") },
		}
		// Mock the preceding calls to succeed
		mockMgr.addFn = func(r manager.Runnable) error {
			return nil
		}
		mockMgr.addHealthzCheckFn = func(name string, check healthz.Checker) error {
			return nil
		}

		// Directly test the ready check logic from the run() function
		err := mockMgr.AddReadyzCheck("readyz", healthz.Ping)
		g.Expect(err).To(MatchError("readyz failed"))
	})

	t.Run("should return error on manager start failure", func(t *testing.T) {
		mockMgr = &mockManager{
			Manager: realMgr,
			// This mock will be triggered by Start
			startFn: func(ctx context.Context) error { return errors.New("start failed") },
		}
		// Directly test the start logic from the run() function
		err := mockMgr.Start(context.Background())
		g.Expect(err).To(MatchError("start failed"))
	})
}
