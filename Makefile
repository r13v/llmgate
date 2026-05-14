GO ?= go

TOOLS_DIR := .tools/bin
GOLANGCI_LINT_VERSION := v2.12.2
GOLANGCI_LINT := $(TOOLS_DIR)/golangci-lint

BINARY := bin/llmgate
VERSION ?= dev
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -X github.com/r13v/llmgate/internal/version.version=$(VERSION) -X github.com/r13v/llmgate/internal/version.commit=$(COMMIT) -X github.com/r13v/llmgate/internal/version.date=$(DATE)

.PHONY: build package test test-e2e lint fmt check clean update-main tools

build:
	mkdir -p bin
	$(GO) build -ldflags "$(LDFLAGS)" -o $(BINARY) ./cmd/llmgate

package:
	GO=$(GO) VERSION=main COMMIT=$(COMMIT) DATE=$(DATE) scripts/package.sh

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

update-main:
	git fetch origin +refs/heads/main:refs/remotes/origin/main
	git merge --ff-only refs/remotes/origin/main

tools: $(GOLANGCI_LINT)

$(GOLANGCI_LINT):
	mkdir -p $(TOOLS_DIR)
	GOBIN=$(abspath $(TOOLS_DIR)) $(GO) install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)
