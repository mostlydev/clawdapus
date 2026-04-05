# Wake Adapters Discovery

Slice 0 for `2026-04-04-conditional-invoke-scheduling.md`.

## Purpose

Establish the concrete wake path per driver before the main plan's Slice 2
freezes the manifest format. "Wake" = cause the runner to execute a specific
prompt/turn on demand via an external trigger.

## Findings (validated 2026-04-04 against live container images)

**All five target drivers have viable wake paths.** Two patterns emerged:

- **Pattern B (register-then-trigger-by-id)** — compile-time registration in
  the runner's native cron store, scheduler triggers via `cron run <id>`.
  Works for OpenClaw, Hermes, NanoBot.
- **Pattern A (ad-hoc message)** — scheduler calls `<runner> agent -m "..."`
  with no pre-registration. Works for PicoClaw, NullClaw.

| Driver | Adapter | Wake command | Enable/disable | Run history |
|---|---|---|---|---|
| **OpenClaw** | Pattern B | `openclaw cron run <id>` | `openclaw cron enable/disable <id>` | `openclaw cron runs` |
| **Hermes** | Pattern B | `hermes cron run <job_id>` | `hermes cron pause/resume <job_id>` | (via `cron status`) |
| **NanoBot** | Pattern B | `nanobot cron run <id>` | `nanobot cron enable` | — |
| **PicoClaw** | Pattern A | `picoclaw agent -m "<msg>"` | `picoclaw cron enable/disable` | — |
| **NullClaw** | Pattern A | `nullclaw agent -m "<msg>"` | — | — |

### Evidence per driver

**OpenClaw** (`openclaw:latest` — `OpenClaw 2026.3.24`):

```
$ openclaw --help
Commands:
  agent                Run one agent turn via the Gateway
  cron *               Manage cron jobs via the Gateway scheduler
    ...
    run         Run a cron job now (debug)
    runs        Show cron run history (JSONL-backed)
    enable      Enable a cron job
    disable     Disable a cron job
    list        List cron jobs
```

`openclaw agent --help` shows it accepts `--message`, `--agent`, `--channel`,
`--deliver`, `--reply-channel`, `--reply-to`, `--session-id`, `--to`, etc. —
full delivery routing control. So OpenClaw supports **both** Pattern A and
Pattern B cleanly. We recommend Pattern B because it preserves the
pre-compiled delivery config attached to each job in jobs.json.

**Hermes** (`hermes:latest` — hermes-agent 0.5.1):

```
$ hermes cron --help
positional arguments:
  {list,create,add,edit,pause,resume,run,remove,rm,delete,status,tick}
    run        Run a job on the next scheduler tick
    pause      Pause a scheduled job
    resume     Resume a paused job
    tick       Run due jobs once and exit
```

No top-level `hermes agent` subcommand. Pattern A not available. Pattern B
via `hermes cron run <job_id>` is the wake path. Note: "Run a job on the
next scheduler tick" suggests fire is queued, not synchronous — acceptable
at cron-minute granularity, but worth verifying for tighter SLAs.

**NanoBot** (`nanobot:latest`):

```
$ nanobot --help
Commands:
  agent     Interact with the agent directly.
  cron      Manage scheduled tasks
    run     Manually run a job.
    enable  Enable or disable a job.
```

Both Pattern A (`nanobot agent -m "..."`) and Pattern B (`nanobot cron run
<id>`) available. Recommend Pattern B for symmetry with OpenClaw.

**PicoClaw** (`picoclaw:latest` — from `docker.io/sipeed/picoclaw:latest`):

```
$ picoclaw cron --help
Available Commands:
  add         Add a new scheduled job
  disable     Disable a job
  enable      Enable a job
  list        List all scheduled jobs
  remove      Remove a job by ID
```

