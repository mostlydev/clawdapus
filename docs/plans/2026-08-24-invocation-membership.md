# Invocation-registered claws and live membership (2026-08-24)

**Status:** DRAFT — plan branch `plan/invocation-membership`; ADR-027 (amending ADR-002) will be written with PR-3; an issue must be opened and
the branch renamed `issue-<n>-invocation-membership` before implementation (issue-first
workflow). Consumes the contract in cllama `docs/plans/2026-08-24-invocations.md`; this plan
does not re-decide it.

**Depends on:** a tagged cllama release shipping the v1 `Invocation` wire types and client,
the control API (`POST/GET/DELETE /control/v1/invocations`, parent delegation ceiling), the
`pod-members` feed contract, the legacy context-directory adapter, and the conformance fixture.
Release discipline applies: the submodule pointer and `DefaultCllamaTag` move only with that tag.

## Why now

`claw up` compiles identity once. Every claw's bearer, context, tools, memory binding, feeds,
rules and budget are written into `.claw-runtime/context/<agent-id>/` before the pod starts
(`cmd/claw/compose_up.go`, `internal/cllama.GenerateContextDir`); peers are broadcast as
`CLAW_HANDLE_*` environment at compile time. cllama already re-reads context per request, so
the proxy is dynamic; membership is not. Nothing can join a running pod, peers cannot learn
of a join without a redeploy, and identity is a directory name plus a plaintext secret.

cllama's invocation plan makes the invocation the runtime authority and the filesystem the
proxy's private persistence. Clawdapus therefore stops materializing runtime context and
**registers** each member; the pod file remains desired topology and defaults.

## Operator stories

- **S2 — a declared member starts under the invocation contract.** Unchanged operator loop.
  `claw up` materializes container topology; the trusted controller requests one invocation
  per running member and injects only the proxy URL and bearer. Acceptance: all four retained
  drivers reach cllama with a bearer and no provider credential; `claw audit` shows role and
  purpose; no workload invocation bearer remains in the runtime tree after container creation;
  the effective upstream request for
  `examples/trading-desk` is semantically equivalent to today's (cllama's legacy-adapter
  parity test extended with clawdapus fixtures); `TestSpikeRollCall` stays green.
- **S3 — an operator spawns and retires a member live.** `claw spawn analyst --role
  researcher --purpose incident-42` calls the `claw-api` controller, which registers the
  invocation with cllama and creates one labeled container through the Docker API without
  rebuilding or restarting the pod; `claw ps` derives live membership from labeled Docker
  containers; `claw retire analyst-1` revokes the invocation and removes the container;
  controller restart reconstructs state from Docker plus cllama. Acceptance: existing container
  ids unchanged; the member's first request is audited with its role; after retire the bearer
  fails with `invocation_revoked`; `claw down` removes spawned members.
- **S4 — a member creates a narrower child.** An authenticated member calls `claw-api` to
  spawn a child; `claw-api`, the only component with Docker access, asks cllama to mint a
  child invocation within the parent's delegation ceiling; widening fails before any
  container or record exists; retirement cascades per the stated parent/child rule.
- **S5 — membership and social topology change live.** After S3/S4, existing members see the
  join on their next turn through a cllama-injected `pod-members` feed served by `claw-api`;
  after retirement they see the departure. No redeploy, no environment rewrite, no Docker
  socket in workloads. Handles and routing metadata are feed data; the compile-time
  `CLAW_HANDLE_*` broadcast remains only for non-claw services that cannot read feeds.
- **S6 — memory views are identity-, role- and purpose-bound.** Two invocations of one
  stable subject with different roles never share context, tools, continuity or memory view;
  the single configured memory service receives subject, role, purpose/view and invocation
  provenance. Compaction and scheduling are the memory implementation's concern; the pod
  example uses `examples/reference-memory` with a deterministic derived view.

## Decisions

1. **Two-phase `claw up`, HTTP only.** Compose emission is unchanged in shape. `claw up`
   brings up the cllama service(s) first and waits for `/health`, registers one invocation
   per cllama-enabled service (per ordinal for `count > 1`) with the pod-scoped controller
   credential minted into the cllama environment at compile time
   (`.claw-runtime/control.token`, 0600). It writes each workload bearer to a
   uniquely named 0600 temporary env file, lets Compose create the target
   container, verifies the container carries the new invocation id, and deletes
   the temporary file immediately. The bearer remains only in the workload's
   process environment and Docker's protected container configuration; it is
   never a durable context artifact. Re-running `claw up` leaves unchanged
   services alone. For a changed input digest it creates the replacement
   invocation first, recreates and verifies only that service, then revokes the
   old invocation; failure revokes the replacement and reports the exact
   reconciliation state. There is no filesystem context transport; the context
   mount disappears when the legacy adapter is retired.
