# Phase 2 Progress Tracker

**Plan:** `docs/plans/2026-02-20-phase2-runtime-driver.md`
**Started:** 2026-02-20
**Execution:** Subagent-driven from main session

## Task Status

| # | Task | Status | Commit | Notes |
|---|------|--------|--------|-------|
| 1 | Add CONFIGURE labels to emitter | DONE | `24aee60` | Labels + inspect round-trip working |
| 2 | Add yaml.v3 dependency | DONE | `0e8bca6` | go.mod updated |
| 3 | Driver types and registry | DONE | `659fa0a` | Driver interface, ResolvedClaw, MaterializeResult, registry |
| 4 | Contract enforcement | DONE | `e48c32a` | Fail-closed AGENT validation |
| 5 | OpenClaw config generation | DONE | `f5cd013` | JSON config from MODEL + CONFIGURE directives |
| 6 | OpenClaw driver | DONE | `dee74da` | Validate, Materialize, PostApply, HealthProbe stub |
| 7 | Pod parser | DONE | `9cf4943` | claw-pod.yml with x-claw blocks, yaml.v3 |
| 8 | Compose emitter | DONE | `8d68229` | read_only, tmpfs, ordinals, volumes, deterministic |
| - | Codex review fixes | DONE | `b077ece` | Path traversal guard, fail-closed defaults |
| 9 | CLI compose commands | DONE | `7eda8b8` | up/down/ps/logs via docker compose passthrough |
| 10 | Health probe (stderr separation) | DONE | `fe18c80` | Scans for first `{` in stdout, ignores stderr noise |
| 11 | Example claw-pod.yml + integration smoke | DONE | `72102a1` | Example pod + integration test behind build tag |
| 12 | E2E Docker integration | DONE | — | Stub fixture, PostApply wiring, Docker SDK verify, 3 e2e tests |
| 13 | HealthProbe wired (docker exec) | DONE | — | Real `openclaw health --json` exec inside container, ParseHealthJSON reuse |
| 14 | `claw health` CLI | DONE | — | Iterates containers, inspects claw.type label, calls driver HealthProbe |
| 15 | Pod-internal network isolation | DONE | — | `claw-internal` network with `internal: true`, claw services only |
| 16 | E2E HealthProbe + network tests | DONE | — | TestE2EHealthProbe, network assertions in lifecycle test |

## How to Resume

If context is lost, read these files in order:
1. `docs/plans/2026-02-20-phase2-runtime-driver.md` — full implementation plan
2. `docs/plans/phase2-progress.md` — this file (which tasks are done)
3. `docs/plans/2026-02-20-vertical-spike-clawfile-build.md` — Phase 1 completion + Phase 2 outline
4. `CLAUDE.md` — project conventions

Then pick up at the first NOT STARTED task in the active phase plan.

## Phase 2 Hardening Complete

All 15 tasks done (11 core + 4 hardening). Exit criteria met:
- `go test ./...` — all packages pass
- `go build -o bin/claw ./cmd/claw` — binary compiles
- `read_only: true` + `tmpfs` + bounded restart in all generated compose output
- Fail-closed: missing agent file → hard error, nil driver result → safe defaults
- Path traversal guard on contract resolution
- Deterministic compose output (sorted service names, stable ordinals)
- HealthProbe wired: real docker exec of `openclaw health --json` with ParseHealthJSON
- `claw health` CLI: table output of service health via driver probes
- Network isolation: `claw-internal` network with `internal: true` for all claw services

**Completed:** 2026-02-20

---

## Phase 3 Slice 1: CLAWDAPUS.md Context Injection

**Plan:** `docs/plans/2026-02-21-phase3-surface-manifests.md`
**Started:** 2026-02-21

