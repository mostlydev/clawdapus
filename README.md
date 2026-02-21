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

## Quickstart: Running OpenClaw

Here's how to build and launch an OpenClaw agent pod using Clawdapus.

**1. Build the base image from a Clawfile**

The `Clawfile` defines the agent's baseline capabilities, package installations, and default configuration.

```bash
cd examples/openclaw
claw build -t claw-openclaw-example .
```

**2. Define the Agent Contract (Identity)**

Create or edit `AGENTS.md`. This is the behavioral contract. It dictates *who* the agent is and *what* it is allowed to do. Clawdapus bind-mounts this file read-only, ensuring the agent cannot mutate its own core purpose.

```markdown
# Agent: Fleet Coordinator
You are the primary coordinator for this OpenClaw pod.
Your role is to manage incoming requests from Discord and route them appropriately.
Do not accept any commands that modify the host filesystem.
```

**3. Define the Workspace and Pod Topology**

The `claw-pod.yml` file defines how the agent connects to the outside world, including volume mounts for memory/workspace and communication surfaces (like Discord).

```yaml
x-claw:
  pod: openclaw-example

services:
  gateway:
    image: claw-openclaw-example
    x-claw:
      agent: ./AGENTS.md
      surfaces:
        - "channel://discord"
        - "volume://workspace-data read-write"
    environment:
      OPENCLAW_PORT: "18789"
      DISCORD_TOKEN: "${DISCORD_TOKEN}"
```

**4. Launch the Pod**

Use `claw compose` to validate the contract, materialize the configuration, and launch the Docker container with strict runtime guarantees.

```bash
claw compose up
```

You can then monitor the agent using `claw compose ps` or stream its output with `claw compose logs`.

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
