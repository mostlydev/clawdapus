# The cllama Proxy Specification

**Status:** Draft (v1)

`cllama` is an open standard and reference architecture for a context-aware, bidirectional Large Language Model (LLM) governance proxy. It is designed to run as a **shared pod-level service** managed by Clawdapus, serving multiple autonomous agents (Claws) within the same pod.

This document defines the contract between Clawdapus (the orchestrator) and a `cllama` proxy (the policy enforcer). Any container image that adheres to this specification can be used as a `CLLAMA` proxy.

## 1. Core Principles

- **Bidirectional Interception:** `cllama` intercepts outbound prompts (agent → provider) and inbound responses (provider → agent).
- **Multi-Agent Identity:** A single proxy serves multiple agents. Identity is established via unique per-agent **Bearer Tokens** supplied in the `Authorization` header.
- **Intelligent Authorization:** The proxy is context-aware. It uses the bearer token to load the specific agent's identity, active rules (`enforce`), and available tools to make dynamic allow/deny/amend decisions.
- **Credential Starvation:** The proxy acts as a secure firewall. Agent containers are provisioned with unique dummy tokens. The proxy holds the real provider API keys, preventing agents from bypassing governance.
- **Conversational Upgradability:** While not strictly required for v1, the proxy architecture is designed to eventually support natural language configuration (updating rules dynamically via conversation).

## 2. API Contract

A `cllama` sidecar MUST expose a canonical ingress surface matrix for runner traffic.

Minimum required surfaces:

| Surface | Path | Payload family | Default use |
|---|---|---|---|
| OpenAI Chat Completions | `POST /v1/chat/completions` | OpenAI-compatible chat/completions | All non-Anthropic providers unless an explicit exception is documented |
| Anthropic Messages | `POST /v1/messages` | Anthropic Messages | Anthropic-family providers and explicit Anthropic-wire exceptions |

- **Listen Port:** The proxy MUST listen on `0.0.0.0:8080`.
- **Base URL Replacement:** Clawdapus configures the agent's runner (e.g., OpenClaw, Claude Code) to use `http://cllama-<type>:8080/v1` as its LLM base URL (first proxy in chain when chaining is enabled). The runner then targets one of the canonical ingress paths beneath that base URL.
- **Provider Identity vs Transport:** Operator-facing model refs keep provider identity (`google/gemini-*`, `anthropic/*`, etc.). The proxy ingress surface is a transport contract selected by infrastructure; runners MUST NOT invent synthetic provider prefixes such as `cllama/google`, and the shared ingress contract rejects them when compiling cllama-facing config.
- **Vendor-Native Extensions:** Additional vendor-native ingress surfaces MAY exist, but only as explicit, documented exceptions when a concrete runner cannot target the canonical surfaces. They are not the default contract.
- **Implementation Scope (Phase 4):** The wire protocol supports chained proxies, but runtime enforcement currently allows only one proxy type per pod. Declaring multiple proxy types fails fast until Phase 5 chain execution is implemented.

## 3. Context Injection (The Environment & Shared Mounts)

Clawdapus injects the pod's operational context into the `cllama` container at startup. Because a single proxy serves multiple agents, context is provided through a combination of global environment variables and a **Shared Context Mount**.

### Environment Variables (Global Pod Context)

| Variable | Description |
|---|---|
| `CLAW_POD` | The name of the pod (e.g., `crypto-ops`). |
| `PROVIDER_API_KEY_*` | The real provider keys (e.g., `OPENAI_API_KEY`, `ANTHROPIC_API_KEY`) supplied securely by the operator. |
| `CLAW_CONTEXT_ROOT` | The path to the shared context directory (defaults to `/claw/context`). |

### Shared Context Mount (Agent-Specific Context)

Clawdapus bind-mounts a shared directory into the proxy (at `CLAW_CONTEXT_ROOT`) containing subdirectories for every agent in the pod. The directory name matches the agent's ID.

```text
/claw/context/
├── crypto-crusher-0/
│   ├── AGENTS.md        # Compiled contract (includes, enforce, guide)
│   ├── CLAWDAPUS.md     # Infrastructure map
│   ├── metadata.json    # Identity, handles, and active policy modules
│   └── runtime-reminders.json # Optional operator-authored reminder snippets
├── crypto-crusher-1/
│   └── ...
```

## 4. Pipeline Execution (The Request Lifecycle)

When an agent makes a request to the proxy, it MUST include a unique **Bearer Token** in the `Authorization` header:

`Authorization: Bearer <agent-id>:<secure-secret>`

Agents SHOULD also include `X-Claw-Consumer-Session-Epoch` when the runner can provide a process-stable restart identifier. The value is opaque to `cllama`; it must stay stable for the lifetime of the consumer process and change when that process restarts. `cllama` uses this optional header to decide whether channel-context cursors represent the current consumer session or a previous one. Missing or blank values preserve legacy cursor behavior.

The proxy SHOULD execute the following pipeline:

### A. Pre-Flight (Ingress & Identity)
1. **Identity Resolution:** The proxy uses the `<agent-id>` portion (e.g., `crypto-crusher-0`) to resolve the agent's context from the corresponding subdirectory in `CLAW_CONTEXT_ROOT`.
2. **Authentication:** The proxy MUST validate the `<secure-secret>` before processing the request.
3. **Model Validation:** Ensure the requested `model` is within the `CLAW_ALLOWED_MODELS` list (parsed from `metadata.json`).

### B. Outbound Interception (Context, Routing & Policy Slots)
1. **Context Aggregation:** The proxy loads the agent-specific compiled context from `CLAW_CONTEXT_ROOT` and MAY inject infrastructure-owned runtime context such as runtime reminders, feeds, memory recall, time context, and channel deltas.
2. **Tool Scoping:** If the agent's request contains `tools`, the proxy evaluates the request against the compiled tool manifest for that agent. The reference implementation only exposes tools declared for that agent; policy-plane implementations MAY further filter or deny tools.
3. **Prompt Decoration (Pre-Prompting):** Policy-plane implementations MAY modify the outbound `messages` array, injecting specific rules, priorities, or warnings based on the compiled context. The passthrough reference does not perform policy prompt decoration.
4. **Policy Blocking:** If the outbound prompt violates a loaded policy module, a policy-plane implementation MAY short-circuit the request and return an error or a mock response. The passthrough reference does not perform policy blocking.
5. **Forced Model Routing, Budgets & Compute Metering:** Even if the agent requests a specific model (e.g., `gpt-4o`), the proxy MAY seamlessly rewrite the request to use a different, operator-approved model (e.g., `claude-3-haiku-20240307`) or provider. The reference implementation meters usage, records cost telemetry, and enforces compiled per-agent budget/request caps before provider dispatch. On cap breach it returns `429 Too Many Requests` and emits an `intervention` such as `budget_exceeded` or `rate_limited`. If the budget ledger is unavailable, the reference implementation logs `budget_check_unavailable` and defaults to fail-open unless `CLLAMA_BUDGET_FAIL_MODE=closed` is configured.

### C. Provider Execution
The proxy strips the dummy token, attaches the real `PROVIDER_API_KEY`, and forwards the decorated request to the upstream LLM provider.

### D. Inbound Interception (Policy Evaluation Slot)
1. **Response Evaluation:** Policy-plane implementations MAY evaluate the provider's response against the compiled contract and active policy modules. The passthrough reference forwards provider responses without policy evaluation.
2. **Amendment:** If the response contains restricted information (e.g., PII leakage) or violates the tone/instructions of the contract, a policy-plane implementation MAY rewrite the content. The passthrough reference does not amend responses.
3. **Drift Scoring:** Behavioral drift is an organization-specific policy metric. A policy-plane implementation MAY emit drift or score telemetry, but the passthrough reference does not define or emit a built-in `drift_score`.

### E. Egress
The (potentially amended) response is returned to the agent container.

## 5. Output and Audit Logging

The `cllama` proxy MUST emit structured JSON logs to `stdout`. Clawdapus collects these logs for the `claw audit` command.

Logs must contain the following fields:
- `ts`: ISO-8601 UTC timestamp.
- `claw_id`: The calling agent.
- `type`: one of `request`, `response`, `error`, `intervention`, `feed_fetch`, `feed_injection`, `runtime_reminder`, `memory_op`, `channel_context_op`, or `provider_pool`.
- `intervention`: If the proxy modified routing, mediation, or other request handling, it describes why. In the reference logger this field is present on every event and is `null` when no intervention occurred.

