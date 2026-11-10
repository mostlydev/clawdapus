# Task-home security review and acceptance cases

Reviewed design commit `1ad3888`, ADR-027 and its linked plan, for issue #394.
This review covers the opt-in API-backed Hermes profile. It does not approve
subscription authentication, publication, or a release.

## Verdict on the proposed design

The capability is acceptable: writable task homes, native terminal/file tools,
task-local history, skills and hooks need no blanket prohibition. A single
ledger can safely own reservations; no second reservation store is required.
An opaque ID scoped by installation and a supervisor-owned parent are suitable
for home resolution. The proposed fixed-destination gate is suitable in
principle; packet isolation remains a live acceptance requirement.

Security AGREE is withheld for the initial draft on these concrete points:

1. The wrapper writes managed credentials into the persistent home, contrary
   to the stated ephemeral-authority contract. Generate managed credentials
   outside the home. Environment-only credentials are being probed against
   the pinned image. A worker can copy its own short-lived run token; the
   enforceable guarantee is that infrastructure does not persist provider
   credentials or grant authority based on worker-controlled configuration.
2. Capturing harness stdout does not bound hostile native shell output: the
   shell may reopen a process's stdout descriptor. Both services need an
   independent Docker logging budget. Rotation losing the result must produce
   an explicit failure, never an inferred answer.
3. An admission-time home size check does not prevent an active worker from
   exhausting the host disk. An enforced storage cap answers that threat.
   Alternatively, the operator can accept that limitation for an internal
   trial. That scope decision was sent through the coordinator; no answer
   has been presumed. An admission check alone must not be described as a
   hard quota.

The architect accepted ephemeral credentials and log rotation in room chat;
the final document and implementation must be reviewed again after changes.
Home inspection/deletion must not follow worker-created symlinks. A bounded
metadata walk is distinct from opening worker-controlled file contents.
No host content parser is needed for the result/session protocol.

## Executable red cases

`internal/runs/taskhome_security_test.go` adds four compiling acceptance cases:

- only the selected home is mounted;
- the worker has a project-private internal network;
- simultaneous distinct runs for one home yield exactly one acceptance;
- revoke outage removes the old worker but retains the home reservation,
  permits unrelated homes, and releases the reservation only after ACK.

Command: `go test ./internal/runs -run '^TestTaskHomeSecurity' -count=1`.
All four currently fail at the admission prerequisite: `spec_invalid`, unknown
`home` field. The later assertions have **not** executed. They must run after
the schema/profile is implemented; a compilation failure is not the red signal.
JSON fixtures must be aligned with the final compiler-owned schema during
implementation. Render assertions do not replace live isolation tests.

## Required live adversarial acceptance

Each case runs through the real opt-in supervisor/template path. Use generic
task names and fake inference; no provider sign-in or real keys are necessary.

### Hostile task A and task B

Place distinct markers in workspace files, session messages, memories and
skills. Launch both workers. Task A attempts absolute/relative path traversal,
symlinks to parent and B, `/proc/*/root` and mount paths, guessed home IDs and
session IDs, and Docker/host socket access. Neither marker from B may be
retrieved through filesystem, history or result paths. The generated project
must mount only A's inner home, never the shared parent or supervisor metadata.

Positive controls: A writes/reads its own marker and resumes it in a new
container. B can read its marker. A reads declared reference content but cannot
write it. Reset removes A's workspace **and** resumable history, produces a new
session, and leaves B intact. Removing the mount restriction should make the
negative case fail; a worker that never started is not a pass.

### Peer listener

B opens a TCP listener on all worker interfaces and serves B's marker. Confirm
a local request in B succeeds. From A, attempt B's actual IP and service name,
plus any reachable gate interface. No request reaches B. A must still complete
a streamed inference through its gate to cllama. The gate has one fixed
destination and port, no generic HTTP proxy/CONNECT destination, no mounts or
credentials, and no route exposed to cllama's UI/admin listener. Deliberately
sharing the worker network must turn this test red.

### Same-home race and recovery

Start two different run IDs simultaneously for one installation/home. Exactly
one is accepted and at most one mounts/spawns. Relaunching the accepted key is
idempotent. Kill/restart the supervisor after acceptance and at spawn boundaries;
the reservation survives. Delete/reset racing with launch uses the same lock.
No deletion or successor starts while an old container still exists. Two
installations using the same textual home ID remain distinct. Corrupt or
ambiguous active ledger state fails closed rather than freeing a home.

### Revoke outage

Make cllama revocation fail, cancel A, and verify both worker and gate are
removed. The run remains non-terminal without an ACK; a second run on A's home
is refused. A different home continues. Restart the supervisor during the outage
and repeat the refusal. Restore ACK, then admit one successor with a fresh
identity. Old identity requests fail. Test container-removal failure separately:
an ACK alone must not release the home.

### Untrusted results and next-run state

Malformed, oversized, duplicate-key or unknown-field headers fail explicitly.
Reject invalid outcome/session alphabets, stale question files, and absent
stored-turn proof. A question file must belong to this turn and be consumed
once; the next answer must not re-emit an old question. Symlink/FIFO/oversized
files cannot hang the wrapper or redirect host operations. A same-task hook
may run, but cannot change the compiled tool grant, image, network, limits or
fresh run identity. Modified home config never enables a provider credential
or additional mount/network.

### Resource limits

Flood stdout/stderr and verify rotation bounds host logs independently of the
wrapper. Fill home storage and demonstrate the selected enforcement, or record
the operator's explicit internal-trial exception. Bound any admission metadata
scan so file count cannot indefinitely stall cancellation or unrelated homes.

## Pinned Hermes probe

`go test -tags spike ./internal/driver/hermes -run '^TestSpikeTaskHomeHermesContract$' -count=1 -v`
uses the pinned image and a local fake provider. It runs first/resume phases in
different containers on one bind-mounted home. It checks stored message rows,
WAL mode, actual prior-message replay to inference, unknown-session failure,
and a native terminal write as UID 65534 with a read-only root. It uses a fake
credential supplied only through the process environment. It does not prove
compression, supervisor integration, gate streaming, Linux-host bind behavior,
or adversarial cross-task isolation.

Observed on macOS/OrbStack Docker 29.8.2 with local image
`ghcr.io/mostlydev/hermes-base:v2026.6.19-claw.3`:

- Probe passed in two separate containers. First session had two message rows;
  resumed session had four and replayed the prior assistant marker to the fake
  provider. Unknown exact ID exited 1 without a model request. Native terminal
  wrote its marker with no TTY. Fake credentials were environment-only.
- One timing sample: container Created timestamp to first chat/completions
  request was 7.925 seconds first-turn, 4.397 seconds resumed. Image was already
  local; these are not image-pull timings or production latency estimates.
- `-Q` still printed a missing-tirith-scanner warning on stdout. Raw harness
  stdout is therefore not a clean result protocol.
- An intentionally malformed fake stream produced exit 1 but left one user
  message in SQLite. Stored-history proof must verify this successful turn's
  assistant row or validated compression descendant; row-count >= 1 is unsound.
- Existing Hermes unit tests and existing runs unit tests passed, with the four
  deliberate new red cases excluded from the latter command.
- Probe containers and networks were removed after the checks.
