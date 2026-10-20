# Per-run worker supervisor (2026-10-20)

**Status:** Agreed in review (2026-10-20), issue #372. Companion: cllama#54 (revocation fence).

## Goal

An external orchestrator launches one isolated, ephemeral worker per unit of
work. It observes the worker, cancels it, and learns how it ended. It never
touches Docker and never supplies container configuration.

## Shape in one paragraph

`claw up` compiles a **run template** from a pod service marked
`x-claw.lifecycle: per-run`. The service is resolved, validated and built like
any other claw, but it is not started. A new host process, `claw runs serve`,
listens on a Unix socket. On a launch it:

1. checks the request against the template grant;
2. persists intent in a durable ledger;
3. mints a fresh cllama identity for the run;
4. renders a **per-run Compose project** that joins the pod network;
5. starts it with `docker compose`.

On a cancel it tombstones the run, calls cllama's revoke primitive (cllama#54),
and only then reports `revoked`. It then tears the project down and reports
`stopped`. Compose stays the only lifecycle writer (ADR-002). The Docker SDK is
used for reads only. Workers get no socket and no host mounts.

## Why host-side and per-run Compose (and not claw-api)

- **The socket.** A Unix socket with peer-UID checks only works on the host. On
  macOS, Docker runs in a VM, so a socket bind-mounted out of a container is
  not a reliable host endpoint.
- **ADR-002.** A per-run project keeps `docker compose` as the sole writer.
  Having claw-api create containers through the SDK would need ADR-027, a draft
  on an unmerged plan branch that also depends on unshipped cllama Invocation
  APIs. This design does not block that direction: the run ledger, labels and
  template boundary carry over. A later migration moves the writer into the
  controller and does not change the wire contract.
- **The host already owns the inputs.** `claw up` already runs on the host,
  writes `.claw-runtime/context/` (cllama reads it per request) and owns
  `compose.generated.yml`.

## Pod schema

```yaml
services:
  worker:
    image: my-picoclaw-worker:latest   # or build:
    x-claw:
      agent: ./AGENTS.md
      lifecycle: per-run                # new; default "service"
      models: { primary: anthropic/claude-sonnet-5-5 }
      tools: [service://ticket-api]     # the GRANT: the upper bound for any run
      cllama: passthrough
      run:                              # new, optional limits
        max-runtime: 30m
        max-task-bytes: 32768
        max-result-bytes: 262144
        memory: 1g
        pids: 256
```

Validation:

- a per-run service must be cllama-enabled;
- `count` is rejected;
- `handles` and channel surfaces are rejected, because a worker has no Discord
  identity;
- `invoke` and schedules are rejected;
- `memory` is rejected in v1;
- validation goes through a dedicated per-run path
  (`driver.RunTemplateValidator`), so a long-lived service's requirements
  (for example, PicoClaw rejecting zero channels) are never weakened;
- the driver must declare one-shot support. That is PicoClaw only in v1; every
  other `CLAW_TYPE` fails closed with a clear message.

## Compile output (`claw up`)

- `compose.generated.yml` omits per-run services.
- `.claw-runs/templates/<service>.json` (0600, contains no secrets):
  - `template_id`, a digest over the normalized template;
  - the pinned image ID (resolved at `claw up`);
  - driver type, user, workdir and command shape;
  - pod network names, resolved from the generated compose and the project
    name;
  - the cllama proxy URL;
  - the tools grant, as the tool names in the compiled `tools.json`;
  - model slots;
  - limits.
- `.claw-runs/templates/<service>/context/`: the compiled cllama context for the
  template, with an empty `token`. At launch the supervisor clones it into
  `.claw-runtime/context/<run-agent-id>/`, writes a fresh token into
  `metadata.json` and filters `tools.json` down to the spec allowlist.
  `service-auth/` stays proxy-side, as today. `metadata.json` model policy is
  **rebuilt** for the selected model grant through `InjectCompiledModelPolicy`.
  Pointing the PicoClaw config at a slot is not enough: the worker can send
  raw proxy requests, so its config is no authority boundary. An empty tools
  allowlist writes an explicit empty `tools.json` manifest, which removes
  every managed tool. Feeds and `service-auth` entries are filtered to the
  surviving tools only.
- `.claw-revocations/` (0700) is mounted read-write into cllama as
  `CLAW_REVOCATION_DIR` for every cllama pod. It is proxy-owned, and
  `claw up` never rotates it.
