# Version to use for building/pushing image targets
VERSION ?= v0.0.1

# Image URL to use all building/pushing image targets
IMG ?= ktasker.com/kubetasker-controller:$(VERSION)

# Image URL for the frontend service
FRONTEND_IMG ?= ktasker.com/kubetasker-frontend:$(VERSION)

# Get the currently used golang install path (in GOPATH/bin, unless GOBIN is set)
ifeq (,$(shell go env GOBIN))
GOBIN=$(shell go env GOPATH)/bin
else
GOBIN=$(shell go env GOBIN)
endif

# CONTAINER_TOOL defines the container tool to be used for building images.
# Be aware that the target commands are only tested with Docker which is
# scaffolded by default. However, you might want to replace it to use other
# tools. (i.e. podman)
CONTAINER_TOOL ?= docker

# Setting SHELL to bash allows bash commands to be executed by recipes.
# Options are set to exit when a recipe line exits non-zero or a piped command fails.
SHELL = /usr/bin/env bash -o pipefail
.SHELLFLAGS = -ec

PYTHON=python3
PYVENV=.kubetasker_pyenv

CONTROLLER=kubetasker-controller
FRONTEND=kubetasker-frontend
FRONTEND_PORT=8000
CONTROLLER_PORT=8090

# Path to the Helm charts directory
CHART_ROOT ?= helm

.PHONY: all
all: build

##@ General

# The help target prints out all targets with their descriptions organized
# beneath their categories. The categories are represented by '##@' and the
# target descriptions by '##'. The awk command is responsible for reading the
# entire set of makefiles included in this invocation, looking for lines of the
# file as xyz: ## something, and then pretty-format the target and help. Then,
# if there's a line with ##@ something, that gets pretty-printed as a category.
# More info on the usage of ANSI control characters for terminal formatting:
# https://en.wikipedia.org/wiki/ANSI_escape_code#SGR_parameters
# More info on the awk command:
# http://linuxcommand.org/lc3_adv_awk.php

.PHONY: help
help: ## Display this help.
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)

##@ Development

