# Conditional Invoke Scheduling Plan

Tracking issue: mostlydev/clawdapus#107

## Status

Proposed. Supersedes ADR-006 ("INVOKE Scheduling Mechanism") with a new ADR-022
("Scheduler Authority and Runtime Control").

## Goal

Pod operators should be able to express **conditions** on `x-claw.invoke` entries
that are evaluated **before** the agent wakes, and should be able to **pause,
resume, skip, and manually fire** scheduled jobs at runtime without rebuilding
images or regenerating compose files. Fleet-level visibility into scheduled jobs
(next fire, last fire, skip reasons) should live in one place.

Concretely: on Tiverton today, ~50 scheduled invocations across 8 agents burn
tokens on weekends and market holidays because cron can approximate weekday
hours but cannot express "US equities trading day." This plan kills those wasted
turns and gives the operator a runtime control plane.

## Current State

- `x-claw.invoke` entries are parsed into `pod.InvokeEntry` (`internal/pod/
  types.go:30`) with only `Schedule`, `Message`, `Name`, `To`.
- At `claw up`, each driver translates invocations into its runner-native job
  store:
  - **OpenClaw** — writes `jobs.json` into `/app/state/cron/`
    (`internal/driver/openclaw/jobs.go`).
  - **Hermes** — writes `jobs.json` into `hermes-home/`
    (`internal/driver/hermes/jobs.go`).
  - **PicoClaw** — writes its own cron store via
    `generateCronJobsJSON` (`internal/driver/picoclaw/driver.go:362`).
  - **NanoBot** — writes `cron/jobs.json` into `nanobot-home/`
    (`internal/driver/nanobot/driver.go:117`).
  - **NullClaw** — registers cron entries via `nullclaw cron add` in PostApply
    using `docker exec` (`internal/driver/nullclaw/driver.go:201`). The
    invocation message is baked into a shell command at image build time.
  - **NanoClaw / MicroClaw** — warn and drop invocations.
- **Image-level and pod-level invocations are merged** into one
  `rc.Invocations` slice at `cmd/claw/compose_up.go:303`, with no origin tag.
  `driver.Invocation` at `internal/driver/types.go:18` is typed as "image
  labels **or** pod x-claw.invoke."
- Runner's internal scheduler owns the timer and fires the job.
- No runtime control surface: pausing a job requires editing `claw-pod.yml` and
  re-running `claw up`.
- No uniform fire audit: each runner tracks its own execution state in its own
  format; there is no cross-runner view.
- No condition expression is available today beyond what cron itself encodes.

This is the model ADR-006 committed to. It was correct for that era — Clawdapus
was a compiler, not a governance layer.

## Design Decision

**Clawdapus owns the scheduler.** `claw up` compiles a per-pod schedule
manifest; `claw-api` loads the manifest, evaluates cron + conditions, and wakes
agents through per-driver **wake adapters** at fire time. For pod-origin
invocations, supported drivers either keep a **disabled native registration**
(Pattern B) or skip native registration entirely (Pattern A). Image-origin
invocations continue to use runner-native cron unchanged.

This supersedes ADR-006. The precedent is **ADR-019** (model policy authority):
runners are untrusted for governance decisions. Scheduling is governance.
Market-hours gating, pause-for-incident, skip-on-policy-violation — these are
operator decisions, not runtime decisions.

Hosting: the scheduler lives **inside `claw-api`**, not as a separate sidecar.
Rationale: it reuses claw-api's principal/auth machinery, shares a state store
with the write-plane endpoints, and keeps pod service count down. It's a
goroutine, not a service.

**claw-api injection rule change.** Today, claw-api is only auto-injected when
`x-claw.master` is set (`cmd/claw/compose_up.go:128`). This plan expands that:
claw-api is auto-injected when `x-claw.master` is set **or** any service has
pod-level `x-claw.invoke` entries. Pods that use `invoke:` without a master
will gain a claw-api service after migration. The injected claw-api in this
mode runs with a scheduler-only principal scope if no master is configured.
For v1, that scheduler principal is **host/operator-facing** (written to
`principals.json`) rather than auto-injected into arbitrary services. Pod-
internal token projection can be added later alongside explicit schedule CLI
or self-surface work.

## Origin Split

`driver.Invocation` gains an `Origin` field: `InvocationOrigin` enum with
values `OriginImage` and `OriginPod`. The merge at
`cmd/claw/compose_up.go:303` tags each entry:

