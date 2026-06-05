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
WEB_ADDR ?= 127.0.0.1:3000
WEB_PID_FILE ?= $(CACHE_DIR)/pyttechat-web.pid
WEB_LOG_FILE ?= $(CACHE_DIR)/pyttechat-web.log
FAKE_RESPONSES_ADDR ?= 127.0.0.1:8080
FAKE_RESPONSES_STREAM_DELAY ?= 150ms
FAKE_RESPONSES_PID_FILE ?= $(CACHE_DIR)/fake-responses.pid
FAKE_RESPONSES_LOG_FILE ?= $(CACHE_DIR)/fake-responses.log
IMAGE ?= pyttechat:local
CONTAINER_SMOKE_PORT ?= 3007
CONTAINER_SMOKE_FAKE_PORT ?= 8087
CONTAINER_SMOKE_FAKE_ADDR ?= 0.0.0.0:$(CONTAINER_SMOKE_FAKE_PORT)
CONTAINER_SMOKE_PROXY_URL ?= http://host.docker.internal:$(CONTAINER_SMOKE_FAKE_PORT)

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
COVERAGE_PROFILE ?= coverage.out
COVERAGE_MIN ?= 95.0

GO_FILES := $(shell find . \( -path './.agent/skills/references/*/repo' -o -path './third_party' -o -path './.cache' -o -path './.bin' -o -path './bin' \) -prune -o -name '*.go' -print)

VERSION ?= dev
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || printf unknown)
DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -X 'example.com/llm-chat-web/internal/buildinfo.Version=$(VERSION)' -X 'example.com/llm-chat-web/internal/buildinfo.Commit=$(COMMIT)' -X 'example.com/llm-chat-web/internal/buildinfo.Date=$(DATE)'

.PHONY: help cache-dirs fmt fmt-check imports imports-check tidy tidy-check test test-race coverage lint lint-fast vet vuln security deadcode build build-fake-responses image container-smoke serve-start serve-stop serve-status serve-restart serve-fake-start serve-fake-stop clean pre-commit all-tests ci tools

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
		'  coverage      Generate coverage.out, print summary, and enforce minimum coverage.' \
		'  lint          Run golangci-lint.' \
		'  lint-fast     Run fast local lint checks.' \
		'  vet           Run go vet.' \
		'  vuln          Run govulncheck.' \
		'  security      Run gosec.' \
		'  deadcode      Run deadcode as an advisory check.' \
		'  build         Build bin/pyttechat.' \
		'  build-fake-responses Build bin/fake-responses.' \
		'  image         Build the single-container image.' \
		'  container-smoke Build and smoke test the container image.' \
		'  serve-start   Start local web server in the background.' \
		'  serve-stop    Stop local web server started by serve-start.' \
		'  serve-status  Show local web server status.' \
		'  serve-restart Restart local web server.' \
		'  serve-fake-start Start fake Responses API and web app for streaming UI checks.' \
		'  serve-fake-stop Stop fake Responses API and web app.' \
		'  clean         Remove local build and coverage artifacts.' \
		'  pre-commit    Run all configured pre-commit hooks.' \
		'  all-tests     Run all commit-gating checks.' \
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
	$(GO) test -coverprofile=$(COVERAGE_PROFILE) ./...
	@coverage_output=$$($(GO) tool cover -func=$(COVERAGE_PROFILE)); \
	printf '%s\n' "$$coverage_output"; \
	total=$$(printf '%s\n' "$$coverage_output" | awk '/^total:/ { gsub(/%/, "", $$3); print $$3 }'); \
	awk -v total="$$total" -v minimum="$(COVERAGE_MIN)" 'BEGIN { \
		if (total == "") { \
			print "could not determine total coverage"; \
			exit 1; \
		} \
		if (total + 0 < minimum + 0) { \
			printf "coverage %.1f%% is below required %.1f%%\n", total, minimum; \
			exit 1; \
		} \
		printf "coverage %.1f%% meets required %.1f%%\n", total, minimum; \
	}'

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
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/pyttechat ./cmd/pyttechat

build-fake-responses: cache-dirs
	mkdir -p $(BUILD_DIR)
	$(GO) build -trimpath -o $(BUILD_DIR)/fake-responses ./cmd/fake-responses

