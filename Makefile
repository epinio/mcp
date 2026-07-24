# Makefile for epinio-mcp — build, test, quality gates, and cluster install.
#
# This is the single supported tooling entry point: the local build/test/lint
# loop (the same gates CI runs) plus the cluster install targets under Deploy.
#
# Run `make help` for the target list.

BINARY      := epinio-mcp
PKG         := github.com/epinio/mcp
OUT         := dist
# Version is derived from git: the nearest tag, plus a -dirty suffix when the
# working tree has uncommitted changes. Falls back to "dev" outside a git tree.
VERSION     ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS     := -s -w -X main.version=$(VERSION)
GO          ?= go

# Pin golangci-lint via `go run` so contributors and CI use the same version
# without a separate install step.
GOLANGCI    := $(GO) run github.com/golangci/golangci-lint/cmd/golangci-lint@v1.64.8

# Deploy targets. NAMESPACE is where the MCP app is deployed — core and elevated
# alike, created if missing. Override freely: make setup NAMESPACE=workspace.
# EPINIO_INSTALL_NS is the fixed namespace where Epinio itself runs; cluster-prep
# waits on the chart-server pod there. Override EPINIO when the CLI isn't on
# PATH, e.g. make setup EPINIO=/path/to/epinio.
EPINIO            ?= epinio
NAMESPACE         ?= mcp
EPINIO_INSTALL_NS ?= epinio

.DEFAULT_GOAL := help

# Deploy recipes are multi-line shell scripts; run each in a single shell with
# fail-fast semantics so a failed step aborts the target.
.ONESHELL:
.SHELLFLAGS := -ec

##@ General

.PHONY: help
help: ## Print this help
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} \
		/^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2 } \
		/^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) }' $(MAKEFILE_LIST)

##@ Build

.PHONY: build
build: ## Build the server binary into dist/ with version ldflags
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(OUT)/$(BINARY) .

.PHONY: run
run: ## Build and run the server locally (reads EPINIO_* env vars)
	$(GO) run -ldflags "$(LDFLAGS)" .

.PHONY: clean
clean: ## Remove build artifacts
	rm -rf $(OUT)

##@ Quality

.PHONY: fmt
fmt: ## Format all Go code
	gofmt -w .

.PHONY: fmt-check
fmt-check: ## Fail if any Go file is not gofmt-clean
	@unformatted=$$(gofmt -l .); \
	if [ -n "$$unformatted" ]; then \
		echo "These files are not gofmt-clean:"; echo "$$unformatted"; exit 1; \
	fi

.PHONY: vet
vet: ## Run go vet
	$(GO) vet ./...

.PHONY: lint
lint: ## Run golangci-lint
	$(GOLANGCI) run ./...

.PHONY: tidy
tidy: ## Tidy and verify go.mod/go.sum
	$(GO) mod tidy
	$(GO) mod verify

##@ Test

.PHONY: test
test: ## Run unit tests with the race detector
	$(GO) test -race ./...

.PHONY: cover
cover: ## Run tests and write a coverage profile to dist/coverage.out
	@mkdir -p $(OUT)
	$(GO) test -race -coverprofile=$(OUT)/coverage.out ./...
	$(GO) tool cover -func=$(OUT)/coverage.out | tail -1

.PHONY: cover-html
cover-html: cover ## Open the HTML coverage report
	$(GO) tool cover -html=$(OUT)/coverage.out

##@ Aggregate

.PHONY: check
check: fmt-check vet lint test ## Run all CI gates (fmt-check, vet, lint, test)

##@ Container

.PHONY: image
image: ## Build the container image (TAG overrides the version)
	docker build -f install/Dockerfile \
		--build-arg VERSION=$(VERSION) \
		-t ghcr.io/epinio/mcp:$(or $(TAG),$(VERSION)) .

##@ Deploy (core)

.PHONY: setup
setup: push verify ## Core install: push the MCP to Epinio (epinio.yml) and smoke-test it

.PHONY: namespace
namespace: ## Ensure the target Epinio namespace exists (idempotent)
	$(EPINIO) namespace show $(NAMESPACE) >/dev/null 2>&1 || $(EPINIO) namespace create $(NAMESPACE)

.PHONY: push
push: namespace ## Push the MCP. MANIFEST overrides the manifest file (default epinio.yml)
	$(EPINIO) target $(NAMESPACE)
	$(EPINIO) push $(or $(MANIFEST),epinio.yml)
	echo "✓ push complete"

.PHONY: verify
verify: ## Smoke-test /healthz and /readyz on the deployed MCP
	$(EPINIO) target $(NAMESPACE)
	route=$$($(EPINIO) app show epinio-mcp 2>/dev/null | awk '/Active Routes/{f=1; next} f && /\|/{gsub(/[| ]/, ""); if(length($$0)>4){print; exit}}')
	if [ -z "$$route" ]; then
		echo "ERROR: could not determine route — is epinio-mcp running? Try: $(EPINIO) app show epinio-mcp"
		exit 1
	fi
	echo "Route: https://$$route"
	for i in $$(seq 1 20); do
		h=$$(curl -sk -o /dev/null -w "%{http_code}" "https://$$route/healthz")
		r=$$(curl -sk -o /dev/null -w "%{http_code}" "https://$$route/readyz")
		if [ "$$h" = "200" ] && [ "$$r" != "404" ]; then break; fi
		echo "  ($$i/20) healthz=$$h readyz=$$r — retrying in 3s"
		sleep 3
	done
	echo "--- /healthz ---"; curl -sk "https://$$route/healthz"; echo
	echo "--- /readyz ---";  curl -sk "https://$$route/readyz";  echo

##@ Deploy (elevated)

.PHONY: elevated-setup
elevated-setup: cluster-prep ## Elevated install: register the elevated appchart, then push (epinio-elevated.yml) + verify
	$(MAKE) push MANIFEST=epinio-elevated.yml
	$(MAKE) verify

.PHONY: cluster-prep
cluster-prep: ## (elevated, cluster-admin, once) register the chart server and the standard-elevated appchart
	kubectl apply -f manifests/chart-server.yaml
	kubectl -n $(EPINIO_INSTALL_NS) wait pod/chart-server --for=condition=ready --timeout=60s
	kubectl apply -f manifests/standard-elevated-appchart.yaml
	echo "✓ cluster-prep complete"
