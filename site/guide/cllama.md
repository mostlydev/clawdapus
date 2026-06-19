# cllama: The Governance Proxy

When a reasoning model tries to govern itself, the guardrails are part of the same cognitive process they are trying to constrain. This is the fundamental problem with prompt-level safety: the judge and the defendant share the same brain.

`cllama` is a **separate process** sitting between the runner and the LLM provider. The runner thinks it is talking directly to the model. It never sees the proxy. This is principle number eight: **think twice, act once.**

## How It Works

The proxy sits on the network path between every agent in the pod and the LLM providers. When an agent makes an API call to what it believes is OpenAI or Anthropic, the request goes to cllama instead. The proxy evaluates, routes, and logs the request, then forwards it to the real provider.

```
Agent → (bearer token) → cllama proxy → (real API key) → LLM Provider
                              ↓
                     audit log + dashboard
```

A single proxy instance serves the entire pod. Bearer tokens resolve which agent is calling, so the proxy can apply per-agent model policy, managed-tool budgets, and logging.

## Credential Starvation

Isolation is achieved by strictly separating secrets:

- **The proxy holds the real API keys.** Provider credentials (OpenRouter, Anthropic, OpenAI, Gemini/Google, Vercel AI Gateway, xAI) are configured in the pod-level `cllama-defaults.env` block and never enter agent containers.
- **Agents get unique bearer tokens.** Each agent (and each ordinal of a scaled agent) receives a unique token generated during `claw up`.
- **No credentials, no bypass.** Because agents lack the credentials to call providers directly, all successful inference *must* pass through the proxy -- even if a malicious prompt tricks the agent into ignoring its configured base URL.

::: warning Keys Never Enter Agent Containers
Provider API keys belong in `x-claw.cllama-defaults.env` at the pod level. They are injected into the cllama proxy container only. Agent containers receive bearer tokens, not API keys.
:::

## Identity Resolution

The proxy uses bearer tokens to resolve caller identity. Each token maps to a specific agent (or agent ordinal), which means the proxy can:

- Apply per-agent model policy and record cost telemetry
- Track per-agent token usage and spend
- Log which agent made which request
- Enforce different model access per agent

The token format is `<agent-id>:<secret>`, generated fresh on every `claw up`. The proxy loads a principals file mapping tokens to agent identities and their compiled contract context.

When a request arrives, the proxy:

1. Extracts the `<agent-id>` from the bearer token.
2. Loads the agent's context from `CLAW_CONTEXT_ROOT/<agent-id>/`.
3. Validates the `<secure-secret>` against `metadata.json` principals.
4. Checks the requested model against the agent's allowed models.

Token validation is fail-closed: unknown or missing tokens are denied before any provider call is made.

## Transport Model

