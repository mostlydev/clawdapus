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
- Build: `go build -o bin/claw ./cmd/claw`
- Test: `go test ./...` (unit tests run without Docker; E2E tests require build tag `e2e`)
- Vet: `go vet ./...`

## Key Package Structure

- `internal/clawfile/` — Clawfile parser + Dockerfile emitter (Phase 1)
- `internal/driver/` — Driver interface, registry, enforcement ops, OpenClaw + generic drivers
- `internal/driver/openclaw/` — JSON5-aware config injection, CLAWDAPUS.md generation, skill generation
- `internal/pod/` — claw-pod.yml parser, compose emitter
- `internal/inspect/` — label parsing from built images
- `internal/runtime/` — Docker SDK wrapper (read-only only)
- `cmd/claw/compose_up.go` — main orchestration for `claw compose up`

## Implementation Status (as of 2026-02-21)

| Phase | Status |
|-------|--------|
| Phase 1 — Clawfile parser + build | DONE |
| Phase 2 — Driver framework + pod runtime + OpenClaw + volume surfaces | DONE |
| Phase 3 Slice 1 — CLAWDAPUS.md + multi-claw | DONE |
| Phase 3 Slice 2 — Service surface skills | DONE |
| Phase 3.5 — HANDLE directive + social topology | DONE |
| Phase 3 Slice 3 — Channel surface bindings | PENDING |
| Phase 4 — cllama sidecar | NOT STARTED |
| Phase 5 — Drift scoring | NOT STARTED |
| Phase 6 — Recipe promotion | NOT STARTED |

## Important Implementation Decisions (settled)

- CLI commands are `claw compose up/down/ps/logs/health` (not `claw up` directly)
- Config injection uses Go-native JSON5 patching in-memory — never shells out to `openclaw config set` (too slow/noisy)
- Config written on host, mounted read-only into container (not mutated inside container)
- JSON (not JSON5) for generated config — JSON is valid JSON5, YAGNI
- `read_only: true` + `tmpfs` + `restart: on-failure` for all Claw services in generated compose
- Docker SDK is read-only only — `docker compose` is the sole lifecycle authority
- Surface-generated skills use precedence: service-emitted (`claw.skill.emit`) > operator override > fallback stub
- HANDLE env vars injected at lowest priority — never override pod env or driver env
- OpenClaw config is JSON5; driver uses JSON5-aware patcher, never raw jq or standard JSON marshaling
- OpenClaw `openclaw health --json` emits noise to stderr; driver uses stdout-only parsing, scans for first `{`
- OpenClaw schema validates config keys against Zod schema; driver must only inject known-valid keys

## Conventions

- Don't add signatures to commit messages
- Archive of prior OpenClaw runtime is in `archive/openclaw-runtime/` — reference only
- Generated files (`Dockerfile.generated`, `compose.generated.yml`) are build artifacts — inspectable but not hand-edited
- Fail-closed everywhere: missing contract → no start, unknown surface target → error, preflight failure → abort
