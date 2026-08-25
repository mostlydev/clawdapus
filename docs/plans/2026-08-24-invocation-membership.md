# Invocation-registered claws and live membership (2026-08-24)

**Status:** DRAFT — plan branch `plan/invocation-membership`; ADR-027
(amending ADR-002, ADR-010, ADR-015, and ADR-022) will be written with PR-2;
an issue must be opened and
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
  `claw up` materializes infrastructure plus declared member templates; the trusted controller
  requests one invocation per member and creates its container with only the proxy URL and
  bearer. Acceptance: all four retained
  drivers reach cllama with a bearer and no provider credential; `claw audit` shows role and
  purpose; no workload invocation bearer remains in the runtime tree after container creation;
  declared Compose runtime semantics plus `claw ps`/logs/health/exec remain
  intact and the secret-free member descriptor renders as valid eject output;
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
- **S8 — an organization role onboards a claw exactly as it onboards a
  human-operated harness.** The pod names a declared effective organization
  role. `claw-api` selects the same cllama bundle entry a my-cli launch selects;
  it may add member identity, pod topology, a Flux task reference, and other
  bounded late runtime context, but it cannot author or replace the stable role
  contract, skills, tools, rules, memory, model policy, budget, or channels.

## Decisions

1. **Two-phase `claw up`, one member-lifecycle writer.** Phase one uses Compose
   for infrastructure and non-claw services only and waits for health. An
   organization-managed pod starts `claw-api` against the organization's
   central cllama URL; it does not start a pod-local cllama or publish an
   organization bundle. Its deployment `auth_ref` resolves an
   administrator-provisioned controller issuer bound to the pod's stable
   controller id. A standalone pod also starts a local cllama. The host uses a
   bootstrap bundle-admin credential to publish one explicit flat pod-local
   bundle, then creates a distinct controller issuer for `claw-api`; the
   controller never receives the bundle-admin credential. Required credential
   references are passed through Compose interpolation and neither raw value is
   written into `.claw-runtime` or generated YAML. If a trusted service fails
   health or either credential is absent, phase two does not run and the exact
   recovery command is reported.
   Phase two sends the complete desired set of declared member templates through
   the existing `docker compose exec claw-api` local-client path. Host Docker
   access remains pod-admin authority; no new host credential or public
   controller endpoint is introduced. The controller registers an Invocation
   per member and creates every claw container through the Docker API, injecting
   the proxy URL and bearer directly into the container environment. It stores
   no raw Invocation bearer. Re-running `claw up` compares template
   and Invocation-input digests: unchanged members stay running; changed
   members use create new Invocation, replace and verify one container, then
   revoke old Invocation; removed declared members are retired. A failed
   replacement revokes the new Invocation and reports the exact state. There is
   no context or bearer file transport.
2. **`internal/cllama` submits a role selection and runtime envelope; it does
   not author the role loadout or compile context.** `AgentContextInput` and
   `GenerateContextDir` are replaced by `BuildInvocationSelection(rc
   *driver.ResolvedClaw, ...)` emitting the v1 selection: subject `{kind:
   member, id: <pod>/<service>[/<ordinal>]}`, organization, declared effective
   role, labels, purpose, harness, expiry, pod/service/ordinal/image identity,
   and bounded late pod/Flux-task context. Stable contract modules, skills,
   business tools, memory policy, rules, models, budget, channels, and
   delegation resolve from the published role entry. `pod-members`, child-spawn,
   and other controller/runtime surfaces are an audited runtime envelope and do
   not alter the role-entry digest. Goldens assert the selection/late-input
   boundary and semantic equivalence through cllama's adapter, not byte
   equality.