2. **`internal/cllama` submits trusted inputs; it does not compile context.** `AgentContextInput`
   and `GenerateContextDir` are replaced by `BuildInvocationRequest(rc *driver.ResolvedClaw, ...)`
   emitting the v1 request: subject `{kind: member, id: <pod>/<service>[/<ordinal>]}`, role,
   labels, purpose, expiry; input modules by kind (effective contract incl. `enforce`/`guide`
   includes, infrastructure map, context blocks); feeds (including `pod-members`); tools
   (ADR-020); memory (ADR-021); rules; model policy (ADR-019); budget; channel allowlist.
   The existing builders in `compose_up.go` move behind this function unchanged. Goldens
   assert semantic equivalence through cllama's adapter, not byte equality.
3. **Pod schema.** Service `x-claw.role` (must exist in pod-level `x-claw.roles[]` when that
   list is declared), `x-claw.labels` (map, keys `[a-z0-9_.-]`), `x-claw.purpose`; pod-level
   `labels-defaults` merges additively. `metadata.json` is no longer a file; `claw inspect`
   and clawdash show the invocation.
4. **Live membership: one controller, Docker labels as state (ADR-002 amended by ADR-027).**
   Every dynamic container carries labels (`claw.pod`, `claw.member`, `claw.role`,
   `claw.purpose`, `claw.invocation`, `claw.parent`). `claw-api` is the only component with
   the Docker socket; it creates and removes dynamic containers through the Docker API and
   is the single code path for operator `claw spawn`/`claw retire` (the CLI calls its
   authenticated control surface) and for agent child spawn. Compose remains the base-pod
   lifecycle writer; there is no lease log and no overlay. `claw ps` reads labels. `claw up`
   injects `claw-api` whenever any cllama-enabled service exists (today only for
   `x-claw.master`). ADR-027 records the amendment: the SDK is read-only *except* inside the
   controller for dynamic members, which must carry the labels above and be reconstructible
   after controller restart. The controller accepts a declared member-template
   id plus the narrow role, purpose, labels, and parent fields only. Image,
   command, mounts, networks, privileged mode, socket mounts, and host paths
   come from a `claw up`-compiled controller template and cannot be supplied by
   either an operator request or a workload.
5. **`pod-members` feed.** `claw-api` serves `GET /feeds/pod-members` (ADR-013 shape) built
   from Docker labels plus cllama's invocation list: current member id, role,
   purpose, handles, routing metadata, and container creation time. It is a
   current-state snapshot, so a departure is represented by absence rather than
   an event ledger. The feed has zero cache TTL so the next turn observes the
   current set. `claw up` subscribes every claw by default (`feeds-defaults`
   gains `pod-members`; opt out per service).
6. **claw-api and clawdash read invocations.** `cmd/claw-api/agent_context.go` and the
   clawdash agents view move to `GET /control/v1/invocations[/{id}]` (secrets never
   returned). `claw audit` gains `ROLE`/`PURPOSE` columns and `--role`/`--label` filters.
   `X-Claw-ID` stays equal to the subject id for compatibility.
7. **Drivers unchanged in behaviour.** They already point runners at the proxy with a bearer
   from `rc.CllamaToken`; the bearer now comes from registration, so `Materialize` runs after
   phase one. No driver ever receives a subscription-kind credential.
8. **Controller reconciliation is fail-closed.** At startup, `claw-api`
   compares labeled dynamic containers with cllama's redacted invocation list.
   A live container whose invocation is missing, expired, revoked, or belongs to
   another pod is stopped and recreated from its declared template with a new
   invocation. An invocation with no matching container is revoked. Duplicate
   member identities and mismatched parent labels are quarantined and surfaced
   by `claw doctor`; the controller never guesses. `claw down` asks the
   controller to revoke and remove the complete dynamic descendant tree before
   Compose removes the base pod.

## Delete or demote

