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

// kustomizeTest holds the configuration for a single Kustomize environment test.
type kustomizeTest struct {
	environment       string
	namespace         string
	helmReleaseName   string // Used for templating
	expectedReplicas  map[string]int
	expectedResources map[string]map[string]string
}

// Define tests for each environment to be deployed via Kustomize.
var kustomizeTests = []kustomizeTest{
	{
		environment:      "dev",
		namespace:        "dev", // Kustomize overlay sets this
		helmReleaseName:  "kubetasker-dev",
		expectedReplicas: map[string]int{"controller": 1, "frontend": 1},
		expectedResources: map[string]map[string]string{
			"controller": {"requests.cpu": "100m", "requests.memory": "128Mi", "limits.cpu": "200m", "limits.memory": "256Mi"},
			"frontend":   {"requests.cpu": "50m", "requests.memory": "64Mi", "limits.cpu": "100m", "limits.memory": "128Mi"},
		},
	},
	{
		environment:      "staging",
		namespace:        "staging",
		helmReleaseName:  "kubetasker-staging",
		expectedReplicas: map[string]int{"controller": 2, "frontend": 2},
		expectedResources: map[string]map[string]string{
			"controller": {"requests.cpu": "250m", "requests.memory": "256Mi", "limits.cpu": "500m", "limits.memory": "512Mi"},
			"frontend":   {"requests.cpu": "100m", "requests.memory": "128Mi", "limits.cpu": "250m", "limits.memory": "256Mi"},
		},
	},
	{
		environment:      "prod",
		namespace:        "prod",
		helmReleaseName:  "kubetasker-prod",
		expectedReplicas: map[string]int{"controller": 2, "frontend": 3},
		expectedResources: map[string]map[string]string{
			"controller": {"requests.cpu": "500m", "requests.memory": "512Mi", "limits.cpu": "1", "limits.memory": "1Gi"},
			"frontend":   {"requests.cpu": "200m", "requests.memory": "256Mi", "limits.cpu": "500m", "limits.memory": "512Mi"},
		},
	},
}

