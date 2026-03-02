# Driver Parity Matrix & Integration Plan (Revised)

**Date:** 2026-03-02  
**Status:** Ready for implementation  
**Supersedes:** prior draft in this same file

## 0. Executive Summary

- Add two new first-class drivers: `nanobot` and `picoclaw`.
- Keep first implementation focused on driver correctness and stable channels, then expand channel coverage.
- Do not mix this work with broad rewrites of existing drivers; that was inflating scope with little immediate value.
- Fix several factual errors from the prior draft before implementation (paths, health semantics, current parity claims).

## 1. Detailed Review Findings (Prior Draft)

### 1.1 Critical factual corrections

| Area | Prior draft claim | Verified reality | Impact on implementation |
|------|-------------------|------------------|--------------------------|
| Base image support | `openclaw` had no `BaseImage` support | `openclaw` already implements `BaseImageProvider` in `internal/driver/openclaw/baseimage.go` | Plan should not list this as a gap |
| `openclaw` HANDLE support | Telegram/Slack listed as missing | `openclaw` already maps Telegram + Slack in `internal/driver/openclaw/config.go` | Proposed "add Telegram + Slack" task is obsolete |
| Skills parity | Service/channel skills listed as openclaw-only | Skills are generated centrally in `cmd/claw/compose_up.go` for all drivers (`resolveServiceGeneratedSkills`, `resolveChannelGeneratedSkills`, `resolveHandleSkills`) | Parity gap is mostly closed already |
| `nullclaw` health | Described as HTTP health in driver health probe | `nullclaw` `Materialize` sets Docker healthcheck using HTTP, but `HealthProbe` only checks container running | Matrix should distinguish Docker healthcheck vs `claw health` probing |
| Nanobot cron path | `~/.nanobot/data/cron/jobs.json` | Upstream uses `get_data_dir()/cron/jobs.json`, and `get_data_dir()` resolves to `~/.nanobot` => `~/.nanobot/cron/jobs.json` | Wrong mount path would break INVOKE persistence |
| PicoClaw cron path | `~/.picoclaw/cron.json` | Upstream cron store path is `<workspace>/cron/jobs.json` | Wrong path would result in no scheduled jobs |
| PicoClaw home/config path in container | `/root/.picoclaw/...` | Upstream Docker image runs as non-root user (`USER picoclaw`) and defaults to `~/.picoclaw/config.json` | Mounting `/root` is incorrect for standard image |
| INVOKE feature table | Claimed `at`, `every`, and heartbeat parity | Clawdapus parser currently accepts only 5-field cron in `INVOKE` (`internal/clawfile/parser.go`) | Must not plan unsupported syntax yet |
| Platform validation plan | Suggested extending allowed list in `types.go` | `types.go` does not enforce platform allowlist; parser accepts arbitrary platform names | Real chokepoints are scaffold/platform helpers and per-driver validation |
| Existing-driver parity backlog | Included broad extraction/refactor tasks | Several items are already implemented or not prerequisite to new drivers | Should be moved to separate follow-up epics |

### 1.2 Additional architectural constraints that must be explicit

- `Invocation.To` currently resolves channel name -> channel ID only for Discord (`resolveChannelID` in `cmd/claw/compose_up.go`).
- For non-Discord channels, `To` resolution is currently empty unless supplied as raw ID from image labels.
- `claw init` and `claw agent add` interactive options currently expose only `openclaw` and `generic`, even though parser accepts more types.

## 2. Current Driver Baseline (As Implemented Today)

### 2.1 Core lifecycle and build registration

| Capability | openclaw | nullclaw | microclaw | nanoclaw |
|-----------|----------|----------|-----------|----------|
| `Validate`/`Materialize`/`PostApply`/`HealthProbe` | Yes | Yes | Yes | Yes |
| Build-time registration via blank imports | Yes | Yes | Yes | Yes |
| Health command registration (`claw health`) | Yes | Yes | Yes | Yes |
| `BaseImageProvider` | Yes | No | No | No |

### 2.2 Health semantics (important distinction)

| Driver | Docker healthcheck in compose | `Driver.HealthProbe` used by `claw health` |
|--------|-------------------------------|-------------------------------------------|
| openclaw | `openclaw health --json` | Structured exec probe (`openclaw health --json`) |
| nullclaw | HTTP curl to `localhost:3000/health` | Container-running check only |
| microclaw | `pgrep microclaw` | Container-running check only |
| nanoclaw | `pgrep node.*index` | Container-running check only |

