# kubetasker

[![Project Status: WIP – Initial development is in progress, but there has not yet been a stable, usable release suitable for the public.](https://www.repostatus.org/badges/latest/wip.svg)](https://www.repostatus.org/#wip)

KubeTasker is currently under active development and should be considered experimental. While the core features are functional, it is not yet recommended for production use. APIs may change, and there might be bugs. Feedback and contributions are highly encouraged!

[![E2E Tests](https://github.com/kndclark/kubetasker/actions/workflows/test-e2e.yml/badge.svg)](https://github.com/kndclark/kubetasker/actions/workflows/test-e2e.yml)
[![Go Tests](https://github.com/kndclark/kubetasker/actions/workflows/test.yml/badge.svg)](https://github.com/kndclark/kubetasker/actions/workflows/test.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/kndclark/kubetasker)](https://goreportcard.com/report/github.com/kndclark/kubetasker)
[![Trello](https://img.shields.io/badge/Trello-board-blue.svg)](https://trello.com/b/MduBZGNS/kubetasker)

**KubeTasker** is a lightweight, distributed job scheduler for Kubernetes. It simplifies running one-off or batch jobs by providing a simple Custom Resource Definition (CRD), `Ktask`, which abstracts away the complexity of managing underlying Kubernetes `Job` objects.

## ✨ Features

*   **Simple Job Definitions**: Define tasks with a simple `Ktask` Custom Resource, specifying just the container image and command.
*   **REST API**: A frontend service provides an HTTP endpoint to create and manage `Ktasks` without needing `kubectl`.
*   **Helm & Kustomize Support**: Easily deploy KubeTasker to different environments (`dev`, `staging`, `prod`) with pre-configured Helm charts and Kustomize overlays.
*   **Built with Kubebuilder**: A robust controller built on the popular Kubebuilder framework.

## 🏛️ Architecture

KubeTasker consists of two main components:

1.  **`kubetasker-controller`**: The Go-based Kubernetes controller that watches for `Ktask` resources and creates, manages, and cleans up the corresponding Kubernetes `Job`s.
2.  **`kubetasker-frontend`**: A Python-based web service that exposes a REST API (`/ktask`) and a web GUI for creating and managing `Ktask` resources.

### Deployment Options

*   **Umbrella Chart**: A unified Helm chart in `helm/kubetasker` that deploys both the controller and frontend (and optionally `cert-manager` and `kube-prometheus-stack`).
*   **Kustomize Overlays**: Environment-specific configurations (`dev`, `staging`, `prod`) that use Helm to template the base manifests.
*   **Individual Charts**: Separate Helm charts for `kubetasker-controller` and `kubetasker-frontend` for granular control.

```
kustomize/
├── base/
│   └── kustomization.yaml  # Common resources (controller, frontend, CRD)
└── overlays/
    ├── dev/
    │   └── kustomization.yaml  # Patches for the 'dev' environment
    ├── staging/
    │   └── kustomization.yaml  # Patches for the 'staging' environment
    └── prod/
        └── kustomization.yaml  # Patches for the 'prod' environment
```

Running `kubectl apply -k kustomize/overlays/<env>` will build and deploy the manifests for the specified environment.

---

## 🚀 Getting Started

### Prerequisites

*   A running Kubernetes cluster (e.g., `kind`, `minikube`, or a cloud provider's cluster).
*   `kubectl` installed and configured.
*   `helm` v3+ installed.

### Deployment

You can deploy KubeTasker using the unified Umbrella Chart, Kustomize, or individual Helm charts.

#### 1. Unified Deployment (Umbrella Chart)

The recommended way to deploy the full KubeTasker stack is using the umbrella chart in `helm/kubetasker`.

**Deploy to Development (K8s Namespace: `kubetasker-system`):**
```sh
make deploy-umbrella
```

**Deploy to Staging/Production:**
```sh
# Staging
helm upgrade --install kubetasker helm/kubetasker -f helm/kubetasker/values-staging.yaml --namespace staging --create-namespace
# Production
helm upgrade --install kubetasker helm/kubetasker -f helm/kubetasker/values-prod.yaml --namespace prod --create-namespace
```

#### 2. Monitoring with Prometheus

KubeTasker comes with built-in support for Prometheus monitoring.

**Install Prometheus Stack & KubeTasker with Monitoring:**
```sh
make deploy-monitoring
```
This command:
1. Installs the `kube-prometheus-stack` (Prometheus, Grafana, Operator).
2. Deploys KubeTasker with `ServiceMonitor` resources enabled.

**Access Dashboards:**
*   **Prometheus**: `make dashboard-prometheus` (http://localhost:9090)
*   **Grafana**: `make dashboard-grafana` (See [PROMETHEUS_SETUP.md](docs/PROMETHEUS_SETUP.md) for credentials)

#### 3. Using Kustomize

1.  **Clone the repository:**
    ```sh
    git clone https://github.com/kndclark/kubetasker.git
    cd kubetasker
    ```

2.  **Build Helm Dependencies:**
    The Kustomize base uses Helm to template the initial manifests.
    ```sh
    helm dependency build helm/kubetasker
    ```

3.  **Deploy the `dev` environment:**
    This command applies the Kustomize overlay for the `dev` environment, which creates the `dev` namespace and deploys all necessary resources.
    ```sh
    kubectl apply -k kustomize/overlays/dev
    ```

4.  **Verify the deployment:**
    Check that the controller and frontend pods are running in the `dev` namespace.
    ```sh
    kubectl get pods -n dev
    ```
    You should see output similar to this:
    ```
    NAME                                             READY   STATUS    RESTARTS   AGE
    kubetasker-dev-kubetasker-controller-pod...      1/1     Running   0          1m
    kubetasker-dev-kubetasker-frontend-pod...        1/1     Running   0          1m
    ```

## 📝 Usage

Once deployed, you can create a `Ktask` to run a job.

### Using `kubectl`

1.  **Create a `Ktask` manifest:**
    Save the following YAML as `my-first-ktask.yaml`:
    ```yaml
    apiVersion: task.ktasker.com/v1
    kind: Ktask
    metadata:
      name: hello-world-task
      namespace: dev
    spec:
      image: busybox
      command: ["/bin/sh", "-c", "echo 'Hello from KubeTasker!' && sleep 10 && echo 'Done!'"]
    ```

2.  **Apply the manifest:**
    ```sh
    kubectl apply -f my-first-ktask.yaml
    ```

3.  **Check the results:**
    The controller will create a Kubernetes `Job` named `hello-world-task-job`. You can view its logs to see the output.
    ```sh
    # Wait for the job to complete
    kubectl wait --for=condition=complete job/hello-world-task-job -n dev --timeout=60s

    # Check the logs of the pod created by the job
    kubectl logs -n dev -l job-name=hello-world-task-job
    ```

### Using the GUI Dashboard

KubeTasker includes a web-based dashboard for creating and monitoring tasks.

1.  **Port-forward the frontend service:**
    ```sh
    make dashboard
    # Or manually:
    # kubectl port-forward svc/kubetasker-kubetasker-frontend 8000:8000 -n kubetasker-system
    ```

2.  **Open the Dashboard:**
    Navigate to [http://localhost:8000](http://localhost:8000) in your browser.

    From the dashboard, you can:
    *   **Create**: Fill out the form to launch a new `Ktask`.
    *   **Monitor**: View real-time status updates for tasks in the selector namespace.
    *   **Delete**: Click the "Delete" button next to any task to remove it and its associated Kubernetes resources.

### Using the REST API

The `kubetasker-frontend` service provides a REST endpoint for creating `Ktasks`. This is useful for programmatic access or integration with other services.

1.  **Port-forward the frontend service:**
    ```sh
    make dashboard
    ```

2.  **Create a Ktask (POST):**
    ```sh
    curl -X POST http://localhost:8000/ktask \
    -H "Content-Type: application/json" \
    -d '{
      "apiVersion": "task.ktasker.com/v1",
      "kind": "Ktask",
      "metadata": { "name": "hello-api", "namespace": "default" },
      "spec": { "image": "busybox", "command": ["echo", "Hello from API"] }
    }'
    ```

3.  **List Ktasks (GET):**
    ```sh
    curl http://localhost:8000/ktask?namespace=default
    ```

4.  **Delete a Ktask (DELETE):**
    ```sh
    curl -X DELETE http://localhost:8000/ktask/hello-api?namespace=default
    ```

## 💻 Development & Testing

This project includes a full suite of unit, integration, and end-to-end (E2E) tests.

*   **Run Go controller tests:**
    ```sh
    make test
    ```
*   **Run Python frontend tests:**
    ```sh
    make pyenv
    source .kubetasker_pyenv/bin/activate
    pytest
    ```
*   **Run E2E tests (requires `kind`):**
    ```sh
    make test-e2e
    ```

### Local Development

For a faster development loop, you can run the controller and frontend locally against your Kubernetes cluster. This setup uses your local Go and Python environments and connects to the cluster specified in your `kubeconfig`.

1.  **Run the controller locally:**
    This command starts the controller on your machine with webhooks disabled for simplicity.
    ```sh
    make run-local
    ```

2.  **Run the frontend locally:**
    You can run the frontend in a container or directly with Python (faster for development).

    **Option A: Run in Docker**
    ```sh
    make run-frontend-local
    ```

    **Option B: Run with Uvicorn (requires `make pyenv`)**
    ```sh
    make run-frontend-dev
    ```

## 🤝 Contributing

Contributions are welcome! Feel free to open an issue or submit a pull request. To see my project roadmap and current tasks, check out my Trello board.

## 📄 License

This project is licensed under the Apache 2.0 License. See the `LICENSE` file for details.
