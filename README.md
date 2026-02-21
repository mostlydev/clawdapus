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
claw compose up [pod]     # Launch pod from claw-pod.yml
claw compose down         # Stop and remove pod
claw compose ps           # Show pod status
claw compose logs [svc]   # Stream pod logs
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

## Quickstart: Running OpenClaw (verified)

The commands below were run successfully in this repo on 2026-02-21.

Prerequisites:

- Docker daemon running (`docker`, `docker buildx`, `docker compose`)
- Go toolchain (for building `claw` from source)

**1. Build the CLI binary**

```bash
go build -o bin/claw ./cmd/claw
```

**2. Build the example OpenClaw image**

```bash
cd examples/openclaw
../../bin/claw build -t claw-openclaw-example .
```

**3. Inspect emitted Claw metadata labels**

```bash
../../bin/claw inspect claw-openclaw-example
```

**4. Launch the example pod (detached)**

```bash
../../bin/claw compose up -d claw-pod.yml
```

**5. Check status, then tear down**

```bash
../../bin/claw compose ps
../../bin/claw compose logs gateway
../../bin/claw compose down
```

Notes:

- Run `compose up/ps/logs/down` from the same directory as `compose.generated.yml` (for this example: `examples/openclaw/`).
- `AGENTS.md` already exists in `examples/openclaw/`; edit it to change agent behavior contract.

---

## OpenClaw Image for Testing

Use `alpine/openclaw` and pin a concrete version tag for deterministic tests. Avoid `:latest` in CI.

```yaml
version: "3.8"
services:
  openclaw:
    image: alpine/openclaw:2026.2.19
    ports:
      - "3000:3000"
    volumes:
      - ./config:/app/config
      - ./data:/app/data
      - ./skills:/app/skills
    env_file:
      - .env
    restart: unless-stopped
```

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
3. Contract existence + read-only mount enforcement for `AGENT` (fail-closed preflight)
4. `claw-pod.yml` parsing with `count` scaling and stable ordinal identities
5. Compose generation for volume surfaces and network restriction enforcement
6. Fail-closed post-apply verification before reporting successful `claw up`
7. `claw compose up/down/ps/logs` pod lifecycle commands with deterministic policy-layer behavior

---

## Contributing

Start with [`MANIFESTO.md`](./MANIFESTO.md), then read the architecture and spike plan documents.
