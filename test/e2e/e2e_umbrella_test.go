//go:build e2e
// +build e2e

package e2e

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/kndclark/kubetasker/test/utils"
)

var _ = Describe("Umbrella Chart", Ordered, func() {
	const (
		namespace           = "kubetasker-umbrella-e2e"
		helmReleaseName     = "kubetasker-umbrella"
		controllerFullName  = "kubetasker-umbrella-controller"
		frontendServiceName = "kubetasker-umbrella-frontend"
	)

	BeforeAll(func() {
		By("creating a separate namespace for umbrella chart tests")
		// Use apply to be idempotent.
		cmd := exec.Command("kubectl", "create", "ns", namespace, "--dry-run=client", "-o", "yaml")
		nsYAML, err := utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred())
		cmd = exec.Command("kubectl", "apply", "-f", "-")
		cmd.Stdin = strings.NewReader(nsYAML)
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to create namespace for umbrella test")

		By("deploying KubeTasker with the umbrella chart")
		umbrellaChartPath := filepath.Join(projectRootDir, "kubetasker")
		cmd = exec.Command("helm", "install", helmReleaseName, umbrellaChartPath,
			"--namespace", namespace,
			// Set controller values
			"--set", fmt.Sprintf("kubetasker-controller.image.repository=%s", strings.Split(projectImage, ":")[0]),
			"--set", fmt.Sprintf("kubetasker-controller.image.tag=%s", strings.Split(projectImage, ":")[1]),
			"--set", "kubetasker-controller.image.pullPolicy=IfNotPresent",
			"--set", "kubetasker-controller.fullnameOverride="+controllerFullName,
			"--set", "kubetasker-controller.webhookPrefix=umbrella-",
			// Set frontend values
			"--set", fmt.Sprintf("kubetasker-frontend.image.repository=%s", strings.Split(frontendImage, ":")[0]),
			"--set", fmt.Sprintf("kubetasker-frontend.image.tag=%s", strings.Split(frontendImage, ":")[1]),
			"--set", "kubetasker-frontend.image.pullPolicy=IfNotPresent",
			"--set", "kubetasker-frontend.fullnameOverride="+frontendServiceName,
			"--wait")
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to deploy the KubeTasker umbrella chart")

		By("verifying the controller-manager pod is running")
		verifyControllerUp := func(g Gomega) {
			cmd := exec.Command("kubectl", "wait", "pod", "-l", "control-plane=controller-manager",
				"--for=condition=Ready", "--timeout=2m", "-n", namespace)
			_, err := utils.Run(cmd)
			g.Expect(err).NotTo(HaveOccurred())
		}
		Eventually(verifyControllerUp).Should(Succeed())

		By("verifying the frontend pod is running")
		verifyFrontendUp := func(g Gomega) {
			cmd := exec.Command("kubectl", "wait", "pod", "-l", "app.kubernetes.io/name=kubetasker-frontend",
				"--for=condition=Ready", "--timeout=2m", "-n", namespace)
			_, err := utils.Run(cmd)
			g.Expect(err).NotTo(HaveOccurred())
		}
		Eventually(verifyFrontendUp).Should(Succeed())
	})

	AfterAll(func() {
		By("cleaning up the umbrella chart release")
		cmd := exec.Command("helm", "uninstall", helmReleaseName, "--namespace", namespace)
		_, _ = utils.Run(cmd)

		By("deleting the umbrella chart test namespace")
		cmd = exec.Command("kubectl", "delete", "ns", namespace, "--ignore-not-found")
		_, _ = utils.Run(cmd)

		// Explicitly delete cluster-scoped webhook configurations to prevent test pollution.
		cleanupWebhookConfigurations(controllerFullName)

	})

	// After each test, if the test fails, collect and log debugging information
	// like pod logs and events. This is crucial for diagnosing issues like the 500 error.
	AfterEach(func() {
		logDebugInfoOnFailure(namespace)
	})

	It("should create a Ktask via the frontend and see the corresponding Job succeed", func() {
		const ktaskName = "test-ktask-via-umbrella"
		const jobName = ktaskName + "-job"
		ktaskJSON := fmt.Sprintf(`{
			"apiVersion": "task.ktasker.com/v1",
			"kind": "Ktask",
			"metadata": { "name": "%s", "namespace": "%s" },
			"spec": { "image": "busybox", "command": ["/bin/sh", "-c", "echo 'Hello from umbrella test'"] }
		}`, ktaskName, namespace)

		ktaskJSON = strings.ReplaceAll(ktaskJSON, "\n", "")
		ktaskJSON = strings.ReplaceAll(ktaskJSON, "\t", "")

		By("posting a new Ktask to the frontend service")
		posterPodName := "curl-poster-umbrella"
		shellCmd := fmt.Sprintf("echo '%s' > /tmp/payload.json && curl -s -X POST -H 'Content-Type: application/json' -d @/tmp/payload.json http://%s.%s.svc.cluster.local:8000/ktask -o /dev/null -w %%{http_code}",
			ktaskJSON, frontendServiceName, namespace)

		output, err := runInCurlPod(posterPodName, namespace, shellCmd)
		Expect(err).NotTo(HaveOccurred())
		Expect(strings.TrimSpace(output)).To(Equal("200"), "Frontend service should return 200 OK")

		By("verifying the underlying Job is created and completes successfully")
		verifyJobSucceeded := func(g Gomega) {
			cmd := exec.Command("kubectl", "get", "job", jobName,
				"-n", namespace, "-o", "jsonpath={.status.conditions[?(@.type=='Complete')].status}")
			output, err := utils.Run(cmd)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(output).To(Equal("True"), "Job should have a Complete condition with status True")
		}
		Eventually(verifyJobSucceeded).WithTimeout(2 * time.Minute).Should(Succeed())

		By("verifying the Ktask status becomes 'Succeeded'")
		verifyKtaskSucceeded := func(g Gomega) {
			cmd := exec.Command("kubectl", "get", "ktask", ktaskName,
				"-n", namespace, "-o", "jsonpath={.status.phase}")
			output, err := utils.Run(cmd)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(output).To(Equal("Succeeded"), "Ktask phase should be Succeeded")
		}
		Eventually(verifyKtaskSucceeded).WithTimeout(1 * time.Minute).Should(Succeed())
	})
})