3. **Pod schema.** Pod-level `x-claw.organization` identifies the published
   organization bundle. Service `x-claw.role` must name one declared effective
   role; arbitrary role arrays and attribute-derived unions are invalid.
   `x-claw.labels` (map, keys `[a-z0-9_.-]`) and `x-claw.purpose`; pod-level
   `labels-defaults` merges additively. `metadata.json` is no longer a file; `claw inspect`
   and clawdash show the invocation.
   An organization-managed pod uses the bundle already published to central
   cllama by my-cli's organization control plane. A standalone pod may publish
   one `pod-local`-provenance bundle through the same cllama API. Each local
   role entry is an explicit flat, single-source loadout containing its complete
   dependency set. Clawdapus does not resolve inheritance, invoke MGL, compose
   roles, infer transitive dependencies, or regain a complete-input create
   path; this limited bootstrap is not a second organization compiler.
4. **Live membership: one controller, Docker labels as state (ADR-002 amended by ADR-027).**
   Every member container carries labels (`claw.pod`, `claw.member`, `claw.declared`,
   `claw.role`, `claw.purpose`, `claw.invocation`, `claw.parent`, and input/template
   digests). `claw-api` is the only component with the Docker socket and the only writer for
   declared and dynamic claw containers. Reconcile, operator `claw spawn`/`claw retire`, and
   agent child spawn all use that controller. Compose remains the infrastructure/non-claw
   lifecycle writer; there is no lease log or authoritative lifecycle overlay. `claw ps` reads labels. `claw up`
   injects `claw-api` whenever any cllama-enabled service exists (today only for
   `x-claw.master`). ADR-027 records the amendment: the SDK is read-only *except* inside the
   controller for all members, which must carry the labels above and be reconstructible
   after controller restart. The controller accepts a declared member-template
   id plus the narrow role, purpose, labels, and parent fields only. Image,
   command, mounts, networks, privileged mode, socket mounts, and host paths
   come from a `claw up`-compiled controller template and cannot be supplied by
   either an operator request or a workload.
   Host `claw spawn` and `claw retire` reuse the existing local-client tunnel.
   A member child-spawn endpoint accepts that member's Invocation bearer,
   passes it to cllama's parent-create authority, and derives the parent from
   the resolved Invocation; it does not accept a caller-selected parent id.
   Existing `claw-api` principals remain for unrelated fleet surfaces, but no
   second per-member spawn credential is created. ADR-027 records this narrow
   exception to ADR-015's separate-credential rule.
   The organization bundle's canonical `controllers` map binds the stable
   controller id to one member-subject namespace and an allowed set of
   published roles. An administrator may issue the controller credential only
   for an entry in the current bundle, and cllama rechecks that entry on every
   create; issuer self-claims never confer scope. Thus a dynamically spawned
   member does not require an organization-bundle republish or a static
   `members` row. cllama still resolves the selected role entry and records its
   digest.
   The normalized template includes the complete runtime-relevant Compose
   service configuration. The controller attaches each member to the pod's
   declared Compose networks with the same aliases, waits for declared
   dependencies/health gates, and applies the canonical Compose project,
   service, ordinal, and other labels required by the repository's pinned
   minimum Compose version in addition to `claw.*` labels. A conformance test
   compares Docker inspect output for controller creation with normalized
   `docker compose config` output rather than relying on two labels as a proxy
   for compatibility.
5. **Compose remains an operator and eject surface, not a second writer.**
   `claw up` emits a secret-free, non-authoritative `compose.members.yml` from
   declared member templates, including empty-default Invocation-bearer
   placeholders. Clawdapus never applies this file for lifecycle. Together
   with `compose.generated.yml`, it remains a standard Compose description for
   inspection/debugging and can be rendered as an eject artifact once the
   operator supplies credentials, just as today's eject path depends on its
   `.env`.

   `claw ps`, `claw logs`, and `claw health` discover both infrastructure and
   members from canonical labels through the Docker SDK. The documented `claw
   compose exec <member> ...` path resolves a controller-owned member and execs
   it without recreating it; read-only Compose-compatible operations may use
   both descriptors. Lifecycle-mutating Compose passthrough remains available
   for infrastructure/non-claw services, but is rejected for member targets
   with remediation to the controller operation. ADR-027 amends ADR-002,
   ADR-010, and ADR-022 explicitly: one controller remains the only member
   writer, while the four-verb loop, debug/exec paths, network semantics, and
   inspectable/ejectable artifacts remain supported.
