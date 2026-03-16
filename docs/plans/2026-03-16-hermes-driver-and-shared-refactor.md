# Hermes Driver Plan (Revised)

## Summary

Add Hermes as the seventh first-class driver, but keep the surrounding refactor narrow.

The original draft bundled three different scopes:

1. A new driver (`internal/driver/hermes/`)
2. Cross-driver helper extraction
3. Test-fixture cleanup

That is too much for one change set, and it conflicts with the earlier guidance in `docs/plans/2026-03-01-driver-parity-matrix.md`: do not mix new-driver rollout with broad rewrites unless the rewrite is an exact, low-risk extraction.

This revised plan keeps the larger picture straight:

- Hermes lands under the existing v1 driver model from `docs/plans/2026-02-18-clawdapus-architecture.md`
- compose-time responsibilities stay in `cmd/claw/compose_up.go`
- shared extraction is limited to exact-copy helpers only
- Hermes-specific config, cron translation, and platform wiring stay driver-local
- worker-architecture ambitions remain future work, not part of this implementation

Recommended delivery: two PRs, or at minimum two mergeable tracks in one branch.

## Larger-Picture Fit

### What this must align with

- **Architecture baseline:** drivers are still hardcoded Go translators plus validators, not worker-driven intent executors yet.
- **Runtime authority:** Docker Compose remains the only lifecycle authority. Hermes integration must only materialize files, env, mounts, and health checks.
- **Contract composition:** `x-claw.include` is already compiled before driver materialization. Hermes must consume `rc.AgentHostPath`, not rebuild contract composition logic.
- **Generated skills:** handle skills, service-surface skills, channel-surface skills, and reference includes are already resolved in compose orchestration. Hermes should expose `SkillDir` and `SkillLayout`, not regenerate skills itself.
- **cllama:** Hermes must use the same credential-starvation and proxy-routing model as the other drivers.
- **INVOKE:** per ADR-006, translation stays runner-native. Do not force all drivers through a fake common cron schema.

### What this does not try to solve

- Worker architecture (`docs/plans/2026-02-27-worker-architecture-unified.md`)
- Channel-surface policy parity for Hermes
- A new cross-driver capability model
- Scaffold UX for `claw init` / `claw agent add` when no stable Hermes base image story exists

## Verified Hermes Runtime Notes

These points were checked against the upstream Hermes repository and docs before revising this plan.

### 1. `HERMES_HOME` exists and should be used

The original draft treated Hermes home as fixed at `~/.hermes/` with no override. Upstream code uses `HERMES_HOME` in multiple places:

- gateway PID/status files
- cron storage
- config loading
- SOUL loading

For Clawdapus, that is a feature, not a bug. We should set `HERMES_HOME` explicitly for deterministic container layout.

### 2. `AGENTS.md` is workspace context, not home-directory context

Hermes loads:

- `SOUL.md` from `HERMES_HOME`
- `AGENTS.md` recursively from the working directory

That means mounting `AGENTS.md` into `/root/.hermes/` is the wrong integration shape. Hermes needs a workspace directory with:

- an effective `AGENTS.md`
- `MESSAGING_CWD` and `TERMINAL_CWD` pointing at that workspace

### 3. `config.yaml` is not the only runtime config surface

Hermes also reads `gateway.json`, and some gateway behavior is driven directly from environment variables. The driver should prefer the smallest stable config surface that gets the job done:

- `config.yaml` for model and stable user-facing settings
- `.env` for provider keys and platform credentials

Decision for v1: do **not** write `gateway.json`.

For the supported Hermes v1 scope, `config.yaml` plus `.env` should be enough. Bringing `gateway.json` into the first implementation would add another partially overlapping config surface without a demonstrated need.

### 4. Hermes cron schema is richer than the draft assumed

Upstream Hermes cron jobs support:

- `cron`
- `interval`
- `once`

Clawdapus still only parses 5-field cron today, but the emitted Hermes job shape should match Hermes's real on-disk format, including fields like:

- `schedule`
- `schedule_display`
- `repeat`
- `enabled`
- `state`
- `created_at`
- `next_run_at`
- `deliver`

### 5. Platform support is not a single-token story

The original draft overstated platform parity. For Hermes v1, the safe subset is:

- `discord`
- `telegram`
- `slack`