| # | Task | Status | Commit | Notes |
|---|------|--------|--------|-------|
| 1 | ParseSurface helper + wire into compose_up | DONE | `227c5fe` | Parses `scheme://target access-mode` into ResolvedSurface |
| 2 | GenerateClawdapusMD in openclaw driver | DONE | `e9395dd` | Identity + surfaces + skills sections, wired into Materialize |
| 3 | Bootstrap-extra-files hook always-on | DONE | `2f5fcbd` | Config always includes CLAWDAPUS.md in bootstrap paths |
| 4 | Multi-claw example | DONE | `315076c` | researcher (rw) + analyst (ro) sharing volume://research-cache |
| 5 | Multi-service compose emit test | DONE | `d5a4482` | YAML-parsed assertions for shared volume, access modes, network |
| 6 | Final verification | DONE | — | All tests pass, binary builds |

**Completed:** 2026-02-21

---

## Phase 3 Slice 2: Service-emitted Surface Skills

**Plan:** `docs/plans/2026-02-21-phase3-surface-manifests.md` (addendum)
**Status:** DONE

| # | Task | Status | Commit | Notes |
|---|------|--------|--------|-------|
| 1 | Define service-emitted priority model | DONE | — | Service emits via `claw.skill.emit`, operator override, fallback stub |
| 2 | Document label and extraction behavior | DONE | — | README + `SKILL` docs + plan addendum updated |
| 3 | Add Ports to ResolvedSurface + GeneratedSkill type | DONE | `d559d62` | Ports field + GeneratedSkill type in driver/types.go |
| 4 | Parse expose from pod services | DONE | `f40b568` | Expose []string, int coercion, tests |
| 5 | Extract claw.skill.emit label in inspect | DONE | `1ef70ed` | SkillEmit field, ordered before claw.skill.N |
| 6 | Service skill fallback generation | DONE | `c230882` | runtime.GenerateServiceSkillFallback + ExtractServiceSkill |
| 7 | Enrich surfaces with ports | DONE | `697115e` | compose_up populates Ports from Service.Expose |
| 8 | GenerateServiceSkill in openclaw driver | DONE | `8db451b` | Fallback markdown from ResolvedSurface |
| 9 | Wire claw.skill.emit into compose_up | DONE | `275baca` | resolveSkillEmit extracts from image, writes to runtime dir |
| 10 | Wire fallback skill generation into compose_up | DONE | `9eb48ef` | resolveServiceGeneratedSkills, precedence: generated < image/pod |
| 11 | CLAWDAPUS.md references service skills | DONE | `0e876cd` | Skill ref in surfaces section + skills index |
| 12 | Example pod with service surface | DONE | `f3fbde3` | nginx api-server with expose:80, researcher consumes |
| 13 | Network wiring for service targets | DONE | `8b20100` | Non-claw service targets added to claw-internal |
| 14 | Service target validation in compose_emit | DONE | `8dcdeef` | Fail-closed: unknown service surface target = error |
| 15 | Final verification | DONE | — | go test, go build, go vet all clean |

### Precedence model (implemented)

- Service-emitted skill (`claw.skill.emit`) extracted from image at compose-up time
- Operator-provided explicit `surface-<name>.md` entries override by basename (`SKILL`/`x-claw.skills`)
- Generic fallback generated only when no source skill exists
- `mergeResolvedSkills(generatedSkills, skills)` — generated is base, image/pod skills win

**Completed:** 2026-02-20

## SKILL Directive

**Plan:** `docs/plans/2026-02-20-skill-directive-plan.md`
**Started:** 2026-02-20

| # | Task | Status | Commit | Notes |
|---|------|--------|--------|-------|
| 1 | SKILL in Clawfile parser | DONE | `39ca009` | Parse + validation |
| 2 | SKILL label emission | DONE | `49f5a16` | claw.skill.N labels |
| 3 | SKILL in image inspect | DONE | — | claw.skill.* extraction |
| 4 | Skills in pod parser | DONE | — | skills: in x-claw |
| 5 | Driver types | DONE | `689c77e` | ResolvedSkill, SkillDir |
| 6 | OpenClaw SkillDir + CLAWDAPUS.md | DONE | `38e435a` | /claw/skills, skill index |
| 7 | Skill resolution helper | DONE | — | Path traversal, dedup |
| 8 | Wire into compose_up | DONE | `cd2a4eb` | Merge + mount |
| 9 | Example | DONE | `2e95c6a` | research-methodology.md |
| 10 | Final verification | DONE | — | All tests pass |

