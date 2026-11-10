# ADR-027: Task-Home Profile for Per-Run Workers

**Date:** 2026-11-09
**Status:** Proposed
**Depends on:** ADR-002 (Runtime Authority), ADR-020 (cllama Compiled Tool Mediation)
**Amends:** the per-run worker design agreed for #372 (`docs/plans/2026-10-20-per-run-workers.md`, "As built")
**Tracks:** #394
**Design:** `docs/plans/2026-11-09-task-home-per-run-profile.md`

## Context

Per-run workers (#372) are stateless. A run gets a clean container with a read-only root, tmpfs as
its only writable path, no volumes, no skills and no native tools that act outside the
conversation. Only PicoClaw is supported. That is the right shape for a bounded worker that answers
from what it is handed.

It is the wrong shape for a unit of work that spans several runs. An orchestrator that hands a
worker a task, parks it on a question, and wakes it with the answer needs three things the
stateless profile rules out:

- files and a shell, so the worker can do the work and not only describe it;
- the worker's own conversation history on the next run, so a wake continues a thought;
- a place for both that belongs to one task and to no other.

The #372 rules were written for a worker with no native tools. Several of them, such as one shared
run network, were sound only because of that.

## Decision

Add a second per-run profile, **task home**, that a pod service opts into. The stateless profile
stays as built for every service that does not opt in.

1. **A task has a home; a run is a clean container visiting it.** A launch names a home by an
   opaque ID. The supervisor resolves the ID to a private directory under the pod. The caller never
   names a path, a volume or a mount.
2. **The home is the only persistent writable path.** It is mounted read-write. Reference content is
   declared in the pod, compiled into the template and mounted read-only. The root filesystem stays
   read-only.
3. **One live run per home.** A home is reserved from the moment a launch is accepted until that
   run's container is removed and its authority is revoked. A second launch for a reserved home is
   refused.
4. **Native tools are on in this profile.** The worker has a shell and file tools inside its home.
   Business tools still reach it only through cllama.
5. **Hermes is the first driver for this profile.** It runs one turn per run and resumes its own
   session, by exact session ID, on later runs.
6. **The result is a bounded frame on stdout.** It carries the session ID and whether the worker
   ended with an answer or with a question for a person. The supervisor never opens a file the
   worker can write.
7. **Each run gets its own network.** The worker reaches a small forwarder in its own run project.
   The forwarder relays TCP to one fixed destination, cllama's API port. It is not a proxy: it
   parses nothing, has one listener, and has no route to cllama's operator port. Two workers do
   not share a network.
8. **Homes have their own verbs.** A home can be inspected and deleted through the supervisor.
   Deleting a home is how an orchestrator resets a task.
9. **Authority stays ephemeral.** The run's cllama token reaches the harness only through its
   process environment. Nothing the infrastructure writes into the home is a credential, and
   nothing in the home grants authority to a later run.
10. **A separate meter watches home size.** It mounts only that home read-only, runs as the
    worker UID with no network or credentials, and exits when the logical-byte threshold is
    exceeded. The supervisor then revokes and removes the run as `home_full`. A home the meter
    cannot measure ends the run as `home_meter_unavailable`.

## What this amends

| #372 as built | Task-home profile | Stateless profile |
|---|---|---|
| No volumes; tmpfs is the only writable path | One home bind mount, read-write; compiled content, read-only | Unchanged |
| PicoClaw only | Hermes | Unchanged |
| Native tools that act outside the conversation are disabled | Enabled | Unchanged |
| All workers share one internal run network | One network per run, through a forwarder | Unchanged |
| The result is the bytes after a marker on stdout | The same, plus a one-line header | Unchanged |
| Spec fields: `template`, `task`, `tools`, `max_runtime_seconds` | Adds `home`, required | `home` is refused |

## What this keeps

- `docker compose` is the sole lifecycle writer; the Docker SDK is read-only (ADR-002). The home is
  a host directory the supervisor creates, so no volume is created or removed outside Compose.
- Task text, the cllama token and the runner config reach the worker only through Compose
  interpolation. None of them is written into the rendered project or into the home by the
  supervisor.
- The caller selects within a compiled template. The home policy, the content mounts and the
  forwarder are part of the template and of its ID.
- Every run has a new cllama identity and token. The old one is revoked.
- No worker starts unless the revocation self-test passes. A repeated spawn fails closed.
- An image that declares `VOLUME` is still refused.
- `ValidateRunWorker` stays separate from `Validate`, per driver. Hermes's long-lived channel
  requirements are not weakened, and PicoClaw's per-run validation does not change.
- Existing stateless templates keep their template IDs: every new template field is omitted when
  empty.

## Alternatives considered

- **A named Docker volume per home.** It gives better file semantics on a macOS development host.
  But deleting it is a Docker write outside Compose, and an orchestrator that must archive a home
  cannot read it from the host. A host directory is simpler and inspectable.
- **A separate ledger of home reservations.** A second source of truth that can disagree with the
  run records after a crash. The reservation is derived from the run ledger instead.
- **Connecting cllama to each run's network with `docker network connect`.** One call, no forwarder.
  It needs an exception to ADR-002. The forwarder keeps Compose as the only writer and also narrows
  what the worker can reach on cllama to the API port.
- **Reading the session ID or the result from a file in the home.** The home is writable by the
  worker, so the supervisor would be parsing worker-controlled files on the host. Everything the
  supervisor learns arrives on stdout, bounded.
- **`hermes -z` (one-shot mode).** It is dispatched before resume is handled, so it cannot resume
  a session, and it reports no session ID.
- **Keeping a stopped container for a later run.** A stopped container keeps the old command,
  environment and token and no process memory. It is not a warm worker. Out of scope.

## Consequences

- **The meter is a watchdog, not a hard quota.** Concurrent writes can exhaust the host disk
  before detection or teardown. The operator accepted this limitation for the internal trial,
  with the watchdog and written limitation; kernel-enforced storage quotas remain later work.
- Clawdapus now holds data that outlives a run. Homes are not rotated by `claw up` and not removed
  by `claw down`. Retention is the orchestrator's decision, made through the delete verb.
- A run that cannot get its revocation acknowledged keeps its home reserved. Other homes continue.
- Each run has three containers: worker, forwarder, and networkless storage meter.
- The Hermes per-run contract is pinned to a Hermes version, as the PicoClaw contract is. A Hermes
  upgrade re-proves it.
- The security review of this profile, and its acceptance cases, are recorded with the design.

## Not decided here

- Model access through a subscription sign-in. The design leaves a placeholder; it gets its own
  record.
- Warm workers.
- Network egress beyond cllama.
- Pod-declared `SKILL` files for per-run workers. Skills the worker writes for itself live in its
  home and need no decision.
