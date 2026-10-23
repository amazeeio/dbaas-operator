
# Image URL to use all building/pushing image targets
IMG ?= controller:latest

TIMEOUT = 30m
KIND_CLUSTER ?= dbaas-operator-test
KIND_NODE_VERSION ?= v1.32.2@sha256:f226345927d7e348497136874b6d207e0b32cc52154ad8323129352923a3142f

KIND_VERSION = v0.27.0
KUBECTL_VERSION := v1.32.3
KUSTOMIZE_VERSION := v5.4.3

KUBECTL = $(CURDIR)/local-dev/kubectl
KIND = $(CURDIR)/local-dev/kind
KUSTOMIZE = $(CURDIR)/local-dev/kustomize

ARCH := $(shell uname | tr '[:upper:]' '[:lower:]')

.PHONY: local-dev/kind
local-dev/kind:
	@mkdir -p local-dev
ifeq ($(KIND_VERSION), $(shell kind version 2>/dev/null | sed -nE 's/kind (v[0-9.]+).*/\1/p'))
	$(info linking local kind version $(KIND_VERSION))
	ln -sf $(shell command -v kind) ./local-dev/kind
else
ifneq ($(KIND_VERSION), $(shell ./local-dev/kind version 2>/dev/null | sed -nE 's/kind (v[0-9.]+).*/\1/p'))
	$(info downloading kind version $(KIND_VERSION) for $(ARCH))
	rm local-dev/kind || true
	curl -sSLo local-dev/kind https://kind.sigs.k8s.io/dl/$(KIND_VERSION)/kind-$(ARCH)-amd64
	chmod a+x local-dev/kind
endif
endif

.PHONY: local-dev/kustomize
local-dev/kustomize:
	@mkdir -p local-dev
ifeq ($(KUSTOMIZE_VERSION), $(shell kustomize version 2>/dev/null | sed -nE 's/(v[0-9.]+).*/\1/p'))
	$(info linking local kustomize version $(KUSTOMIZE_VERSION))
	ln -sf $(shell command -v kustomize) ./local-dev/kustomize
else
ifneq ($(KUSTOMIZE_VERSION), $(shell ./local-dev/kustomize version 2>/dev/null | sed -nE 's/(v[0-9.]+).*/\1/p'))
	$(info downloading kustomize version $(KUSTOMIZE_VERSION) for $(ARCH))
	rm local-dev/kustomize || true
	curl -sSL https://github.com/kubernetes-sigs/kustomize/releases/download/kustomize%2F$(KUSTOMIZE_VERSION)/kustomize_$(KUSTOMIZE_VERSION)_$(ARCH)_amd64.tar.gz | tar -xzC local-dev
	chmod a+x local-dev/kustomize
endif
endif

.PHONY: local-dev/kubectl
local-dev/kubectl:
	@mkdir -p local-dev
ifeq ($(KUBECTL_VERSION), $(shell kubectl version --client 2>/dev/null | grep Client | sed -E 's/Client Version: (v[0-9.]+).*/\1/'))
	$(info linking local kubectl version $(KUBECTL_VERSION))
	ln -sf $(shell command -v kubectl) ./local-dev/kubectl
else
ifneq ($(KUBECTL_VERSION), $(shell ./local-dev/kubectl version --client 2>/dev/null | grep Client | sed -E 's/Client Version: (v[0-9.]+).*/\1/'))
	$(info downloading kubectl version $(KUBECTL_VERSION) for $(ARCH))
	rm local-dev/kubectl || true
	curl -sSLo local-dev/kubectl https://dl.k8s.io/release/$(KUBECTL_VERSION)/bin/$(ARCH)/amd64/kubectl
	chmod a+x local-dev/kubectl
endif
endif

.PHONY: local-dev/tools
local-dev/tools: local-dev/kind local-dev/kustomize local-dev/kubectl

# Get the currently used golang install path (in GOPATH/bin, unless GOBIN is set)
ifeq (,$(shell go env GOBIN))
GOBIN=$(shell go env GOPATH)/bin
else
GOBIN=$(shell go env GOBIN)
endif