image:
	docker build -t "$(IMAGE)" .

container-smoke: image build-fake-responses
	@set -eu; \
	name="pyttechat-smoke-$$$$"; \
	volume="pyttechat-smoke-$$$$"; \
	cookies="$$(mktemp)"; \
	body="$$(mktemp)"; \
	fake_log="$$(mktemp)"; \
	fake_pid=""; \
	cleanup() { \
		docker rm -f "$$name" >/dev/null 2>&1 || true; \
		docker volume rm "$$volume" >/dev/null 2>&1 || true; \
		if [ -n "$$fake_pid" ]; then \
			kill "$$fake_pid" >/dev/null 2>&1 || true; \
			wait "$$fake_pid" >/dev/null 2>&1 || true; \
		fi; \
		rm -f "$$cookies" "$$body" "$$fake_log"; \
	}; \
	trap cleanup EXIT; \
	"$(BUILD_DIR)/fake-responses" --addr "$(CONTAINER_SMOKE_FAKE_ADDR)" --stream-delay 0s >"$$fake_log" 2>&1 & \
	fake_pid=$$!; \
	sleep 1; \
	if ! kill -0 "$$fake_pid" 2>/dev/null; then \
		printf '%s\n' 'fake Responses API failed to start'; \
		cat "$$fake_log"; \
		exit 1; \
	fi; \
	docker volume create "$$volume" >/dev/null; \
	docker run -d --name "$$name" --add-host=host.docker.internal:host-gateway -p "127.0.0.1:$(CONTAINER_SMOKE_PORT):3000" -v "$$volume:/var/lib/pyttechat" -e "PYTTECHAT_LLM_PROXY_URL=$(CONTAINER_SMOKE_PROXY_URL)" "$(IMAGE)" >/dev/null; \
	base="http://127.0.0.1:$(CONTAINER_SMOKE_PORT)"; \
	for i in $$(seq 1 50); do \
		if curl -fsS -c "$$cookies" -b "$$cookies" "$$base/register" >"$$body"; then break; fi; \
		sleep 0.2; \
	done; \
	csrf=$$(sed -n 's/.*name="csrf-token" content="\([^"]*\)".*/\1/p' "$$body" | head -n1); \
	test -n "$$csrf"; \
	curl -fsSL -c "$$cookies" -b "$$cookies" \
		--data-urlencode "csrf_token=$$csrf" \
		--data-urlencode "username=smoke" \
		--data-urlencode "password=correct horse" \
		"$$base/register" >"$$body"; \
	csrf=$$(sed -n 's/.*name="csrf-token" content="\([^"]*\)".*/\1/p' "$$body" | head -n1); \
	test -n "$$csrf"; \
	stream_url=$$(curl -fsS -c "$$cookies" -b "$$cookies" \
		-H "Content-Type: application/json" \
		-H "X-CSRF-Token: $$csrf" \
		-d '{"prompt":"container smoke prompt"}' \
		"$$base/chat/turns" | sed -n 's/.*"stream_url":"\([^"]*\)".*/\1/p'); \
	test -n "$$stream_url"; \
	curl -fsS -c "$$cookies" -b "$$cookies" "$$base$$stream_url" >"$$body"; \
	grep -q 'Echo: container smoke prompt' "$$body"; \
	docker rm -f "$$name" >/dev/null; \
	docker run -d --name "$$name" --add-host=host.docker.internal:host-gateway -p "127.0.0.1:$(CONTAINER_SMOKE_PORT):3000" -v "$$volume:/var/lib/pyttechat" -e "PYTTECHAT_LLM_PROXY_URL=$(CONTAINER_SMOKE_PROXY_URL)" "$(IMAGE)" >/dev/null; \
	for i in $$(seq 1 50); do \
		if curl -fsS -c "$$cookies" -b "$$cookies" "$$base/" >"$$body"; then break; fi; \
		sleep 0.2; \
	done; \
	grep -q 'container smoke prompt' "$$body"; \
	grep -q 'Echo: container smoke prompt' "$$body"; \
	printf '%s\n' 'container smoke passed'