Deferred:

- `whatsapp` - requires bridge/session setup, not just a token
- `signal` - requires `SIGNAL_HTTP_URL` and `SIGNAL_ACCOUNT`
- `homeassistant` - requires different credential shape and no current HANDLE mapping in Clawdapus

## Scope

### In Scope

1. Add `internal/driver/hermes/` with full `Driver` implementation.
2. Register Hermes for build-time validation and runtime health probing.
3. Support Hermes v1 HANDLE mapping for `discord`, `telegram`, and `slack`.
4. Support Hermes v1 MODEL + CLLAMA wiring.
5. Support `INVOKE` to Hermes-native `cron/jobs.json` using existing 5-field cron parser constraints.
6. Extract only exact-copy helpers that materially reduce Hermes implementation duplication.

### Explicitly Out of Scope

1. WhatsApp, Signal, and Home Assistant HANDLE support.
2. Automatic mapping from `ResolvedSurface.ChannelConfig` to Hermes gateway allowlists or routing rules.
3. Generic shared cron JSON generation across all drivers.
4. Writing or managing `gateway.json`.
5. Test-fixture builder cleanup as part of Hermes delivery.
6. Hermes scaffold support in `claw init` / `claw agent add`.
7. `BaseImageProvider` for Hermes.
8. Worker-based config/provision/diagnostic execution.

## Shared Extraction Plan

Shared work is justified only when the implementation is already identical in existing drivers.

### A1. `internal/driver/shared/config.go`

Extract exact-copy helpers with signatures that preserve current error behavior:

```go
func SetPath(m map[string]any, path string, value any) error
func ParseConfigSetCommand(line, driverPrefix string) (path string, value any, err error)
func PrimaryModelRef(models map[string]string) (string, error)
```

Target users:

- `SetPath`: openclaw, nullclaw, microclaw, nanobot, picoclaw
- `ParseConfigSetCommand`: openclaw, nullclaw, microclaw, nanobot, picoclaw
- `PrimaryModelRef`: microclaw, nanobot, picoclaw

### A2. `internal/driver/shared/schedule.go`

Extract only the proven common validator:

```go
func IsFiveFieldCron(expr string) bool
```

Do **not** introduce a shared `GenerateCronJobsJSON(...)` abstraction yet. The current driver cron schemas are different enough that a fake common layer will hide real runtime differences rather than simplify them.

### A3. `internal/driver/shared/exec.go`

Extract the duplicated Docker exec/capture helper:

```go
func ExecInContainer(
    ctx context.Context,
    cli *client.Client,
    containerID string,
    cmd []string,
) (stdout, stderr string, exitCode int, err error)
```

Target users:

- openclaw health probe
- picoclaw health probe
- nullclaw cron post-apply
- Hermes health probe

### A4. Driver Migration Rule

Only migrate existing drivers to shared helpers when the helper is a byte-for-byte semantic match. If a driver has even slightly different behavior, leave it local.

This is intentionally conservative.

## Hermes Driver Design

### Registration Touchpoints

Hermes must be wired into the same places as the current built-in drivers:

- `internal/build/build.go` - build-time `CLAW_TYPE` validation
- `cmd/claw/compose_health.go` - runtime `claw health`
- driver tests and build tests

Do **not** claim full first-class scaffold support in this plan. That should stay separate until there is a stable default base image/reference for Hermes.

### Runtime Layout

Use two runtime roots:

1. **Hermes home**
   - host: `<runtime>/hermes-home`
   - container: `/root/.hermes`
   - env: `HERMES_HOME=/root/.hermes`

2. **Hermes workspace**
   - host: `<runtime>/workspace`
   - container: `/workspace`
   - env:
     - `MESSAGING_CWD=/workspace`
     - `TERMINAL_CWD=/workspace`

This matches Hermes's actual split:

- global/persona/config state under `HERMES_HOME`
- project context via `AGENTS.md` in the working directory

### Files Written

Under `hermes-home/`:

- `config.yaml`
- `.env`
- `cron/jobs.json`
- `SOUL.md` when a persona can be mapped cleanly

Under `workspace/`:

- `AGENTS.md` - effective contract built from `rc.AgentHostPath` plus inlined `CLAWDAPUS.md`
- `CLAWDAPUS.md` - optional separate inspection artifact for humans/debugging