### 2.3 Skills/context behavior

- Service/channel/handle skills are generated in compose orchestration, not in a single driver.
- Drivers provide `SkillDir` + `SkillLayout`; compose mounting logic applies across all drivers.
- Therefore parity work should focus on correct `SkillDir`/`SkillLayout` for each new runtime, not re-implementing skill generation.

## 3. Corrected External Runtime Profiles

## 3.1 Nanobot (`HKUDS/nanobot`)

**Validated upstream behavior**

- Config path: `~/.nanobot/config.json`.
- Workspace default: `~/.nanobot/workspace`.
- Cron store: `~/.nanobot/cron/jobs.json`.
- Channels in manager: telegram, whatsapp, discord, feishu, mochat, dingtalk, email, slack, qq, matrix.
- Entry daemon: `nanobot gateway --port <port>`.
- Built-in health endpoint: none detected; status command exists but not HTTP `/health`.
- Container example in repo runs as root and exposes 18790.

**Design implications**

- Use directory mount at `/root/.nanobot`.
- Do not rely on HTTP health; implement container/process liveness probe.
- Generate cron JSON in Nanobot-native structure (`version/jobs[]/schedule/payload/state`).

## 3.2 PicoClaw (`sipeed/picoclaw`)

**Validated upstream behavior**

- Config path resolution: `PICOCLAW_CONFIG` env override, else `~/.picoclaw/config.json`.
- Workspace default: `PICOCLAW_HOME/workspace` (or `~/.picoclaw/workspace`).
- Cron store: `<workspace>/cron/jobs.json`.
- Health endpoints: shared HTTP server exposes `/health` and `/ready`.
- Runtime startup currently fails if channel manager has zero enabled channels (`no channels enabled`).
- Provider config is model-centric: `model_list[]` + `agents.defaults.model_name`.
- Upstream Dockerfile uses non-root user `picoclaw` (uid 1000).

**Design implications**

- Default mount target must be non-root home path (e.g. `/home/picoclaw/.picoclaw`).
- Driver `Validate` should fail-closed when no supported channel is enabled.
- First-pass CLLAMA integration should emit `model_list` entries using `openai/<raw-model>` protocol to keep HTTP-compatible proxy semantics.

## 4. Scope and Non-Goals

## 4.1 In scope (this implementation)

1. Add `nanobot` and `picoclaw` drivers with full interface implementation.
2. Register both drivers for build-time validation and runtime health command.
3. Provide first-pass HANDLE mapping for stable channels.
4. Provide INVOKE -> native cron store translation (5-field cron only).
5. Provide CLLAMA wiring with credential starvation behavior preserved.
6. Add unit tests for parsing, config generation, mounts, and validation failures.

## 4.2 Explicitly out of scope (separate follow-up)

1. New `INVOKE` syntaxes (`at`, `every`) at Clawfile level.
2. Cross-platform `Invocation.To` name resolution beyond Discord.
3. Full channel-surface policy parity across all runtimes.
4. Auto-generation of picoclaw multi-agent `bindings[]` from pod topology.

## 5. Implementation Plan (File-Level)

### Phase 0: Shared Preconditions

**Goal:** Reduce duplication and make new drivers predictable.

### Tasks

1. Add shared helper(s) for model/provider/env resolution.
- Candidate file: `internal/driver/shared/model.go`.
- Extract common logic now duplicated in `microclaw` and `nullclaw` (model split, provider normalization, env token lookup).

2. Extend token-var helper for new platform names.
- Update `internal/driver/shared/platform.go` and tests.
- Keep behavior conservative: return empty string for unknown platforms.

3. Decide MVP channel subset per new driver.
- Nanobot MVP: discord, telegram, slack.
- PicoClaw MVP: discord, telegram, slack, whatsapp, feishu, line, qq, dingtalk, onebot, wecom, wecom_app, pico, maixcam.

### Acceptance criteria

- New shared helper tests pass.
- Existing driver tests still pass unchanged.

### Phase 1: Nanobot Driver (`internal/driver/nanobot/`)

### Files

- `internal/driver/nanobot/driver.go`
- `internal/driver/nanobot/config.go`
- `internal/driver/nanobot/config_test.go`
- `internal/driver/nanobot/driver_test.go`

### Behavior

