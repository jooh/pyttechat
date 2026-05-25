---
name: "librechat"
description: "Use this skill when the task would benefit from inspecting LibreChat as a read-only reference implementation checked out at .agent/skills/references/librechat/repo."
---

# LibreChat

## Purpose

Use this reference for mature LLM chat product behavior: conversation list behavior, message lifecycle, streaming response UX, multi-model/provider concepts, file/image/message rendering patterns, and configuration boundaries worth simplifying.

This repository is read-only reference material. It is not part of the active implementation workspace, and its code should not be copied directly into this project.

## Repo Location

```text
.agent/skills/references/librechat/repo
```

The `repo/` directory is a git submodule pinned in `manifest.yaml`.

## Boundaries

- Do not edit files inside `repo/`.
- Do not include `repo/` in broad project searches, linting, formatting, tests, or refactors.
- Do not introduce Node, React, SPA state management, or bundlers because LibreChat uses them.
- Focused searches directly against this repo are allowed when this skill is relevant.

## When To Use This Reference

- Studying chat UX, conversation lists, message lifecycle, and streaming response behavior.
- Studying model/provider configuration concepts as product behavior.
- Studying file/image/message rendering patterns and what to simplify or avoid.
- Understanding admin/configuration concepts that should remain outside the current scope.

## When Not To Use It

- Selecting this project's frontend architecture.
- Implementing provider routing, tool execution, token accounting, or local LLM orchestration.
- Copying TypeScript/JavaScript source code, schemas, or dependency declarations.

## Context Management

Read `manifest.yaml` first, then inspect only the relevant paths or run a focused search recipe. Keep the main context small by preferring paths, symbols, and concise summaries over long code excerpts.

For large or unfamiliar areas, prefer the sub-agent workflow below when supported.

## Preferred Sub-Agent Workflow

Ask a sub-agent to inspect this reference and return a compact report before loading large reference files into the main context.

Reusable delegation prompt:

```text
Inspect the reference repository at:
.agent/skills/references/librechat/repo

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

- `client/src`: chat UX and interaction behavior reference only.
- `api/server/routes`: conversation, message, model, and settings routes.
- `api/app/clients`: client and stream behavior concepts.
- `packages/data-schemas`: conversation and message schema concepts.
- `packages/data-provider`: provider/model concept names.
- `config`: application configuration examples.

## Important Paths

- `api/server/routes/convos.js`: conversation route behavior.
- `api/server/routes/messages.js`: message route behavior.
- `api/app/clients/TextStream.js`: streaming behavior concepts.
- `client/src`: UX patterns, inspected only with focused searches.
- `packages/data-schemas`: data-shape concepts.

## Paths To Avoid

- `node_modules`: dependency tree, if present.
- `packages/client`: frontend package implementation details.
- `helm`: deployment charts.
- `e2e`: product E2E details unless explicitly studying UX flows.
- `.do`, `.husky`, `.vscode`: project tooling.

## Focused Search Recipes

Use focused searches rooted at `.agent/skills/references/librechat/repo`.

```sh
rg "stream|abort|TextStream" .agent/skills/references/librechat/repo/api/app/clients .agent/skills/references/librechat/repo/api/server .agent/skills/references/librechat/repo/client/src
rg "conversation|message|parentMessageId" .agent/skills/references/librechat/repo/api/server/routes .agent/skills/references/librechat/repo/packages/data-schemas .agent/skills/references/librechat/repo/client/src
rg "model|endpoint|provider" .agent/skills/references/librechat/repo/config .agent/skills/references/librechat/repo/api/server/routes .agent/skills/references/librechat/repo/packages/data-provider
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

Treat this repository as product and behavior guidance, not a source package. Adapt ideas to the active repo's language, architecture, dependencies, security model, and tests. Do not copy source code, generated files, dependency declarations, or configuration wholesale.

## Adaptation Guidance

- Translate mature chat behavior into server-rendered Go UI and shared backend services.
- Keep provider routing, MCP/tool execution, and token accounting delegated to the proxy.
- Preserve this project's no-Node/no-SPA constraint unless future requirements explicitly change it.
