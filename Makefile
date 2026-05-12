GO ?= go

TOOLS_DIR := .tools/bin
GOLANGCI_LINT_VERSION := v2.12.2
GOLANGCI_LINT := $(TOOLS_DIR)/golangci-lint

BINARY := bin/llmgate
VERSION ?= dev
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -X github.com/r13v/llmgate/internal/version.version=$(VERSION) -X github.com/r13v/llmgate/internal/version.commit=$(COMMIT) -X github.com/r13v/llmgate/internal/version.date=$(DATE)

.PHONY: build test test-e2e lint fmt check clean tools

build:
	mkdir -p bin
	$(GO) build -ldflags "$(LDFLAGS)" -o $(BINARY) ./cmd/llmgate

test:
	$(GO) test ./...

test-e2e:
	$(GO) test -tags=e2e ./...

lint: tools
	$(GOLANGCI_LINT) run ./...

fmt:
	$(GO) fmt ./...

check: fmt lint test test-e2e

clean:
	rm -rf bin dist .tools

tools: $(GOLANGCI_LINT)

$(GOLANGCI_LINT):
	mkdir -p $(TOOLS_DIR)
	GOBIN=$(abspath $(TOOLS_DIR)) $(GO) install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)
