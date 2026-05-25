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
- Later JavaScript should be small and vendored only where useful for `EventSource`, copy buttons, and scroll behavior.
- LLM output is untrusted content and must be sanitized before rendering.
- Markdown parsing and sanitization are server-owned. The browser receives sanitized HTML, never raw assistant markdown deltas.

## Boundaries

The chat web app owns login, secure browser sessions, conversation history, message persistence, and the streaming bridge to the browser. It does not own tool execution, MCP server coordination, model routing, token accounting, or provider-specific LLM behavior.

The LLM proxy API owns tool calls, MCP integration, model routing, token accounting, provider access, and related policy enforcement.

## Markdown Streaming

Assistant text from the LLM stream is accumulated on the server. Each non-empty text delta triggers a full server-side render of the current accumulated Markdown with goldmark, GitHub Flavored Markdown extensions, and Chroma class-based syntax highlighting. The rendered preview is sanitized with a strict bluemonday policy before it is sent to the browser as an SSE `preview` event.

The browser replaces the live assistant message body with each sanitized preview. It does not parse Markdown and it does not receive raw assistant text events. Reasoning events remain plain text and are inserted with `textContent`.

When the LLM sends the terminal completion event, the server renders the full assistant message from the complete buffered Markdown, sanitizes it, and includes that final HTML in the `done` event with the response completion timestamp. The browser replaces the live assistant body with `done.html`; this final full-message render is the source of truth for completed assistant output.

## CLI-First Backend Plan

The CLI is the first implementation target because it can exercise backend behavior without browser sessions, OAuth/OIDC, templates, CSRF, SSE, or markdown rendering. It should remain a thin Go command surface over the same `chat` and `llm` service interfaces that future web handlers will use.

Near-term CLI work should focus on backend-facing behavior:

- Prompt execution through the configured LLM proxy client, with deterministic dummy clients kept for tests.
- An interactive chat loop that reuses the same service path as one-shot prompts.
- Model and option flags that are forwarded to the proxy contract, without implementing provider routing locally.
- Conversation continuation and logs/history once durable application state exists.
- Clear streaming and non-streaming boundaries, with browser delivery still planned as SSE later.
- Usage and response metadata display when the proxy exposes it.

## References

Reference repositories are checked out as skill-backed read-only submodules under `.agent/skills/references/*/repo`. Use the relevant reference skill before inspecting one, and do not include reference repos in broad searches, linting, formatting, tests, or refactors.

Miniflux is the primary implementation-shape reference for a small, serious Go web app with server-rendered UI, Postgres state, migrations, configuration, packaging, tests, and OAuth/OIDC support.

Gitea is a mature Go web app reference for auth, sessions, CSRF, external auth provider modeling, account linking, per-request authorization, and handler/integration test organization.

Grafana is a targeted enterprise OAuth/OIDC reference for generic provider configuration, claim mapping, role/group mapping, token refresh, and IdP edge cases. It is not a general architecture template.

LibreChat is included as a mature LLM chat product and UX reference. Use it to study conversation behavior, streaming response UX, message rendering, provider/model concepts, and configuration boundaries. This project is intentionally not trying to replicate LibreChat's Node/React SPA implementation stack.

LLM is included as a mature LLM CLI reference. Use it to study one-shot prompts, interactive chat behavior, continuation semantics, logging, model/options command design, and backend-testable LLM workflows. This project is intentionally not trying to replicate LLM's Python package, plugin ecosystem, or provider orchestration.

OpenResponses is included as the Responses API specification reference for LLM-side interoperability. Use it to study request/response shapes, item lifecycle, semantic streaming events, tool invocation patterns, and compliance expectations. This project should not adopt OpenResponses' documentation-site toolchain or generated artifacts as application dependencies.