.PHONY: manifests
manifests: controller-gen ## Generate WebhookConfiguration, ClusterRole and CustomResourceDefinition objects.
	$(CONTROLLER_GEN) rbac:roleName=manager-role crd webhook paths="./..." output:crd:artifacts:config=config/crd/bases
	rm -f $(CHART_ROOT)/kubetasker-controller/crds/*.yaml
	cp config/crd/bases/*.yaml $(CHART_ROOT)/kubetasker-controller/crds/task.ktasker.com_ktasks.yaml

.PHONY: generate
generate: controller-gen ## Generate code containing DeepCopy, DeepCopyInto, and DeepCopyObject method implementations.
	$(CONTROLLER_GEN) object:headerFile="hack/boilerplate.go.txt" paths="./..."

.PHONY: pyenv
pyenv: ## create python venv for running the API
	rm -rf $(PYVENV) && \
	$(PYTHON) -m venv $(PYVENV) && \
		source $(PYVENV)/bin/activate && \
		$(PYVENV)/bin/pip install --upgrade pip && \
		$(PYVENV)/bin/pip install -r requirements.txt

.PHONY: cluster
cluster:
	$(KIND) cluster create --name $(KIND_CLUSTER)

.PHONY: fmt
fmt: ## Run go fmt against code.
	go fmt ./...

.PHONY: vet
vet: ## Run go vet against code.
	go vet ./...

.PHONY: bump
bump: ## Bump the project version across all files. Usage: make bump part=<major|minor|patch>
	@if [ -z "$(part)" ]; then echo "Usage: make bump part=<major|minor|patch>"; exit 1; fi
	@# Ensure bump-my-version is installed in the venv
	@if [ ! -f "$(PYVENV)/bin/bump-my-version" ]; then \
		echo "Installing bump-my-version..."; \
		$(PYVENV)/bin/pip install bump-my-version; \
	fi
	$(PYVENV)/bin/bump-my-version bump $(part)

.PHONY: golden-update
golden-update: kustomize-manifests kustomize ## Update golden manifest files for tests.
	@echo "--- Updating kustomize golden file..."
	kustomize build config/default > test/golden/kustomize_golden.yaml
	@echo "--- Updating helm golden file..."
	helm template kubetasker-controller-test $(CHART_ROOT)/kubetasker-controller --set image.repository=ktasker.com/kubetasker --set image.tag=$(VERSION) > test/golden/helm_golden.yaml
	@echo "--- Updating frontend static golden file..."
	cat $(CHART_ROOT)/kubetasker-frontend/templates/deployment.yaml > test/golden/frontend_static_golden.yaml
	@echo "--- Updating frontend helm golden file..."
	helm template kubetasker-frontend-test $(CHART_ROOT)/kubetasker-frontend --set image.repository=ktasker.com/kubetasker-frontend --set image.tag=$(VERSION) > test/golden/frontend_helm_golden.yaml
	@echo "--- Updating umbrella chart golden files..."
	@for env in dev staging prod; do \
		echo "--- Generating golden file for $$env environment..."; \
		helm template umbrella-$$env $(CHART_ROOT)/kubetasker \
			-f $(CHART_ROOT)/kubetasker/values-$$env.yaml \
			--set kubetasker-controller.image.repository=controller \
			--set kubetasker-controller.image.tag=$(VERSION) \
			--set kubetasker-frontend.image.repository=ktasker.com/kubetasker-frontend \
			--set kubetasker-frontend.image.tag=$(VERSION) \
			--set kubetasker-controller.certManager.enabled=false \
			> test/golden/umbrella_$$env\_golden.yaml; \
	done
	@echo "--- Updating kustomize overlay golden files..."
	@for env in dev staging prod; do \
		echo "--- Generating kustomize golden file for $$env environment..."; \
		$(KUSTOMIZE) build --load-restrictor LoadRestrictionsNone kustomize/overlays/$$env > test/golden/kustomize_$$env\_golden.yaml; \
	done

.PHONY: golden-diff
golden-diff: ## Show the differences between golden files for manual review.
	@echo "--- Diffing kustomize vs. helm golden files..."
	@echo "NOTE: Differences are expected due to Helm's naming and labeling conventions."
	@diff -u test/golden/kustomize_golden.yaml test/golden/helm_golden.yaml || true

.PHONY: test
test: kustomize-manifests generate fmt vet setup-envtest ## Run tests.
	@echo "--- Running unit and integration tests"
	KUBEBUILDER_ASSETS="$(shell $(ENVTEST) use $(ENVTEST_K8S_VERSION) --bin-dir $(LOCALBIN) -p path)" go test $$(go list ./... | grep -v /e2e | grep -v /golden) -coverprofile cover.out
	@echo "--- Running golden file tests"
	go test -v ./test/golden

KIND_CLUSTER = kubetasker
KIND_CLUSTER_DEV ?= kubetasker-test-e2e

.PHONY: setup-test-e2e
setup-test-e2e: ## Set up a Kind cluster for e2e tests if it does not exist
	@command -v $(KIND) >/dev/null 2>&1 || { \
		echo "Kind is not installed. Please install Kind manually."; \
		exit 1; \
	}
	@case "$$($(KIND) get clusters)" in \
		*"$(KIND_CLUSTER_DEV)"*) \
			echo "Kind cluster '$(KIND_CLUSTER_DEV)' already exists. Skipping creation." ;; \
		*) \
			echo "Creating Kind cluster '$(KIND_CLUSTER_DEV)'..."; \
			$(KIND) create cluster --name $(KIND_CLUSTER_DEV) ;; \
	esac

.PHONY: test-e2e
test-e2e: setup-test-e2e manifests generate fmt vet ## Run the e2e tests. Expected an isolated environment using Kind.
	go test -tags=e2e ./test/e2e/ -v -ginkgo.v -timeout 20m
	$(MAKE) cleanup-test-e2e

.PHONY: cleanup-test-e2e
cleanup-test-e2e: ## Tear down the Kind cluster used for e2e tests
	@$(KIND) delete cluster --name $(KIND_CLUSTER_DEV)

.PHONY: lint
lint: golangci-lint ## Run golangci-lint linter
	$(GOLANGCI_LINT) run

.PHONY: lint-fix
lint-fix: golangci-lint ## Run golangci-lint linter and perform fixes
	$(GOLANGCI_LINT) run --fix

.PHONY: lint-config
lint-config: golangci-lint ## Verify golangci-lint linter configuration
	$(GOLANGCI_LINT) config verify

##@ Build

.PHONY: build
build: manifests generate fmt vet ## Build manager binary.
	go build -o bin/manager cmd/main.go

.PHONY: run
run: manifests generate fmt vet ## Run a controller from your host.
	go run ./cmd/main.go

.PHONY: run-local
run-local: install generate fmt vet ## Run a controller from your host with webhooks disabled.
	ENABLE_WEBHOOKS=false go run ./cmd/main.go

# If you wish to build the manager image targeting other platforms you can use the --platform flag.
# (i.e. docker build --platform linux/arm64). However, you must enable docker buildKit for it.
# More info: https://docs.docker.com/develop/develop-images/build_enhancements/
.PHONY: docker-build
docker-build: ## Build docker image with the manager.
	$(CONTAINER_TOOL) build -t ${IMG} .

.PHONY: docker-clean
docker-clean: ## stop and remove docker image completely
	$(CONTAINER_TOOL) ps -a --filter "ancestor=$(IMG)" --format "{{.Names}}" | xargs -r $(CONTAINER_TOOL) stop && \
	$(CONTAINER_TOOL) ps -a --filter "ancestor=$(IMG)" --format "{{.Names}}" | xargs -r $(CONTAINER_TOOL) rm && \
	$(CONTAINER_TOOL) rmi $(IMG)

.PHONY: docker-push
docker-push: ## Push docker image with the manager.
	$(CONTAINER_TOOL) push ${IMG}

.PHONY: deploy-controller
deploy-controller: docker-build install-cert-manager ## Deploy or update the controller in the current cluster using Helm.
	@echo "--- Loading images into Kind cluster..."
	-$(KIND) load docker-image $(IMG) --name $(KIND_CLUSTER_DEV)
	@echo "--- Deploying controller via Helm..."
	helm upgrade --install $(CONTROLLER) $(CHART_ROOT)/$(CONTROLLER) --set image.repository=$(shell echo $(IMG) | cut -d: -f1) --set image.tag=$(shell echo $(IMG) | cut -d: -f2) --wait
	@echo "--- Restarting controller deployment to apply image changes..."
	$(KUBECTL) rollout restart deployment $(CONTROLLER)

.PHONY: debug-controller
debug-controller: ## Debug the controller deployment by showing pod status, logs, and events.
	@echo "--- Pod Status ---"
	$(KUBECTL) get pods -l control-plane=controller-manager
	@echo "--- Pod Description (Events) ---"
	$(KUBECTL) describe pods -l control-plane=controller-manager
	@echo "--- Pod Logs ---"
	$(KUBECTL) logs -l control-plane=controller-manager --all-containers=true --tail=100
	@echo "--- Service Status ---"
	$(KUBECTL) get svc -l app.kubernetes.io/name=$(CONTROLLER) || true
	@echo "--- Endpoints Status ---"
	$(KUBECTL) get endpoints -l app.kubernetes.io/name=$(CONTROLLER) || true

.PHONY: undeploy-controller
undeploy-controller: ## Undeploy the controller helm release.
	@echo "--- Undeploying controller via Helm..."
	-helm uninstall $(CONTROLLER)

.PHONY: deploy-frontend
deploy-frontend: docker-build-frontend ## Deploy or update the frontend service in the current cluster using Helm.
	@echo "--- Loading frontend image into Kind cluster..."
	-$(KIND) load docker-image $(FRONTEND_IMG) --name $(KIND_CLUSTER_DEV)
	@echo "--- Deploying frontend via Helm..."
	helm upgrade --install $(FRONTEND) $(CHART_ROOT)/$(FRONTEND) --set image.repository=$(shell echo $(FRONTEND_IMG) | cut -d: -f1) --set image.tag=$(shell echo $(FRONTEND_IMG) | cut -d: -f2) --set controllerUrl=http://$(CONTROLLER):$(CONTROLLER_PORT) --wait
	@echo "--- Restarting frontend deployment to apply image changes..."
	$(KUBECTL) rollout restart deployment $(FRONTEND)

.PHONY: debug-frontend
debug-frontend: ## Debug the frontend deployment by showing pod status, logs, and events.
	@echo "--- Pod Status ---"
	$(KUBECTL) get pods -l app.kubernetes.io/name=$(FRONTEND)
	@echo "--- Pod Description (Events) ---"
	$(KUBECTL) describe pods -l app.kubernetes.io/name=$(FRONTEND)
	@echo "--- Pod Logs ---"
	$(KUBECTL) logs -l app.kubernetes.io/name=$(FRONTEND) --all-containers=true --tail=100

.PHONY: undeploy-frontend
undeploy-frontend: ## Undeploy the frontend helm release.
	@echo "--- Undeploying frontend via Helm..."
	-helm uninstall $(FRONTEND)

.PHONY: docker-build-frontend
docker-build-frontend: ## Build the frontend API container image.
	cp requirements.txt $(CHART_ROOT)/$(FRONTEND)/requirements.txt
	$(CONTAINER_TOOL) build -t $(FRONTEND_IMG) -f $(CHART_ROOT)/$(FRONTEND)/Dockerfile $(CHART_ROOT)/$(FRONTEND)
	rm $(CHART_ROOT)/$(FRONTEND)/requirements.txt

.PHONY: load-docker-frontend
load-docker-frontend: ## Load the frontend API container image into the kubetasker cluster
	$(KIND) load docker-image $(FRONTEND_IMG) --name $(KIND_CLUSTER)

.PHONY: docker-push-frontend
docker-push-frontend: ## Push the frontend API container image.
	$(CONTAINER_TOOL) push $(FRONTEND_IMG)

.PHONY: run-frontend-local
run-frontend-local: ## Build and run the frontend API container locally for development.
	# Ensure we are building with the correct tag for local run
	$(MAKE) docker-build-frontend FRONTEND_IMG=$(FRONTEND):latest
	# Stop and remove any existing container with the same name
	-$(CONTAINER_TOOL) stop $(FRONTEND) > /dev/null 2>&1 || true
	-$(CONTAINER_TOOL) rm $(FRONTEND) > /dev/null 2>&1 || true
	$(CONTAINER_TOOL) run -e KUBETASKER_ENV=development -d -p $(FRONTEND_PORT):$(FRONTEND_PORT) --name $(FRONTEND) $(FRONTEND):latest

.PHONY: run-frontend-dev
run-frontend-dev: ## Run the frontend service locally using uvicorn (requires 'make pyenv' first)
	@if [ ! -d "$(PYVENV)" ]; then echo "Python venv not found. Please run 'make pyenv' first."; exit 1; fi
	export KUBETASKER_ENV=development && \
	cd $(CHART_ROOT)/$(FRONTEND) && \
	$(CURDIR)/$(PYVENV)/bin/uvicorn listener:app --reload --port $(FRONTEND_PORT)

.PHONY: smoke-test-frontend
smoke-test-frontend: ## Run a smoke test against the local frontend (starts it, checks it, stops it)
	@echo "--- Starting frontend smoke test ---"
	@# Start uvicorn in background
	@export KUBETASKER_ENV=development && \
	cd $(CHART_ROOT)/$(FRONTEND) && \
	$(CURDIR)/$(PYVENV)/bin/uvicorn listener:app --port $(FRONTEND_PORT) > /tmp/kubetasker-frontend.log 2>&1 & \
	echo $$! > /tmp/kubetasker-frontend.pid
	@echo "Frontend started with PID $$(cat /tmp/kubetasker-frontend.pid). Waiting 5s..."
	@sleep 5
	@echo "Checking root URL..."
	@if curl -s http://127.0.0.1:$(FRONTEND_PORT)/ | grep -q "KubeTasker Dashboard"; then \
		echo "✅ GUI Smoke Test Passed"; \
		kill $$(cat /tmp/kubetasker-frontend.pid) && rm /tmp/kubetasker-frontend.pid; \
	else \
		echo "❌ GUI Smoke Test Failed"; \
		cat /tmp/kubetasker-frontend.log; \
		kill $$(cat /tmp/kubetasker-frontend.pid) && rm /tmp/kubetasker-frontend.pid; \
		exit 1; \
	fi

.PHONY: docker-clean-frontend
docker-clean-frontend: ## Stop and remove the running frontend container and its images.
	-$(CONTAINER_TOOL) stop $(FRONTEND) > /dev/null 2>&1 || true
	-$(CONTAINER_TOOL) rm $(FRONTEND) > /dev/null 2>&1 || true
	-$(CONTAINER_TOOL) rmi $(FRONTEND_IMG) > /dev/null 2>&1 || true
	-$(CONTAINER_TOOL) rmi $(FRONTEND):latest > /dev/null 2>&1 || true

# PLATFORMS defines the target platforms for the manager image be built to provide support to multiple
# architectures. (i.e. make docker-buildx IMG=myregistry/mypoperator:0.0.1). To use this option you need to:
# - be able to use docker buildx. More info: https://docs.docker.com/build/buildx/
# - have enabled BuildKit. More info: https://docs.docker.com/develop/develop-images/build_enhancements/
# - be able to push the image to your registry (i.e. if you do not set a valid value via IMG=<myregistry/image:<tag>> then the export will fail)
# To adequately provide solutions that are compatible with multiple platforms, you should consider using this option.
PLATFORMS ?= linux/arm64,linux/amd64,linux/s390x,linux/ppc64le
.PHONY: docker-buildx
docker-buildx: ## Build and push docker image for the manager for cross-platform support
	# copy existing Dockerfile and insert --platform=${BUILDPLATFORM} into Dockerfile.cross, and preserve the original Dockerfile
	sed -e '1 s/\(^FROM\)/FROM --platform=\$$\{BUILDPLATFORM\}/; t' -e ' 1,// s//FROM --platform=\$$\{BUILDPLATFORM\}/' Dockerfile > Dockerfile.cross
	- $(CONTAINER_TOOL) buildx create --name kubetasker-builder
	$(CONTAINER_TOOL) buildx use kubetasker-builder
	- $(CONTAINER_TOOL) buildx build --push --platform=$(PLATFORMS) --tag ${IMG} -f Dockerfile.cross .
	- $(CONTAINER_TOOL) buildx rm kubetasker-builder
	rm Dockerfile.cross

.PHONY: build-installer
build-installer: manifests generate kustomize ## Generate a consolidated YAML with CRDs and deployment.
	mkdir -p dist
	cd config/manager && $(KUSTOMIZE) edit set image controller=${IMG}
	$(KUSTOMIZE) build config/default > dist/install.yaml

##@ Deployment

ifndef ignore-not-found
  ignore-not-found = false
endif

.PHONY: install
install: manifests kustomize ## Install CRDs into the K8s cluster specified in ~/.kube/config.
	@out="$$( $(KUSTOMIZE) build config/crd 2>/dev/null || true )"; \
	if [ -n "$$out" ]; then echo "$$out" | $(KUBECTL) apply -f -; else echo "No CRDs to install; skipping."; fi

.PHONY: uninstall
uninstall: manifests kustomize ## Uninstall CRDs from the K8s cluster specified in ~/.kube/config. Call with ignore-not-found=true to ignore resource not found errors during deletion.
	@out="$$( $(KUSTOMIZE) build config/crd 2>/dev/null || true )"; \
	if [ -n "$$out" ]; then echo "$$out" | $(KUBECTL) delete --ignore-not-found=$(ignore-not-found) -f -; else echo "No CRDs to delete; skipping."; fi

.PHONY: deploy
deploy: manifests kustomize ## Deploy controller to the K8s cluster specified in ~/.kube/config.
	cd config/manager && $(KUSTOMIZE) edit set image controller=${IMG}
	$(KUSTOMIZE) build config/default | $(KUBECTL) apply -f -

.PHONY: undeploy
undeploy: kustomize ## Undeploy controller from the K8s cluster specified in ~/.kube/config. Call with ignore-not-found=true to ignore resource not found errors during deletion.
	$(KUSTOMIZE) build config/default | $(KUBECTL) delete --ignore-not-found=$(ignore-not-found) -f -

.PHONY: install-cert-manager
install-cert-manager: ## Install cert-manager using Helm if it's not already present.
	@echo "--- Checking for cert-manager release..."
	@if ! helm status cert-manager -n cert-manager > /dev/null 2>&1; then \
		echo "--- cert-manager not found. Installing via Helm..."; \
		helm repo add jetstack https://charts.jetstack.io --force-update; \
		helm repo update; \
		helm install cert-manager jetstack/cert-manager \
			--namespace cert-manager \
			--create-namespace \
			--version v1.14.5 \
			--set installCRDs=true \
			--wait; \
	else \
		echo "--- cert-manager is already installed. Skipping installation."; \
	fi

# Variables for the umbrella deployment
UMBRELLA_NAMESPACE ?= kubetasker-system
UMBRELLA_RELEASE_NAME ?= kubetasker

.PHONY: deploy-umbrella
deploy-umbrella: docker-build docker-build-frontend install-cert-manager ## Deploy the entire KubeTasker stack using the umbrella chart.
	@echo "--- Loading images into Kind cluster..."
	$(KIND) load docker-image $(IMG) --name $(KIND_CLUSTER_DEV)
	$(KIND) load docker-image $(FRONTEND_IMG) --name $(KIND_CLUSTER_DEV)
	@echo "--- Updating Helm dependencies for umbrella chart..."
	helm dependency update $(CHART_ROOT)/kubetasker
	@echo "--- Deploying umbrella chart to namespace '$(UMBRELLA_NAMESPACE)' with release name '$(UMBRELLA_RELEASE_NAME)'..."
	helm upgrade --install $(UMBRELLA_RELEASE_NAME) $(CHART_ROOT)/kubetasker \
		--namespace $(UMBRELLA_NAMESPACE) --create-namespace \
		--set kubetasker-controller.image.repository=$(shell echo $(IMG) | cut -d: -f1) \
		--set kubetasker-controller.image.tag=$(shell echo $(IMG) | cut -d: -f2) \
		--set kubetasker-frontend.image.repository=$(shell echo $(FRONTEND_IMG) | cut -d: -f1) \
		--set kubetasker-frontend.image.tag=$(shell echo $(FRONTEND_IMG) | cut -d: -f2) \
		--set kubetasker-frontend.controllerUrl=http://$(UMBRELLA_RELEASE_NAME)-kubetasker-controller:$(CONTROLLER_PORT) \
		--wait
	@echo "--- KubeTasker umbrella chart deployed successfully."
	@echo "--- To check the status, run: kubectl get pods -n $(UMBRELLA_NAMESPACE)"

.PHONY: debug
debug: debug-umbrella ## Alias for debug-umbrella

.PHONY: debug-umbrella
debug-umbrella: ## Debug the umbrella deployment by showing pod status, logs, and events.
	@echo "--- Pod Status ---"
	$(KUBECTL) get pods -n $(UMBRELLA_NAMESPACE) -l app.kubernetes.io/instance=$(UMBRELLA_RELEASE_NAME)
	@echo "--- Pod Description (Events) ---"
	$(KUBECTL) describe pods -n $(UMBRELLA_NAMESPACE) -l app.kubernetes.io/instance=$(UMBRELLA_RELEASE_NAME)
	@echo "--- Pod Logs ---"
	$(KUBECTL) logs -n $(UMBRELLA_NAMESPACE) -l app.kubernetes.io/instance=$(UMBRELLA_RELEASE_NAME) --all-containers=true --tail=100

.PHONY: install-prometheus
install-prometheus: ## Install kube-prometheus-stack using Helm.
	@echo "--- Checking for kube-prometheus-stack release..."
	@if ! helm status prometheus -n monitoring > /dev/null 2>&1; then \
		echo "--- kube-prometheus-stack not found. Installing via Helm..."; \
		helm repo add prometheus-community https://prometheus-community.github.io/helm-charts; \
		helm repo update; \
		helm install prometheus prometheus-community/kube-prometheus-stack \
			--namespace monitoring \
			--create-namespace \
			--wait; \
	else \
		echo "--- kube-prometheus-stack is already installed. Skipping installation."; \
	fi

.PHONY: deploy-monitoring
deploy-monitoring: docker-build docker-build-frontend install-cert-manager install-prometheus ## Deploy KubeTasker with Prometheus monitoring enabled.
	@echo "--- Loading images into Kind cluster..."
	$(KIND) load docker-image $(IMG) --name $(KIND_CLUSTER_DEV)
	$(KIND) load docker-image $(FRONTEND_IMG) --name $(KIND_CLUSTER_DEV)
	@echo "--- Updating Helm dependencies for umbrella chart..."
	helm dependency update $(CHART_ROOT)/kubetasker
	@echo "--- Deploying umbrella chart with monitoring enabled..."
	helm upgrade --install $(UMBRELLA_RELEASE_NAME) $(CHART_ROOT)/kubetasker \
		--namespace $(UMBRELLA_NAMESPACE) --create-namespace \
		--set kubetasker-controller.image.repository=$(shell echo $(IMG) | cut -d: -f1) \
		--set kubetasker-controller.image.tag=$(shell echo $(IMG) | cut -d: -f2) \
		--set kubetasker-frontend.image.repository=$(shell echo $(FRONTEND_IMG) | cut -d: -f1) \
		--set kubetasker-frontend.image.tag=$(shell echo $(FRONTEND_IMG) | cut -d: -f2) \
		--set kubetasker-controller.serviceMonitor.enabled=true \
		--set kubetasker-frontend.serviceMonitor.enabled=true \
		--set kubetasker-frontend.controllerUrl=http://$(UMBRELLA_RELEASE_NAME)-kubetasker-controller:$(CONTROLLER_PORT) \
		--wait
	@echo "--- KubeTasker with monitoring deployed successfully."

.PHONY: undeploy-umbrella
undeploy-umbrella: ## Undeploy the KubeTasker stack and cert-manager.
	@echo "--- Uninstalling umbrella chart release '$(UMBRELLA_RELEASE_NAME)' from namespace '$(UMBRELLA_NAMESPACE)'..."
	-helm uninstall $(UMBRELLA_RELEASE_NAME) --namespace $(UMBRELLA_NAMESPACE)
	@echo "--- Deleting namespace '$(UMBRELLA_NAMESPACE)'..."
	-$(KUBECTL) delete namespace $(UMBRELLA_NAMESPACE) --ignore-not-found
	@echo "--- Uninstalling cert-manager..."
	-helm uninstall cert-manager --namespace cert-manager
	@echo "--- Deleting cert-manager namespace..."
	-$(KUBECTL) delete namespace cert-manager --ignore-not-found

.PHONY: uninstall-prometheus
uninstall-prometheus: ## Uninstall kube-prometheus-stack.
	@echo "--- Uninstalling kube-prometheus-stack..."
	-helm uninstall prometheus --namespace monitoring
	@echo "--- Deleting monitoring namespace..."
	-$(KUBECTL) delete namespace monitoring --ignore-not-found

.PHONY: dashboard
dashboard: ## Port-forward the frontend pod to localhost:8000
	@echo "--- Port-forwarding KubeTasker Dashboard to http://localhost:8000 ..."
	@POD_NAME=$$(kubectl get pods -n $(UMBRELLA_NAMESPACE) -l app.kubernetes.io/name=kubetasker-frontend --no-headers -o custom-columns=":metadata.name" 2>/dev/null | head -n 1); \
	if [ -z "$$POD_NAME" ]; then \
	  echo "Error: frontend pod not found in namespace $(UMBRELLA_NAMESPACE)"; exit 1; \
	fi; \
	kubectl wait --for=condition=Ready pod/$$POD_NAME -n $(UMBRELLA_NAMESPACE) --timeout=60s; \
	kubectl port-forward -n $(UMBRELLA_NAMESPACE) pod/$$POD_NAME 8000:8000

.PHONY: dashboard-prometheus
dashboard-prometheus: ## Port-forward Prometheus dashboard to localhost:9090
	@echo "--- Port-forwarding Prometheus dashboard to http://localhost:9090 ..."
	$(KUBECTL) port-forward svc/prometheus-kube-prometheus-prometheus -n monitoring 9090:9090

# Variables for Kustomize deployment
ENVS ?= dev staging prod
ENV ?= dev

.PHONY: kustomize-manifests
kustomize-manifests: ## Generate the base manifests required for Kustomize overlays.
	@echo "--- Generating Kustomize base manifests..."
	@echo "--- Creating kustomize/base directory..."
	@mkdir -p kustomize/base
	@echo "--- Generating kustomize/base/all.yaml from Helm chart using values from environment: $(ENV)..."
	@helm template kubetasker-base $(CHART_ROOT)/kubetasker \
		--namespace $(ENV) \
		-f $(CHART_ROOT)/kubetasker/values-$(ENV).yaml \
		--set kubetasker-controller.image.repository=$(shell echo $(IMG) | cut -d: -f1) \
		--set kubetasker-controller.image.tag=$(shell echo $(IMG) | cut -d: -f2) \
		--set kubetasker-frontend.image.repository=$(shell echo $(FRONTEND_IMG) | cut -d: -f1) \
		--set kubetasker-frontend.image.tag=$(shell echo $(FRONTEND_IMG) | cut -d: -f2) \
		--set global.imagePullPolicy=IfNotPresent \
		--set kubetasker-controller.certManager.namespace=$(ENV) \
		--set kubetasker-controller.webhook.namespace=$(ENV) \
		--set kubetasker-controller.webhook.service.namespace=$(ENV) \
		--set kubetasker-frontend.controllerUrl=http://kubetasker-base-kubetasker-controller:8090 \
		> kustomize/base/all.yaml

	@echo "--- Copying authoritative CRD to kustomize/base/crd.yaml..."
	@cp config/crd/bases/task.ktasker.com_ktasks.yaml kustomize/base/crd.yaml
	@echo "--- Kustomize base manifests generated successfully."

.PHONY: deploy-kustomize
deploy-kustomize: kustomize-manifests kustomize install-cert-manager ## Deploy a specific environment using Kustomize (e.g., make deploy-kustomize ENV=prod).
	@echo "--- Deploying environment '$(ENV)' using Kustomize..."
	$(KUBECTL) apply -k kustomize/overlays/$(ENV)

.PHONY: undeploy-kustomize
undeploy-kustomize: kustomize-manifests kustomize ## Undeploy a specific environment using Kustomize (e.g., make undeploy-kustomize ENV=prod).
	@echo "--- Undeploying environment '$(ENV)' using Kustomize..."
	$(KUBECTL) delete --ignore-not-found=true -k kustomize/overlays/$(ENV)

##@ Dependencies

## Location to install dependencies to
LOCALBIN ?= $(shell pwd)/bin
$(LOCALBIN):
	mkdir -p $(LOCALBIN)

## Tool Binaries
KUBECTL ?= kubectl
KIND ?= kind
KUSTOMIZE ?= $(LOCALBIN)/kustomize
CONTROLLER_GEN ?= $(LOCALBIN)/controller-gen
ENVTEST ?= $(LOCALBIN)/setup-envtest
GOLANGCI_LINT = $(LOCALBIN)/golangci-lint

## Tool Versions
KUSTOMIZE_VERSION ?= v5.7.1
CONTROLLER_TOOLS_VERSION ?= v0.19.0
#ENVTEST_VERSION is the version of controller-runtime release branch to fetch the envtest setup script (i.e. release-0.20)
ENVTEST_VERSION ?= $(shell go list -m -f "{{ .Version }}" sigs.k8s.io/controller-runtime | awk -F'[v.]' '{printf "release-%d.%d", $$2, $$3}')
#ENVTEST_K8S_VERSION is the version of Kubernetes to use for setting up ENVTEST binaries (i.e. 1.31)
ENVTEST_K8S_VERSION ?= $(shell go list -m -f "{{ .Version }}" k8s.io/api | awk -F'[v.]' '{printf "1.%d", $$3}')
GOLANGCI_LINT_VERSION ?= v2.4.0

.PHONY: kustomize
kustomize: $(KUSTOMIZE) ## Download kustomize locally if necessary.
$(KUSTOMIZE): $(LOCALBIN)
	$(call go-install-tool,$(KUSTOMIZE),sigs.k8s.io/kustomize/kustomize/v5,$(KUSTOMIZE_VERSION))

.PHONY: controller-gen
controller-gen: $(CONTROLLER_GEN) ## Download controller-gen locally if necessary.
$(CONTROLLER_GEN): $(LOCALBIN)
	$(call go-install-tool,$(CONTROLLER_GEN),sigs.k8s.io/controller-tools/cmd/controller-gen,$(CONTROLLER_TOOLS_VERSION))

.PHONY: setup-envtest
setup-envtest: envtest ## Download the binaries required for ENVTEST in the local bin directory.
	@echo "Setting up envtest binaries for Kubernetes version $(ENVTEST_K8S_VERSION)..."
	@$(ENVTEST) use $(ENVTEST_K8S_VERSION) --bin-dir $(LOCALBIN) -p path || { \
		echo "Error: Failed to set up envtest binaries for version $(ENVTEST_K8S_VERSION)."; \
		exit 1; \
	}

.PHONY: envtest
envtest: $(ENVTEST) ## Download setup-envtest locally if necessary.
$(ENVTEST): $(LOCALBIN)
	$(call go-install-tool,$(ENVTEST),sigs.k8s.io/controller-runtime/tools/setup-envtest,$(ENVTEST_VERSION))

.PHONY: golangci-lint
golangci-lint: $(GOLANGCI_LINT) ## Download golangci-lint locally if necessary.
$(GOLANGCI_LINT): $(LOCALBIN)
	$(call go-install-tool,$(GOLANGCI_LINT),github.com/golangci/golangci-lint/v2/cmd/golangci-lint,$(GOLANGCI_LINT_VERSION))

# go-install-tool will 'go install' any package with custom target and name of binary, if it doesn't exist
# $1 - target path with name of binary
# $2 - package url which can be installed
# $3 - specific version of package
define go-install-tool
@[ -f "$(1)-$(3)" ] && [ "$$(readlink -- "$(1)" 2>/dev/null)" = "$(1)-$(3)" ] || { \
set -e; \
package=$(2)@$(3) ;\
echo "Downloading $${package}" ;\
rm -f $(1) ;\
GOBIN=$(LOCALBIN) go install $${package} ;\
mv $(1) $(1)-$(3) ;\
} ;\
ln -sf $$(realpath $(1)-$(3)) $(1)
endef
