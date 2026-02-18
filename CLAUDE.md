# CLAUDE.md

## What This Is

Clawdapus is infrastructure-layer governance for AI agent containers. The `claw` CLI (Go) treats agents as untrusted workloads — reproducible, inspectable, diffable, killable.

**Key documents:**
- `MANIFESTO.md` — vision, principles, full architecture (source of truth)
- `docs/plans/2026-02-18-clawdapus-architecture.md` — implementation plan and decisions

## Architecture (summary)

- **Clawfile** → extended Dockerfile. `claw build` transpiles to standard Dockerfile, calls `docker build`. Output is OCI image.
- **claw-pod.yml** → extended docker-compose. `claw up` parses `x-claw` blocks, injects cllama sidecars, emits clean `compose.generated.yml`, calls `docker compose`.
- **cllama sidecar** → bidirectional LLM proxy per Claw. Intercepts prompts outbound and responses inbound. Runner never knows.
- **docker compose** is the sole lifecycle authority. Docker SDK is read-only (inspect, logs, events).

## Language & Build

- Go. Single binary. `cmd/claw/main.go` entrypoint.
- Key dependencies: `github.com/moby/buildkit` (Dockerfile parser), `github.com/docker/docker/client` (Docker SDK)

## Conventions

- Don't add signatures to commit messages
- Archive of prior OpenClaw runtime is in `archive/openclaw-runtime/` — reference only