### Skill Mounting

Hermes should participate in the existing compose-mounted skill system:

- `SkillDir: /root/.hermes/skills`
- `SkillLayout: directory`

That allows `cmd/claw/compose_up.go` to keep generating and mounting:

- handle skills
- service/channel surface skills
- reference includes
- operator-provided skills

The Hermes driver itself should not generate these files.

### Validate

Hermes `Validate` should:

1. Require an agent contract (`rc.AgentHostPath`) to exist.
2. Require `MODEL primary` to resolve.
3. Validate `CONFIGURE` lines with `shared.ParseConfigSetCommand(..., "hermes")`.
4. Validate all `INVOKE` expressions with `shared.IsFiveFieldCron`.
5. Require at least one supported HANDLE for v1 gateway deployments.
6. Require the relevant credentials for supported HANDLEs:
   - `discord` -> `DISCORD_BOT_TOKEN`
   - `telegram` -> `TELEGRAM_BOT_TOKEN`
   - `slack` -> `SLACK_BOT_TOKEN` and `SLACK_APP_TOKEN`
7. Require either:
   - provider credentials for direct mode, or
   - `rc.CllamaToken` when CLLAMA is enabled

Unsupported HANDLE platforms should fail clearly rather than silently pretending to support them.

### Materialize

Hermes `Materialize` should:

1. Create `<runtime>/hermes-home` and `<runtime>/workspace`.
2. Write `config.yaml` into Hermes home.
3. Write `.env` into Hermes home.
4. Build effective workspace `AGENTS.md` from:
   - `rc.AgentHostPath` as the already-composed base contract
   - generated `CLAWDAPUS.md` appended inline, because Hermes will not auto-load a standalone `CLAWDAPUS.md`
5. Set `SkillDir` / `SkillLayout` only; let compose mount the actual skills.
6. Generate `cron/jobs.json` in Hermes-native format.
7. Mount the whole Hermes home read-write.
8. Mount the workspace read-write.
9. If persona is present:
   - mount the materialized persona workspace separately for operator visibility
   - set `CLAW_PERSONA_DIR`
   - if a stable `SOUL.md` source exists in that persona workspace, copy it to `HERMES_HOME/SOUL.md`

Container defaults should match the rest of the fleet:

- `ReadOnly: true`
- `Restart: "on-failure"`
- tmpfs for at least `/tmp` and `/run`

### PostApply

Same pattern as the current drivers:

- inspect the container
- fail if it is not running

### Health Model

Distinguish Docker healthcheck from `claw health`, as the repo already does for other drivers.

#### Docker healthcheck

Prefer a Hermes-native probe if the output is stable enough to parse in a container healthcheck. If not, use a conservative process/PID-based probe.

First pass is acceptable as:

```sh
hermes gateway status >/dev/null 2>&1 || pgrep -f 'hermes gateway' >/dev/null
```

#### `Driver.HealthProbe`

- inspect container running state first
- then run either `hermes gateway status` or the same fallback via `shared.ExecInContainer`

No HTTP health endpoint is assumed.

## Hermes Config Strategy

### Model and provider wiring

Do not invent a Hermes-specific provider abstraction in Clawdapus.

Use Hermes's existing custom-endpoint path:

- set the model in `config.yaml`
- use `.env` for `OPENAI_BASE_URL` / `OPENAI_API_KEY` when routing through cllama or another OpenAI-compatible endpoint

When CLLAMA is enabled:

- set `OPENAI_BASE_URL` to the first configured cllama proxy service
- set `OPENAI_API_KEY` to `rc.CllamaToken`
- keep the model reference Hermes-compatible

The exact `config.yaml` fields should mirror Hermes's real user-facing config, not a Clawdapus-invented schema.

Hermes v1 should not emit `gateway.json` for model/provider routing. Keep the first implementation on the simpler `config.yaml` + `.env` path.

### Platform wiring

For v1:

- `discord`
  - `DISCORD_BOT_TOKEN`
  - pass through optional operator-supplied `DISCORD_ALLOWED_USERS`, `DISCORD_HOME_CHANNEL`, and related env vars if present
- `telegram`
  - `TELEGRAM_BOT_TOKEN`
  - pass through optional `TELEGRAM_ALLOWED_USERS`, `TELEGRAM_HOME_CHANNEL`
