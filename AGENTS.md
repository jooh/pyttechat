# Agent Instructions

## Project Goal

Build a minimal server-rendered Go web app that acts as a Backend for Frontend for an LLM chat UI. The browser talks to this app, this app talks to an LLM proxy API, and the proxy owns provider routing, MCP/tool execution, token accounting, and related policy.

This repository is currently Step 1 only: foundation, guardrails, reference implementations, build tooling, and documentation.

## Non-Goals For Now

- Do not implement the real chat app in Step 1.
- Do not add OAuth/OIDC flows yet.
- Do not add Postgres schema or migrations yet.
- Do not add conversation, message, user, or session persistence yet.
- Do not add SSE streaming yet.
- Do not add markdown rendering yet.
- Do not add an admin UI or provider configuration UI yet.
- Do not introduce a Node toolchain unless explicitly approved later.
- Do not introduce React, Next.js, Vue, Svelte, or another SPA framework unless explicitly approved later.

## Architecture Summary

The intended flow is:

```text
Browser
  -> chat web app / BFF
  -> LLM proxy API
  -> LLM APIs and MCP servers
```

The browser must not call the LLM proxy directly. The browser must not store OAuth access tokens in local storage, session storage, JavaScript memory as a durable state mechanism, or any other browser-controlled persistence.

Future authentication should use OAuth/OIDC Authorization Code with PKCE. The app should issue secure server-side sessions through `HttpOnly`, `Secure`, `SameSite` cookies. OAuth tokens should be stored and refreshed server-side. When calling the LLM proxy, the app should forward the user's access token with `Authorization: Bearer`.

Future LLM streaming from the app to the browser should use Server-Sent Events unless requirements justify WebSockets. Postgres should be used for durable application state.

## Reference Implementation Guide

The submodules under `third_party/reference/` are read-only inspiration. Do not copy code from them into this project.

- Miniflux is the primary implementation inspiration. Use it for minimal serious Go web app structure, server-rendered UI, Postgres state, migrations, configuration style, low-dependency discipline, container packaging, testing approach, and OAuth/OIDC support in a small app.
- Gitea is a mature Go web app reference for auth/session handling, CSRF, external auth provider modeling, user creation and account linking from external identity, per-request authorization checks, and handler/integration test organization.
- Grafana is only a targeted reference for enterprise OAuth/OIDC edge cases, including generic OAuth configuration, claim mapping, role/group mapping, token refresh, and session lifetime patterns. Do not use Grafana as a general architecture template.
- LibreChat is a product and behavior reference for mature LLM chat UX. Use it to understand conversation list behavior, message lifecycle, streaming response UX, multi-model/provider concepts, file/image/message rendering patterns, and admin/configuration concepts worth avoiding or simplifying. Do not copy LibreChat's frontend architecture. This project intentionally avoids a Node/React SPA architecture for now.

## Coding Standards

- Keep the app server-rendered and minimal.
- Prefer the Go standard library and small focused dependencies.
- Use boring, auditable code over framework-heavy abstractions.
- Keep comments minimal and useful.
- Format all Go code with `gofmt` and `goimports`.
- Keep generated artifacts, coverage files, binaries, local tools, and temporary files out of Git.
- Preserve clear package boundaries. Avoid adding abstractions before there is concrete duplication or complexity to remove.

## Testing Standards

- `make test` must run `go test ./...`.
- Tests must be deterministic and must not depend on external network.
- Add tests with production behavior. Do not leave security-sensitive behavior covered only by manual checks.
- Integration tests that require services should eventually use `testcontainers-go` or an equivalent controlled harness.
- Future tests should cover OIDC callbacks with fake IdP/JWKS, secure session cookie attributes, CSRF enforcement, per-user conversation authorization, message persistence, LLM proxy JWT pass-through, SSE streaming, cancellation on client disconnect, upstream timeout/error mapping, markdown sanitization, migration behavior against real Postgres, and minimal browser smoke coverage.

## Security Standards

- No direct browser-to-LLM-proxy calls.
- No browser storage of OAuth tokens.
- Use secure server-side sessions.
- Treat OAuth tokens, refresh tokens, session IDs, and LLM proxy credentials as secrets.
- Treat LLM output as untrusted content.
- Future markdown rendering must sanitize model output before display.
- Enforce CSRF protection on state-changing requests.
- Check authorization per request and per resource.
- Prefer explicit timeout, cancellation, and error mapping for upstream calls.

## Dependency Policy

- Prefer the standard library.
- Add dependencies only when they remove meaningful risk or complexity.
- Avoid broad frameworks and hidden runtime behavior.
- Pin tool versions in the Makefile or a clear tools section.
- Keep local tool binaries under `.bin/`.
- Do not vendor third-party source unless explicitly approved.
- Reference submodules are not vendored dependencies.

## Frontend Policy

- Use server-rendered HTML.
- Use modern plain JavaScript only where needed.
- Avoid bundlers and package-manager-driven frontend pipelines unless explicitly approved later.
- Future browser JavaScript should stay small and focused on `EventSource`, markdown rendering, copy buttons, and scroll behavior.
- Do not implement complex client-side state management unless future requirements force it.

## What Not To Implement In Step 1

Do not implement chat routes, templates, OAuth/OIDC, sessions, Postgres migrations, conversation persistence, SSE, LLM proxy calls, markdown rendering, admin configuration, Docker packaging, deployment manifests, or browser tests in this step. The current scope is only the repository foundation and a tiny compile-tested Go placeholder.
