# Clawdapus

Infrastructure-layer governance for AI agent containers.

Clawdapus is to agent bots what Docker is to applications — the layer below the framework, above the operating system, where deployment meets governance. It treats AI agents as untrusted workloads: reproducible, inspectable, diffable, and killable.

> **Swarm is for agents that work *for* you. Clawdapus is for bots that work *as* you.**

---

## Status

**Active development — pre-release.**

The `claw` CLI is being built from scratch in Go. See the documents below for where we are and where we're going.

| Document | Purpose |
|----------|---------|
| [`MANIFESTO.md`](./MANIFESTO.md) | Vision and principles — the source of truth |
| [`docs/plans/2026-02-18-clawdapus-architecture.md`](./docs/plans/2026-02-18-clawdapus-architecture.md) | Architecture plan and phased implementation |
| [`docs/decisions/001-cllama-transport.md`](./docs/decisions/001-cllama-transport.md) | ADR: cllama sidecar as bidirectional LLM proxy |
| [`docs/decisions/002-runtime-authority.md`](./docs/decisions/002-runtime-authority.md) | ADR: compose-only lifecycle, SDK read-only |

The prior OpenClaw-based runtime lives in [`archive/openclaw-runtime/`](./archive/openclaw-runtime/) for reference.

---

## What It Will Do

```
claw doctor               # Check Docker, BuildKit, compose versions
claw build [path]         # Clawfile → Dockerfile → docker build
claw inspect <image>      # Show resolved Claw labels from built image
claw up [pod]             # claw-pod.yml → compose.yml → docker compose up
claw down [pod]           # Stop and remove pod containers
claw ps [pod]             # Fleet status with drift scores and policy-layer health
claw logs <claw>          # Stream logs from a running Claw
claw skillmap <claw>      # Show assembled capability inventory
claw audit <claw>         # Policy interventions (cllama when enabled) and drift events
claw recipe <claw>        # Suggested recipe from mutation log
claw bake <claw>          # Apply recipe to rebuild image
claw snapshot <claw>      # Snapshot a running Claw as a new image
```

### The Clawfile

An extended Dockerfile. Any valid Dockerfile is a valid Clawfile. Extended directives add bot-specific governance:

```dockerfile
FROM openclaw:latest

CLAW_TYPE openclaw
AGENT AGENTS.md

MODEL primary anthropic/claude-sonnet-4-6
CLLAMA cllama-org-policy/anthropic/claude-haiku-4-5 purpose/on-mission tone/professional

INVOKE 0 */4 * * *  run-cycle
INVOKE 0 9 * * 1-5  morning-brief

SURFACE volume://shared-cache    read-write
SURFACE service://company-crm   read-write

TRACK apt pip npm
PRIVILEGE runtime claw-user
```

`claw build` compiles this to a standard Dockerfile and calls `docker build`. Output is an ordinary OCI image — runnable on any Docker host.
Directives express intent, not runner-specific mutation commands. At runtime, `CLAW_TYPE` selects a driver that enforces those intents using runner-native mechanisms (for example JSON/JSON5 config writes, env pins, and read-only mounts).

### The claw-pod.yml

An extended docker-compose file. The `x-claw` extension namespace is already ignored by Docker natively. Mixed clusters of Claws and plain containers:

```yaml
x-claw:
  pod: my-ops
  master: fleet-master

services:
  my-claw:
    build:
      context: .
      dockerfile: Clawfile
    x-claw:
      agent: ./AGENTS.md
      count: 3
      surfaces:
        - volume://shared-cache: read-write
        - service://company-crm: read-write

  company-crm:
    image: custom/crm-mcp-bridge:latest
    x-claw:
      expose:
        protocol: mcp
        port: 3100
      require_cllama:
        - policy/pii-gate
```

---

## Core Concepts

**Behavioral Contract** — A read-only bind-mounted file (AGENTS.md, CLAUDE.md, etc.) defining purpose. Lives on the host. Even a root-compromised container cannot rewrite its mission.

**Persona** — Mutable workspace of identity, memory, and interaction history. Versionable and forkable as OCI artifacts.

**Claw Type Driver** — `CLAW_TYPE` selects the runtime driver for a runner family. Drivers translate abstract directive intent into runner-specific enforcement actions.

**cllama** — Optional LLM-powered judgment proxy layer between the Claw and the world. When enabled, the runner never sees cllama's evaluation.

**Surfaces** — Declared communication channels. Give operators topology visibility; give bots capability discovery via assembled skill maps.

**Drift scoring** — Independent audit of outputs against contract and configured policy layers (including cllama where enabled). Triggers capability restriction or quarantine.

See [`MANIFESTO.md`](./MANIFESTO.md) for the full architecture.

---

## Contributing

The project is pre-release. The best place to engage is the [architecture plan PR](https://github.com/mostlydev/clawdapus/pull/2).
