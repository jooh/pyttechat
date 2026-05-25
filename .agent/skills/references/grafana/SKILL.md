---
name: "grafana"
description: "Use this skill when the task would benefit from inspecting Grafana as a read-only reference implementation checked out at .agent/skills/references/grafana/repo."
---

# Grafana

## Purpose

Use this reference narrowly for enterprise OAuth/OIDC edge cases: generic OAuth configuration, claim mapping, role/group mapping, token refresh, and session lifetime patterns.

This repository is read-only reference material. It is not part of the active implementation workspace, and its code should not be copied directly into this project.

## Repo Location

```text
.agent/skills/references/grafana/repo
```

The `repo/` directory is a git submodule pinned in `manifest.yaml`.

## Boundaries

- Do not edit files inside `repo/`.
- Do not include `repo/` in broad project searches, linting, formatting, tests, or refactors.
- Do not use Grafana as this project's general architecture template.
- Focused searches directly against this repo are allowed when this skill is relevant.

## When To Use This Reference

- Studying generic OAuth/OIDC provider configuration and claim mapping.
- Studying group/role/org mapping edge cases.
- Studying token refresh, session lifetime, and IdP compatibility behavior.
- Validating future OAuth/OIDC security requirements against mature enterprise cases.

## When Not To Use It

- General Go app structure, frontend architecture, routing, or packaging.
- Chat UX, CLI behavior, or Responses API interoperability.
- Copying code, generated files, config defaults, or dependency declarations.

## Context Management

Read `manifest.yaml` first, then inspect only the relevant paths or run a focused search recipe. Keep the main context small by preferring paths, symbols, and concise summaries over long code excerpts.

For large or unfamiliar areas, prefer the sub-agent workflow below when supported.

## Preferred Sub-Agent Workflow

Ask a sub-agent to inspect this reference and return a compact report before loading large reference files into the main context.

Reusable delegation prompt:

```text
Inspect the reference repository at:
.agent/skills/references/grafana/repo

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

- `pkg/login/social`: social login service and connector flow.
- `pkg/login/social/connectors`: generic and provider-specific OAuth connectors.
- `pkg/services/accesscontrol`: role and authorization concepts.
- `conf/defaults.ini` and `conf/sample.ini`: auth configuration surface examples.

## Important Paths

- `pkg/login/social/connectors/generic_oauth.go`: generic OAuth behavior.
- `pkg/login/social/connectors/generic_oauth_test.go`: edge-case tests.
- `pkg/login/social/connectors/org_role_mapper.go`: role/org mapping.
- `pkg/login/social/socialimpl`: social login service implementation.
- `conf/defaults.ini`: auth config keys and defaults.

## Paths To Avoid

- `public`, `packages`, and `.yarn`: frontend and Node workspace.
- `apps`: Grafana app/plugin model.
- `devenv`: development fixtures.
- `pkg/plugins`: plugin system unrelated to this project.

## Focused Search Recipes

Use focused searches rooted at `.agent/skills/references/grafana/repo`.

```sh
rg "generic_oauth|role_attribute_path|groups_attribute_path" .agent/skills/references/grafana/repo/pkg/login/social/connectors .agent/skills/references/grafana/repo/conf
rg "refresh_token|token_refresh|expiry" .agent/skills/references/grafana/repo/pkg/login/social .agent/skills/references/grafana/repo/pkg/services
rg "roleAttribute|OrgRole|groups" .agent/skills/references/grafana/repo/pkg/login/social/connectors
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

- Use Grafana only after the simpler Miniflux/Gitea references are insufficient.
- Translate enterprise behavior into the smallest local contract that meets this project's needs.
- Keep OAuth tokens server-side and add local security-sensitive tests for adapted behavior.
