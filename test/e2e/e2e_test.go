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
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/kndclark/kubetasker/test/utils"
)

const (
	namespace              = "kubetasker-system-e2e"
	helmReleaseName        = "kubetasker-e2e"
	controllerFullName     = helmReleaseName + "-kubetasker-controller"
	metricsRoleBindingName = helmReleaseName + "-metrics-binding"
	frontendDeploymentName = "kubetasker-frontend"
	frontendServiceName    = "kubetasker-frontend-service"
)

var _ = Describe("Manager", Ordered, func() {
	var controllerPodName string

	// Before running the tests, set up the environment by creating the namespace,
	// enforce the restricted security policy to the namespace, installing CRDs,
	// and deploying the controller.
	BeforeAll(func() {
		By("creating manager namespace")
		// Use apply to be idempotent
		var err error
		cmd := exec.Command("kubectl", "create", "ns", namespace, "--dry-run=client", "-o", "yaml")
		nsYAML, err := utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred())
		cmd = exec.Command("kubectl", "apply", "-f", "-")
		cmd.Stdin = strings.NewReader(nsYAML)
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to create namespace")

		By("labeling the namespace for metrics scraping")
		cmd = exec.Command("kubectl", "label", "namespace", namespace, "metrics=enabled", "--overwrite")
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to label namespace for metrics")

		By("deploying the controller-manager")
		cmd = exec.Command("helm", "install", helmReleaseName, "./kubetasker-controller",
			"--namespace", namespace,
			"--set", fmt.Sprintf("image.repository=%s", strings.Split(projectImage, ":")[0]),
			"--set", fmt.Sprintf("image.tag=%s", strings.Split(projectImage, ":")[1]),
			"--set", "image.pullPolicy=IfNotPresent",
			"--set", "webhookPrefix=single-",
			"--set", "fullnameOverride="+controllerFullName,
			"--wait")
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to deploy the controller-manager")

		By("deploying the frontend API service")
		frontendChartPath := filepath.Join(projectRootDir, "kubetasker-frontend")
		cmd = exec.Command("helm", "install", frontendDeploymentName, frontendChartPath,
			"--namespace", namespace,
			"--set", fmt.Sprintf("image.repository=%s", strings.Split(frontendImage, ":")[0]),
			"--set", fmt.Sprintf("image.tag=%s", strings.Split(frontendImage, ":")[1]),
			"--set", "image.pullPolicy=IfNotPresent",
			"--set", "fullnameOverride="+frontendServiceName,
			"--wait")
		_, err = utils.Run(cmd)

		Expect(err).NotTo(HaveOccurred(), "Failed to deploy the frontend API service")

		By("verifying the controller-manager pod is running")
		verifyControllerUp := func(g Gomega) {
			// Get the name of the controller-manager pod
			cmd := exec.Command("kubectl", "get",
				"pods", "-l", "control-plane=controller-manager",
				"-o", "go-template={{ range .items }}{{ .metadata.name }}{{ \"\\n\" }}{{ end }}",
				"-n", namespace,
			)
			podOutput, err := utils.Run(cmd)
			g.Expect(err).NotTo(HaveOccurred())
			podNames := utils.GetNonEmptyLines(podOutput)
			g.Expect(podNames).To(HaveLen(1), "expected 1 controller pod running")
			controllerPodName = podNames[0]
		}
		Eventually(verifyControllerUp).Should(Succeed())

		By("verifying the frontend pod is running")
		verifyFrontendUp := func(g Gomega) {
			// Use `kubectl wait` for a more reliable check. This ensures the pod is not only
			// running but also ready to receive traffic before we proceed.
			cmd := exec.Command("kubectl", "wait", "pod", "-l", "app.kubernetes.io/name=kubetasker-frontend",
				"--for=condition=Ready", "--timeout=2m", "-n", namespace)
			output, err := utils.Run(cmd)
			g.Expect(err).NotTo(HaveOccurred(), "Frontend pod did not become ready in time: %s", output)
		}
		Eventually(verifyFrontendUp).Should(Succeed())

	})

	// After all tests have been executed, clean up by undeploying the controller, uninstalling CRDs,
	// and deleting the namespace.
	AfterAll(func() {
		var cmd *exec.Cmd
		By("cleaning up the curl pod for metrics")
		// Only attempt to delete if the pod was actually created
		if _, err := utils.Run(exec.Command("kubectl", "get", "pod", "curl-metrics", "-n", namespace)); err == nil {
			cmd = exec.Command("kubectl", "delete", "pod", "curl-metrics", "-n", namespace)
			_, _ = utils.Run(cmd)
		}

		By("cleaning up the frontend API service")
		cmd = exec.Command("helm", "uninstall", frontendDeploymentName, "--namespace", namespace)
		if _, err := utils.Run(cmd); err != nil {
			// Helm uninstall might fail if the release was not found, which is okay.
			// We can make this check more robust if needed, but for cleanup, logging a warning is sufficient.
			if !strings.Contains(err.Error(), "release: not found") {
				_, _ = fmt.Fprintf(GinkgoWriter, "warning: failed to uninstall frontend helm release: %v\n", err)
			}
		}

		By("undeploying the controller-manager")
		cmd = exec.Command("helm", "uninstall", helmReleaseName, "--namespace", namespace)
		if _, err := utils.Run(cmd); err != nil {
			_, _ = fmt.Fprintf(GinkgoWriter, "warning: failed to uninstall helm release: %v\n", err)
		}

		By("removing manager namespace")
		cmd = exec.Command("kubectl", "delete", "ns", namespace, "--ignore-not-found")
		if _, err := utils.Run(cmd); err != nil {
			_, _ = fmt.Fprintf(GinkgoWriter, "warning: failed to delete namespace: %v\n", err)
		}
	})

	// After each test, check for failures and collect logs, events,
	// and pod descriptions for debugging.
	AfterEach(func() {
		specReport := CurrentSpecReport()
		logDebugInfoOnFailure(specReport, controllerPodName)
	})

	SetDefaultEventuallyTimeout(2 * time.Minute)
	SetDefaultEventuallyPollingInterval(time.Second)

	Context("Manager", func() {
		It("should run successfully", func() {
			By("validating that the controller-manager pod is running as expected")
			cmd := exec.Command("kubectl", "get",
				"pods", controllerPodName, "-o", "jsonpath={.status.phase}",
				"-n", namespace,
			)
			output, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())
			Expect(output).To(Equal("Running"), "Incorrect controller-manager pod status")
		})

		It("should ensure the metrics endpoint is serving metrics", func() {
			var cmd *exec.Cmd
			var err error

			By("validating that the metrics service is available")
			metricsServiceName := controllerFullName + "-metrics-service"
			cmd = exec.Command("kubectl", "get", "service", metricsServiceName, "-n", namespace)
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Metrics service should exist")

			By("getting the service account token")
			token, err := serviceAccountToken()
			Expect(err).NotTo(HaveOccurred())
			Expect(token).NotTo(BeEmpty())

			By("waiting for the metrics endpoint to be ready")
			verifyMetricsEndpointReady := func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "endpoints", metricsServiceName, "-n", namespace)
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(ContainSubstring("8443"), "Metrics endpoint is not ready")
			}
			Eventually(verifyMetricsEndpointReady).Should(Succeed())

			By("verifying that the controller manager is serving the metrics server")
			verifyMetricsServerStarted := func(g Gomega) {
				cmd := exec.Command("kubectl", "logs", controllerPodName, "-n", namespace)
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(ContainSubstring("controller-runtime.metrics\tServing metrics server"),
					"Metrics server not yet started")
			}
			Eventually(verifyMetricsServerStarted).Should(Succeed())

			By("creating the curl-metrics pod to access the metrics endpoint")
			// Define the pod override as a Go struct for better readability and type safety
			override := podOverride{
				Spec: podSpec{
					Containers: []containerSpec{
						{
							Name:    "curl",
							Image:   "curlimages/curl:latest",
							Command: []string{"curl", "-v", "-k", "-H", fmt.Sprintf("Authorization: Bearer %s", token), fmt.Sprintf("https://%s.%s.svc.cluster.local:8443/metrics", metricsServiceName, namespace)},
							SecurityContext: securityContext{
								ReadOnlyRootFilesystem:   true,
								AllowPrivilegeEscalation: false,
								Capabilities:             capabilities{Drop: []string{"ALL"}},
								RunAsNonRoot:             false,
								RunAsUser:                0,
								SeccompProfile:           seccompProfile{Type: "Unconfined"},
							},
						},
					},
					ControllerFullName: controllerFullName,
				},
			}

			overrideBytes, err := json.Marshal(override)
			Expect(err).NotTo(HaveOccurred(), "Failed to marshal pod override")

			cmd = exec.Command("kubectl", "run", "curl-metrics", "--restart=Never",
				"--namespace", namespace,
				"--image=curlimages/curl:latest",
				"--overrides",
				string(overrideBytes))
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to run curl-metrics pod")

			By("waiting for the curl-metrics pod to complete.")
			verifyCurlUp := func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "pods", "curl-metrics",
					"-o", "jsonpath={.status.phase}",
					"-n", namespace)
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("Succeeded"), "curl pod in wrong status")
			}
			Eventually(verifyCurlUp, 5*time.Minute).Should(Succeed())

			By("getting the metrics by checking curl-metrics logs")
			verifyMetricsAvailable := func(g Gomega) {
				metricsOutput, err := getMetricsOutput()
				g.Expect(err).NotTo(HaveOccurred(), "Failed to retrieve logs from curl pod")
				g.Expect(metricsOutput).NotTo(BeEmpty())
				g.Expect(metricsOutput).To(ContainSubstring("< HTTP/1.1 200 OK"))
			}
			Eventually(verifyMetricsAvailable, 2*time.Minute).Should(Succeed())
		})

		It("should provisioned cert-manager", func() {
			By("validating that cert-manager has the certificate Secret")
			verifyCertManager := func(g Gomega) {
				// The secret name is now dynamically generated by the Helm chart.
				cmd := exec.Command("kubectl", "get", "secret", controllerFullName+"-serving-cert", "-n", namespace)
				_, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
			}
			Eventually(verifyCertManager).Should(Succeed())
		})

		It("should have CA injection for mutating webhooks", func() {
			By("checking CA injection for mutating webhooks")
			verifyCAInjection := func(g Gomega) {
				cmd := exec.Command("kubectl", "get",
					"mutatingwebhookconfigurations.admissionregistration.k8s.io",
					controllerFullName+"-mutating-webhook-configuration",
					"-o", "go-template={{ range .webhooks }}{{ .clientConfig.caBundle }}{{ end }}")
				mwhOutput, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(len(mwhOutput)).To(BeNumerically(">", 10))
			}
			Eventually(verifyCAInjection).Should(Succeed())
		})

		It("should have CA injection for validating webhooks", func() {
			By("checking CA injection for validating webhooks")
			verifyCAInjection := func(g Gomega) {
				cmd := exec.Command("kubectl", "get",
					"validatingwebhookconfigurations.admissionregistration.k8s.io",
					controllerFullName+"-validating-webhook-configuration",
					"-o", "go-template={{ range .webhooks }}{{ .clientConfig.caBundle }}{{ end }}")
				vwhOutput, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(len(vwhOutput)).To(BeNumerically(">", 10))
			}
			Eventually(verifyCAInjection).Should(Succeed())
		})

		It("should have a healthy frontend API service", func() {
			By("verifying the frontend service is accessible within the cluster")
			verifyFrontendHealth := func(g Gomega) {
				curlCmd := fmt.Sprintf("curl -s -o /dev/null -w %%{http_code} http://%s.%s.svc.cluster.local:80/healthz", frontendServiceName, namespace)
				output, err := runInCurlPod("curl-health-check", namespace, curlCmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(strings.TrimSpace(output)).To(Equal("200"))
			}
			// It might take a moment for the service and endpoints to become fully available.
			Eventually(verifyFrontendHealth, "2m").Should(Succeed())
		})

		It("should create a Ktask via the frontend API and see it succeed", func() {
			const ktaskName = "test-ktask-via-frontend"
			const jobName = ktaskName + "-job"
			// JSON payload for the Ktask. Note the escaping for the shell command.
			ktaskJSON := fmt.Sprintf(`{
				"apiVersion": "task.ktasker.com/v1",
				"kind": "Ktask",
				"metadata": {
					"name": "%s",
					"namespace": "%s"
				},
				"spec": {
					"image": "busybox",
					"command": ["/bin/sh", "-c", "echo 'Hello from frontend test'; exit 0"]
				}
			}`, ktaskName, namespace)

			// The JSON must be a compact, single-line string to be safely passed
			// inside the shell command for curl.
			ktaskJSON = strings.ReplaceAll(ktaskJSON, "\n", "")
			ktaskJSON = strings.ReplaceAll(ktaskJSON, "\t", "")

			By("posting a new Ktask to the frontend service")
			// Use a temporary pod to send a POST request to the frontend service.
			// Multi-step approach done to avoid shell fragility.
			posterPodName := "curl-poster"
			shellCmd := fmt.Sprintf("echo '%s' > /tmp/payload.json && curl -s -X POST -H 'Content-Type: application/json' -d @/tmp/payload.json http://%s.%s.svc.cluster.local:80/ktask -o /dev/null -w %%{http_code}",
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
			// Give it enough time for the controller to reconcile and the job to run.
			Eventually(verifyJobSucceeded, "2m").Should(Succeed())

			// Cleanup the Ktask
			cmd := exec.Command("kubectl", "delete", "ktask", ktaskName, "-n", namespace, "--ignore-not-found")
			_, _ = utils.Run(cmd)
		})

		It("should handle a Ktask that results in a successful Job", func() { // Test for successful job
			const ktaskName = "test-ktask-success"
			const ktaskYAML = `
apiVersion: task.ktasker.com/v1
kind: Ktask
metadata:
  name: %s
  namespace: %s
spec:
  image: busybox
  command: ["/bin/sh", "-c", "echo 'Success'; exit 0"]
`
			By("creating a Ktask that is destined to succeed")
			cmd := exec.Command("kubectl", "apply", "-f", "-")
			cmd.Stdin = strings.NewReader(fmt.Sprintf(ktaskYAML, ktaskName, namespace))
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for the Ktask status to become 'Succeeded'")
			verifyKtaskSucceeded := func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "ktask", ktaskName,
					"-n", namespace, "-o", "jsonpath={.status.phase}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("Succeeded"), "Ktask phase should be Succeeded")
			}
			Eventually(verifyKtaskSucceeded, 60*time.Second).Should(Succeed())

			By("verifying the underlying Job is marked as succeeded")
			verifyJobSucceeded := func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "job", ktaskName+"-job",
					"-n", namespace, "-o", "jsonpath={.status.conditions[?(@.type=='Complete')].status}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("True"), "Job should have a Complete condition with status True")
			}
			Eventually(verifyJobSucceeded).Should(Succeed())
		})

		It("should handle a Ktask that results in a RecoverableLogicError", func() { // Test for recoverable logic error
			const ktaskName = "test-ktask-logic-error"
			const ktaskYAML = `
apiVersion: task.ktasker.com/v1
kind: Ktask
metadata:
  name: %s
  namespace: %s
spec:
  image: busybox
  command: ["/bin/sh", "-c", "exit 1"]
  restartPolicy: Never
`
			By("creating a Ktask that is destined to fail")
			cmd := exec.Command("kubectl", "apply", "-f", "-")
			cmd.Stdin = strings.NewReader(fmt.Sprintf(ktaskYAML, ktaskName, namespace))
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for the Ktask status to become 'Failed'")
			verifyKtaskFailed := func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "ktask", ktaskName,
					"-n", namespace, "-o", "jsonpath={.status.phase}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("Failed"), "Ktask phase should be Failed")
			}

			By("verifying the failure reason is TransientFailure")
			verifyFailureReason := func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "ktask", ktaskName,
					"-n", namespace, "-o", "jsonpath={.status.conditions[?(@.type=='JobReady')].reason}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("TransientFailure"))
			}
			// The Job has a backoffLimit of 4, so this might take some time.
			// We'll give it a generous timeout.
			Eventually(verifyKtaskFailed, 3*time.Minute).Should(Succeed())

			By("verifying the underlying Job is marked as failed")
			verifyJobFailed := func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "job", ktaskName+"-job",
					"-n", namespace, "-o", "jsonpath={.status.conditions[?(@.type=='Failed')].status}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("True"), "Job should have a Failed condition with status True")
			}
			Eventually(verifyJobFailed).Should(Succeed())
			Eventually(verifyFailureReason).Should(Succeed())
		})

		It("should handle a Ktask that results in a PermanentFailure", func() { // Test for permanent failure
			const ktaskName = "test-ktask-permanent-fail"
			const ktaskYAML = `
apiVersion: task.ktasker.com/v1
kind: Ktask
metadata:
  name: %s
  namespace: %s
spec:
  image: non-existent-registry/non-existent-image:latest
  command: ["/bin/sh", "-c", "echo 'This will not run'"]
  restartPolicy: Never
`
			By("creating a Ktask with a bad image name")
			cmd := exec.Command("kubectl", "apply", "-f", "-")
			cmd.Stdin = strings.NewReader(fmt.Sprintf(ktaskYAML, ktaskName, namespace))
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for the Ktask status to become 'Failed'")
			verifyKtaskFailed := func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "ktask", ktaskName,
					"-n", namespace, "-o", "jsonpath={.status.phase}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("Failed"), "Ktask phase should be Failed")
			}
			Eventually(verifyKtaskFailed, 3*time.Minute).Should(Succeed())

			By("verifying the failure reason is PermanentFailure")
			verifyFailureReason := func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "ktask", ktaskName,
					"-n", namespace, "-o", "jsonpath={.status.conditions[?(@.type=='JobReady')].reason}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("PermanentFailure"))
			}
			Eventually(verifyFailureReason).Should(Succeed())
		})

		It("should handle a Ktask that results in a ConflictError", func() { // Test for conflict error
			const ktaskName = "test-ktask-conflict-fail"
			const ktaskYAML = `
apiVersion: task.ktasker.com/v1
kind: Ktask
metadata:
  name: %s
  namespace: %s
spec:
  image: busybox 
  command: ["/bin/sh", "-c", "echo 'My secret is $MY_SECRET'"]
  # This env var references a secret that does not exist,
  # which will cause a CreateContainerConfigError.
  env:
  - name: MY_SECRET
    valueFrom:
      secretKeyRef:
        name: non-existent-secret
        key: password
`
			By("creating a Ktask that references a missing ConfigMap")
			cmd := exec.Command("kubectl", "apply", "-f", "-")
			cmd.Stdin = strings.NewReader(fmt.Sprintf(ktaskYAML, ktaskName, namespace))
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for the Ktask status to become 'Failed'")
			verifyKtaskFailed := func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "ktask", ktaskName,
					"-n", namespace, "-o", "jsonpath={.status.phase}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("Failed"), "Ktask phase should be Failed")
			}
			Eventually(verifyKtaskFailed, 3*time.Minute).Should(Succeed())

			By("verifying the failure reason is ConflictError")
			verifyFailureReason := func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "ktask", ktaskName,
					"-n", namespace, "-o", "jsonpath={.status.conditions[?(@.type=='JobReady')].reason}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("ConflictError"))
			}
			Eventually(verifyFailureReason).Should(Succeed())
		})

		It("should clean up the Job when a Ktask is deleted", func() {
			const ktaskName = "test-ktask-cleanup"
			const jobName = ktaskName + "-job"
			const ktaskYAML = `
apiVersion: task.ktasker.com/v1
kind: Ktask
metadata:
  name: %s
  namespace: %s
spec:
  image: busybox
  command: ["/bin/sh", "-c", "echo 'Cleanup test'"]
`
			By("creating a Ktask for the cleanup test")
			cmd := exec.Command("kubectl", "apply", "-f", "-")
			cmd.Stdin = strings.NewReader(fmt.Sprintf(ktaskYAML, ktaskName, namespace))
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for the underlying Job to be created")
			verifyJobCreated := func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "job", jobName, "-n", namespace)
				_, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred(), "Job should be created by the controller")
			}
			Eventually(verifyJobCreated, 60*time.Second).Should(Succeed())

			By("deleting the Ktask")
			cmd = exec.Command("kubectl", "delete", "ktask", ktaskName, "-n", namespace)
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			By("verifying the underlying Job is garbage collected")
			verifyJobDeleted := func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "job", jobName, "-n", namespace)
				_, err := utils.Run(cmd)
				g.Expect(err).To(HaveOccurred(), "Job should be deleted after Ktask is deleted")
				g.Expect(err.Error()).To(ContainSubstring("not found"))
			}
			Eventually(verifyJobDeleted, 60*time.Second).Should(Succeed())
		})

		It("should be rejected by the validating webhook when updating immutable fields", func() {
			const ktaskName = "test-ktask-webhook"
			const ktaskYAML = `
apiVersion: task.ktasker.com/v1
kind: Ktask
metadata:
  name: %s
  namespace: %s
spec:
  image: busybox
  command: ["/bin/sh", "-c", "echo 'webhook test'"]
`
			By("creating a Ktask for the webhook test")
			cmd := exec.Command("kubectl", "apply", "-f", "-")
			cmd.Stdin = strings.NewReader(fmt.Sprintf(ktaskYAML, ktaskName, namespace))
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			// Wait a moment for the object to be fully persisted.
			time.Sleep(2 * time.Second)

			By("attempting to update an immutable field (spec.image)")
			// The image field is often immutable. The webhook should reject this change.
			// We use 'kubectl patch' for a direct update attempt.
			patch := `{"spec":{"image":"nginx"}}`
			cmd = exec.Command("kubectl", "patch", "ktask", ktaskName,
				"-n", namespace, "--type=merge", "-p", patch)

			// We expect this command to fail.
			output, err := utils.Run(cmd)
			Expect(err).To(HaveOccurred(), "The validating webhook should reject the update.")

			// Check for the specific error message from the webhook.
			Expect(output).To(ContainSubstring("admission webhook \"vktask.kb.io\" denied the request"), "The webhook rejection message was not found in the output.")
		})
	})
})

