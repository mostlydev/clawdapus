# Task-home profile for per-run workers (2026-11-09)

**Status:** Proposed, issue #394. Decision record: [ADR-027](../decisions/027-task-home-per-run-profile.md).
Nothing here is built. Security review and failing acceptance cases come before implementation.

This document extends [the per-run worker design](2026-10-20-per-run-workers.md). Read that first;
only the differences are described here.

## Shape in one paragraph

A pod service with `x-claw.lifecycle: per-run` may declare `x-claw.run.home`. `claw up` then
compiles a task-home template. A launch for that template names a home by an opaque ID. The
supervisor resolves the ID to a private host directory, reserves it, and renders a run project with
two services: the worker, with the home mounted read-write and compiled content read-only, and a
forwarder that is the worker's only route to cllama. The worker runs one turn of its harness,
resuming the session stored in the home when there is one, and reports a bounded result frame on
stdout. On teardown the container is removed, the run's authority is revoked, and the home is
released for the next run.

## Pod schema

```yaml
services:
  worker:
    image: my-hermes-worker:latest
    x-claw:
      lifecycle: per-run
      agent: ./AGENTS.md
      cllama: passthrough
      run:
        max-runtime: 30m
        home: {}                 # opts in; an empty map takes the defaults
        content:
          - source: ./reference/handbook
            target: /reference/handbook
```

- `run.home` opts the service into the task-home profile. Its one field, `mount`, defaults to
  `/home/task`.
- `run.content` lists read-only mounts. `source` is a path relative to the pod file, resolved and
  checked at `claw up`: it must exist, stay inside the pod directory after symlink resolution, and
  be a directory or a regular file. `target` is an absolute container path outside the home mount.
- `run.content` without `run.home` is an error.
- A service without `run.home` is compiled exactly as today.
- `skills`, `persona`, `handles`, `invoke`, `memory`, `surfaces` and `count` stay rejected.

## Launch spec

The closed field set gains one field:

| Field | Type | Rule |
|---|---|---|
| `home` | string | Required for a task-home template, refused (`spec_invalid`) for a stateless one. 1 to 128 bytes of `A-Z a-z 0-9 . _ -`, not starting with `.`. |

The field is part of the canonical spec and so of the digest. A supervisor built before this change
refuses the unknown field, which is the wanted failure.

The home is scoped by the run key's installation: two installations that use the same home ID get
different homes.

## Home storage

```
<pod>/.claw-homes/                         0700, never rotated by claw up
  <home-hash>/                             0700, owned by the operator
    meta.json                              0600, supervisor-owned: installation, home ID, created, last run
    home/                                  0777, the directory mounted into the worker
```

- `home-hash` is the first 32 hex characters of `sha256(installation NUL home)`. The caller's ID
  never appears in a path.
- `.claw-homes` goes through `runs.EnsurePrivateDir`, like `.claw-runs`.
- The inner `home/` is world-writable so the fixed unprivileged worker user can write it. The 0700
  parent is what keeps other host users out.
- The supervisor creates the home at the first accepted launch that names it, and never reads or
  writes inside `home/` afterwards.
- `claw down` leaves homes in place. `claw up` leaves them in place.

Inside the home the driver owns the layout. For Hermes:

```
home/
  workspace/        the worker's working directory
  hermes/           HERMES_HOME: state.db (SQLite, WAL), sessions/, memories/, skills/
  .claw-run/
    session         the exact session ID of the last stored turn
    question.md     written by the worker to end a turn with a question
```

## One live run per home

- The run record gains `home` (the ID) and `home_hash`.
- Launch takes a supervisor-wide home admission lock, then scans the ledger. If any non-terminal
  record has the same `home_hash`, the launch is refused with `409 home_busy` and the holder's run
  ID. Otherwise the record is written under the same lock.
- There is no separate reservation file. A home is reserved exactly while a non-terminal record
  names it, so a crash cannot leave the two out of step, and a restart needs no rebuild step.
- A record becomes terminal only after its container is removed and revocation is proven, as today.
  So a run waiting on a revocation acknowledgement keeps its home; other homes are unaffected.
- An idempotent relaunch of the same run key is not a second run and is admitted as today.
- A launch whose spawn fails closed reaches a terminal state through the normal teardown and
  releases the home.

## Home verbs

On the supervisor socket, with the same auth as the run API:

| Verb | Behaviour |
|---|---|
| `GET /v1/homes/{installation}/{home}` | `exists`, `created_at`, `last_run_id`, `reserved_by` (a run ID or empty). |
| `DELETE /v1/homes/{installation}/{home}` | Refused with `409 home_busy` while reserved. Otherwise removes `<home-hash>/` and returns `204`. Deleting an absent home is `204`. Takes the home admission lock, so it cannot interleave with a launch. |

