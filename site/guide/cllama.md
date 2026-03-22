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

A single proxy instance serves the entire pod. Bearer tokens resolve which agent is calling, so the proxy can apply per-agent policy, budgets, and logging.

## Credential Starvation

Isolation is achieved by strictly separating secrets:

- **The proxy holds the real API keys.** Provider credentials (OpenRouter, Anthropic, OpenAI) are configured in the pod-level `cllama-defaults.env` block and never enter agent containers.
- **Agents get unique bearer tokens.** Each agent (and each ordinal of a scaled agent) receives a unique token generated during `claw up`.
- **No credentials, no bypass.** Because agents lack the credentials to call providers directly, all successful inference *must* pass through the proxy -- even if a malicious prompt tricks the agent into ignoring its configured base URL.

::: warning Keys Never Enter Agent Containers
Provider API keys belong in `x-claw.cllama-defaults.env` at the pod level. They are injected into the cllama proxy container only. Agent containers receive bearer tokens, not API keys.
:::

## Identity Resolution

The proxy uses bearer tokens to resolve caller identity. Each token maps to a specific agent (or agent ordinal), which means the proxy can:

- Apply per-agent policy and cost budgets
- Track per-agent token usage and spend
- Log which agent made which request
- Enforce different model access per agent

The token format is `<agent-id>:<secret>`, generated fresh on every `claw up`. The proxy loads a principals file mapping tokens to agent identities and their compiled contract context.

## Bidirectional Interception

The cllama specification defines a full bidirectional interception pipeline. The proxy sits between the runner and the provider, intercepting traffic in both directions. The runner never knows the proxy exists -- it thinks it is talking directly to the model.

### Outbound Interception (Agent to Provider)

Before the LLM sees the prompt, the proxy can evaluate and modify the outbound request:

- **Tool scoping** -- If the agent's request contains `tools`, the proxy evaluates them against the agent's identity and active policy modules. Unauthorized tool calls are silently dropped based on who the agent is and what it is allowed to do.
- **Prompt decoration** -- The proxy may modify the outbound `messages` array, injecting specific rules, priorities, or warnings derived from the agent's compiled `enforce` contract and active policy modules.
- **Policy blocking** -- If the outbound prompt violates a loaded policy module, the proxy may short-circuit the request entirely and return an error or a mock response. The agent never reaches the provider.
- **Forced model routing** -- Even if the agent requests a specific model (e.g., `gpt-4o`), the proxy may seamlessly rewrite the request to use a different, operator-approved model (e.g., `claude-3-haiku`). The agent does not know its model was downgraded. This enforces strict compute budgets across the fleet.

### Inbound Interception (Provider to Agent)

After the provider responds but before the runner sees the result, the proxy can evaluate and amend:

- **Response amendment** -- If the response contains content that drifts from the agent's contracted purpose, the proxy may rewrite or drop that content.
- **PII leakage blocking** -- Responses containing restricted information (personal data, credentials, internal identifiers) can be intercepted before reaching the agent.
- **Drift scoring** -- The proxy analyzes how far the provider's raw response drifted from the agent's ideal behavior defined in the contract, emitting a structured log of the drift score.

::: info Passthrough vs Policy
The reference `passthrough` implementation currently performs identity resolution, model rewriting, and cost tracking only. It does **not** touch the `messages` array -- no prompt decoration, no response amendment. Full bidirectional interception is the `cllama-policy` proxy type, which is future work.
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
│   └── metadata.json    # Identity, bearer token, handles, policy modules
├── crypto-crusher-1/
│   ├── AGENTS.md
│   ├── CLAWDAPUS.md
│   └── metadata.json
└── analyst/
    ├── AGENTS.md
    ├── CLAWDAPUS.md
    └── metadata.json
```

### Container-Side Mount

The host directory is bind-mounted into the cllama container at `/claw/context/<agent-id>/`. The proxy reads `CLAW_CONTEXT_ROOT` (defaults to `/claw/context`) and loads each subdirectory as an agent identity.

::: warning The context/ Segment Is Required
The mount path must include the `context/` directory segment. The proxy expects `CLAW_CONTEXT_ROOT` to point at the directory containing agent subdirectories, not directly at an agent's files.
:::

### Scaled Services

For services with `count > 1`, context is generated **per ordinal**. A service named `crypto-crusher` with `count: 3` produces three separate context directories: `crypto-crusher-0/`, `crypto-crusher-1/`, `crypto-crusher-2/`. Each ordinal gets its own bearer token, its own compiled contract, and its own audit trail.

The `metadata.json` file in each directory contains the bearer token secret used for authentication. The proxy validates incoming tokens against these metadata files to resolve caller identity.

## API Format Handling

The proxy handler supports two distinct API formats through separate code paths, routed by request path:

### OpenAI Format

Requests to `/v1/chat/completions` are handled as OpenAI format. The payload contains a `messages[]` array and a `model` field. The proxy rewrites the `model` field to the operator-assigned provider and model, then forwards the request to the resolved upstream endpoint.

### Anthropic Format

Requests to `/v1/messages` are handled as Anthropic format. The payload uses a top-level `system` field rather than embedding system messages in the `messages` array. The proxy forwards Anthropic-specific headers (`Anthropic-Version`, `Anthropic-Beta`) and routes directly to the Anthropic provider.

### Format Bridging

When the resolved provider uses Anthropic format but the incoming request is OpenAI format (`/v1/chat/completions`), the proxy routes through OpenRouter instead, which accepts OpenAI format for all models. This transparent bridging means agents do not need to know which provider or format their assigned model requires.

::: tip Pure Passthrough
In passthrough mode, the proxy rewrites the `model` field and forwards. It does **not** touch the `messages` array. No prompt decoration, no system message injection -- those capabilities are reserved for the `cllama-policy` proxy type.
:::

## Environment Variables

The cllama container receives its configuration through environment variables injected by `claw up`.

| Variable | Description |
|---|---|
| `CLAW_POD` | The name of the pod (e.g., `crypto-ops`). |
| `CLAW_CONTEXT_ROOT` | Path to the shared context mount root (defaults to `/claw/context`). |
| `PROVIDER_API_KEY_*` | Real provider API keys -- `OPENAI_API_KEY`, `ANTHROPIC_API_KEY`, `OPENROUTER_API_KEY`, etc. |

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
```