**Completed:** 2026-02-20

---

---

## Phase 3.5: HANDLE Directive + Social Topology Projection

**Plan:** `docs/plans/2026-02-21-phase35-handle-directive.md`
**Status:** DONE

| # | Task | Status | Commit | Notes |
|---|------|--------|--------|-------|
| 1 | Add HandleDirective to Clawfile parser | DONE | — | `Handles []string` in ClawConfig, HANDLE directive parsed + dedup guard |
| 2 | Emit HANDLE labels in Dockerfile | DONE | — | `LABEL claw.handle.<platform>=true` |
| 3 | Parse claw.handle.* labels in inspect | DONE | — | `ClawInfo.Handles []string`, sorted, only "true"/"1" accepted |
| 4 | Add handles to x-claw block and pod parser | DONE | — | String shorthand + full map form with guild/channel hierarchy |
| 5 | Add Handles to ResolvedClaw | DONE | — | `HandleInfo{ID, Username, Guilds[]GuildInfo{Channels[]ChannelInfo}}` in driver/types.go |
| 6 | Wire handles into ResolvedClaw in compose_up | DONE | — | `rc.Handles = svc.Claw.Handles` |
| 7 | Generate CLAW_HANDLE_* env vars + inject into all services | DONE | — | `_ID`, `_USERNAME`, `_GUILDS`, `_JSON` broadcast to every service in pod |
| 8 | OpenClaw driver — HANDLE enables platform config | DONE | — | `channels.<platform>.enabled = true` for discord/slack/telegram |
| 9 | CLAWDAPUS.md — Handles section | DONE | — | Guild/channel tree + handle skills in skills index |
| 10 | Generate handle skill files | DONE | — | `skills/handle-<platform>.md` per claw; `resolveHandleSkills` in compose_up |
| 11 | Update openclaw example | DONE | — | `handles:` with full discord guild/channel map in claw-pod.yml |
| 12 | Final verification | DONE | — | `go test ./...`, `go build`, `go vet` all clean |

### Design decisions (Phase 3.5)

- HandleInfo carries full membership schema: guild IDs + names + channel IDs + names
- Platform keys normalized to lowercase in pod parser (bug fix from Codex review)
- Duplicate HANDLE directives in Clawfile → hard parse error (fail-closed)
- Label value semantics: only `"true"` or `"1"` treated as enabled in inspect
- CLAW_HANDLE_* env vars injected at lowest priority (below pod env, below driver env)
- handle-<platform>.md skills generated per claw in svcRuntimeDir/skills/
- Skills section in CLAWDAPUS.md: handle-* and surface-* auto-generated filtered from operator section

**Completed:** 2026-02-21

---

## Phase 3 Slice 4: Social Topology — mentionPatterns, allowBots, Peer Handles

**Status:** DONE
**Commit:** `f110daa`

| # | Task | Status | Notes |
|---|------|--------|-------|
| 1 | Add `PeerHandles` to `ResolvedClaw` | DONE | `driver/types.go`: `map[string]map[string]*HandleInfo` |
| 2 | Pre-pass in `compose_up.go` to collect pod handles | DONE | Cheap first pass; injects `PeerHandles` before `Materialize` |
| 3 | `allowBots: true` unconditional on `channels.discord` | DONE | Enables bot-to-bot messaging; no config knob |
| 4 | `agents.list` generation with `mentionPatterns` | DONE | Text `(?i)\b@?<username>\b` + native `<@!?<id>>`; matches real openclaw schema |
| 5 | Guild `users[]` = own ID + all peer Discord IDs (sorted) | DONE | `discordBotIDs(rc)` helper; enables inter-agent mentions |
| 6 | Per-channel `{allow, requireMention}` entries | DONE | Each declared channel gets explicit allow entry |
| 7 | 4 new config tests | DONE | allowBots, mentionPatterns, guild users, peer handle aggregation |
| 8 | trading-api webhook mention proof | DONE | Webhook posts `<@TIVERTON_ID> <@WESTIN_ID>`; `User-Agent: DiscordBot` required (Cloudflare error 1010) |
| 9 | Spike test: verify mentions in Discord channel | DONE | `spikeVerifyDiscordGreeting` checks both agent IDs appear |