6. **`pod-members` feed.** `claw-api` serves `GET /feeds/pod-members` (ADR-013 shape) built
   from Docker labels plus cllama's invocation list: current member id, role,
   purpose, handles, routing metadata, and container creation time. It is a
   current-state snapshot, so a departure is represented by absence rather than
   an event ledger. The feed has zero cache TTL so the next turn observes the
   current set. `claw up` subscribes every claw by default (`feeds-defaults`
   gains `pod-members`; opt out per service).
7. **claw-api and clawdash read invocations.** `cmd/claw-api/agent_context.go` and the
   clawdash agents view move to `GET /control/v1/invocations[/{id}]` (secrets never
   returned). `claw audit` gains `ROLE`/`PURPOSE` columns and `--role`/`--label` filters.
   `X-Claw-ID` stays equal to the subject id for compatibility.
8. **Drivers emit controller templates.** Driver-visible runtime behavior stays
   the same, but `Materialize` returns a normalized container template rather
   than a Compose member service. The controller adds the registered bearer and
   common labels before Docker creation. No driver receives a provider or
   subscription credential.
9. **Controller reconciliation is fail-closed.** At startup, `claw-api`
   loads its rebuildable, private normalized-template store and compares every
   labeled member container with cllama's redacted invocation list.
   A live container whose invocation is missing, expired, revoked, or belongs to
   another pod is stopped and recreated from its declared template with a new
   invocation. An invocation with no matching container is revoked. Duplicate
   member identities and mismatched parent labels are quarantined and surfaced
   by `claw doctor`; the controller never guesses. The template store is an
   atomic controller-private volume containing no credentials and is never
   mounted into a workload. The pod file remains desired truth for declared
   templates; `claw up` can rebuild the store. If the store is missing or
   corrupt, the controller fails closed and asks for `claw up` rather than
   reconstructing authority from caller-controlled input or Docker labels.
   `claw down` asks the controller to revoke and remove every declared and
   dynamic member, including the descendant tree, before Compose removes the
   infrastructure pod. If the controller is unavailable, normal `claw down`
   stops and reports remediation; an explicit `--force` may remove
   infrastructure only after warning that outstanding Invocations must be
   revoked or allowed to expire.
10. **Clawdapus owns execution lifecycle, not durable business work.** Flux
    owns tasks, waits, wakes, retries, recurrence, checkpoints, and approval
    requests for Flux-managed work. For autonomous pod work, a controller-side
    Flux runtime adapter in `claw-api` uses its own `auth_ref` to poll and claim
    wakes assigned to that declared pod runtime, then starts a declared member
    template and requests a selection-based Invocation. This is a pull adapter,
    not a public `claw-api` endpoint: Flux receives no controller credential,
    and the adapter owns no copied readiness or workflow state. Missing or
    mismatched runtime assignment, template, role, task, or Flux credential
    fails closed before Invocation creation. A human laptop is not a daemon;
    Flux puts the task in the person's work queue and the person explicitly
    runs `my ai --flux-task <id>`, which creates a new Invocation. Existing
    `INVOKE` scheduling remains for standalone pod-local jobs only. A job is
    configured in exactly one mode: a Flux-managed job cannot also be
    independently scheduled by `INVOKE`.