CLI: `claw runs home status <installation> <home>` and `claw runs home rm <installation> <home>`.

A reset is a delete: the next launch for that home creates it empty, finds no session locator and
starts a new session.

## Rendered run project

The task-home profile renders two services and one more network.

```yaml
name: clawrun-<ns>-<runkey>
services:
  worker:
    # as the stateless worker: pinned image ID, read_only, cap_drop ALL,
    # no-new-privileges, user 65534:65534, init, pids and memory limits, labels
    volumes:
      - type: bind
        source: <pod>/.claw-homes/<home-hash>/home
        target: /home/task
      - type: bind
        source: <pod>/<content source>
        target: /reference/handbook
        read_only: true
    networks: [run]
    depends_on: [gate]
  gate:
    # same pinned image, read_only, cap_drop ALL, user 65534:65534, no mounts,
    # no inputs, no token
    networks: [run, claw-runs]
networks:
  run:       { internal: true }          # created and removed with this project
  claw-runs: { external: true, name: claw-runs-<ns> }
```

- The worker is on the run's own network only. The gate is on that network and on the pod's shared
  run network, where cllama is.
- The gate forwards TCP from `gate:<port>` to cllama's API port and nothing else. The worker's
  model base URL is the gate.
- The driver supplies the gate's entrypoint, run from the worker's own image, so no new image is
  published. For Hermes it is a short Python forwarder passed on the command line.
- Bind sources are absolute host paths computed by the supervisor and content paths compiled at
  `claw up`. Static `$` is escaped as elsewhere.
- Both containers carry the run labels, so reconciliation and `compose down` treat them as one run.
  `verifyContainers` expects exactly the worker and the gate in this profile.

## Driver contract changes

`driver.RunWorkerSpec` gains fields that are omitted when empty, so stateless templates keep their
IDs:

```go
type RunWorkerSpec struct {
    // existing fields unchanged
    Home   *RunHomeSpec `json:"home,omitempty"`   // container mount path for the home
    Gate   *RunGateSpec `json:"gate,omitempty"`   // forwarder entrypoint, command and listen port
}

type RunResultContract struct {
    Source string `json:"source"`
    Marker string `json:"marker,omitempty"`
    // Header, when "json-line", means the first line after Marker is a JSON
    // object of at most 1024 bytes; the answer is everything after it.
    Header string `json:"header,omitempty"`
}
```

New run placeholders: none. The home is a mount, not an input.

`RunWorkerOpts` gains the compiled home mount path, the content targets, the gate address and the
result byte limit, so the driver can compile them into static environment.

## Result frame

The worker's wrapper is the only writer to the container's stdout. The harness's own output goes to
files on tmpfs. After the harness exits, the wrapper prints:

```
<marker>{"outcome":"answer","session":"<id>","truncated":false}
<body>
```

- `outcome` is `answer` or `question`.
- `session` is the exact harness session ID that now holds this turn.
- The wrapper cuts the body at the template's result limit, at a rune boundary, and sets
  `truncated`. Stdout therefore never exceeds the header plus the limit, which also bounds the
  container log.
- The supervisor parses the header strictly: one line, at most 1024 bytes, known keys only, known
  outcome values, session ID of at most 128 bytes from a fixed alphabet. Anything else is
  `result_missing`. A stateless template has no header and is parsed as today.
- The run's result view gains `outcome` and `session`. The result body endpoint is unchanged.

The supervisor reads nothing from the home, so no file a worker writes there is ever opened on the
host by Clawdapus.

## Questions

A worker that needs a person writes its question to `.claw-run/question.md` in its home and ends its
turn. The compiled contract tells it so. After the harness exits, the wrapper checks that file: if it
is a non-empty regular file, the wrapper moves it aside, uses its content as the body and reports
`outcome: question`. Otherwise the harness's final text is the body and the outcome is `answer`.

The orchestrator parks the task and later launches a new run for the same home whose task text is
the answer. That text arrives as the next user message in the resumed session.

## Hermes per-run driver

A second `RunWorkerCompiler`, in `internal/driver/hermes/runworker.go`.

**Validation** (`ValidateRunWorker`): an agent contract, cllama, a valid primary `MODEL`, no
`HANDLE`. It does not call the long-lived `Validate`, which requires a channel.

**Commands.** The wrapper is a pinned Python program passed as the entrypoint
(`python3 -c <program> claw-run`), with the task as a positional argument:

