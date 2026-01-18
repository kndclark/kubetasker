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
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
	skipCertManagerInstall   = os.Getenv("CERT_MANAGER_INSTALL_SKIP") == "true"
	skipMetricsServerInstall = os.Getenv("METRICS_SERVER_INSTALL_SKIP") == "true"
	// isCertManagerAlreadyInstalled will be set true when CertManager CRDs be found on the cluster
	isCertManagerAlreadyInstalled = false

	kindClusterName = "kubetasker-test-e2e"
	// projectImage is the name of the image which will be build and loaded
	// with the code source changes to be tested.
	projectImage = "ktasker.com/kubetasker:v0.0.1"
	// frontendImage is the name of the frontend API service image.
	frontendImage  = "ktasker.com/kubetasker-frontend:v0.0.1"
	projectRootDir string
	chartsRoot     string
	testRoot       string
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
	chartsRoot = filepath.Join(projectRootDir, "helm")
	testRoot = filepath.Join(projectRootDir, "test")

	By("building the manager(Operator) image")
	cmd := exec.Command("make", "docker-build", fmt.Sprintf("IMG=%s", projectImage))
	_, err = utils.Run(cmd)
	ExpectWithOffset(1, err).NotTo(HaveOccurred(), "Failed to build the manager(Operator) image")

	By("building the frontend API image")
	frontendDir := filepath.Join(chartsRoot, "kubetasker-frontend")

	// Copy requirements.txt to frontend directory so it is available in the build context
	cmd = exec.Command("cp", filepath.Join(projectRootDir, "requirements.txt"), filepath.Join(frontendDir, "requirements.txt"))
	_, err = utils.Run(cmd)
	ExpectWithOffset(1, err).NotTo(HaveOccurred(), "Failed to copy requirements.txt to frontend dir")
	defer os.Remove(filepath.Join(frontendDir, "requirements.txt"))

	dockerfilePath := filepath.Join(frontendDir, "Dockerfile")
	cmd = exec.Command("docker", "build", "-t", frontendImage, "-f", dockerfilePath, frontendDir)
	_, err = utils.Run(cmd)
	ExpectWithOffset(1, err).NotTo(HaveOccurred(), "Failed to build the frontend API image")

	By("loading the manager(Operator) image on Kind")
	err = utils.LoadImageToKindClusterWithName(kindClusterName, projectImage)
	ExpectWithOffset(1, err).NotTo(HaveOccurred(), "Failed to load the manager(Operator) image into Kind")
	By("loading the frontend API image on Kind")
	err = utils.LoadImageToKindClusterWithName(kindClusterName, frontendImage)
	ExpectWithOffset(1, err).NotTo(HaveOccurred(), "Failed to load the frontend API image into Kind")

	By("updating helm dependencies for the umbrella chart")
	umbrellaChartPath := filepath.Join(chartsRoot, "kubetasker")
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

	if !skipMetricsServerInstall {
		By("checking if metrics-server is installed")
		cmd := exec.Command("kubectl", "get", "apiservice", "v1beta1.metrics.k8s.io")
		if _, err := utils.Run(cmd); err != nil {
			Expect(installMetricsServer()).To(Succeed(), "Failed to install metrics-server")
		} else {
			_, _ = fmt.Fprintf(GinkgoWriter, "WARNING: metrics-server is already installed. Skipping installation...\n")
		}
	}

	// Always clean up cluster-scoped resources before any tests run to prevent poisoning
	CleanupStaleClusterResources()
})

