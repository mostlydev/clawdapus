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

A `cllama` sidecar MUST expose an HTTP API compatible with the OpenAI Chat Completions API (`POST /v1/chat/completions`).

- **Listen Port:** The proxy MUST listen on `0.0.0.0:8080`.
- **Base URL Replacement:** Clawdapus configures the agent's runner (e.g., OpenClaw, Claude Code) to use `http://<sidecar-hostname>:8080/v1` as its LLM base URL.

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