**No `cron run` subcommand.** PicoClaw cannot trigger a registered job by
ID externally. However `picoclaw agent -m "<msg>"` works — Pattern A is the
only viable adapter. Downside: PicoClaw invocations lose access to
pre-compiled delivery routing and must pass `to:` routing separately (if
PicoClaw's `agent` subcommand accepts it — needs further flag inspection).

**NullClaw** (in-repo stub):

Stub echoes `nullclaw agent -m "..."`. No real agent runtime; used only for
contract validation in `TestSpikeRollCall`. Pattern A is the only path and
is already used by the existing driver tests.

## Recommended Adapter Shape

```go
type WakeAdapter interface {
    Wake(ctx context.Context, req WakeRequest) (WakeResult, error)
}

type WakeRequest struct {
    ContainerID   string  // docker container to exec into
    NativeJobID   string  // populated for Pattern B
    Message       string  // populated for Pattern A
    To            string  // delivery target (Pattern A only; Pattern B uses pre-registered delivery)
    CorrelationID string
}

type WakeResult struct {
    ExitCode   int
    Stdout     string
    Stderr     string
    DurationMs int64
}
```

Per-driver adapter implementations call `docker exec` with the appropriate
CLI and flags.

## Compile-Time Contract (Pattern B drivers)

At `claw up`, for OpenClaw / Hermes / NanoBot:

1. Generate the deterministic job ID (already done today).
2. Write the native jobs.json **with `enabled: false`** so the runner's
   scheduler does not fire the job autonomously.
3. Emit the manifest entry with `wake.adapter = <driver>-cron-run` and
   `wake.job_id = <deterministic-id>`.

At fire time:

1. Scheduler ticks, evaluates `when:` gate + pause state.
2. If green-light, call `docker exec <container> <runner> cron run <id>`.
3. Record fire event with exit code and correlation ID.

Open verification: confirm that `enabled: false` suppresses native
auto-fire while still allowing manual `cron run <id>`. All three runners
have both flags, so this is expected to hold, but must be tested during
Slice 2.

## Compile-Time Contract (Pattern A drivers)

At `claw up`, for PicoClaw / NullClaw:

1. Skip runner-native cron registration for pod-origin invocations entirely.
2. Emit the manifest entry with `wake.adapter = <driver>-agent-exec` and
   `wake.message = <invocation.message>`, `wake.to = <invocation.to>`.

At fire time:

1. Scheduler evaluates gate + pause state.
2. If green-light, call
   `docker exec <container> <runner> agent -m "<message>" [routing flags]`.
3. Record fire event.

## Fire History

OpenClaw has `openclaw cron runs` (JSONL-backed run history) natively. This
is candy — the scheduler can optionally cross-reference its own fire events
against OpenClaw's record for observability, but we should NOT rely on
runner-native history as the source of truth. Clawdapus's scheduler records
are authoritative; runner history is corroborative.

## Container Exec Security

claw-api already holds the Docker socket
(`internal/pod/compose_emit.go:363`) and already runs container
restart/stop (`cmd/claw-api/handler.go:460`). The wake adapter surface
expands this to `exec`.

Constraints:

1. **Allowlist**: the scheduler may only exec into containers that appear
   in the schedule manifest. Container IDs are resolved at startup from
   compose service names.
2. **Command template**: each adapter has a fixed command template;
   user-controllable fields (message, to, job_id) are passed as argument
   arrays (never interpolated into a shell string). The `docker exec` API
   naturally takes an argv, so no shell is involved.
3. **Principal scope**: the new `schedule.control` verb authorizes
   fire-now and pause/resume. Scheduler's own fires run under an internal
   principal bound at startup, not an API caller's principal.
4. **Timeout**: each exec has a bounded timeout (default 30s) and is
   cancelable.

## Hermes Timing Caveat

`hermes cron run <job_id>` — "Run a job on the next scheduler tick". This
is queued, not synchronous. Hermes's scheduler must be ticking at a cadence
≤ our desired granularity. Worth validating: does Hermes tick faster than
once a minute? If not, we either accept ≤1-minute wake latency or use
`hermes cron tick` (runs due jobs once) combined with temporarily
un-pausing our job — more complex.

For cron-minute granularity (the Tiverton target), ≤1-minute latency is
fine. Flag this for Slice 2 verification.

## v1 Scope

All five drivers are in scope for v1. Priority order:

1. **OpenClaw** — Tiverton's runner. Pattern B. Clean.
2. **Hermes** — Pattern B. Clean.
3. **NanoBot** — Pattern B. Clean.
4. **NullClaw** — Pattern A. Stub only, kept for spike-test coverage.
5. **PicoClaw** — Pattern A. Acceptable; ships without pre-registered
   delivery routing.

NanoClaw and MicroClaw remain unsupported (they already drop invocations).

## Blocking Items for Slice 2

Reduced to verification, not discovery:

1. **Confirm `enabled: false` + `cron run <id>` interaction** for each
   Pattern B driver. Expected: native fires suppressed, manual trigger
   works.
2. **Confirm Hermes tick cadence** to bound wake latency.
3. **Confirm PicoClaw `agent` flag surface** for `to:` routing (and
   document limits if routing is not flag-accessible).
4. **Design the docker-exec allowlist** implementation in claw-api.

## Status

**Discovery complete.** Slice 0 can be marked done. All blocking unknowns
from the earlier draft of this doc are resolved. Remaining items are
narrow verifications executable during Slice 2 implementation.
