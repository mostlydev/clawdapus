# Reference Memory Adapter

This is the boring reference implementation for the ADR-021 memory contract.

It is intentionally small:

- `retain` stores one derived memory record per stable session-history `entry.id`
- duplicate `retain` for the same agent and `entry.id` is a no-op
- `forget` writes a tombstone and suppresses later recall
- replaying the same `entry.id` after `forget` remains a no-op
- `recall` returns a few recent or query-matching summaries from the retained set

State is file-backed under `MEMORY_REF_DIR` and defaults to `/data/reference-memory`.

This adapter is used by the capability-wave spike and the rollcall example so the shipped example path exercises the same contract documented in `site/guide/memory.md`.
