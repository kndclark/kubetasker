//go:build e2e
// +build e2e

/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/kndclark/kubetasker/test/utils"
)

var (
	// Optional Environment Variables:
	// - CERT_MANAGER_INSTALL_SKIP=true: Skips CertManager installation during test setup.
	// These variables are useful if CertManager is already installed, avoiding
	// re-installation and conflicts.
	skipCertManagerInstall = os.Getenv("CERT_MANAGER_INSTALL_SKIP") == "true"
	// isCertManagerAlreadyInstalled will be set true when CertManager CRDs be found on the cluster
	isCertManagerAlreadyInstalled = false

	kindClusterName = "kubetasker-test-e2e"
	// projectImage is the name of the image which will be build and loaded
	// with the code source changes to be tested.
	projectImage = "ktasker.com/kubetasker:v0.0.1"
	// frontendImage is the name of the frontend API service image.
	frontendImage  = "ktasker.com/kubetasker-frontend:v0.0.1"
	projectRootDir string
)

// TestE2E runs the end-to-end (e2e) test suite for the project. These tests execute in an isolated,
// temporary environment to validate project changes with the purpose of being used in CI jobs.
// The default setup requires Kind, builds/loads the Manager Docker image locally, and installs
// CertManager.
func TestE2E(t *testing.T) {
	RegisterFailHandler(Fail)
	_, _ = fmt.Fprintf(GinkgoWriter, "Starting kubetasker integration test suite\n")
	RunSpecs(t, "e2e suite")
}

var _ = BeforeSuite(func() {
	var err error
	projectRootDir, err = utils.GetProjectDir()
	Expect(err).NotTo(HaveOccurred(), "Failed to get project root dir")

	By("building the manager(Operator) image")
	cmd := exec.Command("make", "docker-build", fmt.Sprintf("IMG=%s", projectImage))
	_, err = utils.Run(cmd)
	ExpectWithOffset(1, err).NotTo(HaveOccurred(), "Failed to build the manager(Operator) image")

	By("building the frontend API image")
	frontendDir := filepath.Join(projectRootDir, "kubetasker-frontend")
	dockerfilePath := filepath.Join(frontendDir, "Dockerfile")
	cmd = exec.Command("docker", "build", "-t", frontendImage, "-f", dockerfilePath, frontendDir)
	_, err = utils.Run(cmd)
	ExpectWithOffset(1, err).NotTo(HaveOccurred(), "Failed to build the frontend API image")

	// TODO(user): If you want to change the e2e test vendor from Kind, ensure the image is
	// built and available before running the tests. Also, remove the following block.
	By("loading the manager(Operator) image on Kind")
	err = utils.LoadImageToKindClusterWithName(kindClusterName, projectImage)
	ExpectWithOffset(1, err).NotTo(HaveOccurred(), "Failed to load the manager(Operator) image into Kind")
	By("loading the frontend API image on Kind")
	err = utils.LoadImageToKindClusterWithName(kindClusterName, frontendImage)
	ExpectWithOffset(1, err).NotTo(HaveOccurred(), "Failed to load the frontend API image into Kind")

	By("updating helm dependencies for the umbrella chart")
	umbrellaChartPath := filepath.Join(projectRootDir, "kubetasker")
	cmd = exec.Command("helm", "dependency", "update", umbrellaChartPath)
	_, err = utils.Run(cmd)
	ExpectWithOffset(1, err).NotTo(HaveOccurred(), "Failed to update helm dependencies")

	// The tests-e2e are intended to run on a temporary cluster that is created and destroyed for testing.
	// To prevent errors when tests run in environments with CertManager already installed,
	// we check for its presence before execution.
	// Setup CertManager before the suite if not skipped and if not already installed
	if !skipCertManagerInstall {
		By("checking if cert manager is installed already")
		isCertManagerAlreadyInstalled = utils.IsCertManagerCRDsInstalled()
		if !isCertManagerAlreadyInstalled {
			_, _ = fmt.Fprintf(GinkgoWriter, "Installing CertManager...\n")
			Expect(utils.InstallCertManager()).To(Succeed(), "Failed to install CertManager")
		} else {
			_, _ = fmt.Fprintf(GinkgoWriter, "WARNING: CertManager is already installed. Skipping installation...\n")
		}
	}
})

