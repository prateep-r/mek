# mek — developer tasks. Run `make` (or `make help`) to list targets.
# Releases are built by GoReleaser from a pushed tag; this Makefile is for local work.

BINARY  := mek
MODULE  := github.com/prateep-r/mek
MAIN    := ./cmd/mek
BIN_DIR := bin
PREFIX  ?= $(HOME)/.local/bin

# Build metadata, same variables GoReleaser sets (falls back outside a git repo).
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w \
	-X $(MODULE)/internal/version.Version=$(VERSION) \
	-X $(MODULE)/internal/version.Commit=$(COMMIT) \
	-X $(MODULE)/internal/version.Date=$(DATE)

GOVULNCHECK := golang.org/x/vuln/cmd/govulncheck@latest

.DEFAULT_GOAL := help
.PHONY: help build run install uninstall test cover fmt fmt-check vet lint vuln tidy check snapshot release-check clean

help: ## Show this help
	@awk 'BEGIN {FS = ":.*## "} /^[a-zA-Z_-]+:.*## / {printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

## ---- build ----

build: ## Build ./bin/mek with version info
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/$(BINARY) $(MAIN)

run: build ## Build and run, e.g. make run ARGS="doctor"
	./$(BIN_DIR)/$(BINARY) $(ARGS)

install: build ## Build and copy to ~/.local/bin (override: make install PREFIX=/usr/local/bin)
	@mkdir -p $(PREFIX)
	install -m 0755 $(BIN_DIR)/$(BINARY) $(PREFIX)/$(BINARY)
	@echo "installed $(PREFIX)/$(BINARY) ($(VERSION))"

uninstall: ## Remove the installed binary (same PREFIX as install)
	rm -f $(PREFIX)/$(BINARY)

## ---- quality ----

test: ## Run unit tests with the race detector
	go test -race ./...

cover: ## Run tests and open an HTML coverage report
	@mkdir -p $(BIN_DIR)
	go test -coverprofile=$(BIN_DIR)/coverage.out ./...
	go tool cover -html=$(BIN_DIR)/coverage.out

fmt: ## Format all Go files
	gofmt -w .

fmt-check: ## Fail if any Go file is not gofmt-ed
	@out=$$(gofmt -l .); if [ -n "$$out" ]; then echo "not gofmt-ed:"; echo "$$out"; exit 1; fi

vet: ## Run go vet
	go vet ./...

lint: ## Run golangci-lint (brew install golangci-lint)
	@command -v golangci-lint >/dev/null || { echo "golangci-lint not found: brew install golangci-lint"; exit 1; }
	golangci-lint run ./...

vuln: ## Check dependencies for known vulnerabilities (govulncheck)
	go run $(GOVULNCHECK) ./...

tidy: ## Tidy go.mod / go.sum
	go mod tidy

check: fmt-check vet test ## Everything CI should pass — run before pushing

## ---- release ----

snapshot: ## GoReleaser build of all archives into dist/ (no publish)
	@command -v goreleaser >/dev/null || { echo "goreleaser not found: brew install goreleaser"; exit 1; }
	goreleaser release --snapshot --clean

release-check: ## Validate .goreleaser.yaml
	@command -v goreleaser >/dev/null || { echo "goreleaser not found: brew install goreleaser"; exit 1; }
	goreleaser check

clean: ## Remove build output
	rm -rf $(BIN_DIR) dist