- **One pod lock, one writer.** `claw runs serve` holds an exclusive `flock`
  on `<pod>/.claw-runs/lock` for its whole lifetime, idle or not.
  - `claw up` and `claw down` take the same lock without blocking, and fail
    closed with "stop `claw runs serve` first" while it is held.
  - Without a supervisor, `claw up` also refuses when the ledger has any
    non-terminal run, because it would rotate `.claw-runtime` and recreate
    cllama under them.
  - `claw down`, once it holds the lock, finalizes non-terminal runs itself
    through the same `internal/runs` code (cancel, revoke, compose down,
    verify) before tearing down the pod. There is never a second independent
    writer.

## Supervisor process

`claw runs serve [-f claw-pod.yml] --socket-dir DIR --allow-uid UID[,UID]`

- Runs in the foreground; it is meant to be supervised by launchd or systemd.
  One supervisor per pod; an exclusive `flock` on the ledger dir enforces this.
- Reads templates and the ledger. Refuses to start when the templates are stale
  relative to `claw-pod.yml` (the same rule as the lifecycle commands).
- Restart reconciliation runs **before** the socket accepts connections (see
  below).

### Transport and auth

- Unix socket at `DIR/claw-runs.sock`. `DIR` must be owned by the supervisor
  UID, mode 0700 or 0750, and no ancestor may be group- or world-writable
  except for the sticky bit. The socket file is 0660.
- **Server authenticates client:** the peer credential (`SO_PEERCRED` on Linux,
  `getpeereid` on darwin) must be in `--allow-uid`. All other peers are
  rejected before any request is read.
- **Client authenticates server:** the client verifies that the socket and its
  directory are owned by the expected supervisor UID with the modes above. It
  also checks the server's peer credential (`SO_PEERCRED` / `LOCAL_PEERCRED`)
  on the connected socket, because path ownership alone is open to rename and
  TOCTOU races. A Go client helper (`internal/runs/client`) does this, and the
  contract documents it for other languages. In v1, every allowed UID holds
  every template and installation grant. `GET /v1/identity` returns the supervisor
  UID, the pod and the protocol version. **Trusted-host assumption:** anyone
  with root or the supervisor UID can impersonate either side. Peer-UID checks
  authenticate a Unix account, not a program.
- HTTP/1.1 over the socket, JSON, `max request` 64 KiB, a per-request deadline
  of 10 s (except long-poll waits, which take an explicit `wait` of 60 s or
  less). Errors carry `{error:{code,message}}` and never echo secrets or spec
  content.

### API (v1)

Key: `installation` (`[a-z0-9-]{1,64}`) plus `run_id` (`[A-Za-z0-9_-]{1,128}`).

| Call | Body | Result |
|---|---|---|
| `POST /v1/runs` | `{installation, run_id, generation, spec_digest, spec, credential?}` | `202 {run}`. Idempotent. Same key and same digest returns the current `{run}`. A different digest is `409 digest_mismatch`. A tombstoned key is `409 cancelled`. A spec over the grant is `422 grant_exceeded`. |
| `GET /v1/runs/{inst}/{run}` | — | `200 {run}`, or `404 not_found`, which means never accepted. `?wait=<state>&timeout=60s` long-polls until that state or a terminal state. |
| `POST /v1/runs/{inst}/{run}/cancel` | `{generation}` | `200 {run}`. Idempotent. Tombstones even an unknown key. `?wait=revoked` blocks until the fence holds. |
| `POST /v1/runs/{inst}/{run}/credential` | `{generation, credential}` | `200 {run}`. Replaces the orchestrator credential, serialized with launch and reconcile. |
| `GET /v1/runs/{inst}/{run}/result` | — | Bounded result bytes once `stopped`. |
| `GET /v1/health`, `GET /v1/identity` | — | |

`spec` (integers, strings, arrays and objects only):

```json
{"template":"worker","task":"…","tools":["ticket-api.get_ticket"],
 "model":"primary","max_runtime_seconds":900}
```

- `spec_digest` is `"sha256:" + hex(sha256(canonical_json(spec)))`, where
  canonical JSON means sorted keys, no whitespace, UTF-8 and no HTML escaping.
  The supervisor recomputes it and rejects a mismatch with
  `400 digest_invalid`.
- The credential is outside the spec, so no token hash ever reaches the digest.

`run` (status):

