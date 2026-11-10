# Task-home profile for per-run workers (2026-11-09)

**Status:** Proposed, issue #394. Decision record: [ADR-027](../decisions/027-task-home-per-run-profile.md).
Security review: [task-home security review](../reviews/2026-11-09-task-home-security.md).

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
- The supervisor creates the home when the first run that names it is spawned. It never opens a
  file inside `home/`: nothing a worker writes there is parsed on the host.
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
- An unfinished record whose reservation does not match its own spec is ambiguous. While one
  exists, every launch that names a home and every home delete is refused (`ledger_unavailable`).

## Home verbs

On the supervisor socket, with the same auth as the run API:

| Verb | Behaviour |
|---|---|
| `GET /v1/homes/{installation}/{home}` | `exists`, `created_at`, `last_run_id`, `reserved_by` (a run ID or empty). |
| `DELETE /v1/homes/{installation}/{home}` | Refused with `409 home_busy` while reserved. Otherwise returns `200`; deleting an absent home is also `200`. |

Delete has two steps. Under the home admission lock, which launch also takes, the home directory is
renamed out of the way and the rename is made durable; from that moment the ID names a new, empty
home. The renamed directory is then removed outside that lock, so a large tree cannot delay
admission for other homes. Removal never follows a symlink. Files the worker user created in its
own subdirectories may not be removable by the supervisor's user; those are removed as the worker
user by a short-lived container run through Compose from the worker image, with no network, no
inputs and only that directory mounted. A removal that fails is reported and retried on the next
delete; the ID is free either way.

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
- The gate forwards TCP from `gate:<port>` to one fixed destination, cllama's API port. It is not
  an HTTP proxy and accepts no destination from the worker. The worker's model base URL is the
  gate.
- Both services carry a Docker log budget (`json-file`, one file, sized from the result limit). A
  worker with a shell can write to the container's stdout directly, so the wrapper's own bound is
  not enough. A flood that rotates the result frame away ends as `result_missing`.
- The driver supplies the gate's entrypoint, run from the worker's own image, so no new image is
  published. For Hermes it is a short Python forwarder passed on the command line.
- Bind sources are absolute host paths computed by the supervisor and content paths compiled at
  `claw up`. Static `$` is escaped as elsewhere.
- The gate carries the pod labels and a `claw.run.gate-for` label, but not the run-key label, so
  the supervisor's view of a run stays one container. A gate with no ledger record is an orphan,
  like a worker, and blocks `claw runs serve` until removed.

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
- The body is the assistant message this turn appended to the session store, not the harness's
  stdout, which can carry warnings.
- The wrapper cuts the body at the template's result limit, at a rune boundary, and sets
  `truncated`.
- The supervisor parses the header strictly: one line, at most 1024 bytes, exactly one JSON object
  with nothing after it, no duplicate keys, known keys only, known outcome values, session ID of
  at most 128 bytes from a fixed alphabet. Anything else is `result_missing`. A stateless template has no header and is parsed as today.
- The run's result view gains `outcome` and `session`. The result body endpoint is unchanged.

The supervisor opens no file in the home, so nothing a worker writes there is ever parsed on the
host by Clawdapus.

## Questions

A worker that needs a person writes its question to `.claw-run/question.md` in its home and ends its
turn. The compiled contract tells it so. The wrapper removes that file before every turn, so a
question left by a killed run is never reported by its successor. After the harness exits, the
wrapper checks the file: if it is a non-empty regular file, the wrapper uses its content as the
body, removes it and reports `outcome: question`. Otherwise the harness's final text is the body and the outcome is `answer`.

The orchestrator parks the task and later launches a new run for the same home whose task text is
the answer. That text arrives as the next user message in the resumed session.

## Hermes per-run driver

A second `RunWorkerCompiler`, in `internal/driver/hermes/runworker.go`.

**Validation** (`ValidateRunWorker`): an agent contract, cllama, a valid primary `MODEL`, no
`HANDLE`. It does not call the long-lived `Validate`, which requires a channel.