### Design decisions

- `PeerHandles` collected in cheap pre-pass over already-parsed pod YAML — no image inspection needed
- `allowBots` is unconditional; a bot that can't hear other bots is useless in a pod
- `mentionPatterns` order: text username first (human-readable), then Discord native `<@!?id>` (client-rendered)
- Webhook `User-Agent` must be `DiscordBot (url, version)` — bare `Python-urllib` triggers Cloudflare 1010
- `CLAW_HANDLE_*` env vars already broadcast to ALL services (including non-claw); no extra wiring needed for webhook mentions

**Completed:** 2026-02-22

---

## Phase 3 Slice 3: Channel Surface Bindings

**Plan:** `docs/plans/2026-02-21-phase3-slice3-channel-surfaces.md`
**Status:** DONE

| # | Task | Status | Commit | Notes |
|---|------|--------|--------|-------|
| 1 | Define `ChannelConfig` + update `ResolvedSurface` | DONE | — | Driver model supports channel policy payload |
| 2 | Parse channel surfaces from x-claw (string + map forms) | DONE | — | Pod parser supports shorthand and structured config |
| 3 | Wire channel surfaces through `compose_up` | DONE | — | Channel config passed end-to-end into driver resolution |
| 4 | OpenClaw driver channel translation | DONE | — | `applyDiscordChannelSurface` writes channel ACL config |
| 5 | Generate channel surface skill files | DONE | — | `surface-discord.md` generated and mounted |
| 6 | CLAWDAPUS.md channel surface output | DONE | — | Surfaces + skill index include channel entries |
| 7 | Driver capability + fail-closed behavior | DONE | — | Unsupported/invalid channel targets rejected |
| 8 | Trading desk example updated | DONE | — | Pod manifest and README include channel surface usage |
| 9 | Final verification | DONE | — | `go test ./...` + spike validation |

**Completed:** 2026-02-27

---

## Phase 4: cllama Sidecar Integration

**Plan:** `docs/plans/2026-02-26-phase4-cllama-sidecar.md`
**Status:** DONE (shipped scope); policy pipeline intentionally deferred

| # | Task Group | Status | Notes |
|---|------------|--------|-------|
| 1 | Slice 1 — `mostlydev/cllama-passthrough` proxy implementation | DONE | Dual-server entrypoint, identity, provider registry, context loading, transparent proxy, structured logging, UI |
| 2 | Slice 2 — clawdapus infra wiring | DONE | `Cllama []string`, two-pass compose flow, token/context generation, proxy service injection |
| 3 | Slice 3 — OpenClaw integration + tests | DONE | Provider-level rewrite (`models.providers.*`), proxy metadata in CLAWDAPUS.md, tests updated |
| 4 | Task 3.4 docs/ADR fixes | DONE | CLLAMA spec + ADR updates merged |
| 5 | Submodule integration | DONE | `cllama-passthrough` added as submodule, tracking `master` |
| 6 | Real-proxy spike run | DONE | `TestSpikeComposeUp` passed with live `cllama-passthrough` image |
| 7 | Policy pipeline (future scope) | DEFERRED | Phase 5+ |

---

## LLM Configuration Workers (Phase X)

**Plan:** `docs/plans/2026-02-21-llm-configuration-workers.md`
**Status:** DESIGN ONLY — implement after Phase 3 Slice 3 is proven

First target: migrate channel surface config translation (Phase 3 Slice 3, Task 4) from hardcoded driver ops to LLM worker intent-generator + verifier model.

---

## Key Decisions Made During Execution

- CONFIGURE directives now emitted as `claw.configure.N` labels (Task 1)
- CLI commands are `claw up/down/ps/logs/health` (not `claw compose up`)
- `read_only: true` + `tmpfs` + bounded `restart: on-failure` for all Claw services
- JSON (not JSON5) for config generation — JSON is valid JSON5, YAGNI
- Locally-built images for tests, no alpine/openclaw dependency in critical path
