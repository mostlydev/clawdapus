# CLAUDE.md

## What This Is

Clawdapus is infrastructure-layer governance for AI agent containers. The `claw` CLI (Go) treats agents as untrusted workloads — reproducible, inspectable, diffable, killable.

**Key documents:**
- `MANIFESTO.md` — vision, principles, full architecture (source of truth)
- `docs/plans/2026-02-18-clawdapus-architecture.md` — implementation plan and decisions
- `docs/CLLAMA_SPEC.md` — cllama proxy specification
- `docs/decisions/` — Architecture Decision Records (ADRs)

## Architecture (summary)

- **Clawfile** → extended Dockerfile. `claw build` transpiles to standard Dockerfile, calls `docker build`. Output is OCI image.
- **CLAW_TYPE** → selects a runtime **driver** that knows how to enforce directives for a specific runner. Not just a label.
- **SKILL / x-claw.skills** → explicit skill files from image labels + pod manifests are resolved and mounted read-only into runner skill directories (`SkillDir`) at runtime.
- **Driver framework** → abstract enforcement ops (set, unset, mount_ro, env, cron_upsert, healthcheck, wake). Each driver translates Clawfile directives into runner-specific config injection. Fail-closed: preflight + post-apply verification.
- **claw-pod.yml** → extended docker-compose. `claw up` parses `x-claw` blocks, runs driver enforcement, optionally injects cllama sidecars, emits clean `compose.generated.yml`, calls `docker compose`.
- **cllama sidecar** → optional bidirectional shared pod-level LLM proxy layer. Services are named `cllama-<type>`; runner talks to the first configured proxy and is unaware of interception.
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
- `cmd/claw/compose_up.go` — main orchestration for `claw up`

## Implementation Status (as of 2026-02-27)

| Phase | Status |
|-------|--------|
| Phase 1 — Clawfile parser + build | DONE |
| Phase 2 — Driver framework + pod runtime + OpenClaw + volume surfaces | DONE |
| Phase 3 Slice 1 — CLAWDAPUS.md + multi-claw | DONE |
| Phase 3 Slice 2 — Service surface skills | DONE |
| Phase 3.5 — HANDLE directive + social topology | DONE |
| Phase 3 Slice 3 — INVOKE scheduling + Discord config wiring | DONE |
| Phase 3 Slice 4 — Social topology: mentionPatterns, allowBots, peer handle users | DONE |
| Phase 3 Slice 5 — Channel surface bindings: ChannelConfig, applyDiscordChannelSurface, surface-discord.md | DONE |
| Phase 4 Slices 2+3 — cllama wiring in clawdapus repo | DONE |
| Phase 4 Slice 1 — cllama-passthrough standalone proxy (in-repo) | DONE |
| Phase 4 Task 3.4 — doc fixes (CLLAMA_SPEC, ADRs) | DONE |
| Phase 4.5 — Unified worker architecture (config, provision, diagnostic) | DESIGN |
| Phase 5 — Drift scoring | NOT STARTED |
| Phase 6 — Recipe promotion | NOT STARTED |

## Important Implementation Decisions (settled)