**Commands.** The wrapper is a pinned Python program passed as the entrypoint
(`python3 -c <program>`), with the task as its one positional argument:

- First turn (no `.claw-run/session` in the home):
  `hermes chat -Q -q <task> --toolsets <compiled list>`
- Later turns:
  `hermes chat -Q -q <task> --toolsets <compiled list> --resume <session ID>`

In this mode Hermes prints the final response on stdout, `session_id: <id>` on stderr, and exits 1
when the run failed. The wrapper:

1. Creates `workspace/`, `hermes/` and `.claw-run/` in the home if absent.
2. Writes the non-secret runner config and the agent contract into the home from its environment,
   replacing whatever a previous run left, and removes any `.env` there. The run token is never
   written: it is passed to Hermes only in that process's environment.
3. Removes `.claw-run/question.md`.
4. Reads `.claw-run/session` without following a link or blocking on a FIFO. A malformed value
   fails the run; an absent one starts a new session.
5. Notes the highest message ID in the session store, then runs Hermes with stdout and stderr
   captured to tmpfs.
6. Takes the session ID from the last `session_id:` line on stderr. This is the ID after any
   mid-run compression, which Hermes reports as the continuation session.
7. Proves the turn was stored: opens `hermes/state.db` read-only and requires an assistant message
   under that session ID with an ID above the one noted in step 5. If the proof fails, it does not
   update `.claw-run/session`, prints no result frame and exits 70.
8. Writes the session ID to `.claw-run/session` atomically and prints the result frame.

If Hermes exits non-zero, the wrapper prints no frame and exits non-zero, and the locator is left
as it was. A resume that names a session Hermes does not have exits non-zero without calling the
model (proven against the pinned image).

**Environment.** `HERMES_HOME=<home>/hermes`, `HOME=<home>`, `TERMINAL_ENV=local`,
`TERMINAL_CWD=<home>/workspace`, `HERMES_YOLO_MODE=1` and `HERMES_ACCEPT_HOOKS=1` (there is no
person to approve a command), `CLAW_MANAGED=true`.

**Model access.** The config names a custom OpenAI-compatible provider whose base URL is the gate
and whose `key_env` names an environment variable. The wrapper sets that variable to the run's
cllama token in the Hermes process environment only. (Hermes sends `OPENAI_API_KEY` only to
`openai.com` hosts, so that variable cannot carry the token.)
Model, approvals and terminal settings are asserted after `CONFIGURE`, so an image directive
cannot reroute them.

**Tools.** The `terminal`, `file`, `memory`, `skills` and `todo` toolsets are on. Messaging, cron,
browser and web toolsets are off: there is no channel, no schedule and no route out.

**Image.** The pinned `hermes-base`. If the contract needs a Hermes patch, that is a coordinated
`hermes-base` release, not part of the feature PR.

## Model access through a subscription sign-in (placeholder)

Out of scope for this profile's first version, which uses API keys held by cllama. The intended
direction is a cllama provider adapter that holds a provider sign-in and gives the worker only its
per-run cllama token, so this profile needs no change. It gets its own issue and decision record.

## Proven and open facts

Proven against the pinned image by `TestSpikeTaskHomeHermesContract` (CLI contract) and
`TestSpikeTaskHomeWorkers` (the whole path: `claw up`, supervisor, gate, cllama, fake provider):
facts 1, 2, 3, 5, 6 and 8 below, and fact 7 on a macOS development host. One measured sample of
fact 9: 6.4 s from launch to a stopped first run with an instant fake model.

A worker killed in the middle of a turn: Hermes had stored that turn's user message and no reply.
The next run resumes the same session, and Hermes presents the unanswered message again with the
new run's text appended to it. So pending work is retried, and the new text does not replace the
old request. An orchestrator's follow-up brief should be written with that in mind.

Open: fact 4 (compression), and fact 7 on Linux.

The facts:

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

- The home has no size quota. Reviewed: this is a host-availability risk that a polling check
  cannot bound. It is recorded as a limitation in ADR-027 and awaits the operator's decision.
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
