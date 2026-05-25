---
name: "gitea"
description: "Use this skill when the task would benefit from inspecting Gitea as a read-only reference implementation checked out at .agent/skills/references/gitea/repo."
---

# Gitea

## Purpose

Use this reference for mature Go web app auth, sessions, CSRF, external auth provider modeling, user creation and account linking, per-request authorization, and handler/integration test organization.

This repository is read-only reference material. It is not part of the active implementation workspace, and its code should not be copied directly into this project.

## Repo Location

```text
.agent/skills/references/gitea/repo
```

The `repo/` directory is a git submodule pinned in `manifest.yaml`.

## Boundaries

- Do not edit files inside `repo/`.
- Do not include `repo/` in broad project searches, linting, formatting, tests, or refactors.
- Do not let Gitea's larger architecture or frontend stack override this project's minimal shape.
- Focused searches directly against this repo are allowed when this skill is relevant.

## When To Use This Reference

- Studying secure auth/session/CSRF middleware and per-request authorization.
- Studying external auth provider modeling, identity linking, and user creation.
- Studying server-rendered route organization for a mature Go app.
- Studying integration test organization for web behavior.

## When Not To Use It

- General architecture for this smaller chat app.
- Repository hosting, Git plumbing, package registry, or Actions features.
- Copying source code, templates, dependency declarations, or frontend tooling.

## Context Management

Read `manifest.yaml` first, then inspect only the relevant paths or run a focused search recipe. Keep the main context small by preferring paths, symbols, and concise summaries over long code excerpts.

For large or unfamiliar areas, prefer the sub-agent workflow below when supported.

## Preferred Sub-Agent Workflow

Ask a sub-agent to inspect this reference and return a compact report before loading large reference files into the main context.

Reusable delegation prompt:

```text
Inspect the reference repository at:
.agent/skills/references/gitea/repo

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

- `services/auth`: auth workflows and provider behavior.
- `services/externalaccount`: external identity/account-linking behavior.
- `modules/auth`: request auth helpers.
- `modules/session`: session infrastructure.
- `routers/web`: server-rendered web route organization.
- `tests/integration`: web integration test patterns.

## Important Paths

- `services/auth`: auth service flows.
- `services/externalaccount`: external account linking and lookup.
- `models/auth`: auth source models.
- `modules/auth` and `modules/session`: auth/session helpers.
- `routers/web/auth`: web auth handlers.
- `tests/integration`: integration test helpers and cases.

## Paths To Avoid

- `.github`: upstream CI details.
- `web_src`: frontend build stack not suitable for this project.
- `models/repo`, `modules/git`, and `services/repository`: repository hosting domain logic.
- `public/assets`: generated or bundled UI assets.

## Focused Search Recipes

Use focused searches rooted at `.agent/skills/references/gitea/repo`.

```sh
rg "csrf|session|Auth" .agent/skills/references/gitea/repo/modules/auth .agent/skills/references/gitea/repo/modules/session .agent/skills/references/gitea/repo/routers/web
rg "ExternalLoginUser|OAuth2|OpenID" .agent/skills/references/gitea/repo/services/auth .agent/skills/references/gitea/repo/services/externalaccount .agent/skills/references/gitea/repo/models/auth
rg "MakeRequest|session|csrf" .agent/skills/references/gitea/repo/tests/integration
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

Treat this repository as implementation guidance, not a source package. Adapt ideas to the active repo's language, architecture, dependencies, security model, and tests. Do not copy source code, generated files, dependency declarations, or configuration wholesale.

## Adaptation Guidance

- Scale patterns down before applying them locally.
- Preserve this project's dependency policy and server-rendered frontend constraints.
- Add local tests for security-sensitive behavior adapted from this reference.
