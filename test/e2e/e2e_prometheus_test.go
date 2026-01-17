//go:build e2e
// +build e2e

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
	prometheusNamespace = "monitoring"
	prometheusRelease   = "kube-prometheus-stack"
	testNamespace       = "kubetasker-prometheus-e2e"
	testRelease         = "kt-prom" // Shortened to avoid 63-char Kubernetes name limit
)

var _ = Describe("Prometheus Integration", Ordered, func() {
	BeforeAll(func() {
		// Cleanup previous runs if any to ensure a clean state as requested
		By("cleaning up previous test runs and potentially conflicting namespaces")
		
		// Namespaces used in different E2E tests
		namespacesToClear := []string{testNamespace, "kubetasker-system-e2e", "kubetasker-system"}
		for _, ns := range namespacesToClear {
			cmd := exec.Command("kubectl", "delete", "namespace", ns, "--ignore-not-found")
			_, _ = utils.Run(cmd)
		}

		// Also uninstall any existing releases with the test release name
		cmd := exec.Command("helm", "uninstall", testRelease, "--namespace", testNamespace, "--ignore-not-found")
		_, _ = utils.Run(cmd)
		
		// Some controllers might be in kube-system or other namespaces if installed via different means
		cmd = exec.Command("kubectl", "delete", "deployment", "kubetasker-controller", "-n", "kube-system", "--ignore-not-found")
		_, _ = utils.Run(cmd)

		// Check if Prometheus is already installed
		By("checking if Prometheus is already installed")
		cmd = exec.Command("kubectl", "get", "namespace", prometheusNamespace)
		_, err := utils.Run(cmd)
		
		if err != nil {
			By("installing Prometheus Operator (kube-prometheus-stack)")
			// Add Prometheus Helm repo
			cmd = exec.Command("helm", "repo", "add", "prometheus-community", 
				"https://prometheus-community.github.io/helm-charts")
			_, _ = utils.Run(cmd) // May already exist
			
			cmd = exec.Command("helm", "repo", "update")
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to update Helm repos")
			
			// Install kube-prometheus-stack
			cmd = exec.Command("helm", "install", prometheusRelease, 
				"prometheus-community/kube-prometheus-stack",
				"--namespace", prometheusNamespace,
				"--create-namespace",
				"--set", "prometheus.prometheusSpec.serviceMonitorSelectorNilUsesHelmValues=false",
				"--wait",
				"--timeout", "5m")
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to install Prometheus")
		} else {
			_, _ = fmt.Fprintf(GinkgoWriter, "Prometheus is already installed, skipping installation\n")
		}
		
		// Wait for Prometheus to be ready
		By("waiting for Prometheus operator to be ready")
		Eventually(func(g Gomega) {
			cmd := exec.Command("kubectl", "get", "pods", "-n", prometheusNamespace,
				"-l", "app.kubernetes.io/name=prometheus", "-o", "jsonpath={.items[0].status.phase}")
			output, err := utils.Run(cmd)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(output).To(Equal("Running"))
		}, 5*time.Minute, 5*time.Second).Should(Succeed())
		
		// Create test namespace
		By("creating test namespace")
		cmd = exec.Command("kubectl", "create", "namespace", testNamespace, "--dry-run=client", "-o", "yaml")
		nsYAML, err := utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred())
		
		cmd = exec.Command("kubectl", "apply", "-f", "-")
		cmd.Stdin = strings.NewReader(nsYAML)
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred())
	})
	
	AfterAll(func() {
		By("cleaning up test namespace")
		cmd := exec.Command("kubectl", "delete", "namespace", testNamespace, "--ignore-not-found")
		_, _ = utils.Run(cmd)
		
		// Note: We don't uninstall Prometheus here as it might be shared across tests
		// Manual cleanup: helm uninstall kube-prometheus-stack -n monitoring
	})
	
	Context("when deploying KubeTasker with ServiceMonitors", func() {
		BeforeAll(func() {
			By("deploying KubeTasker with ServiceMonitors enabled")
			umbrellaChartPath := chartsRoot + "/kubetasker"
			
			helmArgs := []string{
				"install", testRelease, umbrellaChartPath,
				"--namespace", testNamespace,
				"--set", fmt.Sprintf("kubetasker-controller.image.repository=%s", strings.Split(projectImage, ":")[0]),
				"--set", fmt.Sprintf("kubetasker-controller.image.tag=%s", strings.Split(projectImage, ":")[1]),
				"--set", fmt.Sprintf("kubetasker-frontend.image.repository=%s", strings.Split(frontendImage, ":")[0]),
				"--set", fmt.Sprintf("kubetasker-frontend.image.tag=%s", strings.Split(frontendImage, ":")[1]),
				"--set", "global.imagePullPolicy=IfNotPresent",
				"--set", "kubetasker-controller.serviceMonitor.enabled=true",
				"--set", "kubetasker-controller.serviceMonitor.namespace=" + prometheusNamespace,
				"--set", "kubetasker-frontend.serviceMonitor.enabled=true",
				"--set", "kubetasker-frontend.serviceMonitor.namespace=" + prometheusNamespace,
				"--set", fmt.Sprintf("kubetasker-frontend.controllerUrl=http://%s-kubetasker-controller:8090", testRelease),
				"--wait",
				"--timeout", "3m",
			}
			
			cmd := exec.Command("helm", helmArgs...)
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to deploy KubeTasker with ServiceMonitors")
			
			// Wait for pods to be ready
			By("waiting for KubeTasker pods to be ready")
			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "pods", "-n", testNamespace, 
					"-o", "jsonpath={.items[*].status.containerStatuses[*].ready}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				// Check that at least some containers are present and they are all "true"
				g.Expect(output).To(ContainSubstring("true"))
				g.Expect(output).NotTo(ContainSubstring("false"))
			}, 3*time.Minute, 10*time.Second).Should(Succeed())
		})
		
		AfterAll(func() {
			By("uninstalling KubeTasker test release")
			cmd := exec.Command("helm", "uninstall", testRelease, "--namespace", testNamespace)
			_, _ = utils.Run(cmd)
			
			// Cleanup the persistent curl pod
			By("cleaning up the persistent curl pod")
			deleteCmd := exec.Command("kubectl", "delete", "pod", "curl-metrics-debug", "--namespace", testNamespace, "--ignore-not-found", "--now")
			_, _ = utils.Run(deleteCmd)
		})
		
		var curlPodName = "curl-metrics-debug"
		BeforeAll(func() {
			By("ensuring a persistent curl pod for diagnostics")
			// Create a pod that sleeps forever so we can exec into it many times
			cmd := exec.Command("kubectl", "run", curlPodName, "--image=curlimages/curl:latest",
				"--namespace", testNamespace, "--restart=Never", "--", "/bin/sh", "-c", "sleep 3600")
			_, _ = utils.Run(cmd) // Ignore error if it already exists
			
			waitCmd := exec.Command("kubectl", "wait", "--for=condition=Ready", "pod/"+curlPodName,
				"--namespace", testNamespace, "--timeout=60s")
			_, _ = utils.Run(waitCmd)
		})
		
		It("should create ServiceMonitor resources", func() {
			By("verifying controller and frontend ServiceMonitor exists")
			// ServiceMonitor names include the release name prefix
			cmd := exec.Command("kubectl", "get", "servicemonitor", 
				"-n", prometheusNamespace,
				"-o", "jsonpath={.items[*].metadata.name}")
			output, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())
			Expect(output).To(ContainSubstring("kubetasker-controller"))
			Expect(output).To(ContainSubstring("kubetasker-frontend"))
		})
		
		It("should have controller metrics endpoint accessible", func() {
			By("verifying controller metrics endpoint responds")
			
			// Get service account token from the correct namespace and service account
			// The service account name includes the release name prefix
			serviceAccountName := testRelease + "-kubetasker-controller"
			token, err := serviceAccountToken(serviceAccountName, testNamespace)
			Expect(err).NotTo(HaveOccurred())
			
			// The controller exposes metrics on HTTPS port 8443
			// Service name includes release prefix
			serviceName := testRelease + "-kubetasker-controller-metrics-service"
			curlCmd := fmt.Sprintf("curl -k -s -H 'Authorization: Bearer %s' https://%s.%s.svc.cluster.local:8443/metrics",
				token, serviceName, testNamespace)
			
			Eventually(func(g Gomega) {
				// Execute directly instead of runInCurlPod to reuse the pod
				execCmd := exec.Command("kubectl", "exec", curlPodName, "--namespace", testNamespace, "--", "/bin/sh", "-c", curlCmd)
				output, err := utils.Run(execCmd)
				g.Expect(err).NotTo(HaveOccurred(), "Curl command failed: %v", err)
				
				// Check for standard Prometheus metrics
				g.Expect(output).To(ContainSubstring("# HELP"), "Metrics output was empty or invalid. Output: %s", output)
				// Check for controller-runtime metrics (custom kubetasker metrics may not be present yet)
				g.Expect(output).To(ContainSubstring("controller_runtime"))
			}, 2*time.Minute, 5*time.Second).Should(Succeed())
		})
		
		It("should have frontend metrics endpoint accessible", func() {
			By("verifying frontend metrics endpoint responds")
			
			serviceName := testRelease + "-kubetasker-frontend"
			curlCmd := fmt.Sprintf("curl -s http://%s.%s.svc.cluster.local:8000/metrics",
				serviceName, testNamespace)
			
			Eventually(func(g Gomega) {
				// Execute directly instead of runInCurlPod to reuse the pod
				execCmd := exec.Command("kubectl", "exec", curlPodName, "--namespace", testNamespace, "--", "/bin/sh", "-c", curlCmd)
				output, err := utils.Run(execCmd)
				g.Expect(err).NotTo(HaveOccurred(), "Curl command failed: %v", err)
				
				// Check for standard Prometheus metrics
				g.Expect(output).To(ContainSubstring("# HELP"), "Metrics output was empty or invalid. Output: %s", output)
				// Check for custom KubeTasker frontend metrics
				g.Expect(output).To(ContainSubstring("ktask_status_count"))
			}, 2*time.Minute, 5*time.Second).Should(Succeed())
		})
		
		It("should configure Prometheus to scrape KubeTasker targets", func() {
			By("checking Prometheus targets include KubeTasker services")
			
			// Port-forward to Prometheus to check targets
			// Note: This is a simplified check. In production, you'd query the Prometheus API
			Eventually(func(g Gomega) {
				// Check if ServiceMonitor exists and has selectors
				// Get the first kubetasker ServiceMonitor
				cmd := exec.Command("kubectl", "get", "servicemonitor", 
					"-n", prometheusNamespace,
					"-l", "app.kubernetes.io/name=kubetasker-controller",
					"-o", "jsonpath={.items[0].spec.selector.matchLabels}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).NotTo(BeEmpty())
			}, 1*time.Minute, 5*time.Second).Should(Succeed())
		})
		
		It("should track ktask_status_count metric when Ktasks are created", func() {
			By("creating a test Ktask")
			ktaskYAML := fmt.Sprintf(`
apiVersion: task.ktasker.com/v1
kind: Ktask
metadata:
  name: metrics-test-ktask
  namespace: %s
spec:
  image: busybox
  command: ["/bin/sh", "-c", "echo 'Testing metrics'; sleep 5"]
`, testNamespace)
			
			cmd := exec.Command("kubectl", "apply", "-f", "-")
			cmd.Stdin = strings.NewReader(ktaskYAML)
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())
			
			// Give the metrics collector time to scrape
			time.Sleep(35 * time.Second)
			
			By("verifying ktask_status_count metric is updated")
			// Service name includes release prefix
			serviceName := testRelease + "-kubetasker-frontend"
			curlCmd := fmt.Sprintf("curl -s http://%s.%s.svc.cluster.local:8000/metrics | grep ktask_status_count",
				serviceName, testNamespace)
			
			Eventually(func(g Gomega) {
				// Execute directly instead of runInCurlPod to reuse the pod
				execCmd := exec.Command("kubectl", "exec", curlPodName, "--namespace", testNamespace, "--", "/bin/sh", "-c", curlCmd)
				output, err := utils.Run(execCmd)
				g.Expect(err).NotTo(HaveOccurred(), "Curl command failed: %v", err)
				
				// Should see the metric with the test namespace
				g.Expect(output).To(ContainSubstring(fmt.Sprintf(`namespace="%s"`, testNamespace)), "Metric for namespace %s not found in output: %s", testNamespace, output)
				// Should track at least one Ktask
				g.Expect(output).To(MatchRegexp(`ktask_status_count.*} \d+`))
			}, 1*time.Minute, 5*time.Second).Should(Succeed())
			
			// Cleanup
			cmd = exec.Command("kubectl", "delete", "ktask", "metrics-test-ktask", 
				"-n", testNamespace, "--ignore-not-found")
			_, _ = utils.Run(cmd)
		})
	})
})
