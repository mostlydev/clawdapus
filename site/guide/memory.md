# Memory Plane

Session history gives you an immutable ledger. The memory plane turns that ledger into durable, query-aware recall.

Clawdapus captures every successful LLM turn at the proxy boundary and writes it to session history regardless of runner type. A memory service consumes those retained entries, derives useful summaries or facts, and returns relevant context on later turns.

Unlike [feeds](/guide/surfaces-and-skills), memory recall is shaped by the current conversation. Unlike tools, memory recall and retain are infrastructure-driven rather than model-invoked.

## Architecture

The memory plane follows the same compiled-capability pattern as feeds and tools:

1. A memory service self-describes via `claw.describe`, advertising `recall`, `retain`, and optional `forget` endpoints.
2. An agent subscribes in `claw-pod.yml` by naming the memory service.
3. `claw up` resolves the service URL and compiles a per-agent `memory.json` manifest into the managed context directory.
4. `cllama` reads that manifest and orchestrates:
   - pre-turn `recall`
   - post-turn best-effort `retain`
   - governed operator `forget`
5. `claw memory backfill` can replay the immutable ledger into the service's `retain` endpoint.

The memory implementation remains entirely external. Swap embeddings, summaries, graph memory, or a boring keyword store without changing agent config.

## Declaring a Memory Service

A service becomes a memory provider by including a `memory` block in its `claw.describe` descriptor:

```json
{
  "version": 2,
  "memory": {
    "retain": { "path": "/retain" },
    "recall": { "path": "/recall" },
    "forget": { "path": "/forget" }
  }
}
```

All three endpoints are optional:

- declare only `retain` if the service is ingest-only
- declare only `recall` if the service is read-only
- declare `forget` when you want governed tombstone handling

When the descriptor lives at the default image path `/.claw-describe.json`, no explicit `claw.describe` label is required. Use the label only when the descriptor lives somewhere else inside the image.

## Subscribing in Pod YAML

An agent subscribes to a memory service with the `memory` field in its `x-claw` block:

```yaml
services:
  analyst:
    image: analyst:latest
    x-claw:
      agent: ./AGENTS.md
      cllama: passthrough
      memory:
        service: mem-svc
        timeout-ms: 5000

  mem-svc:
    image: reference-memory:latest
    build:
      context: ./examples/reference-memory
      dockerfile: Dockerfile
    expose:
      - "8080"
```

`service` is the compose service name of the memory provider. The service must declare memory capability in its descriptor.

`timeout-ms` controls how long `cllama` waits for hot-path recall before proceeding without it. The default is 300ms.

### Pod-Level Defaults

Use `memory-defaults` at pod level to share one memory relationship across multiple agents:

```yaml
x-claw:
  pod: trading-desk
  memory-defaults:
    service: mem-svc
    timeout-ms: 2000

services:
  analyst:
    x-claw:
      agent: ./agents/analyst/AGENTS.md
      cllama: passthrough

  researcher:
    x-claw:
      agent: ./agents/researcher/AGENTS.md
      cllama: passthrough
```

## What claw up Compiles

During `claw up`, Clawdapus generates `memory.json` in each subscribing agent's context directory:

```text
.claw-runtime/context/
└── analyst/
    ├── AGENTS.md
    ├── CLAWDAPUS.md
    ├── metadata.json
    ├── feeds.json
    ├── tools.json
    └── memory.json
```

The manifest contains the resolved base URL, declared endpoint paths, timeouts, and projected auth:

```json
{
  "version": 1,
  "service": "mem-svc",
  "base_url": "http://mem-svc:8080",
  "recall": { "path": "/recall", "timeout_ms": 5000 },
  "retain": { "path": "/retain", "timeout_ms": 5000 },
  "forget": { "path": "/forget", "timeout_ms": 5000 },
  "auth": { "type": "bearer", "token": "..." }
}
```

Agents do not read this manifest directly. `cllama` does.

## Runtime Behavior

### Recall

Before inference, `cllama` POSTs a request shaped by the current turn to the service's `recall` endpoint. The returned `memories[]` block is injected into the prompt stream before the model call.

`cllama` also applies governance policy on the returned blocks:

- blocked transcript-tail kinds and sources are dropped
- blank or over-budget recall blocks are dropped
- secret-shaped values are redacted before injection
- `memory_op` telemetry records how many blocks were removed by policy

