SHELL := /bin/bash
GO    ?= go

BINARY   := leanctl
PKG      := ./cmd/leanctl
BIN_DIR  := bin

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

MODULE  := github.com/leansignal/leansignal-cli
LDFLAGS := -s -w \
	-X $(MODULE)/internal/build.Version=$(VERSION) \
	-X $(MODULE)/internal/build.Commit=$(COMMIT) \
	-X $(MODULE)/internal/build.Date=$(DATE)

.DEFAULT_GOAL := help

.PHONY: help
help: ## Show this help
	@grep -hE '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) \
		| awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}'

.PHONY: build
build: ## Build ./bin/leanctl
	@mkdir -p $(BIN_DIR)
	$(GO) build -trimpath -ldflags '$(LDFLAGS)' -o $(BIN_DIR)/$(BINARY) $(PKG)
	@echo "built $(BIN_DIR)/$(BINARY) $(VERSION)"

.PHONY: install
install: ## Install leanctl into $$GOBIN (or $$GOPATH/bin)
	$(GO) install -trimpath -ldflags '$(LDFLAGS)' $(PKG)

.PHONY: test
test: ## Run the test suite
	$(GO) test ./... -count=1

.PHONY: test-race
test-race: ## Run the test suite with the race detector
	$(GO) test ./... -count=1 -race

.PHONY: cover
cover: ## Write an HTML coverage report to coverage/
	@mkdir -p coverage
	$(GO) test ./... -count=1 -coverprofile=coverage/coverage.out
	$(GO) tool cover -html=coverage/coverage.out -o coverage/coverage.html
	@echo "coverage/coverage.html"

.PHONY: fmt
fmt: ## Format the source
	$(GO) fmt ./...

.PHONY: vet
vet: ## Run go vet
	$(GO) vet ./...

.PHONY: lint
lint: ## Run golangci-lint (see .golangci.yaml)
	golangci-lint run

.PHONY: check
check: fmt vet lint ## fmt + vet + lint

.PHONY: ci
ci: check test ## Everything CI runs

.PHONY: tidy
tidy: ## Tidy go.mod/go.sum
	$(GO) mod tidy

.PHONY: snapshot
snapshot: ## Build release artifacts locally without publishing
	goreleaser release --snapshot --clean

.PHONY: clean
clean: ## Remove build output
	rm -rf $(BIN_DIR) dist coverage