var _ = Describe("Kustomize Deployments", Ordered, func() {
	for _, t := range kustomizeTests {
		// Capture the test struct for the closure.
		tt := t

		Context(fmt.Sprintf("when deploying the '%s' environment", tt.environment), func() {

			BeforeAll(func() {
				By(fmt.Sprintf("generating manifests for the '%s' environment", tt.environment))
				umbrellaChartPath := filepath.Join(chartsRoot, "kubetasker")
				valuesFilePath := filepath.Join(umbrellaChartPath, fmt.Sprintf("values-%s.yaml", tt.environment))
				kustomizeBaseDir := filepath.Join(projectRootDir, "kustomize", "base")
				err := os.MkdirAll(kustomizeBaseDir, 0755)
				Expect(err).NotTo(HaveOccurred())
				manifestFile := filepath.Join(kustomizeBaseDir, "all.yaml")

				helmTemplateCmd := exec.Command("helm", "template", tt.helmReleaseName, umbrellaChartPath,
					"-f", valuesFilePath,
					"--namespace", tt.namespace, // This is the key fix
					// Override images to use the ones built for the test environment
					"--set", fmt.Sprintf("kubetasker-controller.image.repository=%s", strings.Split(projectImage, ":")[0]),
					"--set", fmt.Sprintf("kubetasker-controller.image.tag=%s", strings.Split(projectImage, ":")[1]),
					"--set", fmt.Sprintf("kubetasker-frontend.image.repository=%s", strings.Split(frontendImage, ":")[0]),
					"--set", fmt.Sprintf("kubetasker-frontend.image.tag=%s", strings.Split(frontendImage, ":")[1]),
					// Ensure image pull policy is set for the local Kind cluster
					"--set", "global.imagePullPolicy=IfNotPresent",
					// Explicitly set the webhook service namespace for the certificate
					"--set", "kubetasker-controller.webhook.service.namespace="+tt.namespace,
				)
				helmOutput, err := utils.Run(helmTemplateCmd)
				Expect(err).NotTo(HaveOccurred())
				err = os.WriteFile(manifestFile, []byte(helmOutput), 0644)
				Expect(err).NotTo(HaveOccurred())

				By("copying the authoritative CRD to the kustomize base")
				crdSourcePath := filepath.Join(projectRootDir, "config", "crd", "bases", "task.ktasker.com_ktasks.yaml")
				crdDestPath := filepath.Join(kustomizeBaseDir, "crd.yaml")
				crdBytes, err := os.ReadFile(crdSourcePath)
				Expect(err).NotTo(HaveOccurred(), "Failed to read authoritative CRD from config/crd/bases")
				err = os.WriteFile(crdDestPath, crdBytes, 0644)
				Expect(err).NotTo(HaveOccurred())

				// Kustomize requires a kustomization.yaml in the base directory.
				// This file will declare the generated all.yaml as a resource.
				baseKustomizationContent := `
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization
resources:
  - crd.yaml
  - all.yaml
`
				err = os.WriteFile(filepath.Join(kustomizeBaseDir, "kustomization.yaml"), []byte(baseKustomizationContent), 0644)
				Expect(err).NotTo(HaveOccurred())

				By(fmt.Sprintf("creating namespace %s for kustomize test", tt.namespace))
				cmd := exec.Command("kubectl", "create", "ns", tt.namespace, "--dry-run=client", "-o", "yaml")
				nsYAML, err := utils.Run(cmd)
				Expect(err).NotTo(HaveOccurred())
				cmd = exec.Command("kubectl", "apply", "-f", "-")
				cmd.Stdin = strings.NewReader(nsYAML)
				_, err = utils.Run(cmd)
				Expect(err).NotTo(HaveOccurred())

				By(fmt.Sprintf("deploying KubeTasker for '%s' using 'kubectl apply -k'", tt.environment))
				overlayPath := filepath.Join(projectRootDir, "kustomize", "overlays", tt.environment)
				applyCmd := exec.Command("kubectl", "apply", "-k", overlayPath)
				_, err = utils.Run(applyCmd)
				Expect(err).NotTo(HaveOccurred(), "Failed to apply kustomized manifests for env: "+tt.environment)

				By("verifying all pods are running")
				Eventually(func(g Gomega) {
					cmd := exec.Command("kubectl", "wait", "pod", "-l", "app.kubernetes.io/name=kubetasker-controller",
						"--for=condition=Ready", "--timeout=90s", "-n", tt.namespace)
					_, err := utils.Run(cmd)
					g.Expect(err).NotTo(HaveOccurred(), "Controller pods for env %s did not become ready", tt.environment)

					cmd = exec.Command("kubectl", "wait", "pod", "-l", "app.kubernetes.io/name=kubetasker-frontend",
						"--for=condition=Ready", "--timeout=90s", "-n", tt.namespace)
					_, err = utils.Run(cmd)
					g.Expect(err).NotTo(HaveOccurred(), "Frontend pods for env %s did not become ready", tt.environment)
				}).WithTimeout(2 * time.Minute).Should(Succeed())
			})

			AfterAll(func() {
				By(fmt.Sprintf("cleaning up the %s environment with Kustomize", tt.environment))
				overlayPath := filepath.Join(projectRootDir, "kustomize", "overlays", tt.environment)
				// Use 'kubectl delete -k' for cleanup as well
				_, _ = utils.Run(exec.Command("kubectl", "delete", "--ignore-not-found=true", "-k", overlayPath))
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
					const ktaskName = "test-ktask-via-kustomize"
					const jobName = ktaskName + "-job"
					ktaskJSON := fmt.Sprintf(`{"apiVersion":"task.ktasker.com/v1","kind":"Ktask","metadata":{"name":"%s","namespace":"%s"},"spec":{"image":"busybox","command":["/bin/sh","-c","echo 'Hello from kustomize test'"]}}`, ktaskName, tt.namespace)

					By("posting a new Ktask to the frontend service")
					posterPodName := "curl-poster-kustomize"
					// The service name is templated by Helm, e.g., "kubetasker-dev-kubetasker-frontend"
					shellCmd := fmt.Sprintf("echo '%s' > /tmp/payload.json && curl -s -X POST -H 'Content-Type: application/json' -d @/tmp/payload.json http://%s-kubetasker-frontend.%s.svc.cluster.local:8000/ktask -o /dev/null -w %%{http_code}",
						ktaskJSON, tt.helmReleaseName, tt.namespace)

					output, err := runInCurlPod(posterPodName, tt.namespace, shellCmd)
					Expect(err).NotTo(HaveOccurred())
					Expect(strings.TrimSpace(output)).To(HavePrefix("200"), "Frontend service should return 200 OK")

					By("verifying the underlying Job is created and completes successfully")
					Eventually(func(g Gomega) {
						cmd := exec.Command("kubectl", "get", "job", jobName,
							"-n", tt.namespace, "-o", "jsonpath={.status.conditions[?(@.type=='Complete')].status}")
						output, err := utils.Run(cmd)
						g.Expect(err).NotTo(HaveOccurred())
						g.Expect(output).To(Equal("True"), "Job should have a Complete condition with status True")
					}).WithTimeout(2 * time.Minute).Should(Succeed())
				})
			}
		})
	}
})