::: warning cllama-env, Not environment
Provider API keys belong in `x-claw.cllama-defaults.env` (or service-level `x-claw.cllama-env`), never in the service's compose `environment:` block. Putting real keys in `environment:` defeats credential starvation -- the agent container would have direct provider access.
:::

## Cost Accounting

The proxy extracts token usage from every LLM response, multiplies by the pricing table, and tracks cost per agent, per provider, and per model. This gives operators real-time visibility into spend without relying on provider dashboards that aggregate across all API keys.

```bash
$ claw ps

TENTACLE          STATUS    CLLAMA    DRIFT
crypto-crusher-0  running   healthy   0.02
crypto-crusher-1  running   healthy   0.04
crypto-crusher-2  running   WARNING   0.31
```

## Audit Logging

Every request through the proxy produces a structured JSON log entry on stdout:

- Timestamp
- Agent identity (resolved from bearer token)
- Model requested and model served
- Request latency
- Token counts (prompt + completion)
- Cost (computed from token counts and pricing)
- Intervention reason (if the proxy modified or blocked the request)

These logs are the raw telemetry for the `claw audit` command and for the Master Claw's fleet governance decisions.

::: info Structured, Not Self-Reported
Drift is independently scored from proxy telemetry -- not self-reported by the agent. The proxy provides a verifiable history of exactly what the bot *tried* to do versus what it was *allowed* to do.
:::

## Operator Dashboard

The cllama proxy serves a real-time web UI:

- **Host port 8181** (container port 8081) by default
- Live agent activity -- which agent is calling, which model, right now
- Provider status and error rates
- Cost breakdown per agent, per model, per time window

The dashboard updates in real time as agents make LLM calls. No polling, no delay.

## Pod YAML Configuration

Enable cllama at the pod level so all services inherit it:

```yaml
x-claw:
  pod: my-fleet
  cllama-defaults:
    proxy: [passthrough]
    env:
      OPENROUTER_API_KEY: "${OPENROUTER_API_KEY}"
      ANTHROPIC_API_KEY: "${ANTHROPIC_API_KEY}"
```

The `passthrough` proxy type implements the transport layer: identity resolution, routing, cost tracking, and audit logging. Future proxy types (`cllama-policy`) will add bidirectional interception -- evaluating outbound prompts and amending inbound responses against the agent's behavioral contract.

## Limitations

Current constraints to be aware of:

- **Single proxy type only.** Multi-proxy is represented in the data model, but the runtime currently fails fast if more than one proxy type is declared per pod. Proxy chaining is a Phase 5 feature.
- **Passthrough only.** The `cllama-policy` proxy type (full bidirectional interception with prompt decoration, tool scoping, and response amendment) is future work. The reference implementation does identity, routing, and cost tracking.
- **No per-turn hooks.** The Clawdapus `Driver` interface has four methods (`Validate`, `Materialize`, `PostApply`, `HealthProbe`) -- all run once at deploy/startup. There is no per-turn or per-request hook. Any per-request context enrichment must go through cllama or a runner-native mechanism.
- **Intervention field quirk.** The cllama logger emits `"intervention": null` on every event (the field has no `omitempty` tag). This is expected behavior, not a missing value.
- **Spec divergences.** The specification uses `intervention_reason` and omits `error` from its type enum. The reference implementation uses `intervention` and emits `error` as a log type. Consumers should handle both.

## The Reference Implementation

The reference implementation is [`cllama`](https://github.com/mostlydev/cllama) -- a zero-dependency Go binary that handles:

- Bearer token identity resolution
- OpenAI and Anthropic API format passthrough
- Format bridging (OpenAI-format requests to Anthropic models via OpenRouter)
- Per-agent token usage and cost tracking
- Structured JSON audit logging
- Real-time operator dashboard

Any OpenAI-compatible proxy image that can consume Clawdapus context can act as the governance layer. The specification is an open standard -- build a proprietary policy engine incorporating DLP, RAG-based context injection, or conversational configuration, as long as it adheres to the API contract and context mount structure.

See the [cllama specification](/reference/cllama-spec) for the full standard.