- Image-label invocations from `info.Invocations` → `OriginImage`.
- Pod-level invocations from `svc.Claw.Invoke` → `OriginPod`.

Downstream:

- **Manifest emission** includes only `OriginPod` invocations.
- **Runner-native cron emission** for `OriginPod` becomes pattern-specific:
  - **Pattern B drivers** keep native registrations for pod-origin entries,
    but write them disabled (`enabled: false` or equivalent). The native
    store is a stable ID + delivery-config registry, not the timing authority.
  - **Pattern A drivers** omit pod-origin entries from native cron entirely.
  - **OriginImage** entries continue to flow through runner-native cron
    unchanged.
- **`when:` is rejected at parse time on image-origin invocations** — image
  labels don't carry the field, and we don't want to teach the label schema
  about calendars.

This keeps the Clawfile `INVOKE` path untouched and avoids a migration for
existing image-bundled scheduled jobs.

## Non-Goals (v1)

- **Dynamic predicates** — "only if positions are open," "only if service X
  healthy," "only if no run in last N minutes." These require the scheduler to
  evaluate runtime state at each tick. The schema will be forward-compatible,
  but v1 only evaluates deterministic calendar conditions.
- **Arbitrary shell/HTTP predicates.** Deferred with dynamic predicates.
- **Clawfile `INVOKE` label semantics changes.** Image-label invocations
  (`claw.invoke.N`, `internal/clawfile/emit.go:92` and
  `internal/inspect/inspect.go:185`) continue to be honored, but they are
  marked with `origin: image` and routed through **runner-native cron**
  exactly as today. Only `origin: pod` invocations flow through the external
  scheduler and may carry `when:`. See "Origin split" below.
- **NanoClaw / MicroClaw** coverage. They currently drop invocations; they
  stay unsupported until they grow scheduling at all.
- **Cross-pod scheduling** (scheduler coordinating across multiple pods).
  Single-pod scope only.

## Schema

### `when:` on `InvokeEntry`

```yaml
x-claw:
  invoke:
    - schedule: "15 8 * * 1-5"
      name: "Pre-market synthesis"
      message: "Pre-market synthesis. Write report to /mnt/clawd-shared/..."
      to: trading-floor
      when:
        calendar: us-equities
        session: regular     # regular | pre-market | after-hours | any-open
```

**Tagged union.** Exactly one discriminator is allowed in v1:

- `calendar: <name>` — evaluates against a built-in calendar. v2 may add:
  - `predicate: service-healthy` (check a pod service)
  - `predicate: positions-open` (check a feed-declared boolean)
  - `predicate: custom` (HTTP callback)

Unknown discriminators are a parse-time hard error in v1. This gives us a clean
"not yet supported" surface instead of silent success.

**Session filter** is calendar-specific. `regular` means "core session hours
only." `any-open` means "pre-market, regular, or after-hours." If `session:` is
omitted, the default is `any-open` (matches common trader intuition).

**When `when:` is absent**, behavior is identical to today — cron-only firing,
no gating.

### Built-in calendars (v1)

Embedded in the `claw` binary as Go data (`internal/schedule/calendars/`):

- `us-equities` — NYSE/NASDAQ. Holidays + early closes through 2027.
- `us-futures` — CME. Nearly-24x5 with daily halts and holiday variants.
- `crypto-24-7` — always open. (Mostly useful as a no-op sentinel.)
- `london-equities` — LSE.
- `eu-equities` — Xetra/Euronext consolidated.

Calendar data lives in `internal/schedule/calendars/*.json`, loaded via
`//go:embed`. Updating calendars is a Clawdapus release, not a pod change.

## Architecture

### Compile-time: schedule manifest emission

At `claw up`, in addition to today's artifacts, the compiler writes a **pod
schedule manifest** at `.claw-runtime/schedule.json`:

```json
{
  "version": 1,
  "pod": "trading-desk",
  "invocations": [
    {
      "id": "tiverton-premarket-synthesis",
      "service": "tiverton",
      "agent_id": "tiverton",
      "schedule": "15 8 * * 1-5",
      "timezone": "America/New_York",
      "message": "Pre-market synthesis...",
      "name": "Pre-market synthesis",
      "to": "trading-floor",
      "when": {
        "calendar": "us-equities",
        "session": "regular"
      },
      "wake": {
        "adapter": "openclaw-exec",
        "target": "<container-name-or-id>",
        "command": ["openclaw", "<run-now-cli-TBD-by-Slice-0>"]
      }
    }
  ]
}
```

