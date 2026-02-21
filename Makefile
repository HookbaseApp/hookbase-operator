# Hookbase Operator Makefile

# Image URLs
IMG ?= ghcr.io/hookbaseapp/hookbase-operator:latest
AGENT_IMG ?= ghcr.io/hookbaseapp/hookbase-agent:latest

# Get the currently used golang install path (in GOPATH/bin, unless GOBIN is set)
ifeq (,$(shell go env GOBIN))
GOBIN=$(shell go env GOPATH)/bin
else
GOBIN=$(shell go env GOBIN)
endif

SHELL = /usr/bin/env bash -o pipefail
.SHELLFLAGS = -ec

.PHONY: all
all: build

##@ General

.PHONY: help
help: ## Display this help.
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)

##@ Development

.PHONY: fmt
fmt: ## Run go fmt against code.
	go fmt ./...

.PHONY: vet
vet: ## Run go vet against code.
	go vet ./...

.PHONY: test
test: fmt vet ## Run tests.
	go test ./... -coverprofile cover.out

.PHONY: lint
lint: ## Run golangci-lint.
	golangci-lint run

##@ Build

.PHONY: build
build: fmt vet ## Build operator and agent binaries.
	CGO_ENABLED=0 go build -ldflags="-s -w" -o bin/operator ./cmd/operator/
	CGO_ENABLED=0 go build -ldflags="-s -w" -o bin/agent ./cmd/agent/

.PHONY: build-operator
build-operator: ## Build operator binary only.
	CGO_ENABLED=0 go build -ldflags="-s -w" -o bin/operator ./cmd/operator/

.PHONY: build-agent
build-agent: ## Build agent binary only.
	CGO_ENABLED=0 go build -ldflags="-s -w" -o bin/agent ./cmd/agent/

.PHONY: run
run: fmt vet ## Run operator from your host.
	go run ./cmd/operator/

##@ Docker

.PHONY: docker-build
docker-build: ## Build operator docker image.
	docker build -t ${IMG} -f Dockerfile ..

.PHONY: docker-build-agent
docker-build-agent: ## Build agent docker image.
	docker build -t ${AGENT_IMG} -f Dockerfile.agent ..

.PHONY: docker-push
docker-push: ## Push operator docker image.
	docker push ${IMG}

.PHONY: docker-push-agent
docker-push-agent: ## Push agent docker image.
	docker push ${AGENT_IMG}

##@ Deployment

.PHONY: install
install: ## Install CRDs into the K8s cluster.
	helm template hookbase-operator ./chart --show-only 'templates/crds/*' | kubectl apply -f -

.PHONY: uninstall
uninstall: ## Uninstall CRDs from the K8s cluster.
	helm template hookbase-operator ./chart --show-only 'templates/crds/*' | kubectl delete -f -

.PHONY: helm-install
helm-install: ## Install via Helm chart.
	helm install hookbase-operator ./chart -n hookbase-system --create-namespace

.PHONY: helm-upgrade
helm-upgrade: ## Upgrade via Helm chart.
	helm upgrade hookbase-operator ./chart -n hookbase-system

.PHONY: helm-uninstall
helm-uninstall: ## Uninstall Helm release.
	helm uninstall hookbase-operator -n hookbase-system

.PHONY: helm-template
helm-template: ## Render Helm templates.
	helm template hookbase-operator ./chart

.PHONY: helm-lint
helm-lint: ## Lint Helm chart.
	helm lint ./chart

##@ Generate

.PHONY: generate
generate: ## Generate code (deepcopy, manifests).
	controller-gen object:headerFile="hack/boilerplate.go.txt" paths="./api/..."
	controller-gen crd paths="./api/..." output:crd:dir=chart/templates/crds

.PHONY: manifests
manifests: ## Generate CRD manifests.
	controller-gen crd paths="./api/..." output:crd:dir=chart/templates/crds
