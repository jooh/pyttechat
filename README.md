# LLM Chat Web

Minimal Go scaffold for a server-rendered LLM chat web app. The app is intended to act as a Backend for Frontend between a browser chat UI and an enterprise LLM proxy API.

Status: scaffold only. This repository does not implement the chat application yet.

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

The placeholder command currently prints build information:

```sh
./bin/chat-web
```

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
