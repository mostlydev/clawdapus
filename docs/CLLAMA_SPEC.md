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
│   └── metadata.json    # Identity, handles, and active policy modules
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

### B. Outbound Interception (Decoration & Governance)
1. **Context Aggregation:** The proxy parses the `enforce` rules from the agent-specific `AGENTS.md`.
2. **Tool Scoping:** If the agent's request contains `tools`, the proxy evaluates the tools against the agent's identity and active policy modules. The proxy MAY drop tools the agent is not authorized to use.
3. **Prompt Decoration (Pre-Prompting):** The proxy MAY modify the outbound `messages` array, injecting specific rules, priorities, or warnings based on the aggregated context.
4. **Policy Blocking:** If the outbound prompt violates a loaded policy module, the proxy MAY short-circuit the request and return an error or a mock response.
5. **Forced Model Routing & Rate Limiting (Compute Metering):** Even if the agent requests a specific model (e.g., `gpt-4o`), the proxy MAY seamlessly rewrite the request to use a different, operator-approved model (e.g., `claude-3-haiku-20240307`) or provider. The proxy MAY also enforce hard rate limits (returning `429 Too Many Requests`). This allows the proxy to enforce strict compute budgets, meter usage, and prevent runaway agents from burning tokens, all without the agent knowing its model was downgraded or throttled by infrastructure.

### C. Provider Execution
The proxy strips the dummy token, attaches the real `PROVIDER_API_KEY`, and forwards the decorated request to the upstream LLM provider.

### D. Inbound Interception (Amendment & Drift Scoring)
1. **Response Evaluation:** The proxy evaluates the provider's response against the `enforce` rules in `/claw/context/<agent-id>/AGENTS.md` and the active `CLAW_POLICY_MODULES`.
2. **Amendment:** If the response contains restricted information (e.g., PII leakage) or violates the tone/instructions of the contract, the proxy MAY rewrite the content.
3. **Drift Scoring:** The proxy analyzes how far the provider's raw response drifted from the agent's ideal behavior defined in the contract. It MUST emit a structured log of this drift score.

### E. Egress
The (potentially amended) response is returned to the agent container.

## 5. Output and Audit Logging

The `cllama` proxy MUST emit structured JSON logs to `stdout`. Clawdapus collects these logs for the `claw audit` command.

Logs must contain the following fields:
- `ts`: ISO-8601 UTC timestamp.
- `claw_id`: The calling agent.
- `type`: one of `request`, `response`, `error`, `intervention`, `feed_fetch`, `provider_pool`, or `memory_op`.
- `intervention`: If the proxy modified a prompt or routing decision, it describes why.

Event-specific fields may also be present:
- `status_code`, `latency_ms`, `tokens_in`, `tokens_out`, `cost_usd` for request/response/error events
- `feed_name`, `feed_url` for feed fetch events
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
- Emits structured logs of all traffic.

This image is used for testing network integration and serves as the boilerplate for operators to build proprietary `cllama` policy engines (e.g., incorporating advanced DLP, RAG-based context injection, or conversational configuration).

### Routing and Compute Metering
Tools like **[ClawRouter](https://github.com/BlockRunAI/ClawRouter)** act as specialized instances of a `cllama` proxy focused entirely on forced model routing, rate limiting, and compute metering. A routing proxy seamlessly intercepts model requests, evaluates them against organizational budgets or provider availability, and dynamically routes, downgrades, or rate-limits the request to strictly contain costs across a fleet of untrusted agents.