- First turn (no `.claw-run/session` in the home):
  `hermes chat -Q -q <task> --toolsets <compiled list>`
- Later turns:
  `hermes chat -Q -q <task> --toolsets <compiled list> --resume <session ID>`

In this mode Hermes prints the final response on stdout, `session_id: <id>` on stderr, and exits 1
when the run failed. The wrapper:

1. Creates `workspace/`, `hermes/` and `.claw-run/` in the home if absent.
2. Writes the runner config, the model credentials and the agent contract into `hermes/` from its
   environment, replacing whatever a previous run left, then removes them from the environment.
3. Reads `.claw-run/session`. A value that is not a well-formed session ID is treated as absent.
4. Runs Hermes with stdout and stderr captured to tmpfs.
5. Takes the session ID from the last `session_id:` line on stderr. This is the ID after any
   mid-run compression, which Hermes reports as the continuation session.
6. Proves the turn was stored: opens `hermes/state.db` read-only and requires at least one message
   row for that session ID. If the proof fails, it does not update `.claw-run/session`, prints no
   result frame and exits 70.
7. Writes the session ID to `.claw-run/session` atomically.
8. Prints the result frame and exits with Hermes's exit code.

A resume that names a session Hermes does not have must fail the run and not start a fresh session
silently. Whether Hermes does this by itself is one of the facts to prove; if it does not, the
wrapper checks the session exists before the turn.

**Environment.** `HERMES_HOME=<home>/hermes`, `HOME=<home>`, `TERMINAL_ENV=local`,
`TERMINAL_CWD=<home>/workspace`, `HERMES_YOLO_MODE=1` and `HERMES_ACCEPT_HOOKS=1` (there is no
person to approve a command), `CLAW_MANAGED=true`.

**Model access.** The config names a custom OpenAI-compatible provider whose base URL is the gate
and whose key is the run's cllama token, as the long-lived Hermes driver does with cllama today.

**Tools.** Terminal and file toolsets on. Messaging, cron, browser and web toolsets off: there is
no channel, no schedule and no route out.

**Image.** The pinned `hermes-base`. If the contract needs a Hermes patch, that is a coordinated
`hermes-base` release, not part of the feature PR.

## Model access through a subscription sign-in (placeholder)

Out of scope for this profile's first version, which uses API keys held by cllama. The intended
direction is a cllama provider adapter that holds a provider sign-in and gives the worker only its
per-run cllama token, so this profile needs no change. It gets its own issue and decision record.

## Facts to prove first

A spike against the pinned Hermes image, before any driver code is merged:

1. `hermes chat -Q -q` with `--resume <id>` continues the named session, and the second turn can
   quote the first.
2. The reported session ID has stored messages in `state.db` after the turn.
3. What a resume of an unknown session ID does.
4. What the reported session ID is after a turn that compresses, and that the next resume sees the
   compressed history.
5. Shell commands run without a TTY and without an approval prompt.
6. Hermes runs as uid 65534 with a read-only root, tmpfs `/tmp` and a writable home.
7. SQLite in WAL mode works on the home bind mount, on Linux and on a macOS development host.
8. The gate forwarder carries a streamed completion.
9. Cold start: container create to first model request, measured.

## Acceptance

Security acceptance cases are written by the security reviewer and recorded on #394. The functional
cases:

- **Resume.** Two runs on one home; the second run's answer depends on the first run's history.
- **Kill.** Kill the worker mid-turn; the next run resumes the last stored session and completes.
- **Reset.** Delete the home; the next run starts a new session and sees nothing of the old one.
- **Question.** A run ends with `outcome: question`; a second run carrying the answer completes.
- **Stateless unchanged.** Existing per-run tests pass untouched, and a stateless template's ID is
  byte-identical before and after.

## Concerns handed to security review

One line each; the controls are the reviewer's call.

- The home has no size quota.
- A worker can leave files in its home that change its own next run (config, hooks, skills).
- Host tooling that reads a home could follow a symlink the worker planted.
- The gate shares the pod's run network with other runs' gates.
- `run.content` sources are read by the worker's shell in full.
- The session ID in the result frame is worker-supplied.

## Implementation order

1. Spike the facts above; record the results on #394.
2. Spec field, home resolution, admission and the home verbs, with unit tests on the existing
   fakes.
3. Renderer: mounts, gate, per-run network; `verifyContainers` for two containers.
4. Result header parsing.
5. Hermes `RunWorkerCompiler` and wrapper.
6. Pod schema and `claw up` compilation.
7. A `spike`-tagged end-to-end test beside `TestSpikePerRun`.
8. Guide and CLI reference.
