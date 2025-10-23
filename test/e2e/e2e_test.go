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
	"os/exec"
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
			"--wait")
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to deploy the controller-manager")

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
			cmd = exec.Command("kubectl", "run", "curl-metrics", "--restart=Never",
				"--namespace", namespace,
				"--image=curlimages/curl:latest",
				"--overrides",
				fmt.Sprintf(`{
					"spec": {
						"containers": [{
							"name": "curl",
							"image": "curlimages/curl:latest",
							"command": ["curl", "-v", "-k", "-H", "Authorization: Bearer %s", "https://%s.%s.svc.cluster.local:8443/metrics"],
							"securityContext": {
								"readOnlyRootFilesystem": true,
								"allowPrivilegeEscalation": false,
								"capabilities": {
									"drop": ["ALL"]
								},
								"runAsNonRoot": false,
								"runAsUser": 0,
								"seccompProfile": {
									"type": "Unconfined"
								}
							}
						}],
						"controllerFullName": "%s"
					}
				}`, token, metricsServiceName, namespace, controllerFullName))
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
				// The secret name is hardcoded to webhook-server-cert in the deployment.
				cmd := exec.Command("kubectl", "get", "secret", "webhook-server-cert", "-n", namespace)
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

		It("should handle a JobRequest that results in a successful Job", func() { // Test for successful job
			const jobRequestName = "test-jobrequest-success"
			const jobRequestYAML = `
apiVersion: custom.custom.io/v1 
kind: JobRequest
metadata:
  name: %s
  namespace: %s
spec:
  image: busybox
  command: ["/bin/sh", "-c", "echo 'Success'; exit 0"]
`
			By("creating a JobRequest that is destined to succeed")
			cmd := exec.Command("kubectl", "apply", "-f", "-")
			cmd.Stdin = strings.NewReader(fmt.Sprintf(jobRequestYAML, jobRequestName, namespace))
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for the JobRequest status to become 'Succeeded'")
			verifyJobRequestSucceeded := func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "jobrequest", jobRequestName,
					"-n", namespace, "-o", "jsonpath={.status.phase}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("Succeeded"), "JobRequest phase should be Succeeded")
			}
			Eventually(verifyJobRequestSucceeded, 60*time.Second).Should(Succeed())

			By("verifying the underlying Job is marked as succeeded")
			verifyJobSucceeded := func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "job", jobRequestName+"-job",
					"-n", namespace, "-o", "jsonpath={.status.conditions[?(@.type=='Complete')].status}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("True"), "Job should have a Complete condition with status True")
			}
			Eventually(verifyJobSucceeded).Should(Succeed())
		})

		It("should handle a JobRequest that results in a RecoverableLogicError", func() { // Test for recoverable logic error
			const jobRequestName = "test-jobrequest-logic-error"
			const jobRequestYAML = `
apiVersion: custom.custom.io/v1
kind: JobRequest
metadata:
  name: %s
  namespace: %s
spec:
  image: busybox
  command: ["/bin/sh", "-c", "exit 1"]
  restartPolicy: Never
`
			By("creating a JobRequest that is destined to fail")
			cmd := exec.Command("kubectl", "apply", "-f", "-")
			cmd.Stdin = strings.NewReader(fmt.Sprintf(jobRequestYAML, jobRequestName, namespace))
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for the JobRequest status to become 'Failed'")
			verifyJobRequestFailed := func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "jobrequest", jobRequestName,
					"-n", namespace, "-o", "jsonpath={.status.phase}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("Failed"), "JobRequest phase should be Failed")
			}

			By("verifying the failure reason is RecoverableLogicError")
			verifyFailureReason := func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "jobrequest", jobRequestName,
					"-n", namespace, "-o", "jsonpath={.status.conditions[?(@.type=='JobReady')].reason}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("RecoverableLogicError"))
			}
			// The Job has a backoffLimit of 4, so this might take some time.
			// We'll give it a generous timeout.
			Eventually(verifyJobRequestFailed, 3*time.Minute).Should(Succeed())

			By("verifying the underlying Job is marked as failed")
			verifyJobFailed := func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "job", jobRequestName+"-job",
					"-n", namespace, "-o", "jsonpath={.status.conditions[?(@.type=='Failed')].status}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("True"), "Job should have a Failed condition with status True")
			}
			Eventually(verifyJobFailed).Should(Succeed())
			Eventually(verifyFailureReason).Should(Succeed())
		})

		It("should handle a JobRequest that results in a PermanentFailure", func() { // Test for permanent failure
			const jobRequestName = "test-jobrequest-permanent-fail"
			const jobRequestYAML = `
apiVersion: custom.custom.io/v1
kind: JobRequest
metadata:
  name: %s
  namespace: %s
spec:
  image: non-existent-registry/non-existent-image:latest
  command: ["/bin/sh", "-c", "echo 'This will not run'"]
  restartPolicy: Never
`
			By("creating a JobRequest with a bad image name")
			cmd := exec.Command("kubectl", "apply", "-f", "-")
			cmd.Stdin = strings.NewReader(fmt.Sprintf(jobRequestYAML, jobRequestName, namespace))
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for the JobRequest status to become 'Failed'")
			verifyJobRequestFailed := func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "jobrequest", jobRequestName,
					"-n", namespace, "-o", "jsonpath={.status.phase}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("Failed"), "JobRequest phase should be Failed")
			}
			Eventually(verifyJobRequestFailed, 3*time.Minute).Should(Succeed())

			By("verifying the failure reason is PermanentFailure")
			verifyFailureReason := func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "jobrequest", jobRequestName,
					"-n", namespace, "-o", "jsonpath={.status.conditions[?(@.type=='JobReady')].reason}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("PermanentFailure"))
			}
			Eventually(verifyFailureReason).Should(Succeed())
		})

		It("should handle a JobRequest that results in a ConflictError", func() { // Test for conflict error
			const jobRequestName = "test-jobrequest-conflict-fail"
			const jobRequestYAML = `
apiVersion: custom.custom.io/v1
kind: JobRequest
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
			By("creating a JobRequest that references a missing ConfigMap")
			cmd := exec.Command("kubectl", "apply", "-f", "-")
			cmd.Stdin = strings.NewReader(fmt.Sprintf(jobRequestYAML, jobRequestName, namespace))
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for the JobRequest status to become 'Failed'")
			verifyJobRequestFailed := func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "jobrequest", jobRequestName,
					"-n", namespace, "-o", "jsonpath={.status.phase}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("Failed"), "JobRequest phase should be Failed")
			}
			Eventually(verifyJobRequestFailed, 3*time.Minute).Should(Succeed())

			By("verifying the failure reason is ConflictError")
			verifyFailureReason := func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "jobrequest", jobRequestName,
					"-n", namespace, "-o", "jsonpath={.status.conditions[?(@.type=='JobReady')].reason}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("ConflictError"))
			}
			Eventually(verifyFailureReason).Should(Succeed())
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