- `id` is deterministic: `sha256(service | schedule | message)[:12]` or similar.
  Same pod compilation produces the same IDs — required for stable state across
  restarts.
- `wake` encodes the adapter the scheduler will use to wake this specific
  invocation, resolved at compile time from the driver.
- **For supported drivers, native jobs.json emission is no longer performed.**
  The driver's `Materialize` is called with an opt-out flag so it skips its
  own cron emission path. For unsupported drivers (NanoClaw etc.), emission
  still happens and the manifest omits those invocations — runner-native cron
  remains in place as a fallback during migration.
- The manifest is bind-mounted into the `claw-api` container.

**State persistence location.** The manifest itself (`schedule.json`) lives
under `.claw-runtime/` because it is a compile artifact and is regenerated on
every `claw up`. **All mutable scheduler state** — pause flags, skip-next
queue, degraded state, and scheduler-owned invocation state — lives under
`.claw-governance/`, which
`claw up` explicitly preserves across resets
(`cmd/claw/compose_up.go:118`). Putting state under `.claw-runtime/` would
wipe it on every recompile (`resetRuntimeDir` at
`cmd/claw/compose_up.go:115`). The write-plane precedent at
`docs/plans/2026-03-22-claw-api-write-plane.md:44` already established
`.claw-governance/` as the persistent governance-state location.

### Runtime: scheduler goroutine in `claw-api`

On startup, `claw-api` loads `.claw-runtime/schedule.json`. A scheduler
goroutine:

1. **Tick once per minute** (cron resolution). For each invocation, compute the
   next fire time using `robfig/cron` + the declared timezone.
2. **At fire time**, evaluate `when:`:
   - Load the calendar (already in memory).
   - Compute session state at the fire instant.
   - If the session condition fails → update the invocation state with the
     last outcome (`calendar-closed`, `calendar-holiday`, `calendar-early-close`)
     and do not wake.
   - If the invocation is **paused** (see below) → update the invocation state
     with the last outcome (`paused-by-operator`) and do not wake.
3. **Otherwise**, call the wake adapter. Update the invocation state with the
   adapter's result (`wake-ok`, `wake-failed`, or `wake-timeout`).
4. **Backoff on repeated wake failures**: if 3 consecutive fires fail, the
   invocation enters `degraded` state and fires at 10% rate until recovery.
   Surfaced in current state and clawdash.

**State is persisted** to `.claw-governance/schedule-state.json`: a
machine-owned JSON document keyed by invocation ID. v1 stores current
config/state only: pause state, skip-next, degraded flags, consecutive
failures, last evaluation timestamps, last wake outcome, and next-fire
metadata. Writes are atomic temp-file + rename. No SQL schema, migrations,
or long-tail event history are required for v1. State survives claw-api
restarts.

**No session-history correlation in v1.** The scheduler's JSON state is
authoritative for current scheduling state only. Precise linkage from a
fire to cllama session-history is deferred until there is a real need for
retained audit history.

### Wake adapters

One adapter per supported driver. Contract:

```go
type WakeAdapter interface {
    Wake(ctx context.Context, target string, payload WakePayload) (WakeResult, error)
}

type WakePayload struct {
    Message        string
    To             string
    InvocationName string
}
```

**Slice 0 discovery is complete.** Full findings in
`docs/plans/2026-04-04-wake-adapters-discovery.md`, validated 2026-04-04
against live container images. Summary:

| Driver | Adapter | Wake command |
|---|---|---|
| **OpenClaw** | Pattern B | `openclaw cron run <id>` |
| **Hermes** | Pattern B | `hermes cron run <job_id>` |
| **NanoBot** | Pattern B | `nanobot cron run <id>` |
| **PicoClaw** | Pattern A | `picoclaw agent -m "<msg>"` |
| **NullClaw** | Pattern A | `nullclaw agent -m "<msg>"` |

**Two adapter patterns**, selected per driver based on CLI surface:

- **Pattern B (register-then-trigger-by-id)** — OpenClaw, Hermes, NanoBot.
  Compile-time writes native jobs.json with `enabled: false`; scheduler
  triggers via `cron run <id>`. Preserves pre-compiled delivery routing.
