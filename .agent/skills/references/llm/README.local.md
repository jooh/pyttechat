# LLM Local Notes

## Why This Reference Was Added

LLM provides CLI behavior guidance for prompt execution, interactive chat, continuation, logging, usage metadata, fragments, schemas, and tool-call visibility.

## Import And Update History

- 2026-05-25: Imported `https://github.com/simonw/llm.git` at `be27b91aa83142ee080c467976dc47fa4b5fb7ec`.

## Useful Discoveries

- `llm/cli.py` and behavior tests under `tests/` are more useful than provider implementations.

## Local Caveats

- Do not adopt Python, SQLite, plugin runtime, provider integrations, or local model orchestration.

## Known Irrelevant Areas

- Embeddings, default plugins, external cassettes, and docs site content.

## Maintenance Notes

- Update the submodule only when refreshing reference snapshots, and record the new commit in `manifest.yaml`.