The proxy exposes a canonical **ingress surface matrix** — a small set of runner-facing HTTP surfaces that together form the cllama transport contract. See [ADR-023](https://github.com/mostlydev/clawdapus/blob/master/docs/decisions/023-cllama-ingress-surface-matrix.md) for the architectural rationale.

| Surface | Path | Payload family | Default use |
|---------|------|----------------|-------------|
| OpenAI Chat Completions | `POST /v1/chat/completions` | OpenAI-compatible chat/completions | All non-Anthropic providers unless an explicit exception is documented |
| Anthropic Messages | `POST /v1/messages` | Anthropic Messages | Anthropic-family providers and explicit Anthropic-wire exceptions |

| Property | Value |
|----------|-------|
| Listen port | `0.0.0.0:8080` |
| Base URL (as seen by runner) | `http://cllama-<type>:8080/v1` |
| Auth header | `Authorization: Bearer <agent-id>:<secure-secret>` |

Clawdapus configures each agent's runner to use the proxy URL as its LLM base URL, and the runner targets one of the canonical ingress paths beneath that base URL. Provider identity (`google/gemini-*`, `anthropic/*`, etc.) stays in operator-facing model refs — runners must not invent synthetic provider prefixes such as `cllama/google`. Two distinct code paths handle OpenAI format (`messages[]`) and Anthropic format (top-level `system` field).

### OpenAI Format

Requests to `/v1/chat/completions` are handled as OpenAI format. The payload contains a `messages[]` array and a `model` field. The proxy rewrites the `model` field to the operator-assigned provider and model, then forwards the request to the resolved upstream endpoint.

### Anthropic Format

Requests to `/v1/messages` are handled as Anthropic format. The payload uses a top-level `system` field rather than embedding system messages in the `messages` array. The proxy forwards Anthropic-specific headers (`Anthropic-Version`, `Anthropic-Beta`) and routes directly to the Anthropic provider.

### Format Bridging

When the resolved provider uses Anthropic format but the incoming request is OpenAI format (`/v1/chat/completions`), the proxy routes through OpenRouter instead, which accepts OpenAI format for all models. This transparent bridging means agents do not need to know which provider or format their assigned model requires.

::: tip Passthrough, Not Policy
In passthrough mode, the reference proxy still performs infrastructure work: model routing, late runtime-context assembly, memory recall/retain, managed tool mediation, telemetry, and cost accounting. It does **not** run a contract-derived policy engine, redact responses, compute drift scores, or amend final provider text.
:::

## The Interception Pipeline

The runner never knows the proxy exists -- it thinks it is talking directly to the model. The reference `passthrough` image implements the transport and compiled-infrastructure pipeline below. Full policy interception is a separate future/custom proxy concern.

### Pre-flight

Identity resolution, token validation, and model authorization. Invalid tokens are rejected before any downstream work begins.

### Runtime Context Assembly

Before the provider call, the proxy appends volatile infrastructure context from compiled manifests: subscribed feeds, memory recall, the current time line, and live Discord channel deltas. This is a *late* runtime-context block rather than a rewrite of the stable system contract. OpenAI-compatible requests receive a later `system` message inserted immediately before the invoking user message; Anthropic requests receive a trailing `user` content block.

The stable system contract and the existing first non-system message stay byte-stable across turns, which preserves prompt-cache identity on cache-supported providers and keeps OpenRouter sticky routing pinned to a stable conversation.

::: tip Stable Contract, Volatile Tail
Feed headers no longer carry the volatile `refreshed <ts>` line in model-visible text -- unchanged feed content with a TTL refresh now produces byte-identical bytes. The `STALE` tag still appears when a feed fetch failed and the rendered text is from the last good fetch.
:::

### Managed Tool Mediation

If `tools.json` is present for the calling agent, cllama injects the compiled managed tool schemas into the upstream request and executes matching tool calls itself. The runner sees only the terminal response. Runner-native tools still pass through to the runner when they are not part of a managed-tool round.

Managed tools can execute through two provider-side transports:

- **HTTP descriptors** use the per-tool `http` metadata from `claw.describe` and call the target service directly.
- **MCP descriptors** use the descriptor's top-level `mcp` block. cllama performs the Streamable HTTP `initialize` / `notifications/initialized` handshake, caches MCP sessions per target, calls `tools/call`, and retries once when an MCP session expires.

Within the mediation loop, cllama preserves the model-visible order that provider APIs require. If a model returns a managed-tool prefix mixed with runner-native tool calls, cllama serializes the managed prefix, logs a `mixed_tool_order_internal_retry` intervention, and retries internally instead of handing an invalid mixed transcript to the runner.

Duplicate managed tool calls are handled without re-executing the provider service. The first call runs normally; later calls with the same canonical tool name and arguments receive the cached model-facing result by default. If a model keeps repeating the same duplicate call, a duplicate streak cutoff disables tools and forces a final answer before the round budget is exhausted. These paths are recorded in `tool_trace` and intervention telemetry.

### Channel Context Cursors

Live Discord channel context is fetched as a delta-since-watermark instead of a full tail every turn. The proxy keeps a per-agent vector cursor (one entry per visible channel) and rewrites the `channel-context` feed URL with `after=<channel_id>:<message_id>` watermarks before sending it to `claw-wall`. The cursor is committed only after a successful 2xx response is recorded by the session-history writer, so streaming truncation, 5xx upstream errors, and 4xx rejections all leave the cursor untouched and the same delta replays on the next mention. When `claw-wall` caps a delta response, cllama appends a `coverage_partial=true omitted_after_cursor=N newest_returned=...` annotation so partial coverage is visible rather than silently swallowed; the cursor still advances to the newest returned message. The wall backfills Discord history on startup before the first forward poll, and feed headers include `backfill_status` so a partial or rate-limited backing window is visible to operators. See [Social Topology · Channel Context Feed](/guide/social-topology#channel-context-feed-claw-wall) for the wire shape.

The cursor ledger lives at `$CLAW_CONTEXT_LEDGER_DIR/<agent-id>/cursor.json`. The default path is `$CLAW_SESSION_HISTORY_DIR/context-ledger` (i.e. inside the existing read-write session-history mount). When session history is disabled, cursors fall back to in-memory only and every cold start re-bootstraps with a 24h tail.

### Provider Execution

The proxy strips the dummy token, attaches the real provider API key, and forwards the request upstream. Declared model failover is implemented for key/provider exhaustion, such as dead keys and rate-limit cooldowns; ordinary upstream 5xx responses are returned rather than silently retried against a different model.

### Egress

The provider response is returned to the agent. The reference passthrough proxy does not amend final text, redact PII, or compute a drift score. Those behaviors belong in a future/custom policy proxy layered on the same context and telemetry contracts.

::: info Passthrough vs Policy
The reference `passthrough` implementation performs identity resolution, model routing, late runtime-context assembly, managed tool mediation, memory orchestration, telemetry, and cost tracking. It does not perform policy blocking, prompt decoration from policy modules, response amendment, PII redaction, or drift scoring. Full bidirectional policy interception is future/custom proxy work.
:::

## Context Mount Structure

The proxy needs to know who each agent is and what it is allowed to do. Clawdapus provides this through a shared context mount -- a directory tree with per-agent subdirectories containing the compiled contract and identity metadata.

### Host-Side Layout

During `claw up`, Clawdapus generates context files under the runtime directory:

```
.claw-runtime/context/
├── crypto-crusher-0/
│   ├── AGENTS.md        # Compiled contract (includes, enforce, guide)
│   ├── CLAWDAPUS.md     # Infrastructure map (surfaces, skills, topology)
│   ├── metadata.json    # Identity, bearer token, handles, model policy
│   ├── service-auth.json
│   ├── feeds.json
│   ├── tools.json
│   ├── memory.json
│   └── channels-allowlist.json
├── crypto-crusher-1/
│   ├── AGENTS.md
│   ├── CLAWDAPUS.md
│   ├── metadata.json
│   └── ...
└── analyst/
    ├── AGENTS.md
    ├── CLAWDAPUS.md
    ├── metadata.json
    └── ...
```

| File | Purpose |
|------|---------|
| `AGENTS.md` | The agent's compiled behavioral contract, including inlined `enforce` and `guide` content from `INCLUDE` directives. |
| `CLAWDAPUS.md` | Infrastructure context: surfaces, mount paths, peer handles, feeds, and available skills. |
| `metadata.json` | Machine-readable identity, handles, bearer token auth, and compiled model policy. |
| `service-auth.json` | Bearer tokens for services the proxy is allowed to call on the agent's behalf. |
| `feeds.json` | Resolved context feed subscriptions and fetch metadata. |
| `tools.json` | Compiled managed tool schemas, execution metadata, auth, and mediation budgets. |
| `memory.json` | Memory service recall/retain/forget endpoints and auth. |
| `channels-allowlist.json` | Channel IDs the agent is authorized to read for channel context and retrieval. |

### Container-Side Mount

The host directory is bind-mounted into the cllama container at `/claw/context/<agent-id>/`. The proxy reads `CLAW_CONTEXT_ROOT` (defaults to `/claw/context`) and loads each subdirectory as an agent identity.

The `context/` directory segment is required in both host and container paths.

::: warning The context/ Segment Is Required
The mount path must include the `context/` directory segment. The proxy expects `CLAW_CONTEXT_ROOT` to point at the directory containing agent subdirectories, not directly at an agent's files.
:::

### Context Mount Contents

The reference loader reads the compiled contract (`AGENTS.md`), infrastructure map (`CLAWDAPUS.md`), identity metadata, service auth, tool manifest, memory manifest, model policy, and channel allowlist. There is still no generic policy-decoration config or response-amendment hook in the context mount.

### Internal Context Snapshots

For operator visibility, cllama stores the most recent provider-visible context assembled for each agent. The read-only internal endpoints are:

- `GET /internal/context`
- `GET /internal/context/<agent-id>/snapshot`

Clawdash reads these through claw-api so operators can inspect the effective system contract, late runtime context, feed blocks or skip notices, memory recall, tool schemas, model route, and redacted metadata for the last turn. Snapshots are diagnostic state only; they are not a control plane and do not mutate agent context.

### Scaled Services

For services with `count > 1`, context is generated **per ordinal**. A service named `crypto-crusher` with `count: 3` produces three separate context directories: `crypto-crusher-0/`, `crypto-crusher-1/`, `crypto-crusher-2/`. Each ordinal gets its own bearer token, its own compiled contract, and its own audit trail.

The `metadata.json` file in each directory contains the bearer token secret used for authentication. The proxy validates incoming tokens against these metadata files to resolve caller identity.

## Environment Variables

The cllama container receives its configuration through environment variables injected by `claw up`.

| Variable | Description |
|---|---|
| `CLAW_POD` | The name of the pod (e.g., `crypto-ops`). |
| `CLAW_CONTEXT_ROOT` | Path to the shared context mount root (defaults to `/claw/context`). |
| `CLAW_SESSION_HISTORY_DIR` | Path to the read-write session history mount (defaults to `/claw/session-history`). When set, also seeds the default `CLAW_CONTEXT_LEDGER_DIR`. |
| `CLAW_CONTEXT_LEDGER_DIR` | Path where per-agent channel cursors are persisted (defaults to `$CLAW_SESSION_HISTORY_DIR/context-ledger`). When unset, cursors fall back to in-memory and every restart re-bootstraps with a 24h tail. |
| `CLLAMA_FEED_MAX_RESPONSE_BYTES` | Per-feed byte cap applied at fetch time before formatting. Default `32768`. Invalid or non-positive values fall back to the default. |
| `CLLAMA_FEED_MAX_TOTAL_BYTES` | Aggregate cap across all formatted feed blocks injected into one request. Default `65536`. Invalid or non-positive values fall back to the default. |
| `CLLAMA_FEED_FETCH_TIMEOUT_MS` | Per-fetch HTTP timeout for feed providers. Default `3000`, sanity range 100–120000; out-of-range values fall back to the default. Raise it when a feed provider computes synchronously under load. |
| `CLLAMA_DISPATCH_CANDIDATE_TIMEOUT_MS` | Per-candidate timeout for non-streaming upstream model dispatch before trying the next declared fallback. Default `60000`. Streaming responses are exempt so long-running streams are not cut off. |
| `CLLAMA_TOOL_SCHEMA_VALIDATION` | Set to `off` to disable pre-dispatch validation of managed tool arguments against the manifest `inputSchema`. On by default; validation fails open on schema constructs it does not understand. |
| `PROVIDER_API_KEY_*` | Real provider API keys -- `OPENAI_API_KEY`, `ANTHROPIC_API_KEY`, `OPENROUTER_API_KEY`, `GEMINI_API_KEY` / `GOOGLE_API_KEY`, `AI_GATEWAY_API_KEY`, etc. |

### Where Provider Keys Go

Provider keys are configured in the pod YAML under `x-claw.cllama-defaults.env`. They are injected into the cllama proxy container only. They must **not** appear in regular agent `environment:` blocks.

```yaml
x-claw:
  pod: my-fleet
  cllama-defaults:
    proxy: [passthrough]
    env:
      OPENROUTER_API_KEY: "${OPENROUTER_API_KEY}"
      ANTHROPIC_API_KEY: "${ANTHROPIC_API_KEY}"
      GEMINI_API_KEY: "${GEMINI_API_KEY}"
```

For native Gemini routing, declare models as `google/<model>`. `GEMINI_API_KEY`
is the primary env name; `GOOGLE_API_KEY` is accepted as a lower-priority
alias. `GOOGLE_BASE_URL` can override the default OpenAI-compatible Google
endpoint when needed.

::: warning cllama-env, Not environment
Provider API keys belong in `x-claw.cllama-defaults.env` (or service-level `x-claw.cllama-env`), never in the service's compose `environment:` block. Putting real keys in `environment:` defeats credential starvation -- the agent container would have direct provider access.
:::

### Feed Injection Budgets

cllama applies two byte budgets when it injects subscribed feeds into a request: a per-feed cap (read at fetch time, before formatting) and an aggregate cap across all formatted feed blocks. Feeds are injected in manifest order until the aggregate cap is reached.

These budgets are intentionally bounded by default -- `32 KB` per feed and `64 KB` aggregate -- so a small pod cannot accidentally turn feed subscriptions into unbounded prompt stuffing. The defaults are independent of the feed *source* window: a `claw-wall` `channel-awareness` feed configured with a large `x-claw.context.channel.max-chars` can return far more than 32 KB, but cllama will still cap what reaches the model unless you raise its budgets too.

Raise both caps together through `x-claw.cllama-defaults.env` (or service-level `x-claw.cllama-env`):

```yaml
x-claw:
  pod: trading-desk
  cllama-defaults:
    proxy: [passthrough]
    env:
      CLLAMA_FEED_MAX_RESPONSE_BYTES: "262144"   # accept up to 256 KB from any one feed
      CLLAMA_FEED_MAX_TOTAL_BYTES: "393216"      # 384 KB across all injected feeds combined
```

Invalid or non-positive values fall back to the bounded defaults, so a typo cannot silently unbound injection. Set `CLLAMA_FEED_MAX_TOTAL_BYTES` high enough to hold the *sum* of every feed a turn carries (market/style context, scaffolds, memory recall, channel awareness, channel context) -- the aggregate cap is shared across all of them, not per feed.

When the aggregate cap does drop a feed, cllama no longer fails silently: the model sees an explicit `--- FEED: <name> skipped (total feed size cap reached; block_bytes=… total_before=… max_total_bytes=…) ---` notice in the runtime context, and a structured `feed_injection` telemetry event records the outcome (see [Telemetry Fields](#telemetry-fields)). Context snapshots store the actual provider-visible blocks and skip notices.

::: tip Skip is in manifest order
The aggregate cap drops whole feeds in manifest order once the budget is exhausted; there is no per-feed priority or reservation yet. If a large feed earlier in the manifest can starve a later one, raise `CLLAMA_FEED_MAX_TOTAL_BYTES` rather than relying on ordering.
:::

## Pod Configuration

### Declaring a cllama Proxy

The proxy is declared in `claw-pod.yml` via the `cllama` field on a service's `x-claw` block:

```yaml
services:
  analyst:
    x-claw:
      agent: analyst
      cllama: passthrough
      cllama-env:
        OPENAI_API_KEY: ${OPENAI_API_KEY}
        ANTHROPIC_API_KEY: ${ANTHROPIC_API_KEY}
```

The `cllama` value specifies the proxy type. Currently only `passthrough` ships as a reference implementation.

### Provider Keys with YAML Anchors

For pods with multiple services using the same provider keys, use YAML anchors to stay DRY:

```yaml
x-claw-env: &cllama-keys
  OPENAI_API_KEY: ${OPENAI_API_KEY}
  ANTHROPIC_API_KEY: ${ANTHROPIC_API_KEY}

services:
  analyst:
    x-claw:
      agent: analyst
      cllama: passthrough
      cllama-env: *cllama-keys
  researcher:
    x-claw:
      agent: researcher
      cllama: passthrough
      cllama-env: *cllama-keys
```

### Native Gemini Routing

Direct Gemini works through Google's OpenAI-compatible endpoint. Use the
`google/<model>` provider prefix and seed the key through `x-claw.cllama-env`.

```yaml
services:
  analyst:
    x-claw:
      agent: analyst
      cllama: passthrough
      models:
        primary: google/gemini-2.5-flash
      cllama-env:
        GEMINI_API_KEY: ${GEMINI_API_KEY}
        # optional override for proxies or alternate endpoints
        GOOGLE_BASE_URL: ${GOOGLE_BASE_URL}
```

If both `GEMINI_API_KEY` and `GOOGLE_API_KEY` are present, cllama prefers
`GEMINI_API_KEY` as the active seed key.

### Vercel AI Gateway Routing

Vercel AI Gateway works through its OpenAI-compatible endpoint. Use the
`vercel/<provider>/<model>` provider prefix and seed the gateway key through
`x-claw.cllama-env`.

```yaml
services:
  analyst:
    x-claw:
      agent: analyst
      cllama: passthrough
      models:
        primary: vercel/anthropic/claude-sonnet-4.6
      cllama-env:
        AI_GATEWAY_API_KEY: ${AI_GATEWAY_API_KEY}
        # optional override for proxies or alternate endpoints
        AI_GATEWAY_BASE_URL: ${AI_GATEWAY_BASE_URL}
```

The OpenAI-compatible `/v1/chat/completions` path forwards
`anthropic/claude-sonnet-4.6` to Vercel as the upstream model. The Anthropic
`/v1/messages` path remains native Anthropic-only.

### Count Expansion with cllama

When a service declares both `cllama` and `count > 1`, each ordinal gets its own bearer token and context directory. The proxy authenticates each ordinal independently:

```yaml
services:
  analyst:
    x-claw:
      agent: analyst
      cllama: passthrough
      count: 3
```

This produces `analyst-0`, `analyst-1`, and `analyst-2`, each with:
- A unique bearer token in format `analyst-N:<secret>`
- A context directory at `/claw/context/analyst-N/`
- Independent telemetry tagged with `claw_id: analyst-N`

## Cost Accounting

The proxy extracts token usage from every LLM response, multiplies by the pricing table, and tracks cost per agent, per provider, and per model. This gives operators real-time visibility into spend without relying on provider dashboards that aggregate across all API keys.

```bash
$ claw audit --since 24h --claw analyst-0

Pod: trading-desk
Events: 128
CLAW       REQ  RESP  ERR  INT  TOOLS  TOOL_ERR  TOK_IN  TOK_OUT  COST_USD  MODELS
analyst-0  64   64    0    1    9      0         81204   18402    0.2130    claude-sonnet-4(64)
```

## Telemetry and Audit

Every request through the proxy produces a structured JSON log entry on stdout. Clawdapus collects these for the `claw audit` command and for the Master Claw's fleet governance decisions.

### Telemetry Fields

| Field | Description |
|-------|-------------|
| `ts` | ISO-8601 UTC timestamp. |
| `claw_id` | The calling agent's identifier. |
| `type` | Event type: `request`, `response`, `error`, `intervention`, `feed_fetch`, `feed_injection`, `memory_op`, `channel_context_op`, `provider_pool`, or normalized session-history `tool_call`. |
| `intervention` | Optional intervention reason. In the reference logger this field is present on every event and is often `null`; non-null values identify a concrete proxy action such as model routing or duplicate managed-tool suppression. |
| `model` | The model used for the request. |
| `tokens_in` | Input token count. |
| `tokens_out` | Output token count. |
| `cost_usd` | Estimated cost for the request/response pair. |
| `latency_ms` | Request duration in milliseconds. |
| `static_system_hash` | sha256 of the stable system contract (`messages[0]` for OpenAI / top-level `system` for Anthropic). Should be byte-stable across turns when nothing about the agent's contract changed. |
| `first_system_hash` | sha256 of the first system message in the assembled payload. v1 mirrors `static_system_hash`; reserved for future Anthropic `cache_control` differentiation. |
| `first_non_system_hash` | sha256 of the first non-system message. Stable on multi-turn runners; expected to drift on single-turn Discord runners and surfaces that drift via this field. |
| `dynamic_context_hash` | sha256 of the late runtime-context block (memory + feeds + time + channel deltas). Changes per turn when new context arrives. |
| `tools_hash` | sha256 of the canonicalized `tools[]` payload. |
| `cached_tokens` | Provider-reported `usage.prompt_tokens_details.cached_tokens` when present. |
| `cache_write_tokens` | Provider-reported `usage.prompt_tokens_details.cache_write_tokens` when present. |

Event-specific fields may also be present depending on `type`:
- `status_code`, `latency_ms`, `tokens_in`, `tokens_out`, `cost_usd`, `cached_tokens`, `cache_write_tokens` — request/response/error events
- `static_system_hash`, `first_system_hash`, `first_non_system_hash`, `dynamic_context_hash`, `tools_hash` — request events (prompt assembly fingerprint)
- `feed_name`, `feed_url`, `fetched_at`, `cached` — feed fetch events
- `feed_name`, `source`, `feed_status` (`included` / `empty` / `skipped_total_cap`), `feed_truncated`, `feed_source_bytes`, `feed_source_exact`, `feed_content_bytes`, `feed_block_bytes`, `feed_total_before`, `feed_total_after`, `feed_max_response_bytes`, `feed_max_total_bytes` — `feed_injection` events (one per manifest entry, recording whether the feed actually reached the provider-visible context after the per-feed and aggregate byte caps)
- `provider`, `key_id`, `action`, `reason`, `cooldown_until` — provider pool events
- `memory_service`, `memory_op`, `memory_status`, `memory_blocks`, `memory_bytes`, `memory_removed` — memory telemetry events

Every request/response pair produces two log events: one with `type: "request"` on ingress and one with `type: "response"` on egress. Error events use `type: "error"`. Intervention events use `type: "intervention"`. Token counts and cost estimates are extracted from the provider's response headers or body and attached to the response event.

### Spec Notes

The formal spec follows the reference implementation's telemetry shape:

- The `intervention` field is typed as `*string` with no `omitempty` tag. Every event emits `"intervention": null`, even when no intervention occurred. This is intentional -- it ensures log parsers can rely on the field always being present.
- The implementation uses `ts` for the timestamp field.
- Older docs and consumers may mention `intervention_reason`; the reference logger uses `intervention`.

These divergences are documented here as practical guidance. The reference implementation is the source of truth for runtime behavior.

::: info Structured, Not Self-Reported
The proxy provides a verifiable history of exactly what the bot *tried* to do versus what the infrastructure allowed. The reference implementation emits raw telemetry only; any behavioral drift score is external or future policy.
:::

## Operator Dashboard

The cllama proxy serves a real-time web UI for operator visibility.

| Property | Value |
|----------|-------|
| Host port | `8181` (default) |
| Container port | `8081` |

The dashboard shows:

- Live agent activity -- which agent is calling, which model, right now
- Provider status and error rates
- Cost breakdown per agent, per model, per time window
- Token usage across the pod

The dashboard updates in real time as agents make LLM calls. No polling, no delay.

## Ecosystem Implementations

### Passthrough Reference

The reference image (`ghcr.io/mostlydev/cllama`) implements the v1 API contract as a compiled-infrastructure proxy:

- Bearer-token identity resolution and validation.
- Environment validation (`CLAW_POD`, `CLAW_CONTEXT_ROOT`, provider credentials).
- OpenAI and Anthropic API format passthrough with format bridging.
- Late runtime-context assembly from compiled feeds, memory recall, time, and channel context.
- Managed tool injection and mediation from `tools.json`.
- Per-agent token usage and cost tracking.
- Structured audit logging of all traffic.
- Real-time operator dashboard.
- No policy prompt decoration, response amendment, PII redaction, or built-in drift scoring.

This image is used for testing and serves as the starting point for building custom policy engines.

### Future: Policy Plane

The policy-plane milestone adds bidirectional policy interception -- prompt decoration from policy modules, policy blocking, response amendment, redaction, and organization-specific drift scoring. The passthrough reference establishes the transport, identity, context, tool, memory, and telemetry contracts; policy services build policy logic on top.

### Third-Party Engines

Any OpenAI-compatible proxy that consumes the Clawdapus context mount layout can act as a governance layer. The spec defines the contract, not the implementation. Operators can build proprietary engines incorporating advanced DLP, RAG-based context injection, or conversational configuration.

### ClawRouter

[ClawRouter](https://github.com/BlockRunAI/ClawRouter) is a specialized cllama implementation focused on forced model routing, rate limiting, and compute metering. The reference passthrough provides the routing and telemetry contract; stricter budget or rate-limit enforcement belongs to a policy/budget implementation layered on that contract.

## Security Model

### Credential Isolation

The proxy enforces a strict credential boundary. Agent containers never see real provider API keys. The flow is:

1. `claw up` generates a dummy bearer token for each agent.
2. The agent's runner is configured with the proxy URL and dummy token.
3. The proxy receives the dummy token, validates it, strips it, and attaches the real provider key.
4. The agent cannot extract the real key because it only communicates with the proxy, never directly with the provider.

### Network Isolation

Within the pod's Docker network, agents can reach the proxy at `http://cllama-<type>:8080`. They cannot reach the provider directly because no provider credentials exist in their environment. Even if an agent attempted to call the provider API directly, it would lack authentication.

### Token Validation

Bearer tokens are validated against the `principals` field in each agent's `metadata.json`. A request with an invalid or missing token is rejected before any provider call is made. This is fail-closed: unknown tokens are denied, not passed through.

## Implementation Notes

These notes reflect the current state of the reference implementation (`cllama/` submodule) and are useful for debugging or extending.

### Proxy Handler

The proxy handler (`cllama/internal/proxy/handler.go`) has separate OpenAI and Anthropic request paths. It resolves model policy, appends late runtime context, mediates managed tools when `tools.json` is present, and forwards to the provider. There is no generic middleware hook system, policy prompt decoration, or response-amendment engine in the reference implementation.

### Logger Internals

The logger (`cllama/internal/logging/logger.go`) writes one JSON object per line to stdout. The `intervention` field is declared as `*string` (pointer to string) with no `omitempty` struct tag, so Go's JSON marshaler emits `"intervention": null` on every event. This is intentional -- it ensures log parsers can rely on the field always being present.

### Image Resolution

Operator flow is explicit now:

1. `claw pull` fetches the pinned cllama image the current `claw` binary expects.
2. `claw up` stays strict and tells you to run `claw pull` when the proxy image is missing.
3. `claw up --fix` performs that remediation automatically.

### Build and Publish

For end users, prefer `claw pull`. The raw multi-arch build below is contributor-only release tooling:

```bash
docker buildx build \
  --platform linux/amd64,linux/arm64 \
  -t ghcr.io/mostlydev/cllama:<tag> \
  --push cllama/
```

The `cllama/` directory is a git submodule pointing to the cllama source repository. Fresh clones leave it empty unless submodules are initialized. The published image on `ghcr.io` is public, so end users normally pull the pre-built image rather than building from source.

## Limitations

Current constraints to be aware of:

- **Single proxy type only.** Multi-proxy is represented in the data model, but the runtime currently fails fast if more than one proxy type is declared per pod. Proxy chaining is future work.
- **Passthrough only.** Full bidirectional policy interception with prompt decoration, policy blocking, response amendment, and redaction is future policy-plane work. The reference implementation does identity, routing, compiled context injection, managed tools, memory orchestration, telemetry, and cost tracking.
- **No per-turn hooks.** The Clawdapus `Driver` interface has four methods (`Validate`, `Materialize`, `PostApply`, `HealthProbe`) -- all run once at deploy/startup. There is no per-turn or per-request hook. Any per-request context enrichment must go through cllama or a runner-native mechanism.
- **Intervention field quirk.** The cllama logger emits `"intervention": null` on every event (the field has no `omitempty` tag). This is expected behavior, not a missing value.
- **Telemetry compatibility.** The reference implementation uses `intervention` and `ts`. Older logs or downstream consumers may still account for historical `intervention_reason` or `timestamp` names.

See the full [cllama specification on GitHub](https://github.com/mostlydev/clawdapus/blob/master/docs/CLLAMA_SPEC.md) for the formal standard.