```json
{"installation":"…","run_id":"…","generation":3,"spec_digest":"sha256:…",
 "state":"running","revoked":false,"exit_code":null,
 "result":{"bytes":1234,"truncated":false},
 "credential":"held|missing|none","callbacks":{"started":"acked","end":"pending"},
 "timestamps":{"accepted":"…","running":"…","revoked":null,"stopped":null},
 "error":null}
```

### States

```
accepted ─► running ─► stopped           (exit 0; the result is read)
   │           ├────► failed             (non-zero exit, timeout, spawn error)
   │           └─cancel─► revoking ─► revoked ─► stopped
   └─cancel─► revoking ─► revoked ─► stopped   (no container is ever created)
```

- `revoked` is reported only after both of these hold:
  1. the cllama revoke endpoint returned 200 for the run's agent ID;
  2. the run's tool allowlist was emptied.
- `stopped` means the project is down and no container carries the run labels.
- **An observed exit is not terminal.** On a natural exit, a spawn error, a
  timeout or an authority expiry, the run keeps its state (`running`, or
  `revoking` after a cancel) and gains `exit_observed{code,at}`. It moves to
  `stopped` or `failed` only after both of these hold:
  1. cllama revoke has returned 200 for `run_agent_id`;
  2. no container carries the run labels.

  A cllama 503 keeps the run pending, with retry and backoff, across restarts.
  Natural-exit revocation is recorded in the `revoked` boolean and is not
  reported as a cancel claim.
- **Terminal cleanup:** the project is down, the per-run context dir is
  removed, the per-run secret files are removed and the tombstone is kept.

### Generation

`generation` is a positive integer and a monotonic fence per run key. The
supervisor stores the highest one it has seen. A command whose generation is
below the stored one gets `409 stale_generation`. A run identity never revives:
after a cancel, any launch with that key is refused whatever its generation.

### Ordering guarantees

Launch, cancel, credential replacement and reconciliation for one key are
serialized by a per-key mutex.

- **Launch:**
  1. Authenticate the peer and validate the request. No side effects happen
     before this point.
  2. Check the grant and `authority_expires_at`.
  3. Mint the run identity: `run_agent_id = <template>-run-<128-bit hex>` and
     the cllama secret. Write the secret, plus the credential if given, to
     0600 per-run files and fsync them.
  4. Write the `accepted` record and fsync it. It holds the key, generation,
     digest, `template_id`, image ID, `run_agent_id`, the project name and
     path, and the deadlines.
  5. Respond `202`.
  6. Asynchronously:
     1. write the context;
     2. render the project file, and record it before `compose create`;
     3. `create`, then `cp` the config, then `start`;
     4. mark `running`.

  Spawn and reconcile only **read** the recorded identity. They never mint, so
  a retry cannot leave a second live proxy context behind.
- **Cancel:**
  1. write the tombstone (fsync) before any other action;
  2. empty the per-run `tools.json`;
  3. call the cllama revoke with the run generation;
  4. mark `revoked`;
  5. run `compose down -v --remove-orphans`;
  6. verify that no container carries the run labels;
  7. mark `stopped`.

  Because a cancel is serialized with a launch and the tombstone is written
  first, a cancel always beats a delayed launch. If the launch's async spawn is
  in flight, the cancel waits for it to reach a point where the project exists
  or never will.
- **Lost acknowledgement:** a retried launch finds the ledger record and
  returns its state, so it never spawns twice. The project name is
  deterministic (`clawrun-<sha256(inst/run)[:16]>`), so even a crash between
  `compose up` and the ledger update converges on one project.

### Restart reconciliation (before serving)

For every non-terminal ledger record, the supervisor looks up containers by
label (`claw.run.key`) through the SDK, which is read-only. It verifies the
key, generation, `spec_digest`, `template_id` and image ID against the ledger.
A mismatch ends the run as `failed(mismatch)`, torn down through the
**recorded** project file.

| Ledger state | Containers found | Action |
|---|---|---|
| accepted | none | Resume the spawn, or abandon it as `failed` with `spawn_interrupted` if the run is past its deadline. |
| running | one running | Resume monitoring. |
| running | exited | Read the result, then finalize. |
| any | more than one | Stop all of them, mark `failed` with `duplicate`, and report. This should never happen. |
| revoking | any | Re-run cancel from the revoke step. Revoke is idempotent. |