// CleanupStaleClusterResources aggressively deletes global resources like webhooks and clusterroles
// that might have been left over from previous failed or interrupted test runs.
func CleanupStaleClusterResources() {
	By("scrubbing stale cluster-scoped resources")

	// Patterns for stale webhooks
	webhookPatterns := []string{
		"kt-sched-kubetasker-controller",
		"kt-prom-kubetasker-controller",
		"kubetasker-dev-kubetasker-controller",
		"kubetasker-staging-kubetasker-controller",
		"kubetasker-prod-kubetasker-controller",
		"kubetasker-e2e-kubetasker-controller",
		"kubetasker-kubetasker-controller",
		"kubetasker-controller",
	}

	for _, p := range webhookPatterns {
		mutating := p + "-mutating-webhook-configuration"
		validating := p + "-validating-webhook-configuration"

		_, _ = utils.Run(exec.Command("kubectl", "delete", "mutatingwebhookconfiguration", mutating, "--ignore-not-found"))
		_, _ = utils.Run(exec.Command("kubectl", "delete", "validatingwebhookconfiguration", validating, "--ignore-not-found"))
	}

	// Also try deleting by label just in case
	_, _ = utils.Run(exec.Command("kubectl", "delete", "mutatingwebhookconfiguration", "-l", "app.kubernetes.io/part-of=kubetasker", "--ignore-not-found"))
	_, _ = utils.Run(exec.Command("kubectl", "delete", "validatingwebhookconfiguration", "-l", "app.kubernetes.io/part-of=kubetasker", "--ignore-not-found"))

	// Stale ClusterRoles/Bindings
	rbacPatterns := []string{
		"kt-prom-kubetasker-controller-manager-role",
		"kt-prom-kubetasker-controller-metrics-reader",
		"kt-prom-kubetasker-controller-proxy-role",
		"kt-prom-kubetasker-frontend-cluster-role",
		"kubetasker-dev-kubetasker-frontend-cluster-role",
		"kubetasker-staging-kubetasker-frontend-cluster-role",
		"kubetasker-prod-kubetasker-frontend-cluster-role",
		"kubetasker-e2e-kubetasker-frontend-cluster-role",
	}

	for _, p := range rbacPatterns {
		_, _ = utils.Run(exec.Command("kubectl", "delete", "clusterrole", p, "--ignore-not-found"))
		bindingName := p + "binding"
		if strings.HasSuffix(p, "-role") {
			bindingName = strings.TrimSuffix(p, "-role") + "-rolebinding"
		}
		_, _ = utils.Run(exec.Command("kubectl", "delete", "clusterrolebinding", bindingName, "--ignore-not-found"))
	}
}