- `GenerateContextDir` and the loose-file context tree: behind the legacy adapter for one
  release, then removed with the context mount.
- Plaintext bearer in `metadata.json` and `cllama.GenerateToken`: removed.
  Per-service env files are single-use 0600 launch inputs and are deleted after
  the container is verified; they are not retained as runtime state.
- Compile-time `CLAW_HANDLE_*` for claws: demoted to non-claw services only.
- Duplicate `internal/cllama` manifest types: replaced by cllama's v1 wire types pinned by
  the conformance fixture.

## Invariants preserved

- ADR-002 as amended by ADR-027: compose is the base-pod lifecycle writer; only `claw-api` creates dynamic containers; workloads never receive the socket.
- ADR-013/017/019/020/021/023/025: feeds, pod defaults, model policy, tools, memory,
  ingress and policy semantics move inside the invocation unchanged.
- Release discipline; public artifacts never name downstream deployments; workloads never
  receive the Docker socket.

## Tests

- Unit: `BuildInvocationRequest` goldens (trading-desk, quickstart); two-phase up ordering
  and failure injection with a fake compose runner; env-file permissions and
  guaranteed cleanup; unchanged-member no-op and changed-member create,
  verify, revoke ordering; label-derived membership and orphan reconciliation
  with a fake Docker client; controller create/remove request shapes and
  rejection of caller-supplied image, command, mount, network, privilege, and
  host-path changes; parser tests for `role`/`labels`/`purpose`/`labels-defaults`; audit
  columns/filters; `pod-members` feed rendering from fixtures; claw-api/clawdash on a fake
  control API.
- Integration (`-tags integration`): register against the released cllama binary with an
  httptest upstream; revoke → 403; unchanged digest → no re-registration; conformance fixture
  pinned.

## Spikes (hermetic first; live drills as extra evidence)

- `TestSpikeRegistration` (S2, Docker, fake provider): two-phase up; pinned real
  images for all four retained drivers reach a real cllama with invocation
  bearers only; Anthropic Messages and OpenAI Responses requests reach the fake
  provider; one managed-tool loop completes; semantic parity holds; temporary
  bearer files are gone; audit columns are correct.
- `TestSpikeRegistrationFailureRecovery` (S2, Docker): inject failure after
  registration, after container creation, and before old-token revocation;
  assert no unreported orphan, no mixed invocation, deterministic recovery,
  and unchanged member container ids.
- `TestSpikeRollCall` (S2, live evidence): unchanged green.
- `TestSpikeSpawnRetire` (S3, Docker): CLI → `claw-api` → Docker API; no restarts of
  existing containers; declared-template enforcement; labels-derived `ps`;
  controller restart recovery from missing-container, missing-invocation, and
  duplicate-member cases; `invocation_revoked` after retire; `claw down`
  removes and revokes the descendant tree; no socket in any workload.
- `TestSpikeChildDelegation` (S4, Docker): child within ceiling succeeds;
  attempts to widen role, tool, model, memory view, budget, TTL, channel, or
  further delegation fail with no container and no record; caller-supplied
  Docker settings fail at the controller boundary.
- `TestSpikeMembershipFeed` (S5, Docker): existing members' next turn contains the join and,
  later, the departure; no env rewrite; no socket in workloads.
- `TestSpikeMemoryViews` (S6, Docker, fake memory service): metadata and isolation.
- Existing quickstart/docs spikes unchanged.

Every credential-free spike above is a required CI check and fails, rather than
skips, when Docker, the pinned cllama binary, a retained driver image, or a fake
dependency is unavailable. Credentialed Discord/provider drills remain extra
release evidence.

## Sequencing

```
cllama tag (v1 types/client, control API, delegation ceiling, pod-members feed contract, legacy adapter, fixture)
   └─► PR-1 BuildInvocationRequest + two-phase up + env files (semantic goldens; pin bump in the same release)
         └─► PR-2 role/labels/purpose schema + audit + claw-api/clawdash on the control API + pod-members feed
               └─► PR-3 ADR-027 + claw-api controller create/remove + `claw spawn/retire` + restart recovery
                     └─► PR-4 child delegation through claw-api (parent ceiling)
                           └─► PR-5 memory view example + S6 spike
```

Each PR carries `Closes #<n>`. Site changelog entries go under `## Unreleased`; no pin, badge,
or nav changes in feature PRs.
