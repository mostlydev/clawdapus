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

The stateless profile described on this page supports the **PicoClaw** runner only. The opt-in [task-home profile](#task-homes) supports **Hermes**. Any other combination fails `claw up`.

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

### Reasoning effort

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

### OpenRouter provider preferences

A template whose slot routes through OpenRouter can pin that slot's upstream endpoints:

```yaml
    x-claw:
      lifecycle: per-run
      models:
        primary: openrouter/google/gemini-3.8-flash
      reasoning-effort:
        primary: high
      provider-preferences:
        primary:
          only: [google-vertex/global]   # OpenRouter endpoint slugs
          allow_fallbacks: false
          require_parameters: true
          zdr: true
          data_collection: deny
```

The worker sends these as OpenRouter's `provider` object on every request for that slot, beside `reasoning_effort`. Only these five keys are accepted, and each can only narrow routing:

- `zdr`, `require_parameters`: `true` only;
- `allow_fallbacks`: `false` only;
- `data_collection`: `deny` only;
- `only`: a non-empty list of `provider[/variant][/region][/tier]` slugs.

`claw up` fails on any other key or value, on a slot with no model, on a slot whose model is not `openrouter/*`, and on a service that is not `lifecycle: per-run`. cllama's [data policy](/guide/data-policy) also sets OpenRouter's zero-retention and no-data-collection controls itself, and refuses a request whose preferences would weaken them. An `only` pin selects endpoints but is not evidence of where processing happens: under the default US/EU policy the slot still needs a regional route or a matching `processing:<region>` attestation.

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

## Task homes

A stateless worker answers from what it is handed. A **task-home** worker carries one unit of work across several runs: it has a shell and files, and each later run resumes its own conversation. The rule is: *a task has a home; a run is a clean container visiting it.* The design and its reasons are in [ADR-027](https://github.com/mostlydev/clawdapus/blob/master/docs/decisions/027-task-home-per-run-profile.md).

The Hermes task-home profile rejects `reasoning-effort` and `provider-preferences` until request-option projection is supported; it never silently drops those settings.

A service opts in with `x-claw.run.home`. Everything above still applies unless this section says otherwise, and a service that does not opt in is unchanged.

```yaml
services:
  worker:
    image: my-hermes-worker:latest   # CLAW_TYPE hermes
    x-claw:
      agent: ./AGENTS.md
      lifecycle: per-run
      run:
        max-runtime: 30m
        home: {}                     # opts in; mount defaults to /home/task
        content:                     # optional read-only reference mounts
          - source: ./reference/handbook
            target: /reference/handbook
```

- **Home.** A launch names a home with the spec field `home`: an opaque ID of 1 to 128 characters from `A-Z a-z 0-9 . _ -`, not starting with a dot. The supervisor maps the installation and the ID to a private directory under `.claw-homes/`; the caller never names a path. Two installations that use the same ID get different homes. `home` is required for a task-home template and refused for a stateless one.
- **One live run per home.** A home is reserved from the moment a launch is accepted until that run's container is removed and its authority is revoked. Another launch for it is refused with `409 home_busy`. A run still waiting for cllama's revocation acknowledgement keeps its home; other homes are unaffected.
- **Mounts.** The home is the only persistent writable path. `content` sources are resolved at `claw up`, must stay inside the pod directory, and are mounted read-only. The root filesystem stays read-only.
- **Native tools.** The worker has Hermes's terminal, file, memory, skills and todo tools, working inside its home. Managed business tools still reach it through cllama.
- **Network.** Each run has its own internal network holding the worker and a small forwarder. The forwarder relays to cllama's API port and nowhere else. Two workers cannot reach each other.
- **Resume.** The first run starts a Hermes session. Each later run for the same home resumes that exact session, and its task text arrives as the next message. A worker killed mid-turn resumes from the last stored turn.
- **Result.** A finished run's status carries `result.outcome` (`answer` or `question`) and `result.session`. A worker that needs a person writes its question to `.claw-run/question.md` in its home and ends its turn; the run then ends with `outcome: question` and the question as its result. Launch a new run for the same home with the answer as the task.
- **Failure reason.** When the worker's wrapper ends a run itself, the failure code names why, in the run's status and in the `end` callback's `reason`. Public status exposes the wrapper's exit code as `exit_code`.

  | Reason | Meaning |
  |---|---|
  | `worker_runner_exit` | Hermes exited non-zero. The persisted ledger records its exit code at `exit_observed.runner_code` |
  | `worker_turn_not_stored` | Hermes exited 0 but stored no reply for this turn in the home |
  | `worker_session_unproven` | A reply was stored, but the home's store does not show which session it belongs to, or Hermes reported another session |
  | `worker_session_locator_malformed` | The home's saved session ID is not a valid ID |
  | `worker_home_invalid` | A managed path in the home is not what the wrapper can safely write |
  | `worker_usage` | The worker was started without a task |

  The report is one allowlisted name and, for `worker_runner_exit`, a number. It carries no worker output, session ID or path. A report the supervisor does not recognise leaves the reason as `exit_nonzero`, and a worker that exits 0 can never report a failure of its own choosing.
- **Session proof.** The wrapper reads the turn's session from the home's store, not from Hermes's output: on a resume it is the saved session or its compression continuation, on a first run the one new session without a parent. Missing session records or incompatible ancestry metadata fail with `worker_session_unproven`; the wrapper does not guess from the newest message. Hermes's own `session_id:` line is only a cross-check. That line can be lost when Hermes's background review thread has redirected the process's stderr at the moment it is printed; in releases up to v0.32.1 a lost line ended a stored, successful turn with exit 70.
- **Authority.** The run's cllama token is new for every run and is never written into the home.

### Deliverables

A worker hands files to the person who gave it the task by putting them in `deliverables/` at the top of its home. The worker's contract tells it so. The supervisor serves that one directory, flat, and nothing else in the home.

```
GET /v1/homes/{installation}/{home}/deliverables
->  {"deliverables":[{"name":"report.md","bytes":1820,"sha256":"3f1a…","modified_at":"2026-11-10T21:30:00Z"}],
     "truncated":false}

GET /v1/homes/{installation}/{home}/deliverables/report.md?sha256=3f1a…
->  {"name":"report.md","bytes":1820,"sha256":"3f1a…","content_base64":"IyBSZXBvcnQK…"}
```

- A deliverable is a regular file directly in `deliverables/`, with a name of letters, digits, `.`, `_`, `-` and spaces that starts with a letter or digit (at most 128 characters), and at most 524288 bytes (512 KiB). Subdirectories, links, FIFOs, hidden files and larger files are not served.
- The list holds at most 200 files, sorted by name. `truncated` is true when there were more, or when the directory holds more than 4096 entries of any kind.
- A fetch returns the bytes as base64 in JSON, at most about 700 KiB in all. Its `sha256` is the digest of exactly the bytes returned. Pass the digest from the list as `?sha256=`; if the file has changed since, the fetch is refused with `deliverable_changed` (409) and no content.
- Other refusals: `deliverable_unknown` (404), `deliverable_too_large` (413), `home_unknown` (404).
- `deliverables_unreadable` (503) means the supervisor could not read the directory or a file (including permission, storage, or file-descriptor errors). It is never reported as an empty list or a missing file. The worker runs as another user with private file modes; its wrapper makes `deliverables/` and the regular files directly in it readable at the start and at the end of every turn, and leaves the rest of the home private. A turn that was killed before it ended leaves its new files unreadable until the next run for that home starts.
- The worker is untrusted and can change the directory at any time. The supervisor opens each path component relative to its own homes directory without following links, never blocks on a FIFO, and takes a file's size, digest and bytes from one open descriptor. No host path is accepted or returned.
- It works whether or not a run currently holds the home.

Inspect and delete homes through the supervisor:

```bash
claw runs home status <installation> <home>
claw runs home rm <installation> <home>      # refused while a run reserves it
```

The same verbs are `GET` and `DELETE /v1/homes/{installation}/{home}` on the socket. Deleting a home is how to reset a task: the next run for that ID starts with an empty home and a new session. `claw up` and `claw down` leave homes in place.

::: warning Storage is not capped
`run.home.max-bytes` sets a logical-byte watchdog threshold (default 1073741824, or 1 GiB).
A separate meter mounts the home read-only with no network or credentials. Crossing the threshold
ends the run as `home_full` through normal revocation and teardown. A home the meter cannot measure
(over 5,000,000 entries, or an unreadable directory) and a missing or failed meter end it as
`home_meter_unavailable`.

This is a watchdog, not a hard quota: a fast writer can fill the host disk before detection or
teardown. Use the profile where you accept that limitation; it was accepted for the internal trial.
After a killed turn, inspect durable effects before repeating them. Resume preserves history,
but does not provide exactly-once shell execution.
:::

`go test -tags spike -run '^TestSpikeTaskHomeWorkers$' ./cmd/claw` proves this profile end to end with a local fake provider.

## Model routes and plan sign-ins

A template's model chain is its routing order. `x-claw.models` declares it: `primary`, then
`fallback`, `fallback-2` and so on. cllama tries each entry in order and moves on when one
cannot serve. The chain is part of the template ID, so a run accepted under one chain is
refused at spawn if the template was recompiled with another. A launch carries no routing.

```yaml
services:
  worker:
    x-claw:
      lifecycle: per-run
      models:
        primary: chatgpt/gpt-5.6-terra        # the operator's ChatGPT plan
        fallback: openai/gpt-5.6-terra        # API key, when the plan cannot serve
        fallback-2: vercel/openai/gpt-4.1-nano  # a cheaper model through Vercel AI Gateway
      run:
        home: {}
```

| Provider prefix | Credential | Where it is set |
|---|---|---|
| `openai/`, `anthropic/`, `xai/`, `google/`, `meta/`, `openrouter/` | API key | `x-claw.cllama-env`, for example `OPENAI_API_KEY` |
| `vercel/<provider>/<model>` | Vercel AI Gateway key | `AI_GATEWAY_API_KEY` in `x-claw.cllama-env` |
| `chatgpt/` | The operator's ChatGPT plan sign-in | The sign-in command below. No key, and nothing in the pod file |

### The `chatgpt` plan route

cllama can serve requests from a ChatGPT plan through OpenAI's
[Sign in with ChatGPT plan usage](https://developers.openai.com/siwc/token-sharing-open-source).
OpenAI documents it for open-source, locally hosted apps: the operator's own plan serving the
operator's own agents. It is not for paid or hosted multi-user use. It requires a cllama
release that includes plan routes.

- **Plan first, key second:** put `chatgpt/<model>` first and a keyed entry after it.
- **Plan only:** declare `chatgpt/<model>` alone. No request of that template ever uses a key.

The operator signs in once, on the host. No agent signs in, and a worker cannot reach the
sign-in: it lives only in cllama's auth directory (`.claw-auth/subscriptions/`, owner-only),
which no worker mounts.

```bash
claw compose run --rm -p 127.0.0.1:1455:1455 cllama auth login chatgpt --listen 0.0.0.0:1455
```

The command prints a URL. Open it in a browser on the same machine and sign in; the command
finishes when the browser returns to `127.0.0.1:1455`. The running proxy uses the sign-in
from the next request. `claw compose run --rm cllama auth status` shows the state, and
`claw compose run --rm cllama auth logout chatgpt` signs out.

When the plan route cannot serve, cllama records one of these reasons:

| Reason | Meaning | With a later chain entry | As the last entry |
|---|---|---|---|
| `subscription_signin_required` | Nobody is signed in, or the sign-in expired. The operator signs in again | Moves on | The request fails |
| `subscription_limit_reached` | The plan's usage limit is spent. cllama rests the route (15 minutes by default) and tries it again | Moves on | The request fails |
| `subscription_unavailable` | A temporary fault | Moves on | The request fails |
| `subscription_not_eligible` | The account or its policy does not permit plan usage | The request fails | The request fails |
| `subscription_unsupported_request` | The request uses a feature the plan route does not support | The request fails | The request fails |

The [data policy](/guide/data-policy) applies to the plan route as to any provider. Under a
constrained policy, attest its guarantees for provider `chatgpt` at endpoint
`https://api.openai.com/v1`; an attestation for `openai` does not cover it.

The last two never move a request to another billing path. The plan route sends no
`temperature`, `top_p` or output-token limit, refuses hosted tools, and counts a reply only
once it has completed. The cllama README has the full list.

From cllama v0.17.0, per-run workers also cannot use provider-hosted tools through an API-key fallback, even under a permissive data policy. The final-request guard refuses remote MCP and hosted web search before dispatch. Per-run templates do not accept hosted-capability grants in this version; native function/custom tools are unaffected. The live regression uses a real Hermes worker with a synthetic provider and sign-in, proves ordinary API fallback succeeds, and checks that refused search/MCP requests produce no upstream request. It does not use a real account or move a sign-in store.

### What a run reports

For a [task-home](#task-homes) run, the status and the `end` callback say which credential
class served the run's model calls:

- `served_by`: completed model calls by class, for example `{"subscription": 12, "api_key": 3}`.
- `served_by_complete`: `true` when one cllama process counted the whole run. `false` means
  cllama restarted during the run, or its report was malformed, and the counts are a subset.
- `last_plan_failure`: the most recent plan-route reason from the table above. It is attempt
  history, not the run's result: on a run that succeeded it means a later entry served what
  the plan could not.

The fields are absent when cllama reported nothing, which includes every cllama release
before plan routes. With the trace journal on, each `upstream_request` record also names the
`credential` class, and is the durable per-call record.

### Sign-in state without a run

```
GET /v1/subscriptions   ->   {"subscriptions":[{"provider":"chatgpt","state":"signed_in","account":"you@example.com"}]}
```

`state` is `signed_in`, `not_signed_in` or `signin_required`; `limited_until` is set while
the usage limit is resting. It never carries a token. The call returns
`501 subscriptions_unsupported` when the pod's cllama has no plan routes. `claw runs doctor`
prints the same state, with the sign-in command when one is needed. An orchestrator can use
it to hold work for a plan-only template instead of launching a run that will fail.

### What is proven

`go test -tags spike -run '^TestSpikeTaskHomeRoutes$' ./cmd/claw` runs real Hermes task-home
workers through cllama against a local fake provider. It covers:

- OpenAI direct (`openai/gpt-5.6-terra`) and Vercel AI Gateway (`vercel/openai/gpt-4.1-nano`),
  each with its own key;
- plan first with nobody signed in: the key serves, and the callback names
  `subscription_signin_required`;
- plan only with nobody signed in: the run fails, and the provider receives no request for it;
- the sign-in and status commands above, with a forged callback refused;
- with a synthetic sign-in, the plan serving both templates, the request shape the vendor
  requires, and a spent plan falling back to the key;
- a worker that dumps its environment and mounts and finds no sign-in, key or operator token;
- no sign-in token in the proxy log, the callbacks or the pod directory outside the store.

For the synthetic plan cases the fixture stands in for OpenAI's endpoint on the pod's own
network, with a certificate authority only that test's cllama trusts.

On 2026-11-10, a collaborating test operator also verified an existing real ChatGPT sign-in
with Hermes task-home workers: plan-first first turn, exact-session resume with a durable
file check, separate-home control, and plan-only calls on `chatgpt/gpt-5.6-terra` and
`chatgpt/gpt-6.1-terra`. All 23 responses used the subscription, with no API-key fallback or
plan-failure events. A separate empty-store control failed with
`subscription_signin_required` and zero upstream requests. The sign-in store was not copied, no agent read or wrote it, and the test performed
no sign-in; cllama may have refreshed the record itself. These results establish those
inference paths, not every account, token-refresh, or quota-limit case.

### Live API compatibility verification

On 2026-11-10, the same integration workflow verified `openai/gpt-6.1-terra` through
an API key: first turn (4 responses), exact-session resume (5), and separate-home control
(2). One initial `max_tokens` rejection triggered the Responses adapter; all 11 responses
succeeded, and later calls reused the learned shape without another rejection. A same-build plan regression served six subscription
responses, and the empty-store control again made no upstream request. No sampling
rejection occurred in this live test; that retry branch is covered by synthetic tests.

### Contributor workflow: trying a local cllama build

This contributor workflow builds a distinct local image and a local `claw` binary that pins
it. Run it from a Clawdapus source checkout containing this feature and the cllama commit to
try. It does not overwrite a published image tag or change your installed `claw`.

```bash
trial_tag="local-$(git -C cllama rev-parse --short HEAD)"
docker build -t "ghcr.io/mostlydev/cllama:${trial_tag}" \
  --build-arg CLLAMA_VERSION=local \
  --build-arg CLLAMA_REVISION="$(git -C cllama rev-parse HEAD)" cllama/
go build -ldflags "-X main.infraCllamaTag=${trial_tag}" -o /tmp/claw-plan-trial ./cmd/claw
cd /path/to/pod
/tmp/claw-plan-trial up -d
/tmp/claw-plan-trial runs doctor
```

Use that same local binary for the trial's lifecycle commands. With Clawdapus v0.32.0 or later
installed, use the installed `claw pull` and `claw up -d` to return the pod to published images. A proxy without plan routes makes `claw runs doctor` report that plan
sign-in status is unsupported.

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
| `GET /v1/subscriptions` | — | State of the pod's [plan sign-ins](#sign-in-state-without-a-run). Never a token. |
| `GET /v1/homes/{installation}/{home}/deliverables` | — | Files the home's worker left for its requester. See [deliverables](#deliverables) |
| `GET /v1/homes/{installation}/{home}/deliverables/{name}` | `?sha256=` | One deliverable, base64 in JSON |
| `GET /v1/templates` | — | The compiled templates: `name`, `id`, `context_digest` and `models`, the concrete chain in routing order. See [pinning a queued spec](#pinning-a-queued-spec) |
| `GET /v1/identity`, `GET /v1/health` | — | Protocol version, pod and supervisor UID |

Launch errors:

| Code | Status | Meaning |
|---|---|---|
| `digest_invalid` | 400 | `spec_digest` does not match the canonical spec |
| `digest_mismatch` | 409 | The run exists with a different digest |
| `network_unavailable` | 503 | No free address block for a task-home run's network, or `CLAW_RUN_SUBNET_BASE` is not a usable private IPv4 range |
| `template_changed` | 409 | The spec carries a `template_id` and the template has been recompiled since |
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

### Pinning a queued spec

A spec names its template, and the supervisor binds the run to that template's ID when it admits the launch. An orchestrator that builds a spec and launches it later should pin it, so a pod recompiled in between cannot give the run another model chain, tool grant or contract:

```
GET /v1/templates
->  {"templates":[{"name":"worker","id":"sha256:9f2c…","context_digest":"sha256:41ab…",
                   "models":[{"slot":"primary","ref":"openai/gpt-6.1-terra"},
                             {"slot":"fallback","ref":"chatgpt/gpt-6.1-terra"}],
                   "profile":"task_home","content_targets":["/claw/reference/handbook"],
                   "data_policy":{"regions":["us","eu"],"training":"none","retention":"none",
                                  "local_retention":"metadata","attested_providers":["openai"]}}],
     "invalid":[]}
```

Put the `id` in the spec as `template_id`:

```json
{"template":"worker","template_id":"sha256:9f2c…","task":"Summarize ticket 42","tools":[]}
```

- `template_id` is part of the spec and so of `spec_digest`.
- A launch whose `template_id` is not the template's current ID is refused with `template_changed`. The refusal is permanent for that spec: it is made before any run record, home reservation or credential custody, and its error carries `current_template_id`. Build a new spec against the current template.
- `profile` is `task_home` or `stateless`. `content_targets` lists a task-home template's reference mounts by their path in the worker, never the host path. `data_policy` is what cllama enforces for the template's model calls, with attestations named by provider only.
- A repeat of a launch that was already admitted returns that run unchanged, whatever was compiled since.
- Without `template_id` the run binds to whatever is compiled at admission, as before.
- The listing shows only templates whose files verify against their own ID and context digest; any other is named in `invalid`. It carries no credential, context body or task content.

Each run's status carries `template_id` and `models`, the chain it was admitted with.

When a chain entry was resolved from a [model family](/guide/pod-yaml#model-families), its `models` entry also carries `requested`, the reference the pod wrote:

```json
{"slot":"primary","ref":"openai/gpt-6.1-terra","requested":"openai/@terra"}
```

An entry for an exact model has no `requested`. The value comes from the compiled template and is part of its ID, so a template that uses a family has a new ID after upgrading to a version that records it; a spec pinned to the old ID is refused with `template_changed` as usual. A run keeps the `ref` and `requested` it was admitted with.

`tools` must be a subset of the template grant, and an empty list removes every managed tool. The credential is outside the spec, so the digest never covers a token.

### Generation

`generation` is a positive integer used as a monotonic fence per run. A command whose generation is below the recorded one gets `stale_generation`. A cancelled run never comes back: every later launch for that key is refused, whatever its generation.

### States

```
accepted ─► running ─► stopped    (exit 0 and an answer was found)
   │           ├─────► failed     (exit_nonzero, timeout, authority_expired,
   │           │                   spawn_error, result_missing, mismatch,
   │           │                   task-home: worker_*)
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
- `capture_epoch` and `capture_epoch_observed_at`, the trace capture epoch observed once at spawn and never rewritten. Omitted when there is no usable epoch file.

### Authority, credentials and callbacks

Every launch carries `authority_expires_at`, capped by the template's `max-authority`. When it passes, the supervisor revokes the run, removes the worker and ends the run as `failed` with reason `authority_expired`, whether or not any callback ever succeeded.

If the supervisor was started with `--callback-origin`, a launch may hand over an opaque per-run `credential`. The supervisor stores it in a `0600` file. It never reaches the worker's environment, arguments or mounts, and never appears in logs. The supervisor uses it only for this callback contract:

```
POST {origin}{path}/start      {installation, run_id, generation}
POST {origin}{path}/heartbeat  {installation, run_id, generation}       every --heartbeat
POST {origin}{path}/end        {installation, run_id, generation, outcome, reason, summary, summary_truncated}
                               task-home result only: + {result_outcome, session}
                               task-home, when cllama reports it: + {served_by, served_by_complete, last_plan_failure}
```

For a [task-home](#task-homes) run that has a result, the `end` body carries two more fields: `result_outcome` (`answer` or `question`) and `session` (the harness session ID that holds the turn). They are the values in the run's status (`result.outcome`, `result.session`). They are absent for stateless templates and for any run without a result, whose bodies are unchanged. Callbacks are authenticated by the bearer credential only; the body is not signed.

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
| `claw runs doctor [--remove-orphans]` | Report [model family drift](/guide/pod-yaml#checking-for-drift), check the ledger, find orphaned workers and show plan sign-in state |

Durable state lives in `<pod>/.claw-runs/` (mode `0700`):

- the ledger, with one record per run;
- `0600` secret files;
- tombstones;
- rendered projects;
- results;
- the pod lock.

cllama's revocation ledger in `.claw-revocations/` grows by about 80 bytes per run, up to a 16 MiB bound. See the cllama README for capacity planning.

Already-dispatched remote tool effects are never undone by a cancel. cllama's audit records which dispatches were admitted before the revocation.
