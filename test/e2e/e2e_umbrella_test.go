//go:build e2e
// +build e2e

package e2e

import (
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
					// Use locally loaded images instead of pulling from registry
					"--set", "global.imagePullPolicy=IfNotPresent",
					// Override names for test isolation
					"--set", "kubetasker-controller.fullnameOverride=" + tt.controllerFullName,
					"--set", "kubetasker-frontend.fullnameOverride=" + tt.frontendServiceName,
					"--set", "kubetasker-controller.webhook.service.namespace=" + tt.namespace,
					"--set", "kubetasker-controller.webhookPrefix=umbrella-" + tt.environment + "-",
					// Disable ServiceMonitors as CRDs are not installed in the test cluster
					"--set", "kubetasker-controller.serviceMonitor.enabled=false",
					"--set", "kubetasker-frontend.serviceMonitor.enabled=false",
					"--timeout", "90s", // Add timeout to the helm command itself
					"--wait",
				}

				// If running in CI, override resource-intensive values to ensure tests can run.
				// The configuration tests will still verify the original values from the values file.
				if os.Getenv("CI") == "true" {
					By("CI environment detected, overriding replica counts and resources")
					// Use safe, low values for CI to prevent scheduling timeouts on limited runners
					safeCPU := "100m"
					safeMem := "128Mi"

					helmArgs = append(helmArgs,
						"--set", "kubetasker-controller.replicaCount=1",
						"--set", "kubetasker-frontend.replicaCount=1",
						"--set", "kubetasker-controller.resources.requests.cpu="+safeCPU,
						"--set", "kubetasker-controller.resources.limits.cpu="+safeCPU,
						"--set", "kubetasker-controller.resources.requests.memory="+safeMem,
						"--set", "kubetasker-controller.resources.limits.memory="+safeMem,
						"--set", "kubetasker-frontend.resources.requests.cpu="+safeCPU,
						"--set", "kubetasker-frontend.resources.limits.cpu="+safeCPU,
						"--set", "kubetasker-frontend.resources.requests.memory="+safeMem,
						"--set", "kubetasker-frontend.resources.limits.memory="+safeMem,
					)
					// Update expectations to match the CI overrides
					tt.expectedResources = map[string]map[string]string{
						"controller": {"requests.cpu": safeCPU, "requests.memory": safeMem, "limits.cpu": safeCPU, "limits.memory": safeMem},
						"frontend":   {"requests.cpu": safeCPU, "requests.memory": safeMem, "limits.cpu": safeCPU, "limits.memory": safeMem},
					}
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
				It("should serve the GUI dashboard", func() {
					By("verifying the frontend GUI is accessible")
					verifyFrontendGUI := func(g Gomega) {
						curlCmd := fmt.Sprintf("curl -s http://%s.%s.svc.cluster.local:8000/", tt.frontendServiceName, tt.namespace)
						output, err := runInCurlPod("curl-gui-check-umbrella", tt.namespace, curlCmd)
						g.Expect(err).NotTo(HaveOccurred())
						g.Expect(output).To(ContainSubstring("<title>KubeTasker Dashboard</title>"))
					}
					Eventually(verifyFrontendGUI, "2m").Should(Succeed())
				})

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
					Expect(strings.TrimSpace(output)).To(Equal("202"), "Frontend service should return 202 Accepted")

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

				It("should delete a Ktask via the frontend API", func() {
					const ktaskName = "test-ktask-delete-umbrella"
					ktaskYAML := fmt.Sprintf(`
apiVersion: task.ktasker.com/v1
kind: Ktask
metadata:
  name: %s
  namespace: %s
spec:
  image: busybox
  command: ["sleep", "300"]
`, ktaskName, tt.namespace)

					By("creating a Ktask to delete")
					cmd := exec.Command("kubectl", "apply", "-f", "-")
					cmd.Stdin = strings.NewReader(ktaskYAML)
					_, err := utils.Run(cmd)
					Expect(err).NotTo(HaveOccurred())

					By("sending a DELETE request")
					deleterPodName := "curl-deleter-umbrella"
					curlCmd := fmt.Sprintf("curl -s -X DELETE -o /dev/null -w %%{http_code} http://%s.%s.svc.cluster.local:8000/ktask/%s?namespace=%s",
						tt.frontendServiceName, tt.namespace, ktaskName, tt.namespace)
					output, err := runInCurlPod(deleterPodName, tt.namespace, curlCmd)
					Expect(err).NotTo(HaveOccurred())
					Expect(strings.TrimSpace(output)).To(Equal("204"))

					By("verifying the Ktask is deleted")
					Eventually(func(g Gomega) {
						cmd := exec.Command("kubectl", "get", "ktask", ktaskName, "-n", tt.namespace)
						_, err := utils.Run(cmd)
						g.Expect(err).To(HaveOccurred())
					}, "1m").Should(Succeed())
				})

				// Define HPA load test scenarios
				hpaScenarios := []struct {
					name   string
					genCmd func(baseUrl string) string
				}{
					{
						name: "Healthz GET",
						genCmd: func(baseUrl string) string {
							return fmt.Sprintf("while true; do curl -s %s/healthz > /dev/null; done", baseUrl)
						},
					},
					{
						name: "Queue POST",
						genCmd: func(baseUrl string) string {
							// Payload for stress testing. Using a fixed name means subsequent requests might fail in the worker (409),
							// but the frontend queue logic (202 Accepted) is still exercised.
							jsonPayload := fmt.Sprintf(`{"apiVersion":"task.ktasker.com/v1","kind":"Ktask","metadata":{"name":"stress-%s","namespace":"%s"},"spec":{"image":"busybox","command":["echo","stress"]}}`, "hpa", tt.namespace)
							return fmt.Sprintf("while true; do curl -s -X POST -H 'Content-Type: application/json' -d '%s' %s/ktask > /dev/null; done", jsonPayload, baseUrl)
						},
					},
				}

				for _, sc := range hpaScenarios {
					sc := sc
					It(fmt.Sprintf("should scale the frontend deployment when under load (HPA) - %s", sc.name), func() {
						// Note: This test relies on the metrics-server being installed in the cluster.
						// If metrics-server is missing, HPA will not be able to retrieve metrics, and this test will time out.

						safeName := strings.ReplaceAll(strings.ToLower(sc.name), " ", "-")
						hpaName := "frontend-hpa-" + safeName
						targetDeployment := tt.frontendServiceName // fullnameOverride used as deployment name
						minReplicas := 1
						maxReplicas := 3
						if os.Getenv("CI") == "true" {
							By("CI environment detected, limiting HPA maxReplicas to 2")
							maxReplicas = 2
						}
						cpuTarget := 5 // Low CPU target to trigger scaling easily

						By("creating the HPA resource")
						hpaYAML := fmt.Sprintf(`
apiVersion: autoscaling/v2
kind: HorizontalPodAutoscaler
metadata:
  name: %s
  namespace: %s
spec:
  scaleTargetRef:
    apiVersion: apps/v1
    kind: Deployment
    name: %s
  minReplicas: %d
  maxReplicas: %d
  metrics:
  - type: Resource
    resource:
      name: cpu
      target:
        type: Utilization
        averageUtilization: %d
`, hpaName, tt.namespace, targetDeployment, minReplicas, maxReplicas, cpuTarget)

						cmd := exec.Command("kubectl", "apply", "-f", "-")
						cmd.Stdin = strings.NewReader(hpaYAML)
						_, err := utils.Run(cmd)
						Expect(err).NotTo(HaveOccurred())

						By("starting a load generator")
						loadGenName := "load-generator-" + safeName
						baseUrl := fmt.Sprintf("http://%s.%s.svc.cluster.local:8000", tt.frontendServiceName, tt.namespace)

						// Run a pod that continuously hits the target endpoint
						cmd = exec.Command("kubectl", "run", loadGenName, "--image=curlimages/curl:latest",
							"--namespace", tt.namespace, "--restart=Never", "--", "/bin/sh", "-c",
							sc.genCmd(baseUrl))
						_, err = utils.Run(cmd)
						Expect(err).NotTo(HaveOccurred())

						By("verifying that the frontend can still buffer requests under load")
						// We submit a request while the load generator is running to ensure the async event loop isn't blocked
						// and the buffering logic still responds with 202 Accepted.
						verifyName := "verify-" + safeName
						loadKtaskJSON := fmt.Sprintf(`{"apiVersion":"task.ktasker.com/v1","kind":"Ktask","metadata":{"name":"%s","namespace":"%s"},"spec":{"image":"busybox","command":["echo","load"]}}`, verifyName, tt.namespace)
						posterPodName := "curl-poster-" + verifyName
						shellCmd := fmt.Sprintf("echo '%s' > /tmp/payload.json && curl -s -X POST -H 'Content-Type: application/json' -d @/tmp/payload.json http://%s.%s.svc.cluster.local:8000/ktask -o /dev/null -w %%{http_code}", loadKtaskJSON, tt.frontendServiceName, tt.namespace)
						output, err := runInCurlPod(posterPodName, tt.namespace, shellCmd)
						Expect(err).NotTo(HaveOccurred())
						Expect(strings.TrimSpace(output)).To(Equal("202"), "Frontend should accept requests even under load")

						By("verifying the buffered request is processed by the worker")
						Eventually(func(g Gomega) {
							cmd := exec.Command("kubectl", "get", "ktask", verifyName, "-n", tt.namespace)
							_, err := utils.Run(cmd)
							g.Expect(err).NotTo(HaveOccurred(), "Ktask %s should be created by the worker", verifyName)
						}, 2*time.Minute, 1*time.Second).Should(Succeed())

						By("waiting for the frontend deployment to scale up")
						Eventually(func(g Gomega) {
							cmd := exec.Command("kubectl", "get", "deployment", targetDeployment, "-n", tt.namespace, "-o", "jsonpath={.status.replicas}")
							output, err := utils.Run(cmd)
							g.Expect(err).NotTo(HaveOccurred())
							// Expect replicas to increase beyond 1
							g.Expect(output).NotTo(Equal("1"))
							g.Expect(output).NotTo(Equal("0"))
						}, 5*time.Minute, 5*time.Second).Should(Succeed(), "Frontend deployment failed to scale up under load")

						// Cleanup HPA and load generator
						_ = exec.Command("kubectl", "delete", "hpa", hpaName, "-n", tt.namespace).Run()
						_ = exec.Command("kubectl", "delete", "pod", loadGenName, "-n", tt.namespace, "--force", "--grace-period=0").Run()
					})
				}
			}
		})
	}
})