### Retain

After a successful completion, `cllama` asynchronously POSTs the normalized session-history entry to the service's `retain` endpoint.

Retain is best-effort:

- the turn is already durable in `history.jsonl`
- retain failures do not fail the user-visible inference response
- secret-shaped values in retained request/response payloads are scrubbed before the memory backend receives them
- retain metadata and telemetry include `policy_removed` / `memory_removed` counts when redactions happened upstream

### Forget

Operators can tombstone retained entries without mutating the immutable session ledger:

```bash
claw memory forget mem-svc --agent analyst-0 --entry-id hist1_abc123 --reason "operator request"
```

This dispatches the service's `forget` endpoint when declared and also records infra-owned tombstones locally so later backfill does not resurrect forgotten entries by accident.

### Backfill

When you add or swap memory services, replay the durable ledger into the service's `retain` endpoint:

```bash
claw memory backfill mem-svc
```

Useful flags:

| Flag | Description |
|------|-------------|
| `--after <RFC3339>` | Replay only entries after this timestamp |
| `--limit <n>` | Maximum entries to replay per agent (0 means all) |
| `--agent <id>` | Restrict replay to one or more agent IDs |
| `--url <url>` | Override the retain endpoint URL |
| `--auth-token <token>` | Override the bearer token used for retain |

Backfill uses the same stable session-history `entry.id` values as live retain, and it honors local forget tombstones. Indexed `--after` reads avoid rescanning the ledger from byte zero on every replay.

### Session History Export

Session history is useful even without a memory backend. Export one agent's retained ledger as NDJSON with:

```bash
claw history export analyst-0 --limit 200
```

`--after <RFC3339>` filters by timestamp and `--limit <n>` caps the emitted entries. The export path streams large JSONL entries with a buffered reader, so it is appropriate for real retained turns, not only small test fixtures.

## Dedupe Contract for Memory Backends

The stable session-history `entry.id` is the idempotency key for the memory plane.

Backends should follow these rules:

1. Treat `retain` as idempotent by `(agent_id, entry.id)`.
2. Expect the same `entry.id` to arrive from both live retain and later replay/backfill.
3. Do not create duplicate memory rows or duplicate recall blocks for repeated `retain` of the same entry.
4. Treat `forget` as a tombstone, not a destructive rewrite of session history.
5. Once an `entry.id` is forgotten, later replay/backfill of that same `entry.id` must remain a no-op.
6. Never return tombstoned entries from `recall`.

This is what makes replay safe and backend swaps boring.

## Reference Memory Adapter

Clawdapus ships a deliberately small reference adapter at [`examples/reference-memory`](https://github.com/mostlydev/clawdapus/tree/master/examples/reference-memory).

It is intentionally boring:

- file-backed under `MEMORY_REF_DIR` (default `/data/reference-memory`)
- one retained record per stable `entry.id`
- duplicate retain is a no-op
- forget writes tombstones
- replay of a tombstoned `entry.id` stays suppressed
- recall returns a few recent or token-matching summaries

The rollcall example and the capability-wave spike both build this adapter, so the shipped example path exercises the same contract described above.

## Channel Memory and Awareness

Channel context is a memory-adjacent surface for chat rooms. When Discord handles are present, `claw up` injects `claw-wall` and serves two built-in feeds:

- `channel-context` -- the cursored mention/turn tail used to answer "what just happened?"
- `channel-awareness` -- an uncursored room-awareness window used to preserve broader local context

Pods can also declare `x-claw.channel-memory` to attach a durable channel-memory service. In that configuration, the channel retrieval tools can combine the raw `claw-wall` buffer with retained channel-memory results, so an agent can search beyond the current raw window while still respecting its channel allowlist.

## Telemetry

Memory operations emit structured `memory_op` log events through `cllama`. Look for:

- `memory_service`
- `memory_op`
- `memory_status`
- `memory_blocks`
- `memory_bytes`
- `memory_removed`

Example:

```json
{
  "ts": "2026-04-03T14:32:01Z",
  "claw_id": "analyst",
  "type": "memory_op",
  "memory_service": "mem-svc",
  "memory_op": "recall",
  "memory_status": "succeeded",
  "memory_blocks": 3,
  "memory_bytes": 1482,
  "memory_removed": 1
}
```

`claw audit` surfaces these normalized memory events alongside other proxy telemetry.
