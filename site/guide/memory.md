# Memory Plane

Session history gives you retention. The memory plane gives you recall.

Clawdapus captures every successful LLM turn at the proxy boundary and writes it to a durable ledger — regardless of runner type, outside the runtime tree. That ledger is the raw material. A **memory service** consumes it: deriving facts, commitments, episodic summaries, or embeddings, and surfacing relevant context back into future inference turns automatically.

Unlike [feeds](/guide/surfaces-and-skills), which deliver the same cached content on every turn, memory recall is query-aware — shaped by the current conversation, not a fixed snapshot.

## Architecture

The memory plane follows the same compiled-capability pattern as feeds and tools:

1. A memory service self-describes via `claw.describe`, advertising its `recall`, `retain`, and `forget` endpoints.
2. An agent subscribes in `claw-pod.yml` by naming the service.
3. `claw up` resolves the service's base URL and compiles a `memory.json` manifest into the agent's context directory.
4. The governance proxy reads the manifest and orchestrates request-time recall before inference and post-turn retention after a response.

The memory implementation lives entirely in the memory service. The proxy provides the orchestration contract. Swap the memory backend — embeddings, graph, keyword, summarization — without changing any agent or pod configuration.

## Declaring a Memory Service

A service becomes a memory provider by including a `memory` block in its `claw.describe` descriptor:

```json
{
  "memory": {
    "retain": { "path": "/memory/retain" },
    "recall": { "path": "/memory/recall" },
    "forget": { "path": "/memory/forget" }
  }
}
```

All three endpoints are optional. A service that only ingests history (no recall) declares only `retain`. A service that supports read-only recall without write declares only `recall`.

## Subscribing in Pod YAML

An agent subscribes to a memory service using the `memory` field in its `x-claw` block:

```yaml
services:
  analyst:
    image: analyst:latest
    x-claw:
      agent: ./AGENTS.md
      cllama: passthrough
      memory:
        service: mem-svc
        timeout-ms: 5000   # optional, default 300ms

  mem-svc:
    image: my-memory-service:latest
    expose:
      - "8080"
```

`service` is the compose service name of the memory provider. The service must have an image with a valid `claw.describe` descriptor declaring memory capabilities.

`timeout-ms` controls how long the proxy waits for recall before proceeding without it. Defaults to 300ms. Set higher for slower or network-remote memory backends.

### Pod-Level Defaults

Use `memory-defaults` at the pod level to share memory configuration across services:

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
      # inherits memory-defaults

  researcher:
    x-claw:
      agent: ./agents/researcher/AGENTS.md
      cllama: passthrough
      # inherits memory-defaults
```

## What claw up Compiles

During `claw up`, Clawdapus resolves the memory service descriptor and generates a `memory.json` manifest in each subscribing agent's context directory:

```
.claw-runtime/context/
└── analyst/
    ├── AGENTS.md
    ├── CLAWDAPUS.md
    ├── metadata.json
    ├── feeds.json
    ├── tools.json
    └── memory.json      ← compiled memory manifest
```

The manifest contains:

```json
{
  "version": 1,
  "service": "mem-svc",
  "base_url": "http://mem-svc:8080",
  "recall":  { "path": "/memory/recall",  "timeout_ms": 5000 },
  "retain":  { "path": "/memory/retain",  "timeout_ms": 5000 },
  "forget":  { "path": "/memory/forget",  "timeout_ms": 5000 },
  "auth": { "type": "bearer", "token": "..." }
}
```

The `base_url` is resolved from the service's `expose` port. The `auth` block is populated if the memory service declares auth requirements in its descriptor. Agents never see this file directly — the proxy reads it.

## Backfilling History

When you add a memory service to an existing pod, or swap memory backends, you can replay the retained session history into the new service's `retain` endpoint:

```bash
claw memory backfill <memory-service>
```

This reads each subscribing agent's `history.jsonl` ledger and POSTs the entries to the service's retain endpoint in order.

### Flags

| Flag | Description |
|------|-------------|
| `--after <RFC3339>` | Only replay entries after this timestamp |
| `--limit <n>` | Maximum entries to replay per agent (0 means all) |
| `--url <url>` | Override the retain endpoint URL (e.g., when the service isn't running via compose) |
| `--auth-token <token>` | Override the bearer token for the retain endpoint |

### Examples

Backfill all history:

```bash
claw memory backfill mem-svc
```

Backfill only the last week:

```bash
claw memory backfill mem-svc --after 2026-03-27T00:00:00Z
```

Backfill into a standalone service (not running inside compose):

```bash
claw memory backfill mem-svc --url http://localhost:9090/memory/retain
```

## Telemetry

Memory operations emit structured log events through the cllama proxy. Look for `type: "memory_op"` in audit output:

```json
{
  "ts": "2026-04-03T14:32:01Z",
  "claw_id": "analyst",
  "type": "memory_op",
  "memory_service": "mem-svc",
  "memory_op": "recall",
  "memory_status": "ok",
  "memory_blocks": 4,
  "memory_bytes": 1842
}
```

See the [cllama guide](/guide/cllama#telemetry-and-audit) for the full telemetry field reference.

## What's Planned

The recall side of the ambient memory plane is still in design. The current release compiles memory manifests and supports backfill. The proxy-side orchestration — automatic pre-turn recall and post-turn retention — is the next step. See [ADR-021](https://github.com/mostlydev/clawdapus/blob/master/docs/decisions/021-memory-plane-and-pluggable-recall.md) for the full design.