var _ = AfterSuite(func() {
	// Teardown CertManager after the suite if not skipped and if it was not already installed
	if !skipCertManagerInstall && !isCertManagerAlreadyInstalled {
		_, _ = fmt.Fprintf(GinkgoWriter, "Uninstalling CertManager...\n")
		utils.UninstallCertManager()
	}
})

// logDebugInfoOnFailure checks if the current Ginkgo spec has failed. If it has, it captures
// a comprehensive snapshot of the test namespace's state, including pod logs, descriptions,
// events, and webhook configurations. This is invaluable for debugging CI/CD failures.
func logDebugInfoOnFailure(namespace string) {
	if !CurrentSpecReport().Failed() {
		return
	}

	// logCommand is a helper to execute a command and print its output to the Ginkgo writer.
	logCommand := func(description string, cmd *exec.Cmd) {
		By(description)
		output, err := utils.Run(cmd)
		if err != nil {
			_, _ = fmt.Fprintf(GinkgoWriter, "Failed to run command for '%s': %v\n", description, err)
			return
		}
		_, _ = fmt.Fprintf(GinkgoWriter, "%s:\n%s\n\n", description, output)
	}

	// --- Capture Namespace State ---
	logCommand("Fetching all pods in namespace",
		exec.Command("kubectl", "get", "pods", "-n", namespace, "-o", "wide"))

	logCommand("Fetching all events in namespace",
		exec.Command("kubectl", "get", "events", "-n", namespace, "--sort-by=.lastTimestamp"))

	// --- Capture Controller Details ---
	logCommand("Fetching controller-manager pod logs",
		exec.Command("kubectl", "logs", "--selector=control-plane=controller-manager", "-n", namespace, "--tail=100"))

	logCommand("Fetching controller-manager pod description",
		exec.Command("kubectl", "describe", "pod", "--selector=control-plane=controller-manager", "-n", namespace))

	// --- Capture Frontend Details ---
	logCommand("Fetching frontend pod logs",
		exec.Command("kubectl", "logs", "--selector=app.kubernetes.io/name=kubetasker-frontend", "-n", namespace, "--tail=100"))

	logCommand("Fetching frontend pod description",
		exec.Command("kubectl", "describe", "pod", "--selector=app.kubernetes.io/name=kubetasker-frontend", "-n", namespace))

	// --- Capture Webhook Configurations ---
	logCommand("Fetching MutatingWebhookConfiguration YAML",
		exec.Command("kubectl", "get", "mutatingwebhookconfigurations.admissionregistration.k8s.io",
			"-l", "app.kubernetes.io/part-of=kubetasker", "-o", "yaml"))

	logCommand("Fetching ValidatingWebhookConfiguration YAML",
		exec.Command("kubectl", "get", "validatingwebhookconfigurations.admissionregistration.k8s.io",
			"-l", "app.kubernetes.io/part-of=kubetasker", "-o", "yaml"))
}

// deletes the cluster-scoped webhook configurations (important step to prevent test pollution)
func cleanupWebhookConfigurations(controllerFullName string) {
	By(fmt.Sprintf("cleaning up webhook configurations for %s", controllerFullName))
	mutatingWebhookName := controllerFullName + "-mutating-webhook-configuration"
	validatingWebhookName := controllerFullName + "-validating-webhook-configuration"
	_, _ = utils.Run(exec.Command("kubectl", "delete", "mutatingwebhookconfigurations.admissionregistration.k8s.io", mutatingWebhookName, "--ignore-not-found"))
	_, _ = utils.Run(exec.Command("kubectl", "delete", "validatingwebhookconfigurations.admissionregistration.k8s.io", validatingWebhookName, "--ignore-not-found"))
}
