//go:build e2e
// +build e2e

package e2e

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/kndclark/kubetasker/test/utils"
)

var _ = Describe("Scheduling Constraints", Ordered, func() {
	const schedulingNamespace = "kubetasker-scheduling-e2e"
	const releaseName = "kt-sched"
	var targetNode string

	BeforeAll(func() {
		By("creating the scheduling namespace")
		utils.Run(exec.Command("kubectl", "create", "ns", schedulingNamespace))

		By("identifying a node to label and taint")
		cmd := exec.Command("kubectl", "get", "nodes", "-o", "jsonpath={.items[0].metadata.name}")
		output, err := utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred())
		targetNode = strings.TrimSpace(output)
		Expect(targetNode).NotTo(BeEmpty())

		By("labeling the node for affinity test")
		_, err = utils.Run(exec.Command("kubectl", "label", "node", targetNode, "node-role=general", "--overwrite"))
		Expect(err).NotTo(HaveOccurred())

		By("deploying KubeTasker with affinity enabled")
		umbrellaChartPath := filepath.Join(chartsRoot, "kubetasker")

		// Use --set for all values to avoid external file dependency
		cmd = exec.Command("helm", "install", releaseName, umbrellaChartPath,
			"--namespace", schedulingNamespace,
			"--set", "global.imagePullPolicy=IfNotPresent",
			"--set", "kubetasker-controller.image.repository="+strings.Split(projectImage, ":")[0],
			"--set", "kubetasker-controller.image.tag="+strings.Split(projectImage, ":")[1],
			"--set", "kubetasker-controller.webhookPrefix=sched-",
			"--set", "kubetasker-frontend.image.repository="+strings.Split(frontendImage, ":")[0],
			"--set", "kubetasker-frontend.image.tag="+strings.Split(frontendImage, ":")[1],
			"--set", "kubetasker-frontend.controllerUrl=http://"+releaseName+"-kubetasker-controller:8090",
			"--set", "kubetasker-frontend.affinity.nodeAffinity.requiredDuringSchedulingIgnoredDuringExecution.nodeSelectorTerms[0].matchExpressions[0].key=node-role",
			"--set", "kubetasker-frontend.affinity.nodeAffinity.requiredDuringSchedulingIgnoredDuringExecution.nodeSelectorTerms[0].matchExpressions[0].operator=In",
			"--set", "kubetasker-frontend.affinity.nodeAffinity.requiredDuringSchedulingIgnoredDuringExecution.nodeSelectorTerms[0].matchExpressions[0].values[0]=general",
			"--wait", "--timeout=5m")

		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to deploy KubeTasker with affinity")
	})

	AfterAll(func() {
		By("cleaning up scheduling namespace and Helm release")
		utils.Run(exec.Command("helm", "uninstall", releaseName, "--namespace", schedulingNamespace))
		utils.Run(exec.Command("kubectl", "delete", "ns", schedulingNamespace, "--ignore-not-found"))

		By("cleaning up node labels and taints")
		utils.Run(exec.Command("kubectl", "label", "node", targetNode, "node-role-", "--overwrite"))
		utils.Run(exec.Command("kubectl", "taint", "node", targetNode, "kubetasker-"))

		// Also clean up any lingering cluster-scoped resources (webhooks, RBAC)
		CleanupStaleClusterResources()
	})

	Context("Configuration Inspection", func() {
		It("should have the correct Node Affinity settings on the Frontend pod", func() {
			By("inspecting the frontend pod spec for affinity rules")
			cmd := exec.Command("kubectl", "get", "pods", "-n", schedulingNamespace, "-l", "app.kubernetes.io/name=kubetasker-frontend", "-o", "jsonpath={.items[0].spec.affinity.nodeAffinity}")
			output, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())
			Expect(output).To(ContainSubstring("node-role"), "Affinity key not found")
			Expect(output).To(ContainSubstring("general"), "Affinity value not found")
		})
	})

	Context("Node Affinity Verification", func() {
		It("should have placed Frontend pod on the labeled node", func() {
			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "pods", "-n", schedulingNamespace, "-l", "app.kubernetes.io/name=kubetasker-frontend", "-o", "jsonpath={.items[0].spec.nodeName}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(strings.TrimSpace(output)).To(Equal(targetNode))
			}, "2m", "5s").Should(Succeed())
		})
	})

	Context("Taints and Tolerations Verification", func() {
		const ktaskName = "taint-test-ktask"

		It("should remain Pending if it doesn't tolerate the node taint", func() {
			By("tainting the node")
			_, err := utils.Run(exec.Command("kubectl", "taint", "node", targetNode, "kubetasker=tasks:NoSchedule", "--overwrite"))
			Expect(err).NotTo(HaveOccurred())

			By("creating a Ktask without tolerations")
			ktaskYAML := fmt.Sprintf(`
apiVersion: task.ktasker.com/v1
kind: Ktask
metadata:
  name: %s
  namespace: %s
spec:
  image: busybox
  command: ["/bin/sh", "-c", "echo unreachable; exit 0"]
`, ktaskName, schedulingNamespace)
			cmd := exec.Command("kubectl", "apply", "-f", "-")
			cmd.Stdin = strings.NewReader(ktaskYAML)
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			By("verifying the job remains Pending or Unscheduled")
			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "pods", "-n", schedulingNamespace, "-l", "job-name="+ktaskName+"-job", "-o", "json")
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
				if len(podList.Items) == 0 {
					g.Expect(false).To(BeTrue(), "Pod not yet created")
				}
				g.Expect(podList.Items[0].Status.Phase).To(Equal("Pending"))
			}, "30s", "5s").Should(Succeed())
		})

		It("should schedule and run if it tolerates the node taint", func() {
			By("creating a Ktask with tolerations")
			ktaskYAML := fmt.Sprintf(`
apiVersion: task.ktasker.com/v1
kind: Ktask
metadata:
  name: %s-tolerated
  namespace: %s
spec:
  image: busybox
  command: ["/bin/sh", "-c", "echo tolerated; exit 0"]
  tolerations:
  - key: "kubetasker"
    operator: "Equal"
    value: "tasks"
    effect: "NoSchedule"
`, ktaskName, schedulingNamespace)
			cmd := exec.Command("kubectl", "apply", "-f", "-")
			cmd.Stdin = strings.NewReader(ktaskYAML)
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			By("verifying the Ktask completes successfully")
			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "ktask", ktaskName+"-tolerated", "-n", schedulingNamespace, "-o", "jsonpath={.status.phase}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(strings.TrimSpace(output)).To(Equal("Succeeded"))
			}, "2m", "10s").Should(Succeed())
		})

		It("should confirm tolerations are present in the Pod spec", func() {
			By("inspecting the tolerated pod spec")
			// The job name is derived from the Ktask name: <ktask-name>-job
			podLabel := "job-name=" + ktaskName + "-tolerated-job"
			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "pods", "-n", schedulingNamespace, "-l", podLabel, "-o", "jsonpath={.items[0].spec.tolerations}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(ContainSubstring(`"key":"kubetasker"`))
				g.Expect(output).To(ContainSubstring(`"value":"tasks"`))
				g.Expect(output).To(ContainSubstring(`"effect":"NoSchedule"`))
			}, "1m", "5s").Should(Succeed())
		})

		AfterAll(func() {
			By("cleaning up the taint from the node")
			_, _ = utils.Run(exec.Command("kubectl", "taint", "node", targetNode, "kubetasker-"))

			By("verifying the taint is actually removed")
			cmd := exec.Command("kubectl", "get", "node", targetNode, "-o", "jsonpath={.spec.taints}")
			output, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())
			Expect(output).NotTo(ContainSubstring("kubetasker"), "Node taint was not removed successfully")
		})
	})

	Context("Pod Anti-Affinity Verification", func() {
		const antiAffinityDepName = "anti-affinity-test"

		It("should enforce anti-affinity rules", func() {
			By("deploying a deployment with required pod anti-affinity")
			depYAML := fmt.Sprintf(`
apiVersion: apps/v1
kind: Deployment
metadata:
  name: %s
  namespace: %s
  labels:
    app: %s
spec:
  replicas: 2
  selector:
    matchLabels:
      app: %s
  template:
    metadata:
      labels:
        app: %s
    spec:
      affinity:
        podAntiAffinity:
          requiredDuringSchedulingIgnoredDuringExecution:
          - labelSelector:
              matchExpressions:
              - key: app
                operator: In
                values:
                - %s
            topologyKey: "kubernetes.io/hostname"
      containers:
      - name: busybox
        image: busybox
        command: ["sleep", "3600"]
`, antiAffinityDepName, schedulingNamespace, antiAffinityDepName, antiAffinityDepName, antiAffinityDepName, antiAffinityDepName)

			cmd := exec.Command("kubectl", "apply", "-f", "-")
			cmd.Stdin = strings.NewReader(depYAML)
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			By("checking node count to determine expected behavior")
			cmd = exec.Command("kubectl", "get", "nodes", "--no-headers")
			out, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())
			nodeCount := len(strings.Split(strings.TrimSpace(out), "\n"))

			By("verifying pods scheduling status")
			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "pods", "-n", schedulingNamespace, "-l", "app="+antiAffinityDepName, "-o", "jsonpath={.items[*].status.phase}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())

				// At least one pod should be running regardless of node count
				g.Expect(output).To(ContainSubstring("Running"))

				// If we only have one node, the second pod MUST be Pending due to anti-affinity
				if nodeCount == 1 {
					g.Expect(output).To(ContainSubstring("Pending"), "Expected one pod to be Pending on single-node cluster due to anti-affinity")
				}
			}, "2m", "5s").Should(Succeed())
		})
	})

	Context("Topology Spread Constraints Verification", func() {
		const tscAppName = "tsc-test-app"

		It("should fail to schedule when topology constraints cannot be satisfied", func() {
			By("deploying a deployment with unsatisfiable topology constraints")
			// We use a topologyKey that does not exist on any node.
			// With whenUnsatisfiable: DoNotSchedule, the pods should remain Pending.
			depYAML := fmt.Sprintf(`
apiVersion: apps/v1
kind: Deployment
metadata:
  name: %s
  namespace: %s
  labels:
    app: %s
spec:
  replicas: 1
  selector:
    matchLabels:
      app: %s
  template:
    metadata:
      labels:
        app: %s
    spec:
      topologySpreadConstraints:
      - maxSkew: 1
        topologyKey: "non-existent-topology-key"
        whenUnsatisfiable: DoNotSchedule
        labelSelector:
          matchLabels:
            app: %s
      containers:
      - name: busybox
        image: busybox
        command: ["sleep", "3600"]
`, tscAppName, schedulingNamespace, tscAppName, tscAppName, tscAppName, tscAppName)

			cmd := exec.Command("kubectl", "apply", "-f", "-")
			cmd.Stdin = strings.NewReader(depYAML)
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			By("verifying the pod remains Pending")
			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "pods", "-n", schedulingNamespace, "-l", "app="+tscAppName, "-o", "jsonpath={.items[0].status.phase}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("Pending"))
			}, "30s", "5s").Should(Succeed())
		})
	})
})