// serviceAccountToken returns a token for the specified service account in the given namespace.
// It uses the Kubernetes TokenRequest API to generate a token by directly sending a request
// via the `kubectl create token` command.
func serviceAccountToken() (string, error) {
	var token string
	var err error
	Eventually(func(g Gomega) {
		cmd := exec.Command("kubectl", "create", "token", controllerFullName, "-n", namespace)
		token, err = utils.Run(cmd)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(token).NotTo(BeEmpty())
	}, "2m", "5s").Should(Succeed(), "Failed to create service account token")

	return strings.TrimSpace(token), nil
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

// getMetricsOutput retrieves and returns the logs from the curl pod used to access the metrics endpoint.
func getMetricsOutput() (string, error) {
	By("getting the curl-metrics logs")
	cmd := exec.Command("kubectl", "logs", "curl-metrics", "-n", namespace)
	return utils.Run(cmd)
}

// tokenRequest is a simplified representation of the Kubernetes TokenRequest API response,
// containing only the token field that we need to extract.
type tokenRequest struct {
	Status struct {
		Token string `json:"token"`
	} `json:"status"`
}

// Helper structs for creating the pod override JSON
type podOverride struct {
	Spec podSpec `json:"spec"`
}

type podSpec struct {
	Containers         []containerSpec `json:"containers"`
	ControllerFullName string          `json:"controllerFullName"`
}

type containerSpec struct {
	Name            string          `json:"name"`
	Image           string          `json:"image"`
	Command         []string        `json:"command"`
	SecurityContext securityContext `json:"securityContext"`
}

type securityContext struct {
	ReadOnlyRootFilesystem   bool           `json:"readOnlyRootFilesystem"`
	AllowPrivilegeEscalation bool           `json:"allowPrivilegeEscalation"`
	Capabilities             capabilities   `json:"capabilities"`
	RunAsNonRoot             bool           `json:"runAsNonRoot"`
	RunAsUser                int            `json:"runAsUser"`
	SeccompProfile           seccompProfile `json:"seccompProfile"`
}

type capabilities struct {
	Drop []string `json:"drop"`
}

type seccompProfile struct {
	Type string `json:"type"`
}

// logDebugInfoOnFailure checks if the spec failed and, if so, logs debugging information
// such as controller logs, events, and pod descriptions.
func logDebugInfoOnFailure(specReport SpecReport, controllerPodName string) {
	if !specReport.Failed() {
		return
	}

	// Helper to run and log a command, writing output to GinkgoWriter
	logCommand := func(description string, cmd *exec.Cmd) {
		By(description)
		output, err := utils.Run(cmd)
		if err != nil {
			_, _ = fmt.Fprintf(GinkgoWriter, "Failed to run command for '%s': %v\n", description, err)
			return
		}
		_, _ = fmt.Fprintf(GinkgoWriter, "%s:\n%s\n", description, output)
	}

	if controllerPodName != "" {
		logCommand("Fetching controller manager pod logs",
			exec.Command("kubectl", "logs", controllerPodName, "-n", namespace))

		logCommand("Fetching controller manager pod description",
			exec.Command("kubectl", "describe", "pod", controllerPodName, "-n", namespace))
	} else {
		_, _ = fmt.Fprintln(GinkgoWriter, "Controller pod name not available, skipping pod-specific logs.")
	}

	logCommand("Fetching Kubernetes events",
		exec.Command("kubectl", "get", "events", "-n", namespace, "--sort-by=.lastTimestamp"))

	// Check if the curl-metrics pod exists before trying to get its logs
	checkCurlPodCmd := exec.Command("kubectl", "get", "pod", "curl-metrics", "-n", namespace, "--ignore-not-found")
	if output, err := utils.Run(checkCurlPodCmd); err == nil && strings.Contains(output, "curl-metrics") {
		logCommand("Fetching curl-metrics logs",
			exec.Command("kubectl", "logs", "curl-metrics", "-n", namespace))
	}
}
