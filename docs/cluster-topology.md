# Cluster Topology Assumptions

KubeTasker is designed and tested against a local multi-node Kubernetes cluster using Kind, and assumes the following cluster topology characteristics.

## Supported / Tested Environments

| Environment | Status | Notes |
| :--- | :--- | :--- |
| Kind (multi-node) | ✅ Primary | Used for development and E2E tests |
| Minikube | ⚠️ Untested | Expected to work with multi-node config |
| Managed K8s (EKS/GKE/AKS) | ⚠️ Untested | No provider-specific features used |

## Kind Cluster Configuration

E2E tests assume a multi-node Kind cluster with:

*   1 control-plane node
*   ≥2 worker nodes

Example Kind configuration:

```yaml
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
nodes:
  - role: control-plane
  - role: worker (i.e. controller)
  - role: worker (i.e. frontend)
```

## Scheduling Assumptions

KubeTasker relies on standard Kubernetes scheduler behavior and assumes:

### Node Identity

Nodes expose the label:

*   `kubernetes.io/hostname`

Used for:

*   Topology Spread Constraints
*   Node-level task distribution

### Scheduler Features Used

The following Kubernetes scheduling primitives are expected to be supported:

*   Node Affinity
*   Taints and Tolerations
*   Topology Spread Constraints

These features are:

*   Available in Kubernetes ≥ 1.19
*   Enabled by default in Kind clusters

## Task Placement Model

KubeTasker schedules task pods with the following expectations:

*   Tasks may be constrained to specific nodes via:
    *   Node labels
    *   Taints / tolerations
*   When topology spread constraints are enabled:
    *   Tasks should be evenly distributed across available worker nodes
    *   Pods may remain Pending if constraints cannot be satisfied

This behavior is validated in the E2E test suite.
