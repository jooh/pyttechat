# Grafana Local Notes

## Why This Reference Was Added

Grafana provides a targeted enterprise OAuth/OIDC reference for provider configuration, claims, role/group mapping, token refresh, and session lifetime behavior.

## Import And Update History

- 2026-05-25: Imported `https://github.com/grafana/grafana.git` at `8523124bd27317228b211568987555efda392b62`.

## Useful Discoveries

- `pkg/login/social/connectors` is the main area for generic OAuth and claim mapping.
- `conf/defaults.ini` and `conf/sample.ini` help identify configuration surfaces.

## Local Caveats

- Grafana is intentionally not a general architecture template for this project.
- Grafana's AGPL-3.0 license reinforces the no-copying rule.

## Known Irrelevant Areas

- Frontend packages, plugin systems, app plugins, generated assets, and development fixtures.

## Maintenance Notes

- Update the submodule only when refreshing reference snapshots, and record the new commit in `manifest.yaml`.
