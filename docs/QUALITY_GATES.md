# Quality Gates

The current scaffold has a tiny deterministic unit test and local build/test tooling. Future application work should expand coverage before adding production behavior.

## Current Checks

- `make fmt-check`
- `make imports-check`
- `make tidy-check`
- `make vet`
- `make lint-fast`
- `make test`
- `make test-race`
- `make coverage` enforces at least 95.0% total statement coverage across this module's Go packages.
- `make build`
- `make all-tests`

Broader PR-quality checks also include:

- `make lint`
- `make vuln`
- `make security`
- `make deadcode`
- `make ci`

Smoke/integration coverage is intentionally separate from the normal Go quality gates:

- `make container-smoke`
- `.github/workflows/smoke.yml`

The smoke workflow builds the container image, starts the fake Responses API, registers a browser user through the server-rendered app, submits a chat turn, consumes the SSE stream, and verifies conversation persistence across a container restart.

`make deadcode` is advisory during the scaffold phase because placeholder code can produce noisy findings. It should become strict once the app has a stable shape.

Project quality gates are scoped to this module's own Go packages. The reference submodules under `.agent/skills/references/*/repo` are read-only design references and are not part of lint, security, vulnerability, or deadcode enforcement.

Override the local minimum only when intentionally testing the gate:

```sh
COVERAGE_MIN=90.0 make coverage
```

## Future Test Coverage

Future app tests should cover:

- OIDC callback with fake IdP/JWKS
- secure session cookie attributes
- CSRF enforcement
- per-user conversation authorization
- message persistence
- LLM proxy JWT pass-through
- SSE streaming happy path
- SSE client disconnect cancellation
- upstream timeout/error mapping
- markdown sanitization
- migration tests against real Postgres
- minimal browser smoke tests

Unit tests must be deterministic and must not depend on external network. Integration tests that require services should use `testcontainers-go` or an equivalent controlled local service harness.
