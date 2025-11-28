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
2.  **`kubetasker-frontend`**: A Python-based web service that exposes a REST API (`/ktask`) for creating `Ktask` resources programmatically.

The project uses a Kustomize structure to manage configurations for different environments. The `base` contains the common resources, and the `overlays` apply environment-specific patches (e.g., for `dev`, `staging`, `prod`).

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
*   **Metrics Server** (Optional): If you plan to enable the Horizontal Pod Autoscaler (HPA) for the frontend service, the Kubernetes Metrics Server must be installed in your cluster.

### Deployment

You can deploy KubeTasker using Kustomize (which wraps Helm) or by using Helm directly.

#### Using Helm

For more direct control, you can deploy KubeTasker using its Helm chart located in `helm/kubetasker`. This project provides pre-configured values files for different environments:

*   `helm/kubetasker/values-dev.yaml`: Minimal resources suitable for local development.
*   `helm/kubetasker/values-staging.yaml`: Increased resources and replicas for staging/testing environments.
*   `helm/kubetasker/values-prod.yaml`: Production-ready configuration with higher resources, more replicas, and pod anti-affinity for high availability.

To deploy to a specific environment, use the `-f` flag with `helm install`.

**Deploy to Development:**
```sh
helm install kubetasker-dev helm/kubetasker -f helm/kubetasker/values-dev.yaml --namespace dev --create-namespace
```

**Deploy to Staging:**
```sh
helm install kubetasker-staging helm/kubetasker -f helm/kubetasker/values-staging.yaml --namespace staging --create-namespace
```

**Deploy to Production:**
```sh
helm install kubetasker-prod helm/kubetasker -f helm/kubetasker/values-prod.yaml --namespace prod --create-namespace
```

#### Using Kustomize

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

### Using the REST API

The `kubetasker-frontend` service provides a REST endpoint for creating `Ktasks`. This is useful for programmatic access or integration with other services.

1.  **Port-forward the frontend service:**
    ```sh
    kubectl port-forward svc/kubetasker-dev-kubetasker-frontend 8000:8000 -n dev
    ```

2.  **Send a POST request with `curl`:**
    ```sh
    curl -X POST http://localhost:8000/ktask \
    -H "Content-Type: application/json" \
    -d '{
      "apiVersion": "task.ktasker.com/v1",
      "kind": "Ktask",
      "metadata": { "name": "hello-from-api", "namespace": "dev" },
      "spec": { "image": "busybox", "command": ["echo", "Hello from the API"] }
    }'
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

2.  **In a separate terminal, run the frontend locally:**
    This command builds and runs the frontend container, exposing it on `localhost:8000`.
    ```sh
    make run-frontend-local
    ```

## 🤝 Contributing

Contributions are welcome! Feel free to open an issue or submit a pull request. To see my project roadmap and current tasks, check out my Trello board.

## 📄 License

This project is licensed under the Apache 2.0 License. See the `LICENSE` file for details.
