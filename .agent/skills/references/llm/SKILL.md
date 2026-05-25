---
name: "llm"
description: "Use this skill when the task would benefit from inspecting LLM as a read-only CLI behavior reference checked out at .agent/skills/references/llm/repo."
---

# LLM

## Purpose

Use this reference for LLM prompt and chat CLI behavior: command ergonomics, interactive chat loops, continuation, prompt/response logging, usage metadata, fragments, schemas, and tool-call visibility.

This repository is read-only reference material. It is not part of the active implementation workspace, and its code should not be copied directly into this project.

## Repo Location

```text
.agent/skills/references/llm/repo
```

The `repo/` directory is a git submodule pinned in `manifest.yaml`.

## Boundaries

- Do not edit files inside `repo/`.
- Do not include `repo/` in broad project searches, linting, formatting, tests, or refactors.
- Do not adopt LLM's Python implementation, SQLite schema, plugin runtime, or provider integrations.
- Focused searches directly against this repo are allowed when this skill is relevant.

## When To Use This Reference

- Designing CLI command ergonomics for prompts, chat, models, and options.
- Studying interactive chat loop and continuation behavior.
- Studying prompt/response logging, usage metadata display, fragments, schemas, or tool-call visibility.
- Designing tests around CLI behavior rather than web-only behavior.

## When Not To Use It

- Implementing provider orchestration, plugins, local models, or embeddings.
- Designing the server-rendered web app.
- Copying Python code, SQLite schemas, dependency declarations, or plugin patterns.

## Context Management

Read `manifest.yaml` first, then inspect only the relevant paths or run a focused search recipe. Keep the main context small by preferring paths, symbols, and concise summaries over long code excerpts.

For large or unfamiliar areas, prefer the sub-agent workflow below when supported.

## Preferred Sub-Agent Workflow

Ask a sub-agent to inspect this reference and return a compact report before loading large reference files into the main context.

Reusable delegation prompt:

```text
Inspect the reference repository at:
.agent/skills/references/llm/repo

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

- `llm/cli.py`: CLI commands and options.
- `llm/models.py`: prompt/chat abstractions and response handling.
- `llm/tools.py`: tool-call concepts.
- `llm/templates.py`: prompt template behavior.
- `tests`: behavior tests for CLI, chat, streaming, logging, and tools.

## Important Paths

- `llm/cli.py`: command ergonomics.
- `llm/models.py`: conversation and response concepts.
- `llm/tools.py`: tool visibility behavior.
- `tests/test_chat.py`: chat behavior expectations.
- `tests/test_cli_options.py`: option behavior expectations.
- `tests/test_llm_logs.py`: logging behavior expectations.

## Paths To Avoid

- `llm/default_plugins`: provider implementation examples.
- `llm/embeddings.py` and `llm/embeddings_migrations.py`: embedding behavior out of scope.
- `tests/cassettes`: recorded external interactions.
- `docs`: documentation site content.

## Focused Search Recipes

Use focused searches rooted at `.agent/skills/references/llm/repo`.

```sh
rg "click.option|model|system" .agent/skills/references/llm/repo/llm/cli.py .agent/skills/references/llm/repo/tests/test_cli_options.py
rg "chat|conversation|continue" .agent/skills/references/llm/repo/llm/cli.py .agent/skills/references/llm/repo/llm/models.py .agent/skills/references/llm/repo/tests/test_chat.py
rg "tool|ToolCall|usage" .agent/skills/references/llm/repo/llm/tools.py .agent/skills/references/llm/repo/llm/models.py .agent/skills/references/llm/repo/tests
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

Treat this repository as CLI behavior guidance, not a source package. Adapt ideas to the active repo's language, architecture, dependencies, security model, and tests. Do not copy source code, schemas, generated files, dependency declarations, or configuration wholesale.

## Adaptation Guidance

- Implement CLI behavior through this project's shared Go backend path.
- Keep provider routing and orchestration delegated to the LLM proxy.
- Add deterministic Go tests for any CLI behavior adapted from this reference.
