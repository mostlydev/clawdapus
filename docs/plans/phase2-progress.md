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
| 14 | `claw compose health` CLI | DONE | — | Iterates containers, inspects claw.type label, calls driver HealthProbe |
| 15 | Pod-internal network isolation | DONE | — | `claw-internal` network with `internal: true`, claw services only |
| 16 | E2E HealthProbe + network tests | DONE | — | TestE2EHealthProbe, network assertions in lifecycle test |

## How to Resume

If context is lost, read these files in order:
1. `docs/plans/2026-02-20-phase2-runtime-driver.md` — full implementation plan
2. `docs/plans/phase2-progress.md` — this file (which tasks are done)
3. `docs/plans/2026-02-20-vertical-spike-clawfile-build.md` — Phase 1 completion + Phase 2 outline
4. `CLAUDE.md` — project conventions

Then pick up at the first PENDING task and dispatch a subagent per the plan.

## Phase 2 Hardening Complete

All 15 tasks done (11 core + 4 hardening). Exit criteria met:
- `go test ./...` — all packages pass
- `go build -o bin/claw ./cmd/claw` — binary compiles
- `read_only: true` + `tmpfs` + bounded restart in all generated compose output
- Fail-closed: missing agent file → hard error, nil driver result → safe defaults
- Path traversal guard on contract resolution
- Deterministic compose output (sorted service names, stable ordinals)
- HealthProbe wired: real docker exec of `openclaw health --json` with ParseHealthJSON
- `claw compose health` CLI: table output of service health via driver probes
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
**Status:** PENDING

| # | Task | Status | Commit | Notes |
|---|------|--------|--------|-------|
| 1 | Add HandleDirective to Clawfile parser | PENDING | — | `internal/clawfile/directives.go` + `parser.go` |
| 2 | Emit HANDLE labels in Dockerfile | PENDING | — | `LABEL claw.handle.<platform>=true` |
| 3 | Parse claw.handle.* labels in inspect | PENDING | — | ClawInfo.Handles []string |
| 4 | Add handles to x-claw block and pod parser | PENDING | — | `handles: { discord: "..." }` |
| 5 | Add Handles to ResolvedClaw | PENDING | — | `internal/driver/types.go` |
| 6 | Wire handles into ResolvedClaw in compose_up | PENDING | — | |
| 7 | Generate CLAW_HANDLE_* env vars + inject into all services | PENDING | — | Broadcast to every service in pod |
| 8 | OpenClaw driver — HANDLE enables platform config | PENDING | — | `channels.<platform>.enabled = true` |
| 9 | CLAWDAPUS.md — Handles section | PENDING | — | Agent's own public identities |
| 10 | Update openclaw example | PENDING | — | `handles:` in claw-pod.yml |
| 11 | Final verification | PENDING | — | `go test ./...` |

---

## Phase 3 Slice 3: Channel Surface Bindings

**Plan:** `docs/plans/2026-02-21-phase3-slice3-channel-surfaces.md`
**Status:** PENDING

| # | Task | Status | Commit | Notes |
|---|------|--------|--------|-------|
| 1 | Define ChannelConfig + update ResolvedSurface | PENDING | — | `internal/driver/types.go` |
| 2 | Parse channel surfaces from x-claw (string + map forms) | PENDING | — | `internal/pod/parser.go` |
| 3 | Wire channel surfaces through compose_up | PENDING | — | Pass ChannelConfig to driver |
| 4 | OpenClaw driver — translate channel surface to config ops | PENDING | — | v1 hardcoded JSON5 patches |
| 5 | Generate surface skill for channel surfaces | PENDING | — | `skills/surface-discord.md` |
| 6 | CLAWDAPUS.md — channel surface section | PENDING | — | |
| 7 | Preflight — driver capability check | PENDING | — | Fail on unsupported channel platform |
| 8 | Update openclaw example | PENDING | — | |
| 9 | Final verification | PENDING | — | `go test ./...` |

---

## LLM Configuration Workers (Phase X)

**Plan:** `docs/plans/2026-02-21-llm-configuration-workers.md`
**Status:** DESIGN ONLY — implement after Phase 3 Slice 3 is proven

First target: migrate channel surface config translation (Phase 3 Slice 3, Task 4) from hardcoded driver ops to LLM worker intent-generator + verifier model.

---

## Key Decisions Made During Execution

- CONFIGURE directives now emitted as `claw.configure.N` labels (Task 1)
- CLI commands are `claw compose up/down/ps/logs` (not `claw up`)
- `read_only: true` + `tmpfs` + bounded `restart: on-failure` for all Claw services
- JSON (not JSON5) for config generation — JSON is valid JSON5, YAGNI
- Locally-built images for tests, no alpine/openclaw dependency in critical path
