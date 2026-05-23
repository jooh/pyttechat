SHELL := /bin/sh

GO ?= go
GOFMT ?= gofmt

BIN_DIR := $(CURDIR)/.bin
BUILD_DIR := $(CURDIR)/bin
CACHE_DIR := $(CURDIR)/.cache
GO_BUILD_CACHE := $(CACHE_DIR)/go-build
GO_MOD_CACHE := $(CACHE_DIR)/go-mod
GO_TMP_DIR := $(CACHE_DIR)/tmp
PRE_COMMIT_CACHE := $(CACHE_DIR)/pre-commit

GOCACHE ?= $(GO_BUILD_CACHE)
GOMODCACHE ?= $(GO_MOD_CACHE)
GOTMPDIR ?= $(GO_TMP_DIR)
PRE_COMMIT_HOME ?= $(PRE_COMMIT_CACHE)

export GOCACHE
export GOMODCACHE
export GOTMPDIR
export PRE_COMMIT_HOME

GOIMPORTS_VERSION := v0.45.0
GOLANGCI_LINT_VERSION := v2.12.2
GOVULNCHECK_VERSION := v1.3.0
GOSEC_VERSION := v2.26.1
DEADCODE_VERSION := v0.45.0

GOIMPORTS := $(BIN_DIR)/goimports
GOLANGCI_LINT := $(BIN_DIR)/golangci-lint
GOVULNCHECK := $(BIN_DIR)/govulncheck
GOSEC := $(BIN_DIR)/gosec
DEADCODE := $(BIN_DIR)/deadcode

GO_FILES := $(shell find . -path './third_party' -prune -o -name '*.go' -print)

VERSION ?= dev
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || printf unknown)
DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -X 'example.com/llm-chat-web/internal/buildinfo.Version=$(VERSION)' -X 'example.com/llm-chat-web/internal/buildinfo.Commit=$(COMMIT)' -X 'example.com/llm-chat-web/internal/buildinfo.Date=$(DATE)'

.PHONY: help cache-dirs fmt fmt-check imports imports-check tidy tidy-check test test-race coverage lint lint-fast vet vuln security deadcode build clean pre-commit ci tools

help:
	@printf '%s\n' \
		'Targets:' \
		'  fmt           Format Go files with gofmt.' \
		'  fmt-check     Check gofmt formatting.' \
		'  imports       Format imports with goimports.' \
		'  imports-check Check goimports formatting.' \
		'  tidy          Run go mod tidy.' \
		'  tidy-check    Check go.mod/go.sum tidiness.' \
		'  test          Run unit tests.' \
		'  test-race     Run unit tests with the race detector.' \
		'  coverage      Generate coverage.out and print coverage summary.' \
		'  lint          Run golangci-lint.' \
		'  lint-fast     Run fast local lint checks.' \
		'  vet           Run go vet.' \
		'  vuln          Run govulncheck.' \
		'  security      Run gosec.' \
		'  deadcode      Run deadcode as an advisory check.' \
		'  build         Build bin/chat-web.' \
		'  clean         Remove local build and coverage artifacts.' \
		'  pre-commit    Run all configured pre-commit hooks.' \
		'  ci            Run PR-quality checks.'

$(BIN_DIR):
	mkdir -p $(BIN_DIR)

cache-dirs:
	mkdir -p "$(GOCACHE)" "$(GOMODCACHE)" "$(GOTMPDIR)" "$(PRE_COMMIT_HOME)"

$(GOIMPORTS): | $(BIN_DIR) cache-dirs
	GOBIN=$(BIN_DIR) $(GO) install golang.org/x/tools/cmd/goimports@$(GOIMPORTS_VERSION)

$(GOLANGCI_LINT): | $(BIN_DIR) cache-dirs
	GOBIN=$(BIN_DIR) $(GO) install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)

$(GOVULNCHECK): | $(BIN_DIR) cache-dirs
	GOBIN=$(BIN_DIR) $(GO) install golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION)

$(GOSEC): | $(BIN_DIR) cache-dirs
	GOBIN=$(BIN_DIR) $(GO) install github.com/securego/gosec/v2/cmd/gosec@$(GOSEC_VERSION)

$(DEADCODE): | $(BIN_DIR) cache-dirs
	GOBIN=$(BIN_DIR) $(GO) install golang.org/x/tools/cmd/deadcode@$(DEADCODE_VERSION)

tools: $(GOIMPORTS) $(GOLANGCI_LINT) $(GOVULNCHECK) $(GOSEC) $(DEADCODE)

fmt:
	$(GOFMT) -w $(GO_FILES)

fmt-check:
	@test -z "$$($(GOFMT) -l $(GO_FILES))" || { $(GOFMT) -l $(GO_FILES); exit 1; }

imports: $(GOIMPORTS)
	$(GOIMPORTS) -w $(GO_FILES)

imports-check: $(GOIMPORTS)
	@test -z "$$($(GOIMPORTS) -l $(GO_FILES))" || { $(GOIMPORTS) -l $(GO_FILES); exit 1; }

tidy: cache-dirs
	$(GO) mod tidy

tidy-check: cache-dirs
	@tmp_dir=$$(mktemp -d); \
	cp go.mod "$$tmp_dir/go.mod"; \
	if [ -f go.sum ]; then cp go.sum "$$tmp_dir/go.sum"; fi; \
	status=0; \
	$(GO) mod tidy || status=$$?; \
	if [ $$status -eq 0 ]; then git diff --exit-code -- go.mod go.sum || status=$$?; fi; \
	cp "$$tmp_dir/go.mod" go.mod; \
	if [ -f "$$tmp_dir/go.sum" ]; then cp "$$tmp_dir/go.sum" go.sum; else rm -f go.sum; fi; \
	rm -rf "$$tmp_dir"; \
	exit $$status

test: cache-dirs
	$(GO) test ./...

test-race: cache-dirs
	$(GO) test -race ./...

coverage: cache-dirs
	$(GO) test -coverprofile=coverage.out ./...
	$(GO) tool cover -func=coverage.out

lint: cache-dirs $(GOLANGCI_LINT)
	$(GOLANGCI_LINT) run ./...

lint-fast: vet

vet: cache-dirs
	$(GO) vet ./...

vuln: cache-dirs $(GOVULNCHECK)
	@pkgs=$$($(GO) list ./...); \
	$(GOVULNCHECK) $$pkgs

security: cache-dirs $(GOSEC)
	@dirs=$$($(GO) list -f '{{.Dir}}' ./...); \
	$(GOSEC) $$dirs

deadcode: cache-dirs $(DEADCODE)
	@pkgs=$$($(GO) list ./...); \
	$(DEADCODE) -test $$pkgs || { \
		printf '%s\n' 'deadcode is advisory during the scaffold phase; review findings above.'; \
		exit 0; \
	}

build: cache-dirs
	mkdir -p $(BUILD_DIR)
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/chat-web ./cmd/chat-web

clean:
	rm -rf $(BUILD_DIR) coverage.out coverage.html *.prof *.test test-results

pre-commit: cache-dirs
	pre-commit run --all-files

ci: fmt-check imports-check tidy-check vet lint test test-race coverage vuln security deadcode build