all: manager

# Run tests
test: generate fmt vet manifests
	go test $$(go list ./... | grep -v /e2e) -coverprofile cover.out

# Build manager binary
manager: generate fmt vet
	go build -o bin/manager cmd/main.go

# Run against the configured Kubernetes cluster in ~/.kube/config
run: generate fmt vet manifests
	go run ./cmd/main.go

# Install CRDs into a cluster
install: manifests local-dev/tools
	$(KUSTOMIZE) build config/crd | $(KUBECTL) apply -f -

# Uninstall CRDs from a cluster
uninstall: manifests local-dev/tools
	$(KUSTOMIZE) build config/crd | $(KUBECTL) delete -f -

# Deploy controller in the configured Kubernetes cluster in ~/.kube/config
deploy: manifests local-dev/tools
	cd config/manager && $(KUSTOMIZE) edit set image controller=${IMG}
	$(KUSTOMIZE) build config/default | $(KUBECTL) apply -f -

# Remove the controller from the configured Kubernetes cluster
undeploy-operator: local-dev/tools
	$(KUSTOMIZE) build config/default | $(KUBECTL) delete --ignore-not-found -f -

# Generate manifests e.g. CRD, RBAC etc.
manifests: controller-gen
	$(CONTROLLER_GEN) rbac:roleName=manager-role crd webhook paths="./..." output:crd:artifacts:config=config/crd/bases

# Run go fmt against code
fmt:
	go fmt ./...

# Run go vet against code
vet:
	go vet ./...

# Generate code
generate: controller-gen
	$(CONTROLLER_GEN) object:headerFile=./hack/boilerplate.go.txt paths="./..."

# Build the docker image
docker-build: test
	docker build . -t ${IMG}

# Push the docker image
docker-push:
	docker push ${IMG}

# find or download controller-gen
# download controller-gen if necessary
controller-gen:
ifeq (, $(shell which controller-gen))
	@{ \
	set -e ;\
	CONTROLLER_GEN_TMP_DIR=$$(mktemp -d) ;\
	cd $$CONTROLLER_GEN_TMP_DIR ;\
	go mod init tmp ;\
	go install sigs.k8s.io/controller-tools/cmd/controller-gen@v0.16.5 ;\
	rm -rf $$CONTROLLER_GEN_TMP_DIR ;\
	}
CONTROLLER_GEN=$(GOBIN)/controller-gen
else
CONTROLLER_GEN=$(shell which controller-gen)
endif

# Create a kind cluster for the e2e tests
.PHONY: kind/create-cluster
kind/create-cluster: local-dev/tools
	$(KIND) create cluster --wait=60s --name=$(KIND_CLUSTER) --image=kindest/node:$(KIND_NODE_VERSION)

# Create a kind cluster locally and run the e2e test suite against it
.PHONY: kind/test-e2e
kind/test-e2e: kind/create-cluster kind/re-test-e2e

# Run the e2e tests against an existing local kind cluster
.PHONY: kind/re-test-e2e
kind/re-test-e2e: local-dev/tools
	$(KIND) export kubeconfig --name=$(KIND_CLUSTER)
	$(MAKE) test-e2e

.PHONY: kind/clean
kind/clean: local-dev/tools
	$(KIND) delete cluster --name=$(KIND_CLUSTER)

# Run the e2e tests against the current kubeconfig context (needs a kind cluster)
.PHONY: test-e2e
test-e2e: local-dev/tools
	KIND_PATH=$(KIND) KUBECTL_PATH=$(KUBECTL) KIND_CLUSTER=$(KIND_CLUSTER) \
	go test ./test/e2e/ -v -ginkgo.v -timeout $(TIMEOUT)

# Used by GitHub Actions, where the kind cluster already exists
.PHONY: github/test-e2e
github/test-e2e: local-dev/tools test-e2e

.PHONY: kind/logs-operator
kind/logs-operator: local-dev/tools
	$(KUBECTL) -n dbaas-operator-system logs -f -l control-plane=controller-manager -c manager

clean: kind/clean
