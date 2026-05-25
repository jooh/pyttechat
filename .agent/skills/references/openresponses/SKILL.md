---
name: "openresponses"
description: "Use this skill when the task would benefit from inspecting OpenResponses as a read-only Responses API specification reference checked out at .agent/skills/references/openresponses/repo."
---

# OpenResponses

## Purpose

Use this reference for Responses API interoperability: request/response shapes, item lifecycle, semantic streaming events, tool invocation patterns, and compliance expectations.

This repository is read-only reference material. It is not part of the active implementation workspace, and its code should not be copied directly into this project.

## Repo Location

```text
.agent/skills/references/openresponses/repo
```

The `repo/` directory is a git submodule pinned in `manifest.yaml`.

## Boundaries

- Do not edit files inside `repo/`.
- Do not include `repo/` in broad project searches, linting, formatting, tests, or refactors.
- Do not adopt OpenResponses' documentation-site toolchain or generated artifacts as project dependencies.
- Focused searches directly against this repo are allowed when this skill is relevant.

## When To Use This Reference

- Studying Responses API request/response shapes and item lifecycle.
- Studying semantic streaming events and SSE parsing expectations.
- Studying tool call and tool output schema concepts.
- Checking compliance behavior for proxy-facing interoperability.

## When Not To Use It

- Choosing this project's frontend or docs architecture.
- Adding generated clients, docs tooling, Node, Astro, or build pipelines.
- Copying generated schema artifacts wholesale into application code.

## Context Management

Read `manifest.yaml` first, then inspect only the relevant paths or run a focused search recipe. Keep the main context small by preferring paths, symbols, and concise summaries over long code excerpts.

For large or unfamiliar areas, prefer the sub-agent workflow below when supported.

## Preferred Sub-Agent Workflow

Ask a sub-agent to inspect this reference and return a compact report before loading large reference files into the main context.

Reusable delegation prompt:

```text
Inspect the reference repository at:
.agent/skills/references/openresponses/repo

Use this current task description:
<paste the current user task or a concise task summary>

Find relevant implementation patterns in this reference repository. Focus only on areas likely to apply to the current task. Avoid unrelated product areas, generated files, vendored dependencies, build outputs, and paths listed as avoid paths in the reference skill or manifest.

Do not edit files.

Return a compact report with these sections:
- relevant_files: paths and one-line reasons
- implementation_flow: concise description of the relevant flow
- key_abstractions: important types, functions, modules, or concepts
- edge_cases: notable behavior, failure modes, or security concerns
- adaptation_guidance: how this can be adapted to the active repo without copying directly
- warnings: mismatches, dependency concerns, or areas not to follow

Prefer paths and concise summaries over large code excerpts. Quote only tiny snippets when exact names or shapes matter.
```

## High-Level Repo Structure

- `schema/openapi.json`: canonical OpenAPI schema.
- `schema/paths/responses.json`: Responses endpoint schema details.
- `schema/components/schemas`: request, response, stream, item, and tool schemas.
- `src/lib/compliance-tests.ts`: compliance behavior expectations.
- `src/lib/sse-parser.ts`: SSE parsing reference.
- `public/streaming`: streaming diagrams/assets.

## Important Paths

- `schema/openapi.json`: full schema.
- `schema/paths/responses.json`: Responses endpoint contract.
- `schema/components/schemas/CreateResponseBody.json`: request body shape.
- `schema/components/schemas/*Tool*.json`: tool call and output shapes.
- `src/lib/compliance-tests.ts`: compliance behavior expectations.
- `src/lib/sse-parser.ts`: SSE parser behavior.

## Paths To Avoid

- `src/generated`: generated artifacts.
- `bin`: docs and schema generation scripts.
- `public/fonts` and `public/img`: static assets.
- `src/components`, `src/layouts`, and `src/pages`: docs-site UI.

## Focused Search Recipes

Use focused searches rooted at `.agent/skills/references/openresponses/repo`.

```sh
rg "CreateResponseBody|ResponseResource|Item" .agent/skills/references/openresponses/repo/schema
rg "stream|event|sse" .agent/skills/references/openresponses/repo/schema .agent/skills/references/openresponses/repo/src/lib .agent/skills/references/openresponses/repo/public/streaming
rg "ToolCall|ToolCallOutput|status" .agent/skills/references/openresponses/repo/schema/components/schemas
```

## Expected Sub-Agent Report Format

Reports should be compact and use these headings:

- `relevant_files`
- `implementation_flow`
- `key_abstractions`
- `edge_cases`
- `adaptation_guidance`
- `warnings`

## Do Not Copy Directly

Treat this repository as specification guidance, not a source package. Adapt ideas to the active repo's language, architecture, dependencies, security model, and tests. Do not copy generated artifacts, dependency declarations, or documentation-site code wholesale.

## Adaptation Guidance

- Prefer explicit local Go structs and tests for any wire contract this app owns.
- Keep provider-specific behavior delegated to the proxy.
- Use compliance expectations to shape deterministic tests, not to import the docs-site toolchain.
