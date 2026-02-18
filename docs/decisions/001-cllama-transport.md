# ADR-001: cllama Transport

**Date:** 2026-02-18
**Status:** Accepted
**Decided during:** 3-agent deliberation (arch-review room)

## Context

cllama is a bidirectional LLM proxy — it intercepts both outbound prompts (runner → LLM) and inbound responses (LLM → runner). The runner must never know cllama exists. The transport mechanism determines how interception works and what runners are supported.

## Decision

**Primary mode: HTTP sidecar proxy (built first).**

Each Claw gets a `<name>-cllama` sidecar container injected into the generated compose file by `claw up`. The sidecar:

- Exposes an OpenAI-compatible API endpoint on a pod-internal network
- Receives the runner's LLM calls (runner's `*_API_BASE` env vars are rewritten to point at the sidecar)
- Holds the real LLM provider API keys (runner never sees them)
- Applies the cllama pipeline bidirectionally (purpose → policy → tone → obfuscation)
- Logs all requests and responses for drift scoring and audit
- Enforces `require_cllama` policy modules on tool calls routed to services

**Secondary mode: adapter (documented, not built yet).**

For runners that use local models or embedded SDK clients that don't make HTTP calls to an LLM base URL, a runner-specific adapter would be needed. This is documented as a known gap. No concrete runner currently requires it — all four target runners (OpenClaw, Nanobot, Claude Code, custom scripts) use HTTP-based LLM calls.

Adapter mode will be designed and built when a concrete runner requires it.

## Consequences

**Positive:**
- Works with any runner that makes HTTP calls to an LLM endpoint — no runner integration needed
- Key isolation is free — runner never holds provider API keys
- Logging and audit are centralized at the sidecar
- Sidecar failure = LLM calls fail = fail-closed (runner can't bypass to direct provider)

**Negative:**
- One extra container per Claw (resource overhead)
- Latency added to every LLM call (proxy hop)
- Runners using local/embedded models are not covered until adapter mode is built
- Streaming responses require the sidecar to handle SSE correctly

**Risks:**
- If a runner uses a non-standard LLM API format (not OpenAI-compatible), the sidecar needs format adapters
- Sidecar must handle all LLM provider quirks (rate limits, retries, error formats) transparently

## Alternatives Considered

1. **Transparent network proxy** — intercept at the network layer (iptables/eBPF). More invisible but much harder to implement, debug, and handle streaming. Rejected: too complex for Phase 2.

2. **Runner SDK hooks** — instrument each runner to call cllama before/after LLM calls. Maximally flexible but requires per-runner integration and doesn't work for opaque runners. Rejected as default: too coupled.

3. **Shared volume FIFO** — runner writes intent to a FIFO, sidecar reads, evaluates, writes response. Simple but doesn't support streaming and adds latency. Rejected: poor UX for streaming-heavy runners.
