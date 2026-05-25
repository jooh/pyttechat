# OpenResponses Local Notes

## Why This Reference Was Added

OpenResponses provides the Responses API specification reference for request/response shapes, semantic streaming, tool invocation lifecycle, and compliance expectations.

## Import And Update History

- 2026-05-25: Imported `https://github.com/openresponses/openresponses.git` at `cd31bc2060a27ee87a05ec97f49c84027eb6c3ba`.

## Useful Discoveries

- `schema` is the primary source of truth.
- `src/lib/compliance-tests.ts` and `src/lib/sse-parser.ts` can help clarify behavior without adopting the docs app.

## Local Caveats

- Do not adopt generated clients, docs-site tooling, Astro, Node, or build pipelines.

## Known Irrelevant Areas

- Docs UI, generated code, fonts, images, and generation scripts.

## Maintenance Notes

- Update the submodule only when refreshing reference snapshots, and record the new commit in `manifest.yaml`.
