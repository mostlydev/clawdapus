# Clawdapus

Infrastructure-layer governance for AI agent containers.

Clawdapus is to agent bots what Docker is to applications: the layer below the framework, above the operating system, where deployment meets governance. It treats AI agents as untrusted workloads that should be reproducible, inspectable, diffable, and killable.

> **Swarm is for agents that work *for* you. Clawdapus is for bots that work *as* you.**

---

## Status

**Active development — pre-release.**

Vertical Spike 1 (Clawfile parse/build) is now implemented in this repository.

Implemented commands:

```bash
claw doctor               # Check Docker CLI, buildx, compose
claw build [path]         # Clawfile -> Dockerfile.generated -> docker build
claw inspect <image>      # Show parsed claw.* labels from image metadata
```

Recent verification:

```bash
go test ./...
go test -tags=integration ./...
go build -o bin/claw ./cmd/claw
./bin/claw build -t claw-openclaw-example examples/openclaw
./bin/claw inspect claw-openclaw-example
```

The OpenClaw reference example is in `examples/openclaw/`.

---

## Current Design Inputs

| Document | Purpose |
|----------|---------|
| [`MANIFESTO.md`](./MANIFESTO.md) | Vision and principles |
| [`docs/plans/2026-02-18-clawdapus-architecture.md`](./docs/plans/2026-02-18-clawdapus-architecture.md) | Architecture and phased implementation |
| [`docs/plans/2026-02-20-vertical-spike-clawfile-build.md`](./docs/plans/2026-02-20-vertical-spike-clawfile-build.md) | Spike 1 completion summary + Phase 2 plan |
| [`docs/decisions/001-cllama-transport.md`](./docs/decisions/001-cllama-transport.md) | ADR: cllama sidecar transport |
| [`docs/decisions/002-runtime-authority.md`](./docs/decisions/002-runtime-authority.md) | ADR: compose lifecycle, SDK read-only |

---

## Clawfile Model

A Clawfile is an extended Dockerfile. Any valid Dockerfile is still valid.

```dockerfile
FROM node:24-bookworm-slim

CLAW_TYPE openclaw
AGENT AGENTS.md
MODEL primary openrouter/anthropic/claude-sonnet-4

CONFIGURE openclaw config set agents.defaults.heartbeat.every 30m
INVOKE 0,30 * * * * heartbeat

SURFACE channel://discord
SURFACE service://fleet-master

PRIVILEGE runtime claw-user
RUN npm install -g openclaw@2026.2.9
```

`claw build` transpiles directives into standard Dockerfile primitives (`LABEL`, generated helper scripts, and cron setup), then runs `docker build`.

---

## Phase 2 Focus

1. Runtime driver framework (`CLAW_TYPE` -> enforcement strategy)
2. OpenClaw driver with Go-native JSON5 config mutation (no repeated `openclaw config set` shellouts)
3. Contract existence + read-only mount enforcement for `AGENT`
4. `claw-pod.yml` parsing and `compose.generated.yml` emission
5. `claw up/down/ps/logs` lifecycle commands with deterministic policy-layer behavior

---

## Contributing

Start with [`MANIFESTO.md`](./MANIFESTO.md), then read the architecture and spike plan documents.
