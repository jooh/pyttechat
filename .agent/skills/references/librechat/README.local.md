# LibreChat Local Notes

## Why This Reference Was Added

LibreChat provides mature LLM chat product and UX behavior for conversation, message, streaming, model/provider, and rendering concepts.

## Import And Update History

- 2026-05-25: Imported `https://github.com/danny-avila/LibreChat.git` at `a00800161c8a0057644725ff60bd34403bb0a9b3`.

## Useful Discoveries

- `api/server/routes`, `api/app/clients`, and `packages/data-schemas` are usually more useful than broad frontend inspection.
- `client/src` should be searched only for targeted UX behavior.

## Local Caveats

- LibreChat's Node/React SPA architecture is intentionally not a pattern for this project.

## Known Irrelevant Areas

- Frontend build systems, deployment charts, project tooling, and provider orchestration implementation details.

## Maintenance Notes

- Update the submodule only when refreshing reference snapshots, and record the new commit in `manifest.yaml`.