1. `Validate`
- Require `AgentHostPath` exists.
- Require primary model present.
- Validate HANDLE env vars for supported Nanobot channels.
- Validate CONFIGURE commands in form `nanobot config set <path> <value>`.
- Warn (not fail) on unsupported HANDLE platforms.

2. `Materialize`
- Create writable runtime home dir: `<runtime>/nanobot-home`.
- Write `config.json` to `<runtime>/nanobot-home/config.json`.
- Write seeded `AGENTS.md` into `<runtime>/nanobot-home/workspace/AGENTS.md`:
  - `user AGENTS.md` content
  - separator
  - generated `CLAWDAPUS.md` context
- Set `SkillDir` to `/root/.nanobot/workspace/skills` and `SkillLayout` to `directory`.
- Mount `<runtime>/nanobot-home` -> `/root/.nanobot` read-write.
- Set read-only rootfs true, tmpfs `/tmp`, restart `on-failure`.
- Export `CLAW_MANAGED=true`.

3. `PostApply`
- Verify container is running.

4. `HealthProbe`
- Container-running probe only (no structured endpoint assumed).

5. Config generation details
- Emit JSON with Nanobot-compatible structure:
  - `agents.defaults.model` from `MODEL primary`.
  - `agents.defaults.workspace` set to `/root/.nanobot/workspace`.
  - `channels.<platform>.enabled=true` and token fields from env.
  - Provider wiring:
    - Direct mode: provider-specific fields in `providers.<provider>`.
    - CLLAMA mode: force proxy base URL + cllama token in provider config.
- Apply CONFIGURE commands last to preserve operator override semantics.

6. INVOKE translation
- If invocations exist, write `cron/jobs.json` at `/root/.nanobot/cron/jobs.json` via mounted home dir.
- Generate Nanobot-native `jobs[]` with schedule kind `cron`, payload kind `agent_turn`.
- `Invocation.Name` maps to `job.name` (fallback to deterministic generated name).
- `Invocation.To` maps to payload `to` only if non-empty; otherwise keep deliver false.

### Acceptance criteria

- `driver.Lookup("nanobot")` succeeds.
- Generated runtime includes config and seeded AGENTS file in mounted home.
- Config honors HANDLE + MODEL + CONFIGURE ordering.
- Cron store file loads with Nanobot `CronService` schema.

### Phase 2: PicoClaw Driver (`internal/driver/picoclaw/`)

### Files

- `internal/driver/picoclaw/driver.go`
- `internal/driver/picoclaw/config.go`
- `internal/driver/picoclaw/config_test.go`
- `internal/driver/picoclaw/driver_test.go`

### Behavior

1. `Validate`
- Require `AgentHostPath` exists.
- Require primary model present.
- Require at least one supported enabled HANDLE (or fail with explicit message), matching upstream `no channels enabled` behavior.
- Validate required tokens/secrets per enabled channel.
- Validate CONFIGURE commands in form `picoclaw config set <path> <value>`.

2. `Materialize`
- Create writable runtime home dir `<runtime>/picoclaw-home`.
- Write config to `<runtime>/picoclaw-home/config.json`.
- Write seeded AGENTS to `<runtime>/picoclaw-home/workspace/AGENTS.md` (contract + CLAWDAPUS context).
- Set `SkillDir` to `/home/picoclaw/.picoclaw/workspace/skills`, `SkillLayout=directory`.
- Mount `<runtime>/picoclaw-home` -> `/home/picoclaw/.picoclaw` read-write.
- Set environment:
  - `PICOCLAW_HOME=/home/picoclaw/.picoclaw`
  - `PICOCLAW_CONFIG=/home/picoclaw/.picoclaw/config.json`
  - `CLAW_MANAGED=true`
- Read-only rootfs true, tmpfs `/tmp`, restart `on-failure`.

3. `PostApply`
- Verify container running.

4. `HealthProbe`
- Exec inside container against localhost health endpoint.
- Parse `/health` JSON (`status` expected `ok` for healthy).
- Optional secondary probe `/ready` for richer detail if available.

5. Config generation details
- Model-centric config, not legacy provider-only shape:
  - `agents.defaults.model_name` must point to an entry in `model_list[]`.
  - Build `model_list[]` from clawdapus model directives.
- CLLAMA wiring:
  - For each model, emit protocol `openai/<raw-model-ref>`.
  - Set `api_base` to cllama proxy URL.
  - Set `api_key` to cllama token.
- Handle/channel mapping to picoclaw channel keys (discord/telegram/slack/etc).
- Apply CONFIGURE last.

