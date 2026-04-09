# ADR-023: Explicit cllama Ingress Surface Matrix

**Date:** 2026-04-09
**Status:** Accepted
**Depends on:** ADR-008 (cllama Sidecar Standard)
**Related to:** ADR-019 (Model Policy Authority and Declared Failover)
**Implementation:** Issue #134

## Context

ADR-008 established `cllama` as a standardized sidecar interface, but the contract text described it primarily as an OpenAI-compatible proxy. The reference implementation and runtime had already moved beyond that: `cllama` accepts both OpenAI Chat Completions (`/v1/chat/completions`) and Anthropic Messages (`/v1/messages`).

That mismatch became operationally important in issue #127. The outage was not fundamentally about Gemini. The real problem was that the system lacked a single authoritative answer to a more basic question:

**What protocol surface should a runner speak when `cllama` is in the path?**

Without an explicit contract:

- provider identity (`google`, `anthropic`, `openrouter`) became entangled with runner transport selection
- OpenClaw carried a private provider-to-protocol switch for `cllama`
- future provider additions could silently reintroduce vendor-native APIs behind `cllama`
- docs described `cllama` as OpenAI-only while the runtime already supported more than that

## Decision

1. `cllama` owns a canonical ingress surface matrix for runner traffic.
2. The minimum required ingress surfaces are:
   - OpenAI Chat Completions: `POST /v1/chat/completions`
   - Anthropic Messages: `POST /v1/messages`
3. Provider identity remains in operator-facing model refs.
   - Example: `google/gemini-3-flash-preview`, `anthropic/claude-sonnet-4`
   - We do not invent synthetic provider prefixes such as `cllama/google`.
   - The shared ingress contract rejects reserved synthetic ingress prefixes when compiling cllama-facing config.
4. When `cllama` is enabled, drivers compile declared model refs to one of the canonical ingress surfaces through shared infrastructure code.
5. Anthropic-family providers, and other explicit Anthropic-wire exceptions, route through the Anthropic Messages surface.
6. All other providers route through the OpenAI Chat Completions surface by default.
7. Vendor-native ingress surfaces are allowed only as explicit, documented exceptions when a concrete runner cannot target the canonical surfaces.
8. Runner-specific configuration must map from the canonical ingress surface, not directly from provider names.

## Rationale

This keeps the separation of concerns clean:

- provider identity remains stable in the operator contract
- transport selection becomes infrastructure-owned instead of runner-owned
- adding a new provider does not require every driver to rediscover which wire protocol should be used behind `cllama`

The result is a smaller and more legible trust boundary. Runners stay untrusted. `cllama` remains the policy and routing layer. Drivers become compilers from model refs to canonical proxy surfaces.

## Consequences

**Positive:**
- A single shared contract now decides which ingress surface a runner should target behind `cllama`.
- OpenClaw no longer owns the canonical provider-to-surface decision in a private helper.
- Future provider additions are less likely to regress into vendor-native routing bugs.
- The public spec and ADR set can describe the actual runtime instead of a partially outdated approximation.

**Negative:**
- The contract is intentionally narrower than "support every upstream API natively", which means truly incompatible runners still require explicit adapter work.
- New canonical surfaces now require an architecture decision, not just a local driver patch.

## Notes

- This ADR extends ADR-008; it does not replace the broader sidecar-standard decision.
- This ADR does not change ADR-019 model-policy authority. It only formalizes the runner-to-proxy transport contract.
- Only OpenClaw needed an immediate integration change for this ADR. The other in-tree drivers do not currently compile provider identity into runner-specific API-surface enums; they only rewrite base URLs, API keys, or generic custom-provider fields when cllama is enabled.
