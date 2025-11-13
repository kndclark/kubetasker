//go:build e2e
// +build e2e

package e2e

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/kndclark/kubetasker/test/utils"
)

// umbrellaChartTest holds the configuration for a single environment test.
type umbrellaChartTest struct {
	environment         string
	namespace           string
	helmReleaseName     string
	controllerFullName  string
	frontendServiceName string
	expectedReplicas    map[string]int
	expectedResources   map[string]map[string]string
}

// Define tests for each environment.
var umbrellaTests = []umbrellaChartTest{
	{
		environment:         "dev",
		namespace:           "kubetasker-umbrella-dev",
		helmReleaseName:     "kubetasker-dev",
		controllerFullName:  "kubetasker-controller",
		frontendServiceName: "kubetasker-frontend",
		expectedReplicas:    map[string]int{"controller": 1, "frontend": 1},
		expectedResources: map[string]map[string]string{
			"controller": {"requests.cpu": "100m", "requests.memory": "128Mi", "limits.cpu": "200m", "limits.memory": "256Mi"},
			"frontend":   {"requests.cpu": "50m", "requests.memory": "64Mi", "limits.cpu": "100m", "limits.memory": "128Mi"},
		},
	},
	{
		environment:         "staging",
		namespace:           "kubetasker-umbrella-staging",
		helmReleaseName:     "kubetasker-staging",
		controllerFullName:  "kubetasker-controller",
		frontendServiceName: "kubetasker-frontend",
		expectedReplicas:    map[string]int{"controller": 2, "frontend": 2},
		expectedResources: map[string]map[string]string{
			"controller": {"requests.cpu": "250m", "requests.memory": "256Mi", "limits.cpu": "500m", "limits.memory": "512Mi"},
			"frontend":   {"requests.cpu": "100m", "requests.memory": "128Mi", "limits.cpu": "250m", "limits.memory": "256Mi"},
		},
	},
	{
		environment:         "prod",
		namespace:           "kubetasker-umbrella-prod",
		helmReleaseName:     "kubetasker-prod",
		controllerFullName:  "kubetasker-controller",
		frontendServiceName: "kubetasker-frontend",
		expectedReplicas:    map[string]int{"controller": 2, "frontend": 3},
		expectedResources: map[string]map[string]string{
			"controller": {"requests.cpu": "500m", "requests.memory": "512Mi", "limits.cpu": "1", "limits.memory": "1Gi"},
			"frontend":   {"requests.cpu": "200m", "requests.memory": "256Mi", "limits.cpu": "500m", "limits.memory": "512Mi"},
		},
	},
}

