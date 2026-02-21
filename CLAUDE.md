# CLAUDE.md

## What This Is

Clawdapus is infrastructure-layer governance for AI agent containers. The `claw` CLI (Go) treats agents as untrusted workloads — reproducible, inspectable, diffable, killable.

**Key documents:**
- `MANIFESTO.md` — vision, principles, full architecture (source of truth)
- `docs/plans/2026-02-18-clawdapus-architecture.md` — implementation plan and decisions

## Architecture (summary)

- **Clawfile** → extended Dockerfile. `claw build` transpiles to standard Dockerfile, calls `docker build`. Output is OCI image.
- **CLAW_TYPE** → selects a runtime **driver** that knows how to enforce directives for a specific runner. Not just a label.
- **SKILL / x-claw.skills** → explicit skill files from image labels + pod manifests are resolved and mounted read-only into runner skill directories (`SkillDir`) at runtime.
- **Driver framework** → abstract enforcement ops (set, unset, mount_ro, env, cron_upsert, healthcheck, wake). Each driver translates Clawfile directives into runner-specific config injection. Fail-closed: preflight + post-apply verification.
- **claw-pod.yml** → extended docker-compose. `claw up` parses `x-claw` blocks, runs driver enforcement, optionally injects cllama sidecars, emits clean `compose.generated.yml`, calls `docker compose`.
- **cllama sidecar** → optional bidirectional LLM proxy per Claw. Intercepts prompts outbound and responses inbound. Runner never knows. Only injected when `CLLAMA` directive is present.
- **Config injection** → primary enforcement model. Surgically writes specific config branches using the runner's own tools (e.g. `openclaw config set` for JSON5-aware OpenClaw config). Works without cllama.
- **Claws are users** → Claws authenticate to services with standard credentials (env vars, Docker secrets). Clawdapus enforces access modes only on mounts where Docker has authority. For services, the service's own auth governs access.
- **docker compose** is the sole lifecycle authority. Docker SDK is read-only (inspect, logs, events).

## Language & Build

- Go. Single binary. `cmd/claw/main.go` entrypoint.
- Key dependencies: `github.com/moby/buildkit` (Dockerfile parser), `github.com/docker/docker/client` (Docker SDK)

## Conventions

- Don't add signatures to commit messages
- Archive of prior OpenClaw runtime is in `archive/openclaw-runtime/` — reference only
