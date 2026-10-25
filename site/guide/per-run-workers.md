---
outline: deep
---

# Per-Run Workers

A per-run worker is a one-shot claw: an external orchestrator starts one isolated worker for one unit of work, watches it, cancels it if it needs to, and learns how it ended. The orchestrator never touches Docker and never supplies container configuration.

Three pieces make this work:

- **A run template compiled by `claw up`.** A pod service declared with `x-claw.lifecycle: per-run` is resolved, validated and compiled like any claw, but it is not started.
- **`claw runs serve`.** A host process that launches, monitors and cancels runs over a local Unix socket.
- **Each run is its own Compose project.** `docker compose` stays the only lifecycle writer ([ADR-002](https://github.com/mostlydev/clawdapus/blob/master/docs/decisions/002-runtime-authority.md)), and the Docker SDK is used for reads only.

Every run gets a fresh cllama identity. Cancelling a run revokes that identity through cllama's durable revocation fence before the run reports `revoked`.

::: warning Requires a cllama with durable revocation
Per-run workers need a cllama that implements the operator revoke endpoint and `CLAW_REVOCATION_DIR` ([cllama#54](https://github.com/mostlydev/cllama/issues/54)). The supervisor proves this with a revocation self-test before it serves, and again before each worker starts. With an older proxy it refuses to start any worker.
:::

Clawdapus configures per-run PicoClaw workers to use OpenAI chat/completions. cllama v0.12.0 or newer serves those requests from a direct Anthropic key and from current OpenAI GPT-6 models; an older proxy needs OpenRouter for Anthropic models and fails on GPT-6. `CLAW_SPIKE_PINNED_CLLAMA=1 go test -tags spike -run '^TestSpikePerRunProviderLive$' ./cmd/claw` proves a real worker against the pinned proxy.

To keep a verifiable trace of every run, enable the cllama [trace journal](/guide/cllama#trace-journal) (cllama v0.13.0 or newer). Revoking a run identity then writes a coverage seal that accounts for every request the run made. `GET /v1/health` reports the capture epoch cllama last wrote, read fresh on each call (`null` when there is no usable epoch file, which does not by itself mean the journal is off), so an orchestrator can pin it before launching. Each run's status records the epoch observed just before its worker started. Both are read from cllama's epoch file and don't prove which proxy process is live: a valid file can outlive a stopped or restarting proxy. The seal's epoch remains the check.

v1 supports the **PicoClaw** runner only. Any other `CLAW_TYPE` with `lifecycle: per-run` fails `claw up`.

## Declaring a template

```yaml
x-claw:
  pod: work-queue
  cllama-defaults:
    proxy: [passthrough]
    env:
      ANTHROPIC_API_KEY: ${ANTHROPIC_API_KEY}
  models-defaults:
    primary: anthropic/claude-sonnet-5

services:
  worker:
    image: my-picoclaw-worker:latest
    x-claw:
      agent: ./AGENTS.md
      lifecycle: per-run          # default: service
      tools: [ticket-api]         # the grant: an upper bound for every run
      run:                        # optional limits
        max-runtime: 30m          # whole seconds, at least 1s
        max-authority: 2h         # furthest authority_expires_at a launch may set
        max-task-bytes: 32768
        max-result-bytes: 262144
        memory: 1g
        pids: 256
```

A per-run service may declare only `image` or `build` as Compose keys; the worker container itself is generated. These are rejected:

- `count`, `handles`, `invoke`, `memory`, `surfaces`, `skills`, `persona` and `claw-api`;
- an image that declares `VOLUME`;
- `x-claw.master` pointing at the template.

Workers have no channel identity, schedule, host mounts or persistent memory. Every business capability reaches them as a cllama-managed tool.

### Reasoning effort (Unreleased)

A template can ask for a reasoning effort per model slot:

```yaml
    x-claw:
      lifecycle: per-run
      models:
        primary: openai/gpt-6-luna
      reasoning-effort:
        primary: high             # none | minimal | low | medium | high
```

The worker then sends `reasoning_effort` on every request for that slot, and cllama forwards it (as `reasoning.effort` for models served through the Responses API). Slots without an entry send no effort, so the model's default applies. `claw up` fails if the service is not `lifecycle: per-run`, if the slot has no model, or if the value is not one of the five above. cllama still rejects a value that the selected model does not support, such as `minimal` on GPT-6 models. To offer the same model at several efforts, declare one template per effort.

### Managed-service bearer authentication

For a per-run consumer, a descriptor's `auth.env` resolves from that consumer's
`x-claw.cllama-env`, including inherited `cllama-defaults.env`. It does **not**
resolve from worker Compose `environment` (which remains forbidden), or look up
an undeclared host variable by name. Missing, blank or unresolved values stop
compilation. Long-lived consumers retain their existing environment-based auth.

Use one host `.env` reference for the proxy's bearer and the service's expected
ingress bearer. For example, with `READER_INGRESS_TOKEN` in the host `.env`:

```yaml
services:
  worker:
    image: my-worker:latest
    x-claw:
      agent: ./AGENTS.md
      lifecycle: per-run
      cllama: passthrough
      models:
        primary: openai/gpt-4o
      tools: [reader]
      cllama-env:
        READER_INGRESS_TOKEN: ${READER_INGRESS_TOKEN}
  reader:
    image: my-reader:latest
    expose: ["8080"]
    environment:
      READER_INGRESS_TOKEN: ${READER_INGRESS_TOKEN}
```

The reader's v2 descriptor declares:

```json
{"version":2,"auth":{"type":"bearer","env":"READER_INGRESS_TOKEN"}}
```

Add the reader's normal `tools` entries to that descriptor. The reader must
validate the incoming bearer itself; the descriptor instructs cllama to send
it, and does not add authentication to the reader implementation.

The resolved credential stays in trusted proxy/service configuration and private
template/proxy context artifacts. It never enters worker inputs, environment,
contract, runner config, rendered run project or public template JSON. Protect
the host `.env`; per-run generated Compose is `0600`, as is the template context
file. Proxy manifests are inside private `0700` context directories. Gateway
credentials for upstream services are separate from this ingress bearer.

Credential-only rotation does not change the template identity; endpoint, tool
and policy changes do. Recompile through the normal drain/stop-supervisor,
`claw up`, start-supervisor sequence when changing credentials. Do not edit
generated manifests or mutate live run contexts as a rotation mechanism.

## What `claw up` produces

- **`compose.generated.yml`** does not contain the per-run service. It adds a `claw-runs-<pod-ns>` network with `internal: true` and attaches cllama to it. Workers join only that network, keeping them off the tool-service and outbound pod networks. Managed business tool calls go through cllama.
  - Concurrent workers can reach each other at L3. Each run has a separate identity and tmpfs; this shared network boundary excludes the tool services and external routing.
  - For per-run pods the file is written `0600`, because it contains the cllama operator token.
- **`.claw-runs/templates/<service>.json`** holds the pinned image ID, the PicoClaw one-shot contract, the tool grant, the limits and the run network. Its template ID digests all of these together with the compiled cllama context, with credentials redacted.
- **`.claw-revocations/`** is mounted read-write into cllama as `CLAW_REVOCATION_DIR`. It is created `0700`, owned by the operator, and never rotated.

`claw up` and `claw down` share a pod lock with `claw runs serve`:

- Both refuse to run while the supervisor holds the lock.
- `claw up` also refuses while any run is unfinished. It would rotate `.claw-runtime` and recreate cllama under live workers.
- `claw down` cancels unfinished runs first. If cllama cannot acknowledge a revocation, `claw down` removes the whole pod, proxy included, and verifies it is gone. Those runs then end with `revocation_proof: proxy_stopped` instead of `cllama_ack`. If Docker cannot remove or verify the containers, teardown reports an error and retains unfinished state for recovery.

## Serving

```sh
claw runs serve --socket-dir /run/claw-runs --allow-uid 1001 \
  --callback-origin https://orchestrator.internal --callback-path '/api/runs/{run_id}'
```

The supervisor runs in the foreground; supervise it with launchd or systemd. Startup happens in this order:

1. Take the pod lock and hold it for the whole lifetime.
2. Run the revocation self-test.
3. Refuse to serve if labelled run workers exist that the ledger does not know (`claw runs doctor` lists them, and `--remove-orphans` removes them through Compose).
4. Reconcile every unfinished run against the containers that actually exist.
5. Accept connections.

Shutdown waits for every in-flight request before releasing the lock. Lifecycle subprocesses inherit the lock, so a killed supervisor's in-flight `compose up` keeps writer authority until it exits.

### Trust model

The socket lives in `--socket-dir` (default `<pod>/.claw-runs/sock`):

- The directory must be owned by the supervisor user, with mode `0700` or `0750`.
- No ancestor may be group- or world-writable unless it carries the sticky bit.

Authentication runs in both directions:

- **The server checks the client.** It reads the peer's UID from the connected socket (`SO_PEERCRED` / `LOCAL_PEERCRED`) and only accepts the supervisor's own UID and any `--allow-uid`.
- **The client checks the server.** The Go client (`internal/runs`) verifies the directory's owner and mode, then checks the server's peer UID on every connection.

This is a **trusted-host** design. A peer-UID check authenticates a Unix account, not a program. Root or the supervisor's UID can impersonate either side, and in v1 every allowed UID holds every template and installation grant of the pod.

## API (v1)

The API is HTTP/1.1 over the Unix socket, with JSON bodies:

- requests are at most 64 KiB, with unknown fields rejected;
- body reads have a 10 s deadline; long-polls wait up to 60 s, with a 70 s response write budget;
- errors use the shape `{"error":{"code":"request_invalid","message":"..."},"run":null}` and never echo task text or credentials.

A run is identified by `installation` (`[a-z0-9][a-z0-9-]{0,63}`) and `run_id` (`[A-Za-z0-9_-]{1,128}`).

| Call | Body | Result |
|---|---|---|
| `POST /v1/runs` | `{installation, run_id, generation, spec_digest, spec, authority_expires_at, credential?}` | `202` for a new run, `200` for an idempotent retry |
| `GET /v1/runs/{installation}/{run_id}` | — | Status. `?wait=running\|revoked\|stopped\|failed&timeout=30s` long-polls. `404 not_found` means the run was never accepted. |
| `POST /v1/runs/{installation}/{run_id}/cancel` | `{generation}` | Status. Idempotent, and also tombstones an unknown key. Accepts `?wait=`. |
| `POST /v1/runs/{installation}/{run_id}/credential` | `{generation, credential, authority_expires_at?}` | Replaces the held credential and optionally extends authority. Persisted before the ACK. |
| `GET /v1/runs/{installation}/{run_id}/result` | — | The bounded answer (`X-Claw-Result-Truncated` header) |
| `GET /v1/identity`, `GET /v1/health` | — | Protocol version, pod and supervisor UID |

Launch errors:

| Code | Status | Meaning |
|---|---|---|
| `digest_invalid` | 400 | `spec_digest` does not match the canonical spec |
| `digest_mismatch` | 409 | The run exists with a different digest |
| `cancelled` | 409 | The run was cancelled. A new attempt needs a new `run_id`. |
| `stale_generation` | 409 | The generation is below the one already recorded |
| `grant_exceeded` | 422 | The spec or `authority_expires_at` exceeds the template |
| `template_unknown` | 422 | The template is not compiled for this pod |
| `callbacks_disabled` | 422 | A credential was sent but no callback origin is configured |

### Spec and digest

```json
{"template":"worker","task":"Summarize ticket 42","tools":["ticket-api.get_ticket"],"max_runtime_seconds":900}
```

The spec allows only strings, integers in ±(2^53−1), and string arrays. Unknown fields, duplicate keys, invalid UTF-8 and lone surrogate escapes are rejected.

`spec_digest` is `"sha256:" + hex(sha256(JCS(spec)))`, using [RFC 8785](https://www.rfc-editor.org/rfc/rfc8785) canonical JSON:

- keys are sorted;
- there is no whitespace;
- only `"`, `\` and control characters are escaped;
- every other character, including U+2028 and U+2029, is literal.

Ruby's `JSON.generate` over a key-sorted hash produces the same bytes for this subset.

`tools` must be a subset of the template grant, and an empty list removes every managed tool. The credential is outside the spec, so the digest never covers a token.

### Generation

`generation` is a positive integer used as a monotonic fence per run. A command whose generation is below the recorded one gets `stale_generation`. A cancelled run never comes back: every later launch for that key is refused, whatever its generation.

### States

```
accepted ─► running ─► stopped    (exit 0 and an answer was found)
   │           ├─────► failed     (exit_nonzero, timeout, authority_expired,
   │           │                   spawn_error, result_missing, mismatch)
   │           └cancel► revoking ─► revoked ─► stopped
   └─cancel──────────► revoking ─► revoked ─► stopped   (no container ever created)
```

- **`revoked`** requires a terminal cllama `200` plus an emptied tool allowlist. Full-pod teardown may instead prove the proxy and workers stopped and remove the run context, recorded as `revocation_proof: proxy_stopped`. Neither proof undoes an already-dispatched remote effect.
- **`stopped` or `failed`** is reported only once no container carries the run's labels.
- **A worker exiting is observed, not terminal.** The run reaches a terminal state only after revocation and removal.
- **If revocation is unavailable**, the worker is still removed, but the run stays `revoking` (`revoked: false`) and keeps retrying.
- **Every run's identity is revoked when it ends**, even on a natural exit. Cleanup passes through `revoking` and `revoked` for natural exits and failures too. `cancelled` records an explicit cancel; cleanup state alone does not imply cancellation.

Status carries several fields beyond the state:

- `run_agent_id`, the cllama identity, for reconciling against proxy audit;
- `revocation_proof`;
- `exit_code`, `failure` and `result`;
- `credential`: `held`, `missing` or `none`;
- `callbacks.start` and `callbacks.end`: `acked`, `rejected`, `abandoned` or `skipped`;
- `deadline`, `authority_expires_at` and timestamps;
- `pending`, which explains a step that is being retried;
- `capture_epoch` and `capture_epoch_observed_at` (Unreleased), the trace capture epoch observed once at spawn and never rewritten. Omitted when there is no usable epoch file.

### Authority, credentials and callbacks

Every launch carries `authority_expires_at`, capped by the template's `max-authority`. When it passes, the supervisor revokes the run, removes the worker and ends the run as `failed` with reason `authority_expired`, whether or not any callback ever succeeded.

If the supervisor was started with `--callback-origin`, a launch may hand over an opaque per-run `credential`. The supervisor stores it in a `0600` file. It never reaches the worker's environment, arguments or mounts, and never appears in logs. The supervisor uses it only for this callback contract:

```
POST {origin}{path}/start      {installation, run_id, generation}
POST {origin}{path}/heartbeat  {installation, run_id, generation}       every --heartbeat
POST {origin}{path}/end        {installation, run_id, generation, outcome, reason, summary, summary_truncated}
```

`outcome` is `succeeded` or `failed`; `reason` is empty on success or the failure code. `summary` contains at most 64 KiB of UTF-8 text. `summary_truncated` is true if either the stored result was truncated or the callback summary limit shortened it. The bounded stored answer remains available through the result endpoint.

The callback rules are:

- The origin comes from configuration, never from the caller. Redirects are not followed.
- `start` is always resolved before `end` for any worker that actually ran. No `end` is sent after a cancel.
- A `401` or `403` means the orchestrator's authority has ended, and the run is torn down.
- If the credential is lost (`credential: missing`), retry the launch with the same spec and a reissued credential. The run keeps its identity and never starts a second worker.

## The worker container

The rendered project contains one container with the following properties:

- **Image and process:** it runs the template's pinned image ID, overrides the entrypoint, and runs as `init` with uid `65534`.
- **Hardening:** the root filesystem is read-only, with `cap_drop: [ALL]` and `no-new-privileges`, plus memory and pid limits. It has a tmpfs `/tmp` and no other writable path.
- **Isolation:** there are no bind mounts, no volumes, no Docker socket and no host namespaces. The only network is the run network.
- **Inputs:**
  - The task, the PicoClaw v3 config (including the run's cllama token) and the contract reach the container through Compose interpolation at `compose up`. The rendered project file contains placeholders. The private ledger retains the immutable spec, including task text; tokens live in separate `0600` secret files for restart recovery.
  - A pinned shell wrapper writes the config into tmpfs, removes it from the environment, and runs `picoclaw agent -m "$1" -s "$2"`. The task is passed only as a positional argument.
- **Native tools:** PicoClaw's native tools that act outside the conversation (`exec`, `web_fetch`, file writes, `spawn` and others) are disabled even if an image `CONFIGURE` enables them.
- **The answer:**
  - The answer is read from the worker's bounded stdout: everything after the first `\n🦞 ` marker.
  - An exit of 0 with no marker is `result_missing`, never an empty success.
  - An answer cut at `max-result-bytes` is reported as truncated.

The one-shot contract is pinned and verified against PicoClaw v0.2.7.

## Operations

| Command | Purpose |
|---|---|
| `claw runs serve` | Run the supervisor |
| `claw runs launch <inst> <run> --spec spec.json [--authority-ttl 30m] [--credential-env VAR]` | Launch a run, for operators and tests |
| `claw runs status <inst> <run> [--wait stopped]` | Show a run's status |
| `claw runs cancel <inst> <run> --generation N [--wait stopped]` | Cancel a run |
| `claw runs result <inst> <run>` | Print the answer |
| `claw runs doctor [--remove-orphans]` | Check the ledger and find orphaned workers |

Durable state lives in `<pod>/.claw-runs/` (mode `0700`):

- the ledger, with one record per run;
- `0600` secret files;
- tombstones;
- rendered projects;
- results;
- the pod lock.

cllama's revocation ledger in `.claw-revocations/` grows by about 80 bytes per run, up to a 16 MiB bound. See the cllama README for capacity planning.

Already-dispatched remote tool effects are never undone by a cancel. cllama's audit records which dispatches were admitted before the revocation.
