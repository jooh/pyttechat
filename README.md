# Pyttechat

Minimal Go scaffold for a server-rendered LLM chat web app. The app is intended to act as a Backend for Frontend between a browser chat UI and an enterprise LLM proxy API.

Status: early backend/web POC. The repository currently has a streaming `pyttechat` CLI, a minimal server-rendered web chat UI, a dummy LLM implementation, and an OpenResponses-compatible proxy client.

## Quick Start

```sh
go test ./...
make test
make build
```

The Makefile keeps Go and pre-commit caches under `.cache/` so normal project checks do not need to write to user-level cache directories. For a bare `go test ./...` in restricted shells, export the same cache locations first:

```sh
export GOCACHE="$PWD/.cache/go-build"
export GOMODCACHE="$PWD/.cache/go-mod"
export GOTMPDIR="$PWD/.cache/tmp"
mkdir -p "$GOCACHE" "$GOMODCACHE" "$GOTMPDIR"
go test ./...
```

Build the CLI and inspect build information:

```sh
make build
./bin/pyttechat version
```

Send a prompt to the dummy LLM:

```sh
./bin/pyttechat ask "hello"
```

Start a single ephemeral multi-turn chat session. Each input line is sent as the next user turn:

```sh
printf 'hello\nagain\n' | ./bin/pyttechat chat
```

Start the web chat POC:

```sh
./bin/pyttechat serve --addr :3000
```

Then open `http://localhost:3000`. Browser sessions are in memory and reset when the process restarts. A submitted turn starts on the server first, and the browser subscribes to that turn with `EventSource`, so closing the stream subscription does not cancel the upstream request. Use the Stop button to cancel an active turn.

Use an OpenResponses-compatible LLM proxy:

```sh
PYTTECHAT_LLM_PROXY_URL=http://localhost:8080 \
PYTTECHAT_LLM_PROXY_TOKEN=token-value \
  ./bin/pyttechat --model gpt-example chat

PYTTECHAT_LLM_PROXY_URL=http://localhost:8080 \
PYTTECHAT_LLM_PROXY_TOKEN=token-value \
  ./bin/pyttechat --model gpt-example serve
```

Set `--reasoning-effort` when you want to request model reasoning options. Assistant answer text streams to stdout. Reasoning events, when returned, stream separately to stderr. Proxy requests default to a 5-minute timeout; override it with `--proxy-timeout` or `PYTTECHAT_LLM_PROXY_TIMEOUT`.

## Fake OpenResponses Provider

The repo includes a deterministic fake OpenAI-compatible Responses API provider for tests and local development. Prefer `internal/llm/openresponses/fakeprovider.NewHandler()` in Go tests instead of hand-written happy-path SSE stubs. Keep custom `httptest` handlers for malformed streams, auth assertions, and narrow parser edge cases.

Run the local harness manually:

```sh
go run ./cmd/fake-responses --addr :8080
```

Verify the streaming endpoint:

```sh
curl -N http://localhost:8080/v1/responses \
  -H 'Content-Type: application/json' \
  -d '{"model":"dummy-responses","input":"hello world","stream":true}'
```

Expected: an SSE stream with typed response lifecycle events, text deltas, `response.completed`, and final `data: [DONE]`.

## Pre-Commit

Install the hooks after cloning:

```sh
pre-commit install
```

Run the same hooks manually:

```sh
make pre-commit
```

The pre-commit checks are intentionally fast: formatting, module tidiness, fast linting, and unit tests.

## Local CI

Run the PR-quality checks locally:

```sh
make ci
```

The Makefile installs pinned Go tools into `.bin/` when needed.

## Reference Submodules

Initialize the read-only reference implementations after cloning:

```sh
git submodule update --init --depth 1 --recursive
```

See `third_party/reference/README.md` and `AGENTS.md` before using the references.