Event-specific fields may also be present:
- `status_code`, `latency_ms`, `tokens_in`, `tokens_out`, `cost_usd` for request/response/error events
- `feed_name`, `feed_url` for feed fetch events
- `feed_name`, `source`, `feed_status`, and byte-budget fields for feed injection events
- `runtime_reminder_id`, `runtime_reminder_status`, `runtime_reminder_cadence`, `runtime_reminder_placement`, and `runtime_reminder_reason` for reminder injection events
- `kind`, `channels`, `retained`, `returned`, `omitted`, byte counts, and source/status fields for channel context operations
- `provider`, `key_id`, `action`, `reason`, `cooldown_until` for provider-pool events
- `memory_service`, `memory_op`, `memory_status`, `memory_blocks`, `memory_bytes`, `memory_removed` for memory telemetry events

## 6. Session History

### Overview

cllama writes a durable JSONL session history at the proxy boundary. This is an infrastructure-owned record of every completed inference transaction — written by the proxy, not by agents. It is distinct from `/claw/memory`, which is runner-owned and agent-writable.

### Environment Variable

| Variable | Default | Description |
|---|---|---|
| `CLAW_SESSION_HISTORY_DIR` | `/claw/session-history` | Host-side base directory for per-agent JSONL history files. When set, cllama writes one file per agent at `<dir>/<agent-id>/history.jsonl`. |

When orchestrated by Clawdapus, `claw up` automatically bind-mounts `.claw-session-history/` (relative to the pod file) into the cllama container at `/claw/session-history` whenever cllama is enabled for any service in the pod.

### Layout

```text
/claw/session-history/
├── crypto-crusher-0/
│   └── history.jsonl
├── crypto-crusher-1/
│   └── history.jsonl
```

One JSONL file per agent. Each line is a single entry. Entries are appended on every successful upstream completion (HTTP 2xx only). Non-2xx responses are not recorded in session history; they appear only in structured audit logs (see §5).

### Entry Schema

Each line is a JSON object with the following fields:

| Field | Type | Description |
|---|---|---|
| `version` | integer | Schema version. Currently `1`. |
| `id` | string | Stable source-event ID for replay and deduplication. |
| `ts` | string | RFC3339 timestamp of when the response was received. |
| `claw_id` | string | Agent ID that issued the request. |
| `path` | string | Request path (e.g., `/v1/chat/completions`). |
| `requested_model` | string | Model string as sent by the agent. |
| `effective_provider` | string | Provider name after routing (e.g., `anthropic`). |
| `effective_model` | string | Model name forwarded to the upstream provider. |
| `status_code` | integer | HTTP status code returned by the upstream. |
| `stream` | boolean | Whether the response was streamed (SSE). |
| `request_original` | object | The request body as received from the agent, before any proxy modification. |
| `request_effective` | object | The request body as forwarded to the upstream provider, after credential swap and any model rewrite. |
| `response` | object | See response payload below. |
| `usage` | object | Token counts extracted from the response: `prompt_tokens` (integer), `completion_tokens` (integer). |
| `usage.reported_cost_usd` | number (float) | Cost in USD reported by the provider; omitted when not available |

**Response payload (`response` field):**

| Field | Type | Description |
|---|---|---|
| `format` | string | `"json"` for standard JSON responses; `"sse"` for Server-Sent Events streams. |
| `json` | object | Present when `format` is `"json"`. The parsed response body. |
| `text` | string | Present when `format` is `"sse"`. The raw event stream text. |

### Phase 1 Scope

Phase 1 is retention only. cllama writes the history; no read API exists. Agents cannot query their own history programmatically. No prompt injection, no retrieval, no summarization. The JSONL files are accessible to operators via the host filesystem mount for offline analysis, auditing, and future tooling.

## 7. Ecosystem Implementations

### The Passthrough Reference
Clawdapus provides a reference image: `ghcr.io/mostlydev/cllama`.

The passthrough reference:
- Adheres to the v1 ingress surface matrix and Listen Port.
- Validates the environment (`CLAW_POD`, `CLAW_CONTEXT_ROOT`, provider credentials), bearer-token identity resolution, and mounts.
- Acts as a pure, transparent proxy (no decoration, no amendment).
- Emits structured logs of all traffic and budget/rate interventions.

This image is used for testing network integration and serves as the boilerplate for operators to build proprietary `cllama` policy engines (e.g., incorporating advanced DLP, RAG-based context injection, or conversational configuration).

### Routing and Compute Metering
Tools like **[ClawRouter](https://github.com/BlockRunAI/ClawRouter)** act as specialized instances of a `cllama` proxy focused on forced model routing, provider availability, rate limiting, and compute metering. The reference passthrough establishes the identity, per-agent budget/rate caps, routing, and telemetry contract; specialized engines can layer richer provider selection and organization-specific cost policy on that contract.