11. **External portals and gated effects are ordinary services.** An API or
    UI-only browser broker is exposed through managed tools; its credential or
    session never reaches the claw. Flux Gate owns short-lived access grants.
    Flux owns approval state but performs no effect. The accounting, mail,
    source-control, payment, or other effect-owning service defines the
    canonical proposal schema and digest. It, or an isolated executor acting
    for it, issues an audience-bound, expiring, single-use signed release token
    after approval. The effect service validates issuer, signature, audience,
    proposal digest, expiry, and nonce, then consumes the nonce atomically.
    Flux stores only proposal/approval/wait/result references and never the
    signing key or effect authority. No `access[]`, approval state machine, or
    effect ledger is added to Clawdapus.

## Delete or demote

- `GenerateContextDir` and the loose-file context tree: behind the legacy adapter for one
  release, then removed with the context mount.
- Plaintext bearer in `metadata.json` and `cllama.GenerateToken`: removed.
  Bearers pass from cllama to the controller in memory and then directly into
  Docker container configuration; no bearer file is emitted.
- Authoritative generated Compose services for claws: replaced by controller
  member templates. A secret-free `compose.members.yml` remains as a
  non-authoritative inspection/eject descriptor and is never applied by
  Clawdapus lifecycle commands.
- Compile-time `CLAW_HANDLE_*` for claws: demoted to non-claw services only.
- Duplicate `internal/cllama` manifest types: replaced by cllama's v1 wire types pinned by
  the conformance fixture.
- Pod-authored stable role inputs and grants: replaced by published effective
  role selection. Standalone pod definitions compile into an explicit
  pod-local bundle rather than bypassing the bundle contract.

## Invariants preserved

- ADR-002 as amended by ADR-027: Compose owns infrastructure/non-claw
  services; only `claw-api` creates or removes claw/member containers;
  workloads never receive the socket.
- ADR-010/022 as amended by ADR-027: the four-verb operator loop, member
  logs/health/exec, declared network semantics, and standard eject descriptors
  remain available without granting Compose member-lifecycle authority.
- ADR-013/017/019/020/021/023/025: feeds, pod defaults, model policy, tools, memory,
  ingress and policy semantics move inside the invocation unchanged.
- Organization-managed claws and human launchers resolve the same effective
  role entry. Runtime-envelope tools and topology are separately provenance
  tagged and cannot widen the business loadout.
- Release discipline; public artifacts never name downstream deployments; workloads never
  receive the Docker socket.

## Tests

- Unit: `BuildInvocationSelection` and normalized member-template goldens
  (trading-desk, quickstart); two-phase infra/reconcile ordering and failure
  injection with fake Compose and controller clients; proof that no cllama
  control or Invocation bearer file is emitted; host operations use only the
  existing local-client tunnel; unchanged-member no-op and changed-member
  create, verify, revoke ordering; declared-member removal; label-derived
  membership, missing/corrupt template-store behavior, and orphan reconciliation
  with a fake Docker client; controller create/remove request shapes and
  rejection of caller-supplied image, command, mount, network, privilege, and
  host-path changes; normalized Compose-config versus Docker-create
  conformance for networks, aliases, mounts, ports, health checks, user,
  command/entrypoint, restart policy, and canonical labels; control-credential
  rotation across repeated up; secret-free `compose.members.yml`; member
  targeting for ps/logs/health/exec; rejection of member-targeted Compose
  lifecycle mutations; parser tests for `role`/`labels`/`purpose`/`labels-defaults`; audit
  columns/filters; `pod-members` feed rendering from fixtures; claw-api/clawdash on a fake
  control API.
- Unit: organization/effective-role selection; controller scope from the
  current bundle's `controllers` entry rather than issuer self-claims;
  rejection of stale, unknown, or disallowed controller ids; separation of
  bundle-admin and controller credentials; central-cllama organization mode
  with no local bundle transport; rejection of caller-authored stable
  inputs/grants; bounded late Flux task context; flat, explicit pod-local
  bundle provenance with inheritance/composition/inferred closure rejected;
  controller-side Flux poll/claim assignment checks; and mutual exclusion of
  Flux-managed versus standalone `INVOKE` scheduling.