var _ = AfterSuite(func() {
	// Teardown CertManager after the suite if not skipped and if it was not already installed
	if !skipCertManagerInstall && !isCertManagerAlreadyInstalled {
		_, _ = fmt.Fprintf(GinkgoWriter, "Uninstalling CertManager...\n")
		utils.UninstallCertManager()
	}

	By("cleaning up test-generated files")
	// Only remove the files generated during the test, not the whole directory.
	_ = os.Remove(filepath.Join(projectRootDir, "kustomize", "base", "all.yaml"))
	_ = os.Remove(filepath.Join(projectRootDir, "kustomize", "base", "crd.yaml"))
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

	// --- Capture Cluster-Wide State for broader context ---
	logCommand("Fetching all pods in cluster",
		exec.Command("kubectl", "get", "pods", "-A", "-o", "wide"))

	logCommand("Searching for cluster-wide failure events",
		exec.Command("bash", "-c", "kubectl get events -A | grep -i failed || true"))

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
	logCommand("Listing all MutatingWebhookConfigurations",
		exec.Command("kubectl", "get", "mutatingwebhookconfigurations.admissionregistration.k8s.io"))

	logCommand("Listing all ValidatingWebhookConfigurations",
		exec.Command("kubectl", "get", "validatingwebhookconfigurations.admissionregistration.k8s.io"))

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

// verifyReplicaCount checks if the number of ready pods for a given label selector matches the expected count.
func verifyReplicaCount(namespace, labelSelector string, expectedCount int) {
	// In CI, we may override the replica count to 1, so we adjust our expectation.
	if os.Getenv("CI") == "true" {
		expectedCount = 1
	}

	Eventually(func(g Gomega) {
		cmd := exec.Command("kubectl", "get", "pods", "-n", namespace, "-l", labelSelector, "-o", "json")
		output, err := utils.Run(cmd)
		g.Expect(err).NotTo(HaveOccurred())

		var podList struct {
			Items []struct {
				Status struct {
					Phase string `json:"phase"`
				} `json:"status"`
			} `json:"items"`
		}
		g.Expect(json.Unmarshal([]byte(output), &podList)).To(Succeed())

		runningPods := 0
		for _, pod := range podList.Items {
			if pod.Status.Phase == "Running" {
				runningPods++
			}
		}
		g.Expect(runningPods).To(Equal(expectedCount), "Incorrect number of running pods found for selector "+labelSelector)
	}).Should(Succeed())
}

// verifyResources checks if the resource requests and limits for a pod's first container match the expected values.
func verifyResources(namespace, labelSelector string, expected map[string]string) {
	Eventually(func(g Gomega) {
		cmd := exec.Command("kubectl", "get", "pods", "-n", namespace, "-l", labelSelector, "-o", "jsonpath={.items[0].spec.containers[0].resources}")
		output, err := utils.Run(cmd)
		g.Expect(err).NotTo(HaveOccurred())

		var resources struct {
			Limits   map[string]string `json:"limits"`
			Requests map[string]string `json:"requests"`
		}
		g.Expect(json.Unmarshal([]byte(output), &resources)).To(Succeed())

		g.Expect(resources.Requests["cpu"]).To(Equal(expected["requests.cpu"]))
		g.Expect(resources.Requests["memory"]).To(Equal(expected["requests.memory"]))
		g.Expect(resources.Limits["cpu"]).To(Equal(expected["limits.cpu"]))
		g.Expect(resources.Limits["memory"]).To(Equal(expected["limits.memory"]))
	}).Should(Succeed())
}

// runInCurlPod creates a temporary pod with a curl image, waits for it to be ready,
// executes a given shell command inside it, and then cleans up the pod.
// It returns the stdout of the executed command or an error.
func runInCurlPod(podName, namespace, shellCmd string) (string, error) {
	// 1. Create the pod that sleeps, providing a stable target for exec.
	cmd := exec.Command("kubectl", "run", podName, "--image=curlimages/curl:latest",
		"--namespace", namespace, "--restart=Never", "--", "/bin/sh", "-c", "sleep 3600")
	if _, err := utils.Run(cmd); err != nil {
		return "", fmt.Errorf("failed to create curl pod %s in namespace %s: %w", podName, namespace, err)
	}

	// 2. Defer the cleanup to ensure the pod is deleted even if subsequent steps fail.
	defer func() {
		deleteCmd := exec.Command("kubectl", "delete", "pod", podName, "--namespace", namespace, "--ignore-not-found", "--now")
		_, _ = utils.Run(deleteCmd)
	}()

	// 3. Wait for the pod to become ready.
	waitCmd := exec.Command("kubectl", "wait", "--for=condition=Ready", "pod/"+podName,
		"--namespace", namespace, "--timeout=60s")
	if _, err := utils.Run(waitCmd); err != nil {
		return "", fmt.Errorf("curl pod %s in namespace %s did not become ready: %w", podName, namespace, err)
	}

	// 4. Execute the provided command inside the pod.
	execCmd := exec.Command("kubectl", "exec", podName, "--namespace", namespace, "--", "/bin/sh", "-c", shellCmd)
	output, err := utils.Run(execCmd)
	return output, err
}

// serviceAccountToken returns a token for the specified service account in the given namespace.
// It uses the Kubernetes TokenRequest API to generate a token by directly sending a request
// via the `kubectl create token` command.
func serviceAccountToken(serviceAccountName, namespaceName string) (string, error) {
	var token string
	var err error
	Eventually(func(g Gomega) {
		cmd := exec.Command("kubectl", "create", "token", serviceAccountName, "-n", namespaceName)
		token, err = utils.Run(cmd)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(token).NotTo(BeEmpty())
	}, "2m", "5s").Should(Succeed(), "Failed to create service account token")

	return strings.TrimSpace(token), nil
}

// installMetricsServer installs the Kubernetes Metrics Server and patches it for Kind compatibility.
func installMetricsServer() error {
	By("installing metrics-server")
	url := "https://github.com/kubernetes-sigs/metrics-server/releases/download/v0.6.4/components.yaml"
	cmd := exec.Command("kubectl", "apply", "-f", url)
	if _, err := utils.Run(cmd); err != nil {
		return fmt.Errorf("failed to apply metrics-server manifest: %w", err)
	}

	// Patch metrics-server to run securely on Kind (insecure-tls required for Kind's self-signed certs)
	patch := `[
	{"op":"add","path":"/spec/template/spec/containers/0/args/-","value":"--kubelet-insecure-tls"},
	{"op":"add","path":"/spec/template/spec/containers/0/args/-","value":"--kubelet-preferred-address-types=InternalIP"}
	]`
	cmd = exec.Command("kubectl", "patch", "deployment", "metrics-server",
		"-n", "kube-system", "--type=json", "-p", patch)

	if _, err := utils.Run(cmd); err != nil {
		return fmt.Errorf("failed to patch metrics-server for Kind: %w", err)
	}

	cmd = exec.Command("kubectl", "wait", "deployment", "metrics-server", "-n", "kube-system", "--for=condition=Available", "--timeout=5m")
	_, err := utils.Run(cmd)
	return err
}
