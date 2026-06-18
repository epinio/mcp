# Makefile for epinio-mcp — build, test, and quality gates.
#
# This complements Taskfile.yml: the Taskfile handles deploying the MCP into a
# cluster (cluster-prep, s3-service, push, verify); this Makefile handles the
# local build/test/lint loop and the same gates CI runs.
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

.DEFAULT_GOAL := help

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