A labelled container with no ledger record can only exist after the ledger is
lost, because the project is recorded before `compose create`. The supervisor
then **refuses to serve**, and `claw runs doctor` lists those containers for
the operator. It never adopts them, and it never builds project names from
untrusted labels.

### Orchestrator credential and lifecycle callbacks

- The orchestrator may hand over a per-run `credential` (an opaque bearer).
- The supervisor holds it in the ledger, in a separate 0600 file per run that
  is never mounted, and uses it **only** for the lifecycle callbacks below. The
  worker never sees it.
- The callback base is `callback-origin` (supervisor config, not caller input)
  plus a templated path from config, e.g. `/api/runs/{run_id}`.

Clawdapus defines the callback contract:

- `POST {base}/start` once the run is `running`;
- `POST {base}/heartbeat` every `heartbeat-interval` while `running`;
- `POST {base}/end` with `{outcome: succeeded|failed, reason, summary}` on a
  natural end. There is no end callback after a cancel: the orchestrator
  already knows.

Delivery is at-least-once, and the ack status is recorded per callback.

- **Authority expiry.** A launch requires `authority_expires_at` (RFC3339),
  capped at now plus the template's `max-authority`. Only a credential
  replacement can extend it.
  - At expiry, the supervisor revokes, tears the run down and ends it as
    `failed(authority_expired)`. This happens whether or not any callback ever
    succeeded, which is acceptance #8.
  - A `401` or `403` from a callback triggers the same path early.
  - `max_runtime_seconds` is a separate, immutable spec field.
- **Credential replacement** is serialized under the per-key lock and
  persisted (fsynced) before the ACK.
- **Credentials never ride:** argv, env, error text, logs, or a redirect.
  Callbacks use a client with redirects disabled, and every target must match
  the configured origin exactly.
- **Cancel of an unknown key:**
  - It is terminal at once (tombstone written, `stopped`, `revoked:false`).
  - This is safe because the `accepted` record is fsynced before any side
    effect, and both commands are serialized under the per-key lock. An
    unknown key under that lock therefore proves that no agent, context or
    spawn intent exists.
  - A cancel of a recorded key always goes through the stable ledger identity
    and the cllama fence.
- A crash before `accepted` is fsynced loses the credential. The orchestrator
  sees `404 not_found` and retries the launch with a reissued credential. A
  crash after `accepted` keeps it.
- `credential: missing` (accepted, but the credential file is lost) is
  reported distinctly, so the orchestrator reissues only when it needs to.
- With no callback origin configured, the credential is refused
  (`422 callbacks_disabled`). Status polling still works.

### Worker container (rendered per-run project)

- **Image and command:** the pinned image ID from the template, run with
  `picoclaw agent -m <task>`. The one-shot contract is still to be pinned; see
  PicoClaw below.
- **Labels:** `claw.pod`, `claw.role=run-worker`, `claw.run.key`,
  `claw.run.installation`, `claw.run.id`, `claw.run.generation`,
  `claw.run.template`.
- **Hardening:** `read_only: true`, `cap_drop: [ALL]`,
  `security_opt: [no-new-privileges:true]`, `pids_limit`, `mem_limit` and a
  tmpfs `/tmp`.
- **Storage and network:**
  - no bind mounts, no Docker socket, no host PID or network namespace;
  - the home and result directory is a project-scoped named volume;
  - it joins only a dedicated `claw-runs` network. `claw up` emits it with
    `internal: true` whenever per-run templates exist, and attaches cllama to
    it as well as to cllama's existing networks.
  - Workers cannot reach tool services, other claws or egress directly. Every
    tool call goes through cllama.
  - A test asserts the rendered project has exactly one network, that it is
    external and internal, and that no tool service is on it.
- **Pinned process:** the template pins `entrypoint` and `command`, so the
  image `ENTRYPOINT` is overridden. `create` runs nothing. The config is
  seeded with `docker compose cp` from a 0600 temp file in a 0700 dir, which
  is removed right away.