serve-start: build cache-dirs
	@if [ -f "$(WEB_PID_FILE)" ]; then \
		pid=$$(cat "$(WEB_PID_FILE)"); \
		if kill -0 "$$pid" 2>/dev/null; then \
			printf 'pyttechat web already running on http://%s (pid %s)\n' "$(WEB_ADDR)" "$$pid"; \
			printf 'log: %s\n' "$(WEB_LOG_FILE)"; \
			exit 0; \
		fi; \
		rm -f "$(WEB_PID_FILE)"; \
	fi; \
	: > "$(WEB_LOG_FILE)"; \
	nohup "$(BUILD_DIR)/pyttechat" serve --addr "$(WEB_ADDR)" >"$(WEB_LOG_FILE)" 2>&1 & \
	pid=$$!; \
	printf '%s\n' "$$pid" >"$(WEB_PID_FILE)"; \
	sleep 1; \
	if ! kill -0 "$$pid" 2>/dev/null; then \
		printf 'pyttechat web failed to start; see %s\n' "$(WEB_LOG_FILE)"; \
		rm -f "$(WEB_PID_FILE)"; \
		exit 1; \
	fi; \
	printf 'pyttechat web started on http://%s (pid %s)\n' "$(WEB_ADDR)" "$$pid"; \
	printf 'log: %s\n' "$(WEB_LOG_FILE)"

serve-stop:
	@if [ ! -f "$(WEB_PID_FILE)" ]; then \
		printf 'pyttechat web is not running; no pid file at %s\n' "$(WEB_PID_FILE)"; \
		exit 0; \
	fi; \
	pid=$$(cat "$(WEB_PID_FILE)"); \
	if ! kill -0 "$$pid" 2>/dev/null; then \
		printf 'removing stale pid file for stopped process %s\n' "$$pid"; \
		rm -f "$(WEB_PID_FILE)"; \
		exit 0; \
	fi; \
	command=$$(ps -p "$$pid" -o command= 2>/dev/null || true); \
	case "$$command" in \
		*"pyttechat serve"*|*"pyttechat "*" serve"*) ;; \
		*) \
			printf 'refusing to stop pid %s; it is not a pyttechat serve process\n' "$$pid"; \
			printf 'command: %s\n' "$$command"; \
			exit 1; \
			;; \
	esac; \
	kill "$$pid"; \
	i=0; \
	while kill -0 "$$pid" 2>/dev/null; do \
		if [ "$$i" -ge 5 ]; then \
			printf 'pyttechat web did not stop after SIGTERM (pid %s)\n' "$$pid"; \
			exit 1; \
		fi; \
		i=$$((i + 1)); \
		sleep 1; \
	done; \
	rm -f "$(WEB_PID_FILE)"; \
	printf 'pyttechat web stopped (pid %s)\n' "$$pid"

serve-status:
	@if [ ! -f "$(WEB_PID_FILE)" ]; then \
		printf 'pyttechat web is not running; no pid file at %s\n' "$(WEB_PID_FILE)"; \
		exit 0; \
	fi; \
	pid=$$(cat "$(WEB_PID_FILE)"); \
	if kill -0 "$$pid" 2>/dev/null; then \
		printf 'pyttechat web appears to be running on http://%s (pid %s)\n' "$(WEB_ADDR)" "$$pid"; \
		printf 'log: %s\n' "$(WEB_LOG_FILE)"; \
	else \
		printf 'pyttechat web is not running; stale pid file contains %s\n' "$$pid"; \
	fi

serve-restart: serve-stop serve-start

