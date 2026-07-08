# kagents Makefile

IMG ?= ghcr.io/amcheste/kagents:latest
CLAUDE_CODE_IMG ?= ghcr.io/amcheste/claude-code-runner:latest
DASHBOARD_IMG ?= ghcr.io/amcheste/claude-teams-dashboard:latest
TRIGGER_IMG ?= ghcr.io/amcheste/kagents-trigger:latest
KIND_CLUSTER_NAME ?= claude-teams

# Tool versions
CONTROLLER_GEN_VERSION ?= v0.17.0
KUSTOMIZE_VERSION ?= v5.5.0

##@ General

.PHONY: help
help: ## Display this help
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) }' $(MAKEFILE_LIST)

##@ Development

.PHONY: manifests
manifests: controller-gen ## Generate CRD manifests
	$(CONTROLLER_GEN) rbac:roleName=manager-role crd webhook paths="./..." output:crd:artifacts:config=config/crd/bases
	# Mirror CRDs into the Helm chart's crds/ dir. Helm installs anything in
	# crds/ on `helm install` (but not on upgrade, by design). Without this
	# `helm install` deploys the operator but leaves it crash-looping waiting
	# for CRDs that were never applied.
	@mkdir -p charts/kagents/crds
	@cp -f config/crd/bases/*.yaml charts/kagents/crds/

.PHONY: generate
generate: controller-gen ## Generate deepcopy methods
	$(CONTROLLER_GEN) object:headerFile="hack/boilerplate.go.txt" paths="./..."

.PHONY: fmt
fmt: ## Run go fmt
	go fmt ./...

.PHONY: vet
vet: ## Run go vet
	go vet ./...

.PHONY: test
test: manifests generate fmt vet ## Run tests
	go test ./... -race -coverprofile cover.out

.PHONY: lint
lint: ## Run golangci-lint
	golangci-lint run

##@ Build

.PHONY: build
build: manifests generate fmt vet ## Build operator binary
	go build -o bin/manager cmd/manager/main.go

.PHONY: run
run: manifests generate fmt vet ## Run operator locally against cluster
	go run cmd/manager/main.go

.PHONY: docker-build
docker-build: ## Build operator Docker image
	docker build -t $(IMG) -f docker/Dockerfile.operator .

.PHONY: docker-build-runner
docker-build-runner: ## Build Claude Code runner Docker image
	docker build -t $(CLAUDE_CODE_IMG) -f docker/Dockerfile.claude-code .

.PHONY: docker-build-dashboard
docker-build-dashboard: ## Build dashboard web UI Docker image
	docker build -t $(DASHBOARD_IMG) -f docker/Dockerfile.dashboard .

.PHONY: docker-build-trigger
docker-build-trigger: ## Build kagents-trigger webhook listener Docker image
	docker build -t $(TRIGGER_IMG) -f docker/Dockerfile.trigger .

.PHONY: docker-push
docker-push: ## Push operator image
	docker push $(IMG)

##@ Kind Development

.PHONY: kind-create
kind-create: ## Create Kind cluster for development
	bash hack/kind-setup.sh

.PHONY: kind-delete
kind-delete: ## Delete Kind cluster
	kind delete cluster --name $(KIND_CLUSTER_NAME)

.PHONY: kind-load
kind-load: docker-build docker-build-runner docker-build-dashboard ## Load images into Kind
	kind load docker-image $(IMG) --name $(KIND_CLUSTER_NAME)
	kind load docker-image $(CLAUDE_CODE_IMG) --name $(KIND_CLUSTER_NAME)
	kind load docker-image $(DASHBOARD_IMG) --name $(KIND_CLUSTER_NAME)

##@ Deployment

.PHONY: install
install: manifests ## Install CRDs into cluster
	kubectl apply -f config/crd/bases/

.PHONY: uninstall
uninstall: ## Uninstall CRDs from cluster
	kubectl delete -f config/crd/bases/

.PHONY: deploy
deploy: manifests ## Deploy operator to cluster
	kubectl apply -f config/rbac/
	kubectl apply -f config/manager/

.PHONY: undeploy
undeploy: ## Remove operator from cluster
	kubectl delete -f config/manager/
	kubectl delete -f config/rbac/

.PHONY: sample
sample: ## Deploy sample AgentTeam
	kubectl apply -f config/samples/

##@ Helm

.PHONY: helm-install
helm-install: ## Install via Helm
	helm install kagents ./charts/kagents \
		--namespace claude-teams-system --create-namespace

.PHONY: helm-uninstall
helm-uninstall: ## Uninstall Helm release
	helm uninstall kagents --namespace claude-teams-system

##@ Testing

ENVTEST_K8S_VERSION ?= 1.31
ENVTEST_VERSION ?= release-0.23
SETUP_ENVTEST = $(shell go env GOPATH)/bin/setup-envtest

.PHONY: test-integration
test-integration: manifests generate envtest ## Run integration tests using envtest (no cluster needed)
	KUBEBUILDER_ASSETS="$(shell $(SETUP_ENVTEST) use $(ENVTEST_K8S_VERSION) -p path)" \
	go test ./internal/controller/... -tags=integration -race -v -count=1

.PHONY: test-all
test-all: test test-integration ## Run unit tests and integration tests

.PHONY: test-acceptance
test-acceptance: ## Run acceptance tests against a live cluster (requires acceptance-up or KUBECONFIG pointing at a prepared cluster)
	go test ./test/acceptance/... -tags=acceptance -v -count=1 -timeout=15m

.PHONY: acceptance-up
acceptance-up: ## Create Kind cluster and deploy operator in acceptance mode (busybox agent + skip-init-script)
	PATH="/opt/homebrew/bin:/usr/local/bin:$(PATH)" bash hack/acceptance-setup.sh

.PHONY: acceptance-down
acceptance-down: ## Tear down Kind acceptance cluster
	kind delete cluster --name $(KIND_CLUSTER_NAME)

.PHONY: mailbox-smoke-test
mailbox-smoke-test: ## Validate mailbox file exchange on shared PVC (requires acceptance-up or any cluster with an 'nfs' StorageClass)
	bash hack/mailbox-smoke-test.sh

.PHONY: test-e2e
test-e2e: ## Run E2E tests against the real Anthropic API (requires e2e-up and ANTHROPIC_API_KEY)
	go test ./test/e2e/... -tags=e2e -v -count=1 -timeout=20m

.PHONY: e2e-up
e2e-up: ## Create Kind cluster + build real runner image + deploy operator for E2E (requires ANTHROPIC_API_KEY)
	PATH="/opt/homebrew/bin:/usr/local/bin:$(PATH)" bash hack/e2e-setup.sh

.PHONY: e2e-down
e2e-down: ## Tear down Kind E2E cluster
	kind delete cluster --name $${KIND_CLUSTER_NAME:-claude-teams-e2e}

.PHONY: envtest
envtest: ## Install setup-envtest
	@test -f $(SETUP_ENVTEST) || go install sigs.k8s.io/controller-runtime/tools/setup-envtest@$(ENVTEST_VERSION)

##@ Tools

CONTROLLER_GEN = $(shell go env GOPATH)/bin/controller-gen
.PHONY: controller-gen
controller-gen: ## Install controller-gen
	@test -f $(CONTROLLER_GEN) || go install sigs.k8s.io/controller-tools/cmd/controller-gen@$(CONTROLLER_GEN_VERSION)

CRD_REF_DOCS_VERSION ?= v0.3.0
CRD_REF_DOCS = $(shell go env GOPATH)/bin/crd-ref-docs
.PHONY: crd-ref-docs
crd-ref-docs: ## Install crd-ref-docs (used by docs-api)
	@go install github.com/elastic/crd-ref-docs@$(CRD_REF_DOCS_VERSION)


##@ Documentation

.PHONY: docs-api
docs-api: crd-ref-docs ## Regenerate the API reference under docs/reference/api/ from kubebuilder markers
	@mkdir -p docs/reference/api
	$(CRD_REF_DOCS) \
		--config=hack/crd-ref-docs-config.yaml \
		--source-path=api/v1alpha1 \
		--renderer=markdown \
		--output-path=docs/reference/api/index.md \
		--output-mode=single
	@echo "API reference regenerated at docs/reference/api/index.md"
