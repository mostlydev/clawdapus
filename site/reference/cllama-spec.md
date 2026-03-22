# cllama Specification

`cllama` is an open standard for a context-aware, bidirectional LLM governance proxy. It runs as a shared pod-level service managed by Clawdapus, serving multiple autonomous agents within the same pod.

This page summarizes the key concepts. For the full specification, see [CLLAMA_SPEC.md on GitHub](https://github.com/mostlydev/clawdapus/blob/master/docs/CLLAMA_SPEC.md).

## Core Principles

- **Bidirectional interception** -- intercepts outbound prompts (agent to provider) and inbound responses (provider to agent).
- **Multi-agent identity** -- a single proxy serves all agents in a pod. Identity is established via unique per-agent bearer tokens.
- **Credential starvation** -- agent containers receive dummy tokens. The proxy holds the real provider API keys. No credentials, no bypass.
- **Context-aware authorization** -- the proxy uses bearer tokens to load agent identity, active rules, and available tools for dynamic allow/deny/amend decisions.

## Transport Model

The proxy exposes an HTTP API compatible with the OpenAI Chat Completions API.

| Property | Value |
|----------|-------|
| Endpoint | `POST /v1/chat/completions` |
| Listen port | `0.0.0.0:8080` |
| Base URL (as seen by runner) | `http://cllama-<type>:8080/v1` |
| Auth header | `Authorization: Bearer <agent-id>:<secure-secret>` |

Clawdapus configures each agent's runner to use the proxy URL as its LLM base URL. The runner thinks it is talking directly to the model provider. Two code paths handle OpenAI format (`messages[]`) and Anthropic format (top-level `system` field).

::: info Proxy chaining
The wire protocol supports chained proxies, but runtime currently allows only one proxy type per pod. Declaring multiple proxy types fails fast until chain execution is implemented.
:::

## Identity Resolution

Every agent in the pod receives a unique bearer token during `claw up`. The token format is `<agent-id>:<secure-secret>`.

When a request arrives, the proxy:

1. Extracts the `<agent-id>` from the bearer token.
2. Loads the agent's context from `CLAW_CONTEXT_ROOT/<agent-id>/`.
3. Validates the `<secure-secret>`.
4. Checks the requested model against the agent's allowed models.

For `count > 1` services, bearer tokens and context are per ordinal (e.g. `analyst-0`, `analyst-1`), not per base service name.

## Context Mount Layout

Clawdapus bind-mounts a shared context directory into the proxy container. Each agent gets a subdirectory containing its compiled governance context.

```
/claw/context/
  analyst-0/
    AGENTS.md          # Compiled contract (includes, enforce, guide)
    CLAWDAPUS.md       # Infrastructure map (surfaces, skills, peers)
    metadata.json      # Identity, handles, active policy modules
  analyst-1/
    ...
```

| File | Purpose |
|------|---------|
| `AGENTS.md` | The agent's compiled behavioral contract, including inlined `enforce` and `guide` content from `INCLUDE` directives. |
| `CLAWDAPUS.md` | Infrastructure context: surfaces, mount paths, peer handles, feeds, and available skills. |
| `metadata.json` | Machine-readable identity (handles, allowed models, bearer token auth). |

Host-side path: `.claw-runtime/context/<agent-id>/`. Mounted into the cllama container at `/claw/context/<agent-id>/`.

## Environment Variables

The proxy receives global pod context via environment variables.

| Variable | Description |
|----------|-------------|
| `CLAW_POD` | The pod name. |
| `PROVIDER_API_KEY_*` | Real provider keys (e.g. `OPENAI_API_KEY`, `ANTHROPIC_API_KEY`). |
| `CLAW_CONTEXT_ROOT` | Path to the shared context directory (defaults to `/claw/context`). |

Provider API keys belong in `x-claw.cllama-env` at the service level, not in regular agent `environment:` blocks.

## Request Lifecycle

### Pre-flight

Identity resolution, token validation, and model authorization.

### Outbound interception

The proxy may:
- Parse `enforce` rules from the agent's contract.
- Scope or drop unauthorized tools.
- Decorate outbound prompts (inject rules, priorities, warnings).
- Block requests that violate policy.
- Rewrite the model (forced routing, downgrades for budget enforcement).
- Enforce rate limits (return `429 Too Many Requests`).

### Provider execution

The proxy strips the dummy token, attaches the real provider API key, and forwards the request upstream.

### Inbound interception

The proxy may:
- Evaluate responses against `enforce` rules.
- Amend content that violates the contract (PII leakage, tone drift).
- Score behavioral drift.

### Egress

The (potentially amended) response is returned to the agent.

## Telemetry Format

The proxy emits structured JSON logs to stdout. Clawdapus collects these for the `claw audit` command.

| Field | Description |
|-------|-------------|
| `timestamp` | ISO-8601 timestamp. |
| `claw_id` | The calling agent's identifier. |
| `type` | Event type: `request`, `response`, `error`, `intervention`. |
| `intervention_reason` | Why the proxy modified a prompt, dropped a tool, or amended a response. References the specific policy module or rule. |

The reference implementation also emits token usage (input/output counts), cost estimates, model name, and latency for every request/response pair.

## Reference Implementation

The passthrough reference image (`ghcr.io/mostlydev/cllama`) implements the v1 API contract as a pure transparent proxy:

- Bearer-token identity resolution and validation.
- Environment validation (`CLAW_POD`, `CLAW_CONTEXT_ROOT`, provider credentials).
- Structured audit logging of all traffic.
- No prompt decoration, no response amendment.

This image is used for testing and serves as the starting point for building custom policy engines.

## Operator Dashboard

The cllama proxy includes a real-time web dashboard.

| Property | Value |
|----------|-------|
| Host port | `8181` (default) |
| Container port | `8081` |

The dashboard shows live agent activity, provider status, token usage, and cost breakdown across the pod.
