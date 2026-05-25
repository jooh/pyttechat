---
name: reference-implementation
description: Use this archetype when creating a concrete local skill for a reference implementation repository checked out as a git submodule under .agent/skills/references/<reference-name>/repo.
---

# Reference Implementation Archetype

Use this archetype to bootstrap a concrete skill for a large external or internal reference repository. The concrete skill should help future agents inspect the reference repo deliberately without treating it as part of the active implementation workspace.

Reference repositories are read-only reference material. They are not source for direct copying, not part of the current product implementation, and not targets for broad refactors, formatting, linting, or tests.

## When To Use

Use this archetype when adding a new reference implementation skill under `.agent/skills/references/<reference-name>/`.

Do not use it to add general project documentation, active implementation code, custom search tools, MCP servers, or one-off notes for references that are not checked out as submodules.

## Target Structure

Concrete reference skills created from this archetype should use:

```text
.agent/
  skills/
    references/
      <reference-name>/
        SKILL.md
        manifest.yaml
        README.local.md
        repo/
```

`repo/` must be a git submodule. Do not place implementation files in `repo/`, and do not edit files inside it unless a user explicitly asks to update the submodule itself.

## Creation Workflow

1. Choose a short lowercase `<reference-name>` using letters, numbers, and hyphens.
2. Create `.agent/skills/references/<reference-name>/`.
3. Add the reference repository as a submodule at `.agent/skills/references/<reference-name>/repo`.
4. Inspect the repository shallowly before writing the concrete skill.
5. Fill out `manifest.yaml` from `templates/manifest.yaml.template`.
6. Generate `SKILL.md` from `templates/SKILL.md.template`.
7. Optionally create `README.local.md` from `templates/README.local.md.template`.
8. Update `.agent/skills/references/registry.yaml`.
9. Update root `AGENTS.md` if the project does not already document the reference skill convention.
10. Add ignore rules for broad search tools if needed.

## Required Metadata

Every concrete reference skill needs enough metadata for an agent to decide whether to load and inspect it:

- Stable `name` and human-readable `display_name`.
- The reference `role`, stated as a concise sentence.
- Local `repo_path`, usually `.agent/skills/references/<reference-name>/repo`.
- `submodule_url`, pinned commit, import date, and license.
- Clear `use_for` and `do_not_use_for` lists.
- Important paths, avoid paths, and focused search recipes.
- Sub-agent policy and expected report sections.
- Notes for caveats, local conventions, or update history.

## Adding The Submodule

Add future reference repos with a command like:

```sh
git submodule add <repo-url> .agent/skills/references/<reference-name>/repo
git -C .agent/skills/references/<reference-name>/repo rev-parse HEAD
```

Record the exact commit in `manifest.yaml` as `pinned_commit`. If the project prefers shallow submodules, use the same submodule style already present in the repo.

Do not add actual reference submodules when creating this archetype.

## Shallow Repo Inspection

Before writing a concrete skill, inspect only enough to make the skill useful:

```sh
find .agent/skills/references/<reference-name>/repo -maxdepth 2 -type f | head -100
find .agent/skills/references/<reference-name>/repo -maxdepth 2 -type d | head -100
git -C .agent/skills/references/<reference-name>/repo rev-parse HEAD
git -C .agent/skills/references/<reference-name>/repo status --short
```

Then run focused searches for the reference role. Avoid full-repo dumps, broad recursive reads, generated directories, vendored dependencies, build outputs, and unrelated app areas.

## Filling Out `manifest.yaml`

Start from `templates/manifest.yaml.template`.

Use concise YAML lists for `use_for`, `do_not_use_for`, `important_paths`, and `avoid_paths`. Search recipes should be named by task or concept, not by tool. Each recipe should include a short description, search terms, likely paths, and paths to avoid.

Set `subagent_policy.prefer_subagent` and `subagent_policy.summary_only` to `true` unless the repository is very small or sub-agent support is unavailable.

## Generating `SKILL.md`

Start from `templates/SKILL.md.template`.

Keep the concrete skill compact and operational. It should tell future agents when the reference is relevant, where to inspect, what to avoid, and how to ask a sub-agent for a compact report. Prefer paths and search recipes over large explanations.

The concrete skill must explicitly say:

- The reference repo is read-only reference material.
- The reference repo is not part of the implementation workspace.
- Broad searches must not include reference repos.
- Focused searches directly against this reference repo are allowed when this skill is relevant.
- Do not copy source code directly from the reference repo into the product.

## Updating The Registry

Add a registry entry in `.agent/skills/references/registry.yaml`:

```yaml
references:
  <reference-name>:
    skill_path: .agent/skills/references/<reference-name>/SKILL.md
    repo_path: .agent/skills/references/<reference-name>/repo
    role: One sentence describing the reference role.
    use_for:
      - focused topic
```

Keep the registry lightweight. Detailed guidance belongs in the concrete skill and manifest.

## Updating Root `AGENTS.md`

If root `AGENTS.md` does not already document the convention, add a short section stating:

- Reference repositories live under `.agent/skills/references/*/repo`.
- They are read-only reference material.
- They are not part of the active implementation workspace.
- Do not include them in broad searches or refactors.
- Use the relevant skill before inspecting a reference repo.
- Prefer sub-agent exploration where supported.

## Ignore Rules

Reference repo submodules should not be included in broad searches.

If `.rgignore` exists, add:

```text
.agent/skills/references/*/repo/
```

If `.fdignore` exists, add the same entry. If neither exists, create `.rgignore` with a short comment and the entry above. Do not over-edit unrelated ignore files.

## Context Management

Keep the main context small. Do not load large files from a reference repo into the main conversation unless the current task requires exact details.

Prefer:

- Reading the concrete skill and manifest first.
- Running focused searches against the specific reference repo path.
- Asking a sub-agent to inspect the reference and return a compact report.
- Using paths, symbols, and short summaries instead of long excerpts.

Avoid:

- Broad `rg` or `find` searches that include every reference repo.
- Copying large code blocks from a reference repo into the main context.
- Letting reference implementation structure override the active repo's architecture.

## Sub-Agent Delegation Policy

When sub-agents are available, delegate reference exploration by default. The main agent should give the sub-agent the current task, the concrete reference repo path, relevant search recipes, and a strict request for a compact report.

The sub-agent must not edit files. It should return relevant files, implementation flow, key abstractions, edge cases, adaptation guidance, and warnings. The main agent remains responsible for deciding how, or whether, the reference pattern should be adapted to the active project.
