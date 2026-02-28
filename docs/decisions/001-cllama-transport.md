# ADR-001: cllama Transport

**Date:** 2026-02-18
**Status:** Accepted
**Decided during:** 3-agent deliberation (arch-review room)

## Context

cllama is a bidirectional LLM proxy — it intercepts both outbound prompts (runner → LLM) and inbound responses (LLM → runner). The runner must never know cllama exists. The transport mechanism determines how interception works and what runners are supported.

## Decision

**Primary mode: Shared Pod-Level Proxy (Updated 2026-02-21).**

*Note: Initially, the architecture specified a "sidecar per Claw" model. This was updated to a shared pod-level proxy to reduce resource overhead and enable centralized compute budgeting across the fleet.*

Each pod with a `CLLAMA` directive gets one or more typed proxy services injected into the generated compose file by `claw up`. Services are named `cllama-<type>` (for example, `cllama-passthrough`). The proxy layer:

- Exposes an OpenAI-compatible API endpoint on a pod-internal network.
- Is configured from `Cllama []string` declarations (image and pod-level), enabling future chainable proxy stacks.
- Resolves multi-agent identity via unique per-agent **Bearer Tokens**.
- Receives the runner's LLM calls (runner's `*_API_BASE` env vars are rewritten to point at the shared proxy).
- Dynamically loads the specific agent's behavioral contract from a shared context mount (`/claw/context/<agent-id>/`).
- Holds the real LLM provider API keys (runners never see them, relying on "Credential Starvation").
- Applies the governance pipeline bidirectionally (decoration, tool scoping, drift scoring).
- Enforces pod-wide rate limits and compute budgets.

**Phase 4 execution boundary:** The data model supports multiple proxy types, but runtime currently fails fast if more than one type is declared. Multi-proxy chain execution is intentionally deferred to Phase 5.

**Secondary mode: adapter (documented, not built yet).**

For runners that use local models or embedded SDK clients that don't make HTTP calls to an LLM base URL, a runner-specific adapter would be needed. This is documented as a known gap. No concrete runner currently requires it — all four target runners (OpenClaw, Nanobot, Claude Code, custom scripts) use HTTP-based LLM calls.

Adapter mode will be designed and built when a concrete runner requires it.

## Consequences

**Positive:**
- Works with any runner that makes HTTP calls to an LLM endpoint — no runner integration needed.
- Key isolation is free — runner never holds provider API keys (Credential Starvation).
- Logging and audit are centralized at the pod proxy.
- Proxy failure = LLM calls fail = fail-closed (runner can't bypass to direct provider).
- **Resource Efficiency:** One proxy per pod per declared proxy type instead of one per agent.
- **Compute Control:** Enables pod-wide rate limiting and centralized budget enforcement.

**Negative:**
- Latency added to every LLM call (proxy hop).
- Runners using local/embedded models are not covered until adapter mode is built.
- Streaming responses require the proxy to handle SSE correctly.

**Risks:**
- If a runner uses a non-standard LLM API format (not OpenAI-compatible), the sidecar needs format adapters
- Sidecar must handle all LLM provider quirks (rate limits, retries, error formats) transparently

## Alternatives Considered

1. **Transparent network proxy** — intercept at the network layer (iptables/eBPF). More invisible but much harder to implement, debug, and handle streaming. Rejected: too complex for Phase 2.

2. **Runner SDK hooks** — instrument each runner to call cllama before/after LLM calls. Maximally flexible but requires per-runner integration and doesn't work for opaque runners. Rejected as default: too coupled.

3. **Shared volume FIFO** — runner writes intent to a FIFO, sidecar reads, evaluates, writes response. Simple but doesn't support streaming and adds latency. Rejected: poor UX for streaming-heavy runners.
