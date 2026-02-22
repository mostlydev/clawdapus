# The cllama Proxy Specification

**Status:** Draft (v1)

`cllama` is an open standard and reference architecture for a context-aware, bidirectional Large Language Model (LLM) governance proxy. It is designed to run as a sidecar container alongside autonomous agents (Claws) managed by Clawdapus.

This document defines the contract between Clawdapus (the orchestrator) and a `cllama` sidecar (the policy enforcer). Any container image that adheres to this specification can be used as a `CLLAMA` proxy.

## 1. Core Principles

- **Bidirectional Interception:** `cllama` intercepts outbound prompts (agent → provider) and inbound responses (provider → agent).
- **Intelligent Authorization:** The sidecar does not just passively log. It is context-aware. It receives the agent's identity, active rules (`enforce`), and available tools, and makes dynamic allow/deny/amend decisions.
- **Credential Starvation:** The proxy acts as a secure firewall. The agent container is provisioned with a dummy token. The proxy holds the real provider API keys, preventing the agent from bypassing governance.
- **Conversational Upgradability:** While not strictly required for v1, the proxy architecture is designed to eventually support natural language configuration (updating rules dynamically via conversation).

## 2. API Contract

A `cllama` sidecar MUST expose an HTTP API compatible with the OpenAI Chat Completions API (`POST /v1/chat/completions`).

- **Listen Port:** The proxy MUST listen on `0.0.0.0:8080`.
- **Base URL Replacement:** Clawdapus configures the agent's runner (e.g., OpenClaw, Claude Code) to use `http://<sidecar-hostname>:8080/v1` as its LLM base URL.

## 3. Context Injection (The Environment)

Clawdapus injects the agent's operational context into the `cllama` container at startup via environment variables and mounted files. The proxy uses this context to scope its enforcement rules.

### Environment Variables

| Variable | Description |
|---|---|
| `CLAW_ID` | The unique, stable ordinal name of the calling agent (e.g., `crypto-crusher-0`). |
| `CLAW_POD` | The name of the pod (e.g., `crypto-ops`). |
| `CLAW_HANDLES_JSON` | A JSON-serialized map of the agent's public platform identities (e.g., `{"discord": "123456"}`). |
| `CLAW_POLICY_MODULES` | Comma-separated list of active policy modules (e.g., `financial-advice-block,pii-filter`). |
| `CLAW_ALLOWED_MODELS` | Comma-separated list of pinned models the agent is allowed to request. The proxy MUST reject requests for models not in this list. |
| `PROVIDER_API_KEY_*` | The real provider keys (e.g., `OPENAI_API_KEY`, `ANTHROPIC_API_KEY`) supplied securely by the operator. |

### Bound Context Files (Read-Only)

Clawdapus bind-mounts the agent's generated behavioral contract and infrastructure map into the proxy container.

- **`/claw/AGENTS.md`**: The compiled behavioral contract. This file is critical for `cllama`. It contains the agent's base instructions, concatenated with any `include` documents marked with the `enforce` or `guide` modes (see ADR-009). The proxy parses this file to understand the specific rules it must enforce for this exact agent instance.
- **`/claw/CLAWDAPUS.md`**: The infrastructure map. Lists the agent's declared surfaces and skills. The proxy can use this to understand what tools the agent *should* be attempting to use.

## 4. Pipeline Execution (The Request Lifecycle)

When the agent sends a `POST /v1/chat/completions` request to the proxy, the proxy SHOULD execute the following pipeline:

### A. Pre-Flight (Ingress)
1. **Authentication:** Verify the agent is using the expected dummy token.
2. **Model Validation:** Ensure the requested `model` is within the `CLAW_ALLOWED_MODELS` list.

### B. Outbound Interception (Decoration & Governance)
1. **Context Aggregation:** The proxy parses the `enforce` rules from `/claw/AGENTS.md`.
2. **Tool Scoping:** If the agent's request contains `tools`, the proxy evaluates the tools against the agent's identity (`CLAW_ID`) and active `CLAW_POLICY_MODULES`. The proxy MAY drop tools the agent is not authorized to use or inject a system prompt explicitly forbidding certain tool arguments.
3. **Prompt Decoration (Pre-Prompting):** The proxy MAY modify the outbound `messages` array, injecting specific rules, priorities, or warnings based on the aggregated context.
4. **Policy Blocking:** If the outbound prompt violently violates a loaded policy module (e.g., attempting a known exploit), the proxy MAY short-circuit the request and return a simulated, non-compliant `400 Bad Request` or a mock response directly to the agent.
5. **Forced Model Routing & Rate Limiting (Compute Metering):** Even if the agent requests a specific model (e.g., `gpt-4o`), the proxy MAY seamlessly rewrite the request to use a different, operator-approved model (e.g., `claude-3-haiku-20240307`) or provider. The proxy MAY also enforce hard rate limits (returning `429 Too Many Requests`). This allows the proxy to enforce strict compute budgets, meter usage, and prevent runaway agents from burning tokens, all without the agent knowing its model was downgraded or throttled by infrastructure.

### C. Provider Execution
The proxy strips the dummy token, attaches the real `PROVIDER_API_KEY`, and forwards the decorated request to the upstream LLM provider.

### D. Inbound Interception (Amendment & Drift Scoring)
1. **Response Evaluation:** The proxy evaluates the provider's response against the `enforce` rules in `/claw/AGENTS.md` and the active `CLAW_POLICY_MODULES`.
2. **Amendment:** If the response contains restricted information (e.g., PII leakage) or violates the tone/instructions of the contract, the proxy MAY rewrite the content.
3. **Drift Scoring:** The proxy analyzes how far the provider's raw response drifted from the agent's ideal behavior defined in the contract. It MUST emit a structured log of this drift score.

### E. Egress
The (potentially amended) response is returned to the agent container.

## 5. Output and Audit Logging

The `cllama` proxy MUST emit structured JSON logs to `stdout`. Clawdapus collects these logs for the `claw audit` command.

Logs must contain the following fields:
- `timestamp`: ISO-8601.
- `claw_id`: The calling agent.
- `type`: `request`, `response`, `intervention`, or `drift_score`.
- `intervention_reason`: If the proxy modified a prompt, dropped a tool, or amended a response, it must describe *why*, referencing the specific policy module or `enforce` rule that triggered the intervention.

## 6. Ecosystem Implementations

### The Passthrough Reference
Clawdapus provides a reference image: `ghcr.io/mostlydev/cllama-passthrough`.

The passthrough reference:
- Adheres to the v1 HTTP API and Listen Port.
- Validates the environment (`CLAW_ID`, etc.) and mounts.
- Acts as a pure, transparent proxy (no decoration, no amendment).
- Emits structured logs of all traffic.

This image is used for testing network integration and serves as the boilerplate for operators to build proprietary `cllama` policy engines (e.g., incorporating advanced DLP, RAG-based context injection, or conversational configuration).

### Routing and Compute Metering
Tools like **[ClawRouter](https://github.com/BlockRunAI/ClawRouter)** act as specialized instances of a `cllama` proxy focused entirely on forced model routing, rate limiting, and compute metering. A routing proxy seamlessly intercepts model requests, evaluates them against organizational budgets or provider availability, and dynamically routes, downgrades, or rate-limits the request to strictly contain costs across a fleet of untrusted agents.
