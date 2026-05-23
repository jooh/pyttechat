# Pyttechat

Minimal Go scaffold for a server-rendered LLM chat web app. The app is intended to act as a Backend for Frontend between a browser chat UI and an enterprise LLM proxy API.

Status: early backend POC. The repository currently has a streaming `pyttechat` CLI with a dummy LLM implementation and an OpenResponses-compatible proxy client. It does not implement the web chat application yet.

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

Use an OpenResponses-compatible LLM proxy:

```sh
PYTTECHAT_LLM_PROXY_URL=http://localhost:8080 \
PYTTECHAT_LLM_PROXY_TOKEN=token-value \
  ./bin/pyttechat --model gpt-example chat
```

Set `--reasoning-effort` when you want to request model reasoning options. Assistant answer text streams to stdout. Reasoning events, when returned, stream separately to stderr. Proxy requests default to a 5-minute timeout; override it with `--proxy-timeout` or `PYTTECHAT_LLM_PROXY_TIMEOUT`.

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
