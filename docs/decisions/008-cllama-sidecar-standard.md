# ADR-008: cllama as a Standardized Sidecar Interface

**Date:** 2026-02-21
**Status:** Accepted

## Context

Initially, `cllama` was conceived as a specific proxy component injected by Clawdapus to intercept LLM traffic. However, as the need for testing, custom policy enforcement, and intelligent authorization grows, treating `cllama` as a single, monolithic implementation is too restrictive. The proxy needs to understand *who* is calling it (identity) and *what* they are allowed to do (rights, instructions) to make contextual decisions.

## Decision

We formalize `cllama` as a **mini-standard** rather than a single hardcoded implementation. 

1. **The cllama Contract:** A `cllama` sidecar is any container image that:
   - Exposes an OpenAI-compatible proxy endpoint.
   - Accepts Clawdapus orchestration context (e.g., agent identity, loaded policy modules, capability labels, and the behavioral contract) injected via environment variables or volume mounts by the Clawdapus pod emitter.
   - Emits standardized logs or labels back to Clawdapus for audit and drift scoring.
2. **Identity and Authorization Awareness:** The Clawdapus driver will inject the agent's identity (ordinal, pod name, `HANDLE` projections) and any `require_cllama` constraints directly into the sidecar's environment. The sidecar uses this context to act as an intelligent authorization and control layer, capable of enforcing rights dynamically.
3. **Reference Implementation:** We will provide a base `cllama-passthrough` container image. This reference image will perform no mutations (acting as a pure proxy) but will validate the contract, log requests, and prove the networking/interception model. It serves as the default for testing and the foundation for custom sidecars.

## Rationale

Formalizing `cllama` as a standard makes the policy pipeline pluggable. Operators can build custom sidecars with proprietary DLP (Data Loss Prevention) rules, specific compliance checks, or advanced drift scoring, simply by conforming to the OpenAI-compatible proxy interface and consuming the injected Clawdapus context. 

Passing identity and rights into the sidecar elevates it from a dumb proxy to a context-aware governance enforcement point, capable of blocking a specific agent from taking an action based on its unique constraints.

## Consequences

**Positive:**
- Extensibility: Operators can drop in their own policy sidecars.
- Testing: The `cllama-passthrough` reference image allows us to verify the interception wiring without complex policy logic.
- Granular Control: Sidecars can enforce per-agent policies because they are strictly aware of the caller's identity, rights, and expected behavior.

**Negative:**
- We must formally define and version the "cllama context contract" (the specific environment variables, log formats, and mounts passed to the sidecar) to ensure compatibility.
