# Gitea Local Notes

## Why This Reference Was Added

Gitea provides mature Go web app examples for auth/session/CSRF, external auth, account linking, authorization, and integration testing.

## Import And Update History

- 2026-05-25: Imported `https://github.com/go-gitea/gitea.git` at `821d3c4672dc34dead382754c8857d2570f1aa38`.

## Useful Discoveries

- `services/auth`, `services/externalaccount`, `modules/auth`, and `modules/session` are the primary areas for this project.
- `tests/integration` is useful when designing realistic handler tests.

## Local Caveats

- Gitea is much larger than this app; copy its maturity of checks, not its breadth of abstraction.

## Known Irrelevant Areas

- Git plumbing, repository hosting, Actions, package registry, and frontend asset pipelines.

## Maintenance Notes

- Update the submodule only when refreshing reference snapshots, and record the new commit in `manifest.yaml`.
