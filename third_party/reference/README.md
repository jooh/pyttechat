# Reference Implementations

These submodules are read-only design references. Do not copy their code into this project and do not modify them as part of normal application work.

## Miniflux

- Primary implementation-shape reference.
- Useful for minimal Go web app structure, server-rendered UI, Postgres state, migrations, configuration, packaging, testing, and OAuth/OIDC support.

## Gitea

- Mature Go web app reference for auth, sessions, CSRF, external auth provider modeling, user/account linking, per-request authorization, and handler/integration test organization.

## Grafana

- Targeted enterprise OAuth/OIDC reference.
- Useful for generic OAuth configuration, claim mapping, role/group mapping, token refresh, and IdP edge cases.
- Do not use as a general architecture template.

## LibreChat

- Product/UX reference for LLM chat behavior.
- Useful for studying conversation UX, streaming response behavior, message rendering, provider/model configuration, and feature boundaries.
- Not an implementation architecture reference for this project.
- Do not introduce Node, React, or SPA machinery merely because LibreChat uses it.

## LLM

- CLI behavior reference for LLM prompt and chat workflows.
- Useful for studying command ergonomics, interactive chat loops, conversation continuation, model/options UX, prompt and response logging, usage metadata, fragments, schemas, and tool-call visibility.
- Not an implementation architecture reference for this Go app.
- Do not copy Python code, adopt its plugin runtime, or add Python dependencies merely because LLM uses them.
