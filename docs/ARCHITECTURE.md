# Architecture

This project will be a minimal server-rendered Go web app for LLM chat. It acts as a Backend for Frontend, not as the LLM orchestration layer.

```text
Browser
  -> chat web app / BFF
  -> LLM proxy API
  -> LLM APIs and MCP servers
```

## Decisions

- Browser clients do not call the LLM proxy directly.
- Browser clients must not hold OAuth access tokens.
- Browser clients receive only an `HttpOnly`, `Secure`, `SameSite` session cookie.
- The app server stores and refreshes OAuth/OIDC tokens server-side.
- The app server forwards the user's access token to the LLM proxy with `Authorization: Bearer`.
- LLM response streaming from the app to the browser should use Server-Sent Events.
- Postgres is the system of record for users, sessions, conversations, messages, and migrations.
- The frontend should stay server-rendered with minimal plain JavaScript.
- Avoid Node, SPA frameworks, bundlers, and complex frontend state unless future requirements force that tradeoff.
- Later JavaScript should be small and vendored only where useful for `EventSource`, markdown rendering, copy buttons, and scroll behavior.
- LLM output is untrusted content and must be sanitized before rendering.

## Boundaries

The chat web app owns login, secure browser sessions, conversation history, message persistence, and the streaming bridge to the browser. It does not own tool execution, MCP server coordination, model routing, token accounting, or provider-specific LLM behavior.

The LLM proxy API owns tool calls, MCP integration, model routing, token accounting, provider access, and related policy enforcement.

## References

Miniflux is the primary implementation-shape reference for a small, serious Go web app with server-rendered UI, Postgres state, migrations, configuration, packaging, tests, and OAuth/OIDC support.

Gitea is a mature Go web app reference for auth, sessions, CSRF, external auth provider modeling, account linking, per-request authorization, and handler/integration test organization.

Grafana is a targeted enterprise OAuth/OIDC reference for generic provider configuration, claim mapping, role/group mapping, token refresh, and IdP edge cases. It is not a general architecture template.

LibreChat is included as a mature LLM chat product and UX reference. Use it to study conversation behavior, streaming response UX, message rendering, provider/model concepts, and configuration boundaries. This project is intentionally not trying to replicate LibreChat's Node/React SPA implementation stack.