6. INVOKE translation
- Write `workspace/cron/jobs.json` in picoclaw-native schema.
- Use schedule kind `cron` with 5-field expression.
- Preserve `Invocation.Name` and `Invocation.To` as available.

### Acceptance criteria

- `driver.Lookup("picoclaw")` succeeds.
- Config uses `model_list[]` + `agents.defaults.model_name` and passes provider creation path.
- Container boots without root assumptions.
- `HealthProbe` returns structured detail from `/health`.

### Phase 3: Wiring and CLI Surface

### Tasks

1. Register drivers in build and health command import sets.
- `internal/build/build.go`
- `cmd/claw/compose_health.go`

2. Extend scaffold type parsing and defaults.
- Update parser + errors in `cmd/claw/scaffold_helpers.go`.
- Add default base images for new types.
- Update interactive choices in:
  - `cmd/claw/init.go`
  - `cmd/claw/agent.go`

3. Keep platform scaffold prompts conservative for now.
- Do not expose all new platforms in init/add prompts in first pass unless token/id templating is fully defined.

4. Update docs.
- `README.md` support table.
- Add examples for nanobot and picoclaw minimal projects.

### Acceptance criteria

- `claw init --type nanobot` and `--type picoclaw` produce valid scaffold.
- `claw up -d` can resolve new driver types end-to-end.

## 6. Test Plan (Must-Have)

### 6.1 Unit tests

- Driver registration tests for each new driver.
- Validate failure tests:
  - missing agent file
  - missing model
  - missing required HANDLE tokens
  - invalid CONFIGURE DSL command
- Config generation tests:
  - direct provider mode
  - cllama mode
  - config override precedence (CONFIGURE after defaults)
- Materialize tests:
  - mount targets and read-only flags
  - skill layout path (`directory`)
  - seeded AGENTS contains CLAWDAPUS context
- INVOKE tests:
  - cron file created
  - payload and schedule schema shape
- Health probe tests:
  - healthy/unhealthy parsing behavior

### 6.2 Integration-style tests (repo-local)

- Add compose fixture(s) with one nanobot and one picoclaw service.
- Verify artifacts under `.claw-runtime/<service>/...`:
  - config paths
  - cron store paths
  - seeded AGENTS
- Verify generated compose includes expected mounts/env for each driver.

### 6.3 Regression protections

- Ensure existing driver tests continue passing.
- Ensure `claw build` validation still rejects unknown CLAW_TYPE values clearly.

## 7. Risks and Mitigations

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| Wrong home/config mount for picoclaw images | High | High | Force `PICOCLAW_HOME` + `PICOCLAW_CONFIG`; avoid `/root` assumptions |
| CLLAMA protocol mismatch in picoclaw `model_list` | Medium | High | Use `openai/<raw-model-ref>` scheme; add focused integration test |
| Nanobot channel field mismatch for long-tail channels | Medium | Medium | Ship stable channel subset first, add channel-specific tests before expansion |
| INVOKE `To` ambiguous outside Discord | High | Medium | Keep current behavior explicit; document limitation; separate follow-up for cross-platform routing |
| Over-scoping with existing-driver parity cleanup | High | Medium | Keep parity cleanup as separate epics after new driver merge |

## 8. Proposed GitHub Issue Breakdown

1. `feat(driver): add nanobot driver (MVP)`
- Driver package + tests
- Stable channels + cron + cllama + health probe
- Build/health registration

2. `feat(driver): add picoclaw driver (MVP)`
- Driver package + tests
- Model-list config + stable channels + cron + health probe
- Non-root path-safe mounts

3. `feat(cli): expose nanobot/picoclaw in init and agent add`
- Scaffolding parser/options/base image defaults
- Docs and examples updates

4. `feat(driver-shared): improve provider/platform helper reuse`
- Extract shared model/env helpers
- Extend token var helper and tests

5. `feat(invoke): cross-platform Invocation.To resolution`
- Generalize `resolveChannelID` beyond Discord
- Add platform-aware delivery mapping

## 9. Implementation Order Recommendation

1. Phase 0 shared helpers.
2. Nanobot driver end-to-end with tests.
3. PicoClaw driver end-to-end with tests.
4. CLI/scaffold exposure and docs.
5. Optional follow-up issues for routing and long-tail parity.

This order keeps risk low, proves architecture with one Python and one Go runtime, and avoids blocking on non-critical parity work.