- **Configuration:**
  - the driver config, which holds the run's cllama token as `api_key`, is
    streamed into the created container with `docker cp -` (a tar on stdin);
  - the order is `compose create`, then `cp`, then `compose start`;
  - no secret file is ever written on the host outside the cllama context dir
    (true of today's context tokens too);
  - env carries only `CLLAMA_URL`-shaped settings and none of the
    orchestrator's values.
- **Deadline:** `max_runtime_seconds`, capped at the template's `max-runtime`.
  When it fires, the supervisor revokes, stops and marks `failed` with
  `timeout`.

### Result

- Read with `docker cp <ctr>:<result-path> -`.
- The tar entry must be a regular file: symlinks, hardlinks and devices are
  rejected.
- The read is bounded by `max-result-bytes`; anything over that is reported as
  `truncated`, and no extra bytes are read.
- Stored at 0600 under `.claw-runs/results/<key>`.

## PicoClaw one-shot contract

Pinned against upstream v0.2.7 by reading the source and by host tests
against a stub provider. **Still unproven in a Linux container**, so the
renderer is gated on a container conformance test.

- **Invocation:** `picoclaw agent -m <task> -s agent:main:<run-key>`. It needs
  no gateway. The session key has to have the `agent:<id>:<rest>` shape;
  anything else is silently ignored and all runs end up sharing one session.
- **Config:** a file is required, because `model_list` has no env binding.
  - The per-run generator emits a **v3** config (`"version":3`, with
    `api_keys:[token]`). Without the version, PicoClaw migrates the config on
    every start and writes beside it, which fails on a read-only home.
  - Old-style `api_key` in a v3 config silently sends no auth header. A golden
    test guards that.
  - The file is seeded with `docker compose cp` between `create` and `start`.
- **Output:** the answer is printed on stdout as `\n🦞 <answer>\n`, after a
  banner. Set `NO_COLOR=1`, and set `PICOCLAW_LOG_FILE=/tmp/picoclaw.log` so
  logs leave stdout.
  - The supervisor reads stdout only, bounded, through the SDK container logs
    (a read).
  - It takes the bytes after the first `\n🦞 ` marker that follows the banner.
  - A version-pinned conformance test covers this parse. No result file is
    needed, which drops the `docker cp` result read.
- **Exit codes:** 0 on success, 1 on a provider, config or auth failure. A
  failing tool still exits 0, because the error goes back to the model.
  Mapping: 0 is `stopped`; anything else is `failed` with
  `exit_code` (stderr tail is not returned; it may echo provider text).
- **Writable paths:** `<workspace>/sessions` on a tmpfs. The home and config
  are read-only once seeded. `/tmp` is a tmpfs.
- **Native tools are on by default** (exec, web_fetch, write_file, spawn,
  and others). The per-run config disables every native tool that can be
  disabled. Some can't be disabled (`read_file`, `list_dir`, `load_image`,
  `reaction`, `message`, `send_file`), so the container itself is the
  boundary: no network but `claw-runs`, a read-only rootfs, no secrets but the
  run's own cllama token, which is revoked when the run ends. Business tools
  are cllama-managed only.
- **SIGTERM:** there is no handler, but it exits promptly. Use `init: true` so
  that PID 1 forwards the signal.
- **Image pin:** `claw pull` builds `picoclaw:latest` from an unpinned
  upstream `:latest`. A per-run template pins the resolved image ID at
  `claw up`, and the conformance test records the PicoClaw version it was
  proven on. Pinning the upstream base itself is a separate issue.

## Phasing (PRs)

1. Pod schema, template compile, validation, and the `claw up` guards (no
   daemon yet). Unit tests and goldens.
2. `internal/runs`: ledger, state machine, digest and grant check, renderer,
   and a compose/docker executor behind an interface. Unit tests with fake
   executors for acceptance cases 3, 4, 6, 7 and 9.
3. `claw runs serve` plus the socket, peer auth, client helper, reconciliation
   and cllama revoke client. Unit tests over a real Unix socket.
4. Callbacks and credential custody, against an httptest orchestrator.
5. Spike `TestSpikePerRunWorker` (Docker, fake provider, real cllama with #54)
   covering all nine live acceptance items. This needs the cllama release.

Release gate: cllama#54 ships as a cllama tag and image first. The submodule
pointer and `DefaultCllamaTag` move only in the maintainer's release; the PR
body states this.

## Open questions

1. Credential custody: the callback contract above, or status-only? The
   recommendation is custody with an allowlisted origin.
2. Is one supervisor per pod right, or one per host spanning pods? Per pod is
   simpler and matches `claw up`.
3. Should `claw down` cancel live runs first? Proposed: yes, `claw down` runs
   cancel-all through the socket if a supervisor is up, otherwise through the
   ledger plus revoke directly, then removes the projects.