- **Pattern A (ad-hoc message)** — PicoClaw, NullClaw. No pre-registration;
  scheduler calls `<runner> agent -m "<msg>"` at fire time. PicoClaw lacks
  `cron run`; NullClaw is a stub.

Critically, **OpenClaw supports Pattern B cleanly** — `openclaw cron run`,
`cron disable/enable`, and `cron runs` (JSONL run history) all exist in
`openclaw:latest` (v2026.3.24). Tiverton's use case is fully addressable.

The adapter shape is **docker exec + CLI** everywhere. claw-api already
holds the Docker socket (`internal/pod/compose_emit.go:363`) and runs
container restart/stop (`cmd/claw-api/handler.go:460`); exec is an
expansion of that surface. The wake adapter security model uses a manifest-
derived container allowlist, `docker exec` argv (no shell interpolation),
and bounded per-call timeouts.

**v1 scope**: all five drivers (OpenClaw, Hermes, NanoBot, PicoClaw,
NullClaw). NanoClaw and MicroClaw remain unsupported (they already drop
invocations).

Adapter selection happens at compile time based on `rc.ClawType`. Unsupported
drivers skip manifest emission for their invocations and fall back to
runner-native cron (no conditions available, but existing behavior
preserved).

### claw-api control endpoints

All endpoints require a principal with the `schedule.*` verbs. New verbs:

- `schedule.read` — list, get
- `schedule.control` — pause, resume, skip-next, fire-now

Endpoints:

```
GET    /schedule                       → list all invocations + state
GET    /schedule/:id                   → single invocation detail
POST   /schedule/:id/pause             → body: {until?: RFC3339, reason?: str}
POST   /schedule/:id/resume            → clear pause
POST   /schedule/:id/skip-next         → skip the next scheduled fire
POST   /schedule/:id/fire              → fire now (respecting or bypassing `when:` per flag)
```

Pause semantics:
- `pause` without `until` = indefinite. Requires explicit `resume`.
- `pause` with `until` = auto-resume at the given time.
- `skip-next` queues one skip and clears itself after the next fire-or-skip.

`fire` endpoint flags:
- `bypass_when: true` — fire even if the calendar gate is closed. Reflected in
  the invocation's last-status metadata as `manual-fire-bypass`.
- `bypass_pause: true` — fire even if paused. Reflected in the invocation's
  last-status metadata as `manual-fire-paused`.

### clawdash schedule page

New page at `/schedule` in clawdash:

- Table: service, name, next fire, last fire, last status, last detail,
  paused?, degraded?.
- Row detail drawer: full schedule, `when:` gate, current pause/degraded state,
  and last wake outcome.
- Row actions: pause, resume, skip-next, fire-now.
- Top-of-page summary: total scheduled, N paused, M degraded.

SSE stream from `/schedule/events` for live state updates.

## Implementation Slices

Ordered; each slice should be mergeable on its own.

### Slice 0 — Wake-path discovery spike (COMPLETE)

Findings captured in
`docs/plans/2026-04-04-wake-adapters-discovery.md`. All five target drivers
have viable wake paths; adapters and command templates frozen:

- OpenClaw / Hermes / NanoBot → Pattern B (`<runner> cron run <id>`)
- PicoClaw / NullClaw → Pattern A (`<runner> agent -m "<msg>"`)

Remaining items (now **narrow verification**, not discovery, and executable
during Slice 2 implementation):

- Confirm `enabled: false` + `cron run <id>` interaction per driver
  (expected: native fires suppressed, manual trigger works).
- Confirm Hermes tick cadence (affects wake latency).
- Confirm PicoClaw `agent -m` flag surface for `to:` routing.
- Design the docker-exec allowlist implementation in claw-api.

### Slice 1 — Schema + calendar primitives + origin split

- Add `When` field to `pod.InvokeEntry` and `driver.Invocation`.
- Add `Origin` field to `driver.Invocation` with `OriginImage` / `OriginPod`
  values. Update the merge at `cmd/claw/compose_up.go:303` to tag both
  sources.
- Parse `when:` in `internal/pod/parser.go` with v1 discriminator validation.
  Reject `when:` at parse time if attached to an image-origin invocation
  (future-proofing).
- Add `internal/schedule/` package:
  - `calendars/*.json` embedded data (us-equities, us-futures, crypto-24-7,
    london-equities, eu-equities) through 2027.
  - `Calendar.SessionAt(t time.Time) SessionState` function.