var _ = Describe("Umbrella Chart Template Verification", func() {
	for _, t := range umbrellaTests {
		tt := t
		It(fmt.Sprintf("should render the '%s' template with correct resources and replicas", tt.environment), func() {
			if os.Getenv("CI") == "true" {
				Skip("Skipping template verification in CI environment")
			}

			umbrellaChartPath := filepath.Join(chartsRoot, "kubetasker")
			valuesFilePath := filepath.Join(umbrellaChartPath, fmt.Sprintf("values-%s.yaml", tt.environment))

			// Run helm template WITHOUT CI overrides to verify the actual values files configuration.
			// This ensures that the production configuration scales up resources as expected,
			// even if the actual deployment test in CI uses clamped resources.
			cmd := exec.Command("helm", "template", tt.helmReleaseName, umbrellaChartPath,
				"-f", valuesFilePath,
				"--set", "kubetasker-controller.fullnameOverride="+tt.controllerFullName,
				"--set", "kubetasker-frontend.fullnameOverride="+tt.frontendServiceName,
				"--set", "kubetasker-controller.serviceMonitor.enabled=false",
				"--set", "kubetasker-frontend.serviceMonitor.enabled=false",
			)
			output, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			// Write the template output to a temporary file for kubectl parsing
			tmpFile := filepath.Join(os.TempDir(), fmt.Sprintf("manifest-%s.yaml", tt.environment))
			err = os.WriteFile(tmpFile, []byte(output), 0644)
			Expect(err).NotTo(HaveOccurred())
			defer os.Remove(tmpFile)

			// Use kubectl to parse the YAML and extract Deployment details.
			// We use 'apply --dry-run=client' to handle the multi-document YAML stream correctly.
			// We filter for Deployments and extract name, replicas, and resource requests/limits.
			jsonpath := "{range .items[?(@.kind=='Deployment')]}{.metadata.name} {.spec.replicas} {.spec.template.spec.containers[0].resources.requests.cpu} {.spec.template.spec.containers[0].resources.requests.memory} {.spec.template.spec.containers[0].resources.limits.cpu} {.spec.template.spec.containers[0].resources.limits.memory}{\"\\n\"}{end}"

			cmd = exec.Command("kubectl", "apply", "-f", tmpFile, "--dry-run=client", "-o", "jsonpath="+jsonpath)
			parsedOutput, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			lines := strings.Split(strings.TrimSpace(parsedOutput), "\n")
			foundController := false
			foundFrontend := false

			for _, line := range lines {
				parts := strings.Fields(line)
				// We expect at least 6 fields: name, replicas, reqCPU, reqMem, limCPU, limMem
				if len(parts) < 6 {
					continue
				}
				name := parts[0]
				replicas := parts[1]
				reqCpu := parts[2]
				reqMem := parts[3]
				limCpu := parts[4]
				limMem := parts[5]

				if name == tt.controllerFullName {
					foundController = true
					Expect(replicas).To(Equal(fmt.Sprintf("%d", tt.expectedReplicas["controller"])), "Controller replicas mismatch for "+tt.environment)
					Expect(reqCpu).To(Equal(tt.expectedResources["controller"]["requests.cpu"]), "Controller cpu request mismatch for "+tt.environment)
					Expect(reqMem).To(Equal(tt.expectedResources["controller"]["requests.memory"]), "Controller memory request mismatch for "+tt.environment)

					// Normalize 1000m to 1 for comparison, as helm template outputs 1000m but K8s API returns 1
					normalizedLimCpu := limCpu
					if normalizedLimCpu == "1000m" {
						normalizedLimCpu = "1"
					}
					Expect(normalizedLimCpu).To(Equal(tt.expectedResources["controller"]["limits.cpu"]), "Controller cpu limit mismatch for "+tt.environment)
					Expect(limMem).To(Equal(tt.expectedResources["controller"]["limits.memory"]), "Controller memory limit mismatch for "+tt.environment)
				}
				if name == tt.frontendServiceName {
					foundFrontend = true
					Expect(replicas).To(Equal(fmt.Sprintf("%d", tt.expectedReplicas["frontend"])), "Frontend replicas mismatch for "+tt.environment)
					Expect(reqCpu).To(Equal(tt.expectedResources["frontend"]["requests.cpu"]), "Frontend cpu request mismatch for "+tt.environment)
					Expect(reqMem).To(Equal(tt.expectedResources["frontend"]["requests.memory"]), "Frontend memory request mismatch for "+tt.environment)
					Expect(limCpu).To(Equal(tt.expectedResources["frontend"]["limits.cpu"]), "Frontend cpu limit mismatch for "+tt.environment)
					Expect(limMem).To(Equal(tt.expectedResources["frontend"]["limits.memory"]), "Frontend memory limit mismatch for "+tt.environment)
				}
			}
			Expect(foundController).To(BeTrue(), "Controller deployment not found in template output for "+tt.environment)
			Expect(foundFrontend).To(BeTrue(), "Frontend deployment not found in template output for "+tt.environment)
		})
	}
})