- `slack`
  - `SLACK_BOT_TOKEN`
  - `SLACK_APP_TOKEN`
  - pass through optional `SLACK_ALLOWED_USERS`, `SLACK_HOME_CHANNEL`

Important: Clawdapus does **not** currently model Hermes's per-platform allowlists in `HandleInfo`, so the driver should not claim to derive them automatically. Operator-provided env passthrough is acceptable for v1.

Hermes v1 should not emit `gateway.json` for this platform wiring either. If a feature needs `gateway.json`, that feature is out of scope for the first implementation.

### CONFIGURE semantics

Support `CONFIGURE hermes config set <path> <value>` as a YAML/config-map mutation step during materialization.

Rules:

- apply Clawdapus-generated defaults first
- apply `CONFIGURE` overrides last
- only patch stable config paths we understand

### PERSONA semantics

Clawdapus PERSONA is a mounted workspace, not just a single file. Hermes wants global `SOUL.md`.

For v1:

- preserve the mounted persona workspace pattern used elsewhere in the repo
- only translate persona to `SOUL.md` when there is a deterministic source file
- do not pretend this is full persona parity with other runtimes

## INVOKE to Hermes Cron

Keep Hermes cron generation local to `internal/driver/hermes/`.

Emit real Hermes-style job entries, for example:

```json
{
  "id": "<deterministic-id>",
  "name": "invoke-01",
  "prompt": "<INVOKE message>",
  "skills": [],
  "schedule": {
    "kind": "cron",
    "expr": "15 8 * * 1-5",
    "display": "15 8 * * 1-5"
  },
  "schedule_display": "15 8 * * 1-5",
  "repeat": {
    "times": null,
    "completed": 0
  },
  "enabled": true,
  "state": "scheduled",
  "created_at": "<iso8601>",
  "next_run_at": "<iso8601>",
  "deliver": "local",
  "origin": null
}
```

Notes:

- `deliver: "local"` is the correct conservative default for Clawdapus-driven scheduled jobs.
- Clawdapus still only emits 5-field cron even though Hermes itself supports `once` and `interval`.
- This is a driver-local translation responsibility, not shared schedule infrastructure.

## Delivery Plan

### PR 1 - narrow shared helpers

1. `shared/config.go`
2. `shared/schedule.go` with only `IsFiveFieldCron`
3. `shared/exec.go`
4. migrate exact-copy call sites only

### PR 2 - Hermes driver

1. `internal/driver/hermes/driver.go`
2. `internal/driver/hermes/config.go`
3. `internal/driver/hermes/jobs.go`
4. driver tests
5. `internal/build/build.go` registration import
6. `cmd/claw/compose_health.go` registration import
7. build/lookup test coverage for `CLAW_TYPE hermes`

Optional follow-up after Hermes lands cleanly:

- `internal/driver/testutil/`
- scaffold support
- broader driver cleanup

## Testing Strategy

- unit tests for `shared.SetPath`, `shared.ParseConfigSetCommand`, `shared.PrimaryModelRef`, `shared.IsFiveFieldCron`, `shared.ExecInContainer`
- Hermes config generation tests
- Hermes `.env` generation tests
- Hermes effective `AGENTS.md` generation tests
- Hermes `jobs.json` generation tests
- Hermes validation tests:
  - missing contract
  - missing primary model
  - unsupported HANDLE platform
  - missing Discord/Telegram/Slack credentials
  - invalid cron
  - CLLAMA enabled without token
- Hermes materialize tests asserting:
  - `HERMES_HOME`
  - `MESSAGING_CWD`
  - `TERMINAL_CWD`
  - home/workspace mounts
  - `SkillDir=/root/.hermes/skills`
  - `SkillLayout=directory`
- build-time test that `CLAW_TYPE hermes` is accepted once registration is wired
- full regression gate on existing driver tests after PR 1

## Decision Summary

Hermes fits the larger Clawdapus picture as an additive driver, not as a reason to redesign the driver framework.

The right implementation posture is:

- narrow shared extraction
- Hermes-specific runtime translation kept local
- explicit acknowledgement of current parity gaps
- clean alignment with contract composition, skill mounting, cllama, and Compose lifecycle authority
