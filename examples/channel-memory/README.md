# Channel Memory Adapter

This is the first executable slice of the digest-backed channel-awareness design.
It is a standalone HTTP service; `claw-wall` does not call it yet.

The adapter stores Discord-style channel messages in SQLite and keeps exact
source provenance for later digest-backed `channel-awareness` work.

## Endpoints

- `POST /ingest` stores one source message. Idempotency is keyed by
  `(source_kind, channel_id, message_id, content_hash)`.
- `POST /source-messages` fetches exact retained source messages by channel and
  message id. By default it returns the current non-deleted version; set
  `include_history: true` to inspect older content-hash versions.
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

Higher-quality `topic_rollup` and `sequence_rollup` blocks belong to the async
LLM worker tracked separately.

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

Declare the adapter once at pod level. `claw up` injects the ingest URL and a
bearer token into `claw-wall`, and injects the matching `CHANNEL_MEMORY_TOKEN`
into the adapter service.

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
