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

## The Reference Implementation

The reference implementation is [`cllama`](https://github.com/mostlydev/cllama) -- a zero-dependency Go binary that handles:

- Bearer token identity resolution
- OpenAI and Anthropic API format passthrough
- Per-agent token usage and cost tracking
- Structured JSON audit logging
- Real-time operator dashboard

The proxy is pure passthrough: it rewrites the `model` field and forwards. It does not touch the `messages` array. No prompt decoration, no system message injection -- that is reserved for the future `cllama-policy` type.

See the [cllama specification](/reference/cllama-spec) for the full standard. Any OpenAI-compatible proxy image that can consume Clawdapus context can act as the governance layer.