- CLI commands are `claw up/down/ps/logs/health` (not `claw compose up`)
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
- `claw-internal` Docker network is NOT `internal: true` — agents need egress for LLM APIs, Discord, Slack, etc.; isolation is at config/mount level, not network level
- Config dir (`/app/config`) and cron dir (`/app/state/cron`) are bind-mounted as directories, not files — openclaw performs atomic writes by renaming temp files alongside the target; file-only mounts cause EBUSY
- `plugins.entries.discord.enabled: true` is pre-seeded in generated config when HANDLE discord is declared — prevents gateway startup doctor from overwriting the config
- INVOKE directive bakes scheduled tasks as image labels (`claw.invoke.N`); driver generates `jobs.json` mounted at `/app/state/cron/`; openclaw cron scheduler picks it up automatically
- `allowBots: true` is unconditional on `channels.discord` — bot-to-bot messaging requires it; no config knob
- `mentionPatterns` auto-derived from each claw's discord handle: text `(?i)\b@?<username>\b` + native `<@!?<id>>`; injected into `agents.list[0].groupChat.mentionPatterns` in openclaw config
- `PeerHandles` on `ResolvedClaw` — pre-pass in `compose_up.go` collects all pod handles; each claw's guild `users[]` allowlist includes own ID + all peer Discord bot IDs (sorted); enables inter-agent mentions
- Webhook `User-Agent` must be `DiscordBot (url, version)` — bare `Python-urllib` is blocked by Cloudflare with error code 1010 (ASN ban); applies to any non-browser HTTP client posting to Discord webhook endpoints
- **HANDLE vs SURFACE channel**: HANDLE = identity (bot ID, guild membership, mentionPatterns, peer bots, allowBots). SURFACE channel = routing ACL (dmPolicy, allowFrom, guild policy). Both coexist; SURFACE runs after HANDLE in `GenerateConfig` so routing config takes precedence over HANDLE defaults
- **Map-form channel surfaces**: `channel://discord: {dm: {...}}` in pod YAML carries `ChannelConfig` parsed at the pod layer (`pod.Parse()`). String-form `"channel://discord"` yields nil `ChannelConfig` (simple enable). `ClawBlock.Surfaces` is `[]driver.ResolvedSurface`, not `[]string`
- **`applyDiscordChannelSurface`** in `config.go` writes `dmPolicy`, `allowFrom`, and per-guild policy into the openclaw config map after the HANDLE loop. Unknown platforms are silently skipped (no error)
- **Channel surface skills**: `GenerateChannelSkill` produces `surface-discord.md` (etc.); `resolveChannelGeneratedSkills` in compose_up.go writes and mounts it alongside service surface skills. CLAWDAPUS.md Surfaces section and Skills index both reference it
- **cllama declaration model**: `Cllama []string` — supports multiple proxy types per agent. Clawfile: multiple `CLLAMA` directives. Pod YAML: `cllama: passthrough` (string coerced to `[]string`) or `cllama: [passthrough, policy]`
- **cllama chain boundary**: data model supports multi-proxy chains; runtime currently fail-fast rejects `len(Cllama) > 1` until chain execution semantics land in Phase 5
- **Credential starvation split**: real provider API keys belong only in `x-claw.cllama-env` (proxy service env), never in agent env blocks. `stripLLMKeys` + preflight check enforce this
- **cllama-passthrough**: Standalone Go binary (in `./cllama-passthrough`). OpenAI-compatible proxy on :8080, operator web UI on :8081. Multi-provider registry (OpenAI, Anthropic, OpenRouter, Ollama). Auth at `/claw/auth/providers.json` (env overrides file)
- **Bearer token format**: `<agent-id>:<48-hex-chars>` via crypto/rand. Proxy validates secret against metadata.json. Per-ordinal tokens for count > 1 services
- **cllama provider-level rewrite (schema fix)**: Plan originally wrote to `agents.defaults.model.baseURL/apiKey` but OpenClaw Zod schema rejects those keys. Fixed: cllama config writes to `models.providers.<provider>.{baseUrl,apiKey,api,models}` — schema-valid, per-provider, handles multi-provider pods. Helper functions: `collectCllamaProviderModels`, `splitModelRef`, `normalizeProviderID`, `defaultModelAPIForProvider` in `config.go`
- **`x-claw.models` (deferred)**: Pod-level per-service model override is not yet supported. Currently models come from Clawfile `MODEL` labels only. Natural extension of existing override pattern but deferred — touches the model resolution path that cllama provider wiring depends on. Candidate for Phase 4.5 or early Phase 5
- **`compose_up.go` two-pass loop**: Pass 1 inspect+resolve all services, then cllama wiring (detection, tokens, preflight, credential starvation, context gen, proxy config), then Pass 2 materialize. Enables pre-materialize token injection
- **Image-baked env preflight**: `inspectImageEnv` via `docker image inspect` checks for provider API keys baked into the image ENV layer — fails fast if found for cllama-enabled agents. Cached per image ref

## Conventions

- Project identity: "Opinionated Cognitive Architecture — Docker on Rails for Claws"
- Don't add signatures to commit messages
- Use `codex exec "prompt"` for external architectural review; config at `~/.codex/config.toml`
- Implementation plans saved to `docs/plans/YYYY-MM-DD-<feature>.md` (accessible to Codex and other tools)
- Archive of prior OpenClaw runtime is in `archive/openclaw-runtime/` — reference only
- Generated files (`Dockerfile.generated`, `compose.generated.yml`) are build artifacts — inspectable but not hand-edited
- Fail-closed everywhere: missing contract → no start, unknown surface target → error, preflight failure → abort
