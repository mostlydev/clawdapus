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
claw compose health       # Probe container health
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
- Go toolchain (for installing `claw` from source)

**1. Install `claw` from the current checkout**

```bash
go install ./cmd/claw
```

**2. Build the example OpenClaw image**

```bash
claw build -t claw-openclaw-example examples/openclaw
```

**3. Inspect emitted Claw metadata labels**

```bash
claw inspect claw-openclaw-example
```

**4. Launch the example pod (detached)**

```bash
claw compose -f examples/openclaw/claw-pod.yml up -d
```

**5. Check status, then tear down**

```bash
claw compose -f examples/openclaw/claw-pod.yml ps
claw compose -f examples/openclaw/claw-pod.yml logs gateway
claw compose -f examples/openclaw/claw-pod.yml down
```

Notes:

- `-f` mirrors `docker compose -f` — it locates `compose.generated.yml` next to the pod file.
- `claw compose up` writes `compose.generated.yml` next to the pod file.
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

SKILL ./skills/openclaw-runbook.md

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
7. `claw compose up/down/ps/logs/health` pod lifecycle commands with deterministic policy-layer behavior

---

## AI Agent Skill

A portable skill file for AI coding agents (Claude Code, etc.) is available at [`skills/clawdapus/SKILL.md`](./skills/clawdapus/SKILL.md). It teaches agents how to use the `claw` CLI, understand Clawfile directives, and work with `claw-pod.yml`.

**Install for Claude Code:**

```bash
cp -r skills/clawdapus ~/.claude/skills/
```

The skill triggers automatically when agents encounter Clawfile directives or `claw` commands.

### SKILL Directive + Runtime Skills

The `SKILL` directive (Clawfile) and `x-claw.skills` list (`claw-pod.yml`) let operators mount markdown files into the runner's skill directory as read-only references.

```dockerfile
SKILL ./skills/research-methods.md
SKILL ./skills/incident-playbook.md
```

```yaml
x-claw:
  skills:
    - ./skills/research-methods.md
```

- Image-level `SKILL` files are mounted first.
- Pod-level `skills` entries override image-level files with the same basename.
- Duplicate basenames across either layer are rejected as a hard validation error.
- Mounted files are available at the runtime skill path declared by the driver (`/claw/skills` for OpenClaw).

### Service surface skills (service-emitted, priority, fallback)

Service surfaces can now provide their own skill from the service image itself.

- Service image declares a label:
  `claw.skill.emit=/app/SKILL.md`
- During `claw compose up`, Clawdapus resolves `service://<name>` surfaces, inspects the target service image, and mounts that file as:
  `/claw/skills/surface-<name>.md`
- If no service-emitted skill exists, Clawdapus still creates a fallback `surface-<name>.md` stub with:
  - Hostname (`service://<name>` target)
  - Known ports from the pod's compose definition (if discoverable)
  - A short "what I am and what this claw can do" section plus env-var hints

By default, fallback precedence is:

1. Service-emitted skill (`claw.skill.emit`) for `surface-<name>.md`
2. Operator skill entries (`SKILL` in Clawfile and `x-claw.skills`) when they target the same `surface-<name>.md` basename
3. Generic fallback skill generated by Clawdapus

You can opt into pure operator documentation by providing `skills/surface-<name>.md` yourself; if present, it overrides the service-emitted content for that same basename.

### Known examples

- `examples/openclaw/Clawfile` includes `SKILL ./skills/openclaw-runbook.md`
- `examples/openclaw/claw-pod.yml` now includes a `x-claw.skills` operator override for a service skill: `./skills/surface-fleet-master.md`

---

## Contributing

Start with [`MANIFESTO.md`](./MANIFESTO.md), then read the architecture and spike plan documents.