serve-fake-start: build build-fake-responses cache-dirs
	@if [ -f "$(WEB_PID_FILE)" ]; then \
		pid=$$(cat "$(WEB_PID_FILE)"); \
		if kill -0 "$$pid" 2>/dev/null; then \
			printf 'pyttechat web already running on http://%s (pid %s); run make serve-stop first\n' "$(WEB_ADDR)" "$$pid"; \
			exit 1; \
		fi; \
		rm -f "$(WEB_PID_FILE)"; \
	fi; \
	if [ -f "$(FAKE_RESPONSES_PID_FILE)" ]; then \
		pid=$$(cat "$(FAKE_RESPONSES_PID_FILE)"); \
		if kill -0 "$$pid" 2>/dev/null; then \
			printf 'fake Responses API already running on http://%s (pid %s)\n' "$(FAKE_RESPONSES_ADDR)" "$$pid"; \
		else \
			rm -f "$(FAKE_RESPONSES_PID_FILE)"; \
		fi; \
	fi; \
	if [ ! -f "$(FAKE_RESPONSES_PID_FILE)" ]; then \
		: > "$(FAKE_RESPONSES_LOG_FILE)"; \
		nohup "$(BUILD_DIR)/fake-responses" --addr "$(FAKE_RESPONSES_ADDR)" --stream-delay "$(FAKE_RESPONSES_STREAM_DELAY)" >"$(FAKE_RESPONSES_LOG_FILE)" 2>&1 & \
		fake_pid=$$!; \
		printf '%s\n' "$$fake_pid" >"$(FAKE_RESPONSES_PID_FILE)"; \
		sleep 1; \
		if ! kill -0 "$$fake_pid" 2>/dev/null; then \
			printf 'fake Responses API failed to start; see %s\n' "$(FAKE_RESPONSES_LOG_FILE)"; \
			rm -f "$(FAKE_RESPONSES_PID_FILE)"; \
			exit 1; \
		fi; \
	fi; \
	: > "$(WEB_LOG_FILE)"; \
	nohup "$(BUILD_DIR)/pyttechat" --proxy-url "http://$(FAKE_RESPONSES_ADDR)" --reasoning-effort low serve --addr "$(WEB_ADDR)" >"$(WEB_LOG_FILE)" 2>&1 & \
	web_pid=$$!; \
	printf '%s\n' "$$web_pid" >"$(WEB_PID_FILE)"; \
	sleep 1; \
	if ! kill -0 "$$web_pid" 2>/dev/null; then \
		printf 'pyttechat web failed to start; see %s\n' "$(WEB_LOG_FILE)"; \
		rm -f "$(WEB_PID_FILE)"; \
		exit 1; \
	fi; \
	printf 'fake Responses API started on http://%s\n' "$(FAKE_RESPONSES_ADDR)"; \
	printf 'fake log: %s\n' "$(FAKE_RESPONSES_LOG_FILE)"; \
	printf 'pyttechat web started on http://%s\n' "$(WEB_ADDR)"; \
	printf 'web log: %s\n' "$(WEB_LOG_FILE)"

serve-fake-stop: serve-stop
	@if [ ! -f "$(FAKE_RESPONSES_PID_FILE)" ]; then \
		printf 'fake Responses API is not running; no pid file at %s\n' "$(FAKE_RESPONSES_PID_FILE)"; \
		exit 0; \
	fi; \
	pid=$$(cat "$(FAKE_RESPONSES_PID_FILE)"); \
	if ! kill -0 "$$pid" 2>/dev/null; then \
		printf 'removing stale pid file for stopped fake Responses API process %s\n' "$$pid"; \
		rm -f "$(FAKE_RESPONSES_PID_FILE)"; \
		exit 0; \
	fi; \
	command=$$(ps -p "$$pid" -o command= 2>/dev/null || true); \
	case "$$command" in \
		*"fake-responses"*) ;; \
		*) \
			printf 'refusing to stop pid %s; it is not a fake-responses process\n' "$$pid"; \
			printf 'command: %s\n' "$$command"; \
			exit 1; \
			;; \
	esac; \
	kill "$$pid"; \
	i=0; \
	while kill -0 "$$pid" 2>/dev/null; do \
		if [ "$$i" -ge 5 ]; then \
			printf 'fake Responses API did not stop after SIGTERM (pid %s)\n' "$$pid"; \
			exit 1; \
		fi; \
		i=$$((i + 1)); \
		sleep 1; \
	done; \
	rm -f "$(FAKE_RESPONSES_PID_FILE)"; \
	printf 'fake Responses API stopped (pid %s)\n' "$$pid"

clean:
	rm -rf $(BUILD_DIR) coverage.out coverage.html *.prof *.test test-results

pre-commit: cache-dirs
	pre-commit run --all-files

all-tests: fmt-check imports-check tidy-check vet lint-fast test test-race coverage build

ci: all-tests lint vuln security deadcode