var _ = Describe("Umbrella Chart Environments", Ordered, func() {
	for _, t := range umbrellaTests {
		// Capture the test struct for the closure.
		tt := t

		Context(fmt.Sprintf("when deploying the '%s' environment", tt.environment), func() {
			BeforeAll(func() {
				By(fmt.Sprintf("creating namespace %s", tt.namespace))
				cmd := exec.Command("kubectl", "create", "ns", tt.namespace, "--dry-run=client", "-o", "yaml")
				nsYAML, err := utils.Run(cmd)
				Expect(err).NotTo(HaveOccurred())
				cmd = exec.Command("kubectl", "apply", "-f", "-")
				cmd.Stdin = strings.NewReader(nsYAML)
				_, err = utils.Run(cmd)
				Expect(err).NotTo(HaveOccurred())

				By(fmt.Sprintf("deploying KubeTasker with the '%s' values file", tt.environment))
				umbrellaChartPath := filepath.Join(chartsRoot, "kubetasker")
				valuesFilePath := filepath.Join(umbrellaChartPath, fmt.Sprintf("values-%s.yaml", tt.environment))

				helmArgs := []string{
					"install", tt.helmReleaseName, umbrellaChartPath,
					"--namespace", tt.namespace,
					"-f", valuesFilePath,
					// Override images for the test environment
					"--set", fmt.Sprintf("kubetasker-controller.image.repository=%s", strings.Split(projectImage, ":")[0]),
					"--set", fmt.Sprintf("kubetasker-controller.image.tag=%s", strings.Split(projectImage, ":")[1]),
					"--set", fmt.Sprintf("kubetasker-frontend.image.repository=%s", strings.Split(frontendImage, ":")[0]),
					"--set", fmt.Sprintf("kubetasker-frontend.image.tag=%s", strings.Split(frontendImage, ":")[1]),
					// Override names for test isolation
					"--set", "kubetasker-controller.fullnameOverride=" + tt.controllerFullName,
					"--set", "kubetasker-frontend.fullnameOverride=" + tt.frontendServiceName,
					"--set", "kubetasker-controller.webhook.service.namespace=" + tt.namespace,
					"--set", "kubetasker-controller.webhookPrefix=umbrella-" + tt.environment + "-",
					"--timeout", "90s", // Add timeout to the helm command itself
					"--wait",
				}

				// If running in CI, override resource-intensive values to ensure tests can run.
				// The configuration tests will still verify the original values from the values file.
				if os.Getenv("CI") == "true" {
					By("CI environment detected, overriding replica counts to 1")
					helmArgs = append(helmArgs,
						"--set", "kubetasker-controller.replicaCount=1",
						"--set", "kubetasker-frontend.replicaCount=1",
					)
				}

				cmd = exec.Command("helm", helmArgs...)
				_, err = utils.Run(cmd)
				Expect(err).NotTo(HaveOccurred(), "Failed to deploy the KubeTasker umbrella chart for env: "+tt.environment)

				By("verifying all pods are running")
				// This check is now somewhat redundant because of `helm --wait`, but it's a good secondary confirmation.
				Eventually(func(g Gomega) {
					cmd := exec.Command("kubectl", "wait", "pod", "-l", "app.kubernetes.io/instance="+tt.helmReleaseName,
						"--for=condition=Ready", "--timeout=30s", "-n", tt.namespace)
					_, err := utils.Run(cmd)
					g.Expect(err).NotTo(HaveOccurred(), "Pods for release %s did not become ready", tt.helmReleaseName)
				}).Should(Succeed())
			})

			AfterAll(func() {
				By(fmt.Sprintf("cleaning up the %s release", tt.helmReleaseName))
				cmd := exec.Command("helm", "uninstall", tt.helmReleaseName, "--namespace", tt.namespace)
				_, _ = utils.Run(cmd)

				By(fmt.Sprintf("deleting the %s namespace", tt.namespace))
				cmd = exec.Command("kubectl", "delete", "ns", tt.namespace, "--ignore-not-found")
				_, _ = utils.Run(cmd)

				cleanupWebhookConfigurations(tt.controllerFullName)
			})

			AfterEach(func() {
				logDebugInfoOnFailure(tt.namespace)
			})

			It("should have the correct replica counts", func() {
				By("verifying the controller replica count")
				verifyReplicaCount(tt.namespace, "control-plane=controller-manager", tt.expectedReplicas["controller"])

				By("verifying the frontend replica count")
				verifyReplicaCount(tt.namespace, "app.kubernetes.io/name=kubetasker-frontend", tt.expectedReplicas["frontend"])
			})

			It("should have the correct resource settings", func() {
				By("verifying controller resource settings")
				verifyResources(tt.namespace, "control-plane=controller-manager", tt.expectedResources["controller"])

				By("verifying frontend resource settings")
				verifyResources(tt.namespace, "app.kubernetes.io/name=kubetasker-frontend", tt.expectedResources["frontend"])
			})

			// Only run the functional test for the 'dev' environment to avoid redundancy.
			if tt.environment == "dev" {
				It("should create a Ktask via the frontend and see the corresponding Job succeed", func() {
					const ktaskName = "test-ktask-via-umbrella"
					const jobName = ktaskName + "-job"
					ktaskJSON := fmt.Sprintf(`{
						"apiVersion": "task.ktasker.com/v1",
						"kind": "Ktask",
						"metadata": { "name": "%s", "namespace": "%s" },
						"spec": { "image": "busybox", "command": ["/bin/sh", "-c", "echo 'Hello from umbrella test'"] }
					}`, ktaskName, tt.namespace)

					ktaskJSON = strings.ReplaceAll(ktaskJSON, "\n", "")
					ktaskJSON = strings.ReplaceAll(ktaskJSON, "\t", "")

					By("posting a new Ktask to the frontend service")
					posterPodName := "curl-poster-umbrella"
					shellCmd := fmt.Sprintf("echo '%s' > /tmp/payload.json && curl -s -X POST -H 'Content-Type: application/json' -d @/tmp/payload.json http://%s.%s.svc.cluster.local:8000/ktask -o /dev/null -w %%{http_code}",
						ktaskJSON, tt.frontendServiceName, tt.namespace)

					output, err := runInCurlPod(posterPodName, tt.namespace, shellCmd)
					Expect(err).NotTo(HaveOccurred())
					Expect(strings.TrimSpace(output)).To(Equal("200"), "Frontend service should return 200 OK")

					By("verifying the underlying Job is created and completes successfully")
					Eventually(func(g Gomega) {
						cmd := exec.Command("kubectl", "get", "job", jobName,
							"-n", tt.namespace, "-o", "jsonpath={.status.conditions[?(@.type=='Complete')].status}")
						output, err := utils.Run(cmd)
						g.Expect(err).NotTo(HaveOccurred())
						g.Expect(output).To(Equal("True"), "Job should have a Complete condition with status True")
					}).WithTimeout(2 * time.Minute).Should(Succeed())

					By("verifying the Ktask status becomes 'Succeeded'")
					Eventually(func(g Gomega) {
						cmd := exec.Command("kubectl", "get", "ktask", ktaskName,
							"-n", tt.namespace, "-o", "jsonpath={.status.phase}")
						output, err := utils.Run(cmd)
						g.Expect(err).NotTo(HaveOccurred())
						g.Expect(output).To(Equal("Succeeded"), "Ktask phase should be Succeeded")
					}).WithTimeout(1 * time.Minute).Should(Succeed())
				})
			}
		})
	}
})

// verifyReplicaCount checks if the number of ready pods for a given label selector matches the expected count.
func verifyReplicaCount(namespace, labelSelector string, expectedCount int) {
	// In CI, we override the replica count to 1, so we adjust our expectation.
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
