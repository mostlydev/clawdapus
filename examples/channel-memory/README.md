# Channel Memory Adapter

This is the channel-memory adapter for digest-backed channel awareness.
When a pod declares `x-claw.channel-memory.service`, `claw up` wires `claw-wall`
to ingest retained channel messages and request digest blocks from this service.

The adapter stores Discord-style channel messages in SQLite and keeps exact
source provenance for digest-backed `channel-awareness` work.

## Endpoints

- `POST /ingest` stores one source message. Idempotency is keyed by
  `(source_kind, channel_id, message_id, content_hash)`.
- `POST /source-messages` fetches exact retained source messages by channel and
  message id. By default it returns the current non-deleted version; set
  `include_history: true` to inspect older content-hash versions.
- `POST /search` searches current retained source messages and sparse derived
  blocks within explicit channel ids, returning source handles for exact
  follow-up retrieval.
- `POST /digest` returns deterministic digest blocks over already-retained
  messages. It does not call an LLM.
- `POST /coverage-gaps` records an explicit missing source range.
- `POST /forget` suppresses source messages and marks derived blocks dirty.
- `GET /health` reports process liveness.

## Deterministic Processor

The deterministic path is intentionally conservative:

- preserves obvious hard events as faithful `hard_event` blocks
- keeps ordinary retained content as `raw_excerpt` blocks
- collapses runtime/status noise into sparse `telemetry_count` blocks
- emits coverage-gap metadata from stored gap records
- creates tombstone blocks for deleted messages without carrying deleted content

Higher-quality `topic_rollup` and `sequence_rollup` blocks come from the async
LLM worker below.

## Async LLM Digest Worker

An optional background worker compresses verbose `raw_excerpt` material into
sparse `topic_rollup` / `sequence_rollup` blocks using an LLM. It is strictly
off the `/digest` hot path — `/digest` only ever reads already-generated blocks
and never calls a model.

The worker is conservative by design:

- only `raw_excerpt` windows are summarized; hard events, tombstones, and
  telemetry blocks keep their faithful deterministic form
- every generated block requires structured JSON output citing the exact source
  message ids it summarized; malformed or provenance-free results are rejected
  and the deterministic blocks keep serving
- work is cached by source-message ids plus content hashes, so an unchanged
  window is never re-summarized
- each block stores its provider, model, version, and cost in `metadata_json`
- conservative per-channel and per-pod daily call caps are enforced, with usage
  tracked in `llm_usage`; over budget, disabled, or failing all fall back to
  deterministic-only output
- editing, deleting, or forgetting a covered source dirties the rollup via
  shared provenance, so stale summaries stop serving until regenerated

It is disabled unless `CHANNEL_MEMORY_LLM_ENABLED=true` and
`CHANNEL_MEMORY_LLM_BASE_URL` (an OpenAI-compatible chat-completions endpoint)
are set. Tuning knobs: `CHANNEL_MEMORY_LLM_MODEL`, `CHANNEL_MEMORY_LLM_PROVIDER`,
`CHANNEL_MEMORY_LLM_VERSION`, `CHANNEL_MEMORY_LLM_API_KEY`,
`CHANNEL_MEMORY_LLM_WINDOW`, `CHANNEL_MEMORY_LLM_MIN_WINDOW`,
`CHANNEL_MEMORY_LLM_PER_CHANNEL_DAILY`, `CHANNEL_MEMORY_LLM_PER_POD_DAILY`,
`CHANNEL_MEMORY_LLM_PER_CHANNEL_DAILY_USD`,
`CHANNEL_MEMORY_LLM_PER_POD_DAILY_USD`,
`CHANNEL_MEMORY_LLM_COST_PER_CALL_USD`, and
`CHANNEL_MEMORY_LLM_INTERVAL_SECONDS`. Set
`CHANNEL_MEMORY_LLM_TIMEOUT_SECONDS` to tune the upstream LLM HTTP timeout
(default 90 seconds).

## Storage

State is stored under `CHANNEL_MEMORY_DIR` and defaults to
`/data/channel-memory`. Set `CHANNEL_MEMORY_DB` to choose an exact SQLite file.
Set `CHANNEL_MEMORY_TOKEN` to require bearer authentication on all data
endpoints. `/health` remains unauthenticated.

The schema includes:

- `source_messages`
- `derived_blocks`
- `derived_block_sources`
- `coverage_gaps`
- `processing_queue`
- `llm_usage`

`source_messages` uses explicit `observed_seq`, `observed_at`, and `is_current`
fields so edited messages create new rows while exact retrieval can still select
the current version deterministically.

## Build

This Dockerfile expects the repository root as build context because the service
uses the repository Go module:

```sh
docker build -f examples/channel-memory/Dockerfile -t channel-memory:latest .
```

## Pod Wiring

Declare the adapter once at pod level. `claw up` injects ingest and digest URLs
plus a bearer token into `claw-wall`; claw-wall derives search and exact-source
retrieval URLs from the same service. It also injects the matching
`CHANNEL_MEMORY_TOKEN` into the adapter service, and changes generated
`channel-awareness` feeds to request `context_kind=raw_window+digest`.

```yaml
x-claw:
  channel-memory:
    service: channel-memory

services:
  channel-memory:
    build:
      context: .
      dockerfile: examples/channel-memory/Dockerfile
    expose:
      - "8080"
```
