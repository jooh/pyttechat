# Miniflux Local Notes

## Why This Reference Was Added

Miniflux is the primary implementation-shape reference for this project's minimal server-rendered Go web app.

## Import And Update History

- 2026-05-25: Imported `https://github.com/miniflux/v2.git` at `483da488d80292791a53d393f6a94ad17489e9f0`.

## Useful Discoveries

- `internal/ui` is the highest-value area for handler, route, session, CSRF, and template patterns.
- `internal/config` and `internal/database` are useful for small, auditable infrastructure shape.

## Local Caveats

- Miniflux is an RSS reader; domain logic should not shape this chat app.
- Use Grafana rather than Miniflux for enterprise OAuth/OIDC edge cases.

## Known Irrelevant Areas

- Feed reading, external feed integrations, and generated/static binary assets.

## Maintenance Notes

- Update the submodule only when refreshing reference snapshots, and record the new commit in `manifest.yaml`.