- Integration (`-tags integration`): register against the released cllama binary with an
  httptest upstream; revoke → 403; unchanged digest → no re-registration; conformance fixture
  pinned from cllama's canonical testdata rather than copied locally.

## Spikes (hermetic first; live drills as extra evidence)

- `TestSpikeRegistration` (S2, Docker, fake provider): Compose starts infra,
  controller reconcile creates declared members, and pinned real
  images for all four retained drivers reach a real cllama with invocation
  bearers only; every protocol those drivers use (OpenAI Chat, OpenAI Responses,
  and Anthropic Messages as applicable) reaches the fake provider; one
  managed-tool loop completes; semantic parity holds; no bearer file exists;
  audit columns are correct. Each controller-created member has the expected
  Compose networks/aliases and canonical labels; `claw ps`, `claw logs`,
  `claw health`, and `claw compose exec` work; the combined secret-free Compose
  descriptors validate and render as an eject artifact, while attempting a
  member lifecycle mutation through Compose fails with remediation.
- `TestSpikeRegistrationFailureRecovery` (S2, Docker): inject failure after
  registration, after container creation, and before old-token revocation;
  assert no unreported orphan, no mixed invocation, deterministic recovery,
  and unchanged member container ids. Re-running unchanged `claw up` is a
  controller no-op; changing one template replaces only that member.
- `TestSpikeRollCall` (S2, live evidence): unchanged green.
- `TestSpikeSpawnRetire` (S3, Docker): CLI → `claw-api` → Docker API; no restarts of
  existing containers; declared-template enforcement; labels-derived `ps`;
  controller restart recovery from missing-container, missing-invocation, and
  duplicate-member cases; `invocation_revoked` after retire; `claw down`
  removes and revokes the descendant tree; unavailable-controller down fails
  closed; spawned members retain network/label semantics and work through
  ps/logs/health/exec; no socket in any workload.
- `TestSpikeChildDelegation` (S4, Docker): child within ceiling succeeds;
  attempts to widen role, tool, model, memory view, budget, TTL, channel, or
  further delegation fail with no container and no record; caller-supplied
  Docker settings fail at the controller boundary; the parent is derived from
  the Invocation bearer and no second member credential is accepted.
- `TestSpikeMembershipFeed` (S5, Docker): existing members' next turn contains the join and,
  later, the departure; no env rewrite; no socket in workloads.
- `TestSpikeMemoryViews` (S6, Docker, fake memory service): metadata and isolation.
- `TestSpikeRoleOnboarding` (S8, cross-repository, Docker): consume the same
  published billing role as my-cli; prove matching role/contract/skill/tool
  digests with distinct member runtime/principal binding; exercise fake
  accounting and browser-broker tools with no secret exposure; create a fake
  Flux approval wait; let the controller adapter poll and claim its assigned
  wake, then start a new Invocation; and prove the fake effect service rejects
  a missing, mismatched, changed, forged, expired, replayed, or wrong-audience
  release token.
- Existing quickstart/docs spikes unchanged.

Every credential-free spike above is a required CI check and fails, rather than
skips, when Docker, the pinned cllama binary, a retained driver image, or a fake
dependency is unavailable. Credentialed Discord/provider drills remain extra
release evidence.

## Sequencing

```
cllama tag (v1 types/client, control API, delegation ceiling, pod-members feed contract, legacy adapter, fixture)
   └─► PR-1 BuildInvocationSelection + controller member templates + two-phase infra/reconcile (semantic goldens; pin bump in the same release)
         └─► PR-2 ADR-027 + controller ownership of all claw containers + declared reconcile/recovery
               └─► PR-3 role/labels/purpose schema + audit + claw-api/clawdash reads + pod-members feed
                     └─► PR-4 `claw spawn/retire` + child delegation through the same controller
                           └─► PR-5 memory view example + S6 spike
```

Each PR carries `Closes #<n>`. Site changelog entries go under `## Unreleased`; no pin, badge,
or nav changes in feature PRs.
