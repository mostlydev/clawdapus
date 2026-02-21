# Clawdapus

Infrastructure layer for AI agent runtimes.

Clawdapus gives operator teams the same deployment primitives they already rely on for applications: reproducible builds, policy-governed mounts, composable topology, and explicit runtime contracts for inter-agent behavior.

- **Agents are workloads, not trusted infrastructure**
- **Network and identity are explicit**
- **Context is versioned and discoverable at runtime**

---

## What Clawdapus enables

Clawdapus turns `Clawfile` + `claw-pod.yml` into a deployable, governable agent fleet:

- Build immutable agent images from familiar Dockerfile syntax plus Clawdapus directives
- Run multiple claws and services together with explicit compose-time rules
- Project discoverability with `SURFACE` and runtime identity with `HANDLE`
- Feed each claw operational context through mounted `SKILL` files
- Mount service-emitted documentation and generated fallbacks so agents can find and use peers

This is aimed at the same use case you described: public communication bots + internal services that can discover each other, evolve safely, and be independently operated.

---

## Why this is useful for distributed AI teams

A fleet should be able to:

- discover peers without manual hardcoding,
- expose integration metadata in a controlled way,
- evolve independently, and
- be observable and auditable through generated manifests.

Clawdapus supports this by defining **runtime intent** in code, then materializing it into concrete artifacts before startup.

---

## Core capabilities (today)

1. `Clawfile` directives: `CLAW_TYPE`, `MODEL`, `PRIVILEGE`, `CONFIGURE`, `INVOKE`, `SURFACE`, `SKILL`, `HANDLE`, `AGENT`
2. Deterministic build output (`claw build`) with Docker label materialization
3. Pod lifecycle primitives (`claw compose up/down/ps/logs/health`)
4. `claw inspect` for generated labels and metadata
5. Skill distribution model:
   - image-level `SKILL`
   - pod-level `x-claw.skills`
   - service-emitted skill extraction from service images via `claw.skill.emit`
   - generated `surface-<target>.md` fallback when explicit skill is missing
6. Surface reference generation for service DNS + discovered ports
7. `CLAW_HANDLE_*` context values generated into the runtime contract

---

## Architecture snapshot

Clawdapus has three explicit layers:

1. **Clawfile** — what each agent image should include and how it behaves
2. **claw-pod.yml** — how those images run together in a pod
3. **Runtime driver** — how to materialize metadata, mounts, and policy at compose-time

Execution flow:

- Parse and validate declarations
- Generate compose + runtime artifacts
- Attach contract files and skill files into `/claw/skills`
- Run the pod deterministically

This keeps image build, topology, and operational context explicit and reviewable.

---

## Quickstart (working reference)

### Prerequisites

- Docker (`docker`, `docker buildx`, `docker compose`)
- Go toolchain (to install `claw` from source)

### Commands used in this repo

```bash

go install ./cmd/claw
claw build -t claw-openclaw-example examples/openclaw
claw inspect claw-openclaw-example
claw compose -f examples/openclaw/claw-pod.yml up -d
claw compose -f examples/openclaw/claw-pod.yml ps
claw compose -f examples/openclaw/claw-pod.yml logs gateway
claw compose -f examples/openclaw/claw-pod.yml down
```

`claw compose` emits `compose.generated.yml` next to the pod file for easy inspection.

### Supported top-level commands

```bash
claw doctor
claw build [path]
claw inspect <image>
claw compose up [pod]
claw compose down
claw compose ps
claw compose logs [svc]
claw compose health
```

---

## Skill model and inter-service context

### 1) Operator-mounted skills

```dockerfile
SKILL ./skills/research-methods.md
```

```yaml
x-claw:
  skills:
    - ./skills/research-methods.md
```

Duplicate basenames are rejected as hard validation errors.

### 2) Service-emitted skills (preferred)

For `SURFACE service://fleet-master`:

- Service image exposes a label such as:
  - `claw.skill.emit=/app/SKILL.md`
- Clawdapus mounts that as:
  - `/claw/skills/surface-fleet-master.md`

### 3) Fallback skill generation

If no service-emitted skill is available, Clawdapus creates a minimal skill containing:

- hostname and surface target
- known ports discovered from compose/service definitions
- network scope and credential hints

Operators may still override with explicit `skills/surface-*.md`.

---

## Example use patterns

- **Public bot + internal control plane**
  - Run Discord/Slack bot claws and an API/service layer in one pod
  - Use `SURFACE` for local addressability and `HANDLE` for downstream policy wiring

- **Composable workforce**
  - Route tasks between claw roles with stable, generated identities and documented interfaces

- **Evolving services**
  - Ship richer service-provided skills over time while keeping generic fallback as a safety net

---

## Current status

**Active development — pre-release**

- Vertical Spike 1 (Clawfile parse/build) is implemented
- Handle directives and service-surface skill behavior are in active development with ongoing hardening

---

## OpenClaw image reference

Use pinned `alpine/openclaw` images for predictable tests.

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

## Documentation and plans

- [`MANIFESTO.md`](./MANIFESTO.md)
- [`docs/plans/2026-02-18-clawdapus-architecture.md`](./docs/plans/2026-02-18-clawdapus-architecture.md)
- [`docs/plans/2026-02-20-vertical-spike-clawfile-build.md`](./docs/plans/2026-02-20-vertical-spike-clawfile-build.md)
- [`docs/plans/2026-02-21-phase35-handle-directive.md`](./docs/plans/2026-02-21-phase35-handle-directive.md)
- [`docs/decisions/001-cllama-transport.md`](./docs/decisions/001-cllama-transport.md)
- [`docs/decisions/002-runtime-authority.md`](./docs/decisions/002-runtime-authority.md)
- [`docs/reviews/handle-directive-bugs.md`](./docs/reviews/handle-directive-bugs.md)

## AI agent guidance

The repository includes a skill guide at [`skills/clawdapus/SKILL.md`](./skills/clawdapus/SKILL.md).

Install for Claude Code:

```bash
cp -r skills/clawdapus ~/.claude/skills/
```

## Contributing

Start with [`MANIFESTO.md`](./MANIFESTO.md) and align with the plan files before contributing.
