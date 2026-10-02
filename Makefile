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

# Pinned, so CI results never change on their own; bump deliberately.
GOVULNCHECK := golang.org/x/vuln/cmd/govulncheck@v1.8.0

# Coverage counts product code only (not the test/ helpers).
COVER_DIR  := $(abspath $(BIN_DIR)/cover)
COVER_PKGS := ./cmd/...,./internal/...

.DEFAULT_GOAL := help
.PHONY: help build run install uninstall test test-integration test-e2e test-contract test-emulator test-all docker-test docker-up docker-down docker-clean cover cover-html fmt fmt-check vet lint vuln tidy check snapshot release-check clean

help: ## Show this help
	@awk 'BEGIN {FS = ":.*## "} /^[a-zA-Z0-9_-]+:.*## / {printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

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

test-integration: ## Run the real binary against stub cloud CLIs (env, guard, audit, signals)
	go test -tags integration -count=1 ./test/integration/...

test-e2e: ## Install from release artifacts with install.sh, then user journeys (incl. real prompts on a pty)
	go test -tags e2e -count=1 ./test/e2e/...

test-contract: ## Run mek with the real cloud CLIs, offline (a missing CLI is skipped)
	go test -tags contract -count=1 ./test/contract/...

test-emulator: ## Real CLIs against Floci cloud emulators in docker (AWS, GCP, Azure)
	go test -tags emulator -count=1 ./test/emulator/...

test-all: check test-integration test-e2e test-contract test-emulator ## Every test layer

cover: ## Combined unit + integration coverage; fails below 100%
	@rm -rf $(COVER_DIR) && mkdir -p $(COVER_DIR)/unit $(COVER_DIR)/integration
	go test -count=1 -cover -coverpkg=$(COVER_PKGS) ./cmd/... ./internal/... -args -test.gocoverdir=$(COVER_DIR)/unit
	MEK_COVERDIR=$(COVER_DIR)/integration go test -tags integration -count=1 ./test/integration/...
	go tool covdata textfmt -i=$(COVER_DIR)/unit,$(COVER_DIR)/integration -o $(COVER_DIR)/cover.out
	@go tool cover -func=$(COVER_DIR)/cover.out | awk '$$3 != "100.0%"'; \
	  total=$$(go tool cover -func=$(COVER_DIR)/cover.out | awk '/^total:/ {print $$3}'); \
	  echo "total coverage: $$total"; [ "$$total" = "100.0%" ] || { echo "coverage must be 100%"; exit 1; }

cover-html: cover ## Open the combined coverage report in a browser
	go tool cover -html=$(COVER_DIR)/cover.out

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

## ---- docker (mek's own, isolated test environment) ----

COMPOSE := docker compose -f test/docker/compose.yaml

docker-test: ## Every test layer in mek's container (real CLIs + Floci); TARGETS="..." to pick
	$(COMPOSE) run --rm --build tests $(TARGETS); status=$$?; $(COMPOSE) down; exit $$status

docker-up: ## Start mek's Floci emulators on 127.0.0.1:14566 (AWS) / 14588 (GCP) / 14577 (Azure)
	$(COMPOSE) up -d --wait floci-aws floci-gcp floci-az

docker-down: ## Stop and remove mek's containers, network and cache volume
	$(COMPOSE) --profile tests down --volumes --remove-orphans

docker-clean: docker-down ## docker-down, and remove the mek-test image too
	-docker image rm mek-test:local

## ---- release ----

snapshot: ## GoReleaser build of all archives into dist/ (no publish)
	@command -v goreleaser >/dev/null || { echo "goreleaser not found: brew install goreleaser"; exit 1; }
	goreleaser release --snapshot --clean

release-check: ## Validate .goreleaser.yaml
	@command -v goreleaser >/dev/null || { echo "goreleaser not found: brew install goreleaser"; exit 1; }
	goreleaser check

clean: ## Remove build output
	rm -rf $(BIN_DIR) dist