- Unit tests: calendar edge cases (holiday, early close, DST transitions,
  timezone boundary).

### Slice 2 — Schedule manifest emission + claw-api injection

- Generate `.claw-runtime/schedule.json` in `cmd/claw/compose_up.go`. Only
  `OriginPod` invocations are included.
- **Per-pattern native-cron emission**:
  - **Pattern B drivers (OpenClaw, Hermes, NanoBot)** continue to write
    native jobs.json for `OriginPod` entries, but with **`enabled: false`**
    (or the runner's equivalent disabled flag). The native store is now
    used as a stable ID registry + delivery-config store, not a trigger.
  - **Pattern A drivers (PicoClaw, NullClaw)** skip native cron emission
    entirely for `OriginPod` entries. There is no pre-registered job.
  - `OriginImage` entries flow through runner-native cron unchanged, with
    `enabled: true`, for all drivers.
- Deterministic invocation IDs (`sha256(service | schedule | message)[:12]`).
  These IDs double as the `NativeJobID` for Pattern B registrations.
- Wake-adapter resolution per driver (identities frozen by Slice 0).
- **Expand claw-api auto-injection**: inject claw-api when
  `x-claw.master` is set **or** any service has pod-level `x-claw.invoke`
  entries. Bind-mount the manifest into the claw-api container. Ensure the
  governance dir (`.claw-governance/`) is mounted.
- For unsupported drivers (NanoClaw, MicroClaw), fall back to today's
  behavior and emit a warning when `when:` is declared ("calendar gating
  not supported for driver X; invocation ignored").

### Slice 2.5 — JSON state store shape

Before Slice 3 lands, freeze the file format for
`.claw-governance/schedule-state.json`:

- Top-level `version` plus an `invocations` map keyed by invocation ID.
- Per invocation: pause state (`paused`, `paused_until`, `pause_reason`),
  `skip_next`, `degraded`, `consecutive_failures`, `last_evaluated_at`,
  `last_attempted_at`, `last_fired_at`, `last_skipped_at`, `last_status`,
  `last_detail`.
- Optional top-level pod metadata if useful for debugging (`pod`, `updated_at`).
- Atomic write strategy (`.tmp` + rename) and bounded in-memory mutation path.

Explicit non-goals for v1:

- No SQLite.
- No append-only event log.
- No per-fire/session-history correlation.

Output: a short state-shape note or tests that lock the JSON structure so
Slice 3 can implement against it without inventing schema mid-stream.

### Slice 3 — Scheduler goroutine in claw-api

- Load manifest at startup.
- Per-minute tick, cron evaluation via `robfig/cron`.
- Calendar gate evaluation.
- Fire adapter dispatch (stub adapters initially).
- State persistence to JSON (`.claw-governance/schedule-state.json`).
- Current-state updates for last outcome / pause / degraded / skip-next.
- Expose `/schedule` read endpoints.

### Slice 4 — Wake adapters

All adapters are `docker exec + CLI argv` shaped. Five implementations:

- **`openclaw-exec`** (Pattern B): `openclaw cron run <id>`. Verify
  `enabled: false` suppresses native auto-fire.
- **`hermes-exec`** (Pattern B): `hermes cron run <job_id>`. Verify tick
  cadence for wake latency.
- **`nanobot-exec`** (Pattern B): `nanobot cron run <id>`.
- **`picoclaw-exec`** (Pattern A): `picoclaw agent -m "<msg>"` plus any
  routing flags confirmed by the `to:` verification below.
- **`nullclaw-exec`** (Pattern A): `nullclaw agent -m "<msg>"`. Stub only.

Other Slice 4 work:

- Shared docker-exec allowlist: containers allowed are derived from the
  schedule manifest at scheduler startup.
- Adapter contract tests using stub runners.
- **PicoClaw `to:` routing verification gate**: before shipping the
  picoclaw-exec adapter, confirm that `picoclaw agent -m "<msg>"` can
  reach a specific Discord channel / delivery target. If `to:` routing
  isn't available on the `agent` subcommand, either document the
  limitation (picoclaw pod-origin invocations ignore `to:`) or defer
  PicoClaw to Stage 2. This is a gate, not a footnote.
- Update `TestSpikeRollCall` to verify scheduled fires reach agents
  through the scheduler, not runner-native cron.

### Slice 5 — Control plane + dashboard

- `/schedule/:id/{pause,resume,skip-next,fire}` endpoints.
- Principal scopes: `schedule.read`, `schedule.control`.
- `claw api schedule {list,get,pause,resume,fire}` CLI subcommands.
- Clawdash schedule page + SSE stream.

### Slice 6 — Migration + docs

- Migration note: existing pods get automatic manifest emission on next
  `claw up`; supported drivers flip to the Pattern A / Pattern B emission
  model; state is fresh (`schedule-state.json` starts empty).
- `claw inspect` surfaces scheduled invocations from the manifest.
- CLAWDAPUS.md reference update.
- ADR-022 drafted and merged alongside Slice 3.
- ADR-006 marked superseded.

## Migration Path

Existing pods (Tiverton, trading-desk, master-claw, rollcall) have `invoke:`
entries today without `when:`. On first `claw up` after this lands:

1. Manifest is emitted for all supported drivers.
2. **Pattern B drivers** (OpenClaw, Hermes, NanoBot): native jobs.json is
   still written, but pod-origin jobs now carry `enabled: false`. The
   runner's scheduler stops auto-firing those jobs. Pre-compiled delivery
   routing remains attached to each native job and is used by the
   scheduler's `cron run <id>` wake.
3. **Pattern A drivers** (PicoClaw, NullClaw): native cron registration is
   skipped for pod-origin invocations. The scheduler calls `agent -m`
   directly.
4. Scheduler state starts empty in claw-api (`schedule-state.json`).
5. Operator can add `when:` fields incrementally.

**Risk**: at the moment the migration flips on, scheduled jobs migrate from
runner-native timers to the scheduler's timer. Expected impact: within-minute
timing differences. No operator action required beyond a `claw up`.

**Rollback**: re-exposing runner-native emission is a single flag on the
compiler. The manifest is additive until the driver opt-out lands; the
opt-out can be gated by a pod-level flag (`x-claw.scheduler: external` vs
`runner`) to let operators opt in incrementally if the default flip is too
aggressive.

## ADR-022 Skeleton

**Title**: Scheduler Authority and Runtime Control

**Decision**: Clawdapus owns the scheduler. Runners are woken through driver
adapters. Pod-origin scheduling state is infrastructure-owned, while
runner-native job stores remain as disabled registries for Pattern B drivers.

**Rationale**:
- Scheduling is governance policy. Per ADR-019, runners are untrusted for
  governance decisions.
- Uniform control surface (pause/resume/fire) is impossible across 7 runner
  formats; trivially expressible with one scheduler.
- Fleet-level scheduling state ("what is scheduled, what is paused, what last
  happened") wants a single source of truth.

**Supersedes**: ADR-006. ADR-006's pragmatic argument (no extra runtime
processes) no longer applies — Clawdapus pods already include claw-api,
claw-wall, and cllama as infrastructure services.

**Consequences**:
- + Uniform conditions, pauses, audit, dashboard.
- + NanoClaw / MicroClaw can eventually gain scheduling for free once they
     grow wake adapters.
- − Scheduler failure stops all scheduled fires (mitigated by claw-api restart
     policy + health alerts).
- − Per-driver wake adapters are maintenance surface.
- − Slightly worse timing precision (external tick + HTTP hop vs in-runner
     cron). Acceptable at cron-minute granularity.

## Open Questions

1. **Wake-path details** — resolved by Slice 0. Blocking for Slice 2.
2. **Timezone declaration.** Should `schedule:` carry an implicit timezone
   (from the calendar when `when:` is present, falling back to UTC), or should
   `timezone:` be a separate field on the invocation? Leaning: implicit from
   calendar, explicit override allowed.
3. **Backfill on scheduler startup.** If claw-api was down for an hour and 5
   fires were missed, do we fire-once-on-startup, fire-all-missed, or skip
   with a skip-event per miss? Leaning: skip-with-event. Operator can
   `fire-now` manually if they want the backlog.
4. **Per-agent scheduler quota.** Should an operator be able to cap "max fires
   per agent per hour" as a runaway guard? Probably Stage 2.
5. **claw-api principal mode without a master.** When claw-api is injected
   solely because `invoke:` is present (no master), what principal scopes
   does it expose? Leaning: only `schedule.*` verbs plus read-only health;
   no fleet governance surface until a master is declared.
