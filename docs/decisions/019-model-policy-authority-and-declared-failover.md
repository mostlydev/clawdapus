# ADR-019: Model Policy Authority and Declared Failover

**Date:** 2026-03-27
**Status:** Accepted
**Depends on:** ADR-007 (LLM Isolation via Credential Starvation), ADR-001 (cllama Transport), ADR-014 (Telemetry Normalization)
**Implementation:** Plan: docs/plans/2026-03-27-cllama-model-policy-and-declared-fallbacks.md

## Context

ADR-007 established that runners are untrusted workloads. Credential starvation ensures a runner cannot call a provider directly — all inference must route through cllama. However, a gap remained: **cllama trusted the runner's model selection**. The runner told cllama which provider and model to use, and cllama accepted that choice without consulting any operator-declared policy.

This gap allows three classes of failure:

**Runner bugs.** A runner may serialize the wrong model slot, strip a provider prefix, or silently rewrite a model ID during serialization. These are not hypothetical. The incident that prompted this ADR: the OpenClaw runner strips the provider prefix from `xai/grok-4.1-fast` and sends bare `grok-4.1-fast` to cllama. cllama rejects the request because it requires a provider-prefixed model string. The agent fails. Real money is on the line.

**Policy bypass.** A runner that can choose its own provider can escalate to a more expensive model, switch to a provider that was never approved for the pod, or — via prompt injection — leak context to an undeclared provider. Credential starvation stops the agent from calling a banned provider *directly*, but it does not stop the agent from asking cllama to forward traffic there.

**Incomplete failover.** cllama can rotate keys within a single provider when a key goes dead or enters cooldown. It cannot move to a declared fallback provider when the primary provider itself is fully exhausted. An operator who declares `MODEL primary xai/grok-4.1-fast` and `MODEL fallback anthropic/claude-haiku-4-5` expects the fallback to be used automatically — not a 502.

The root problem is that model authority is in the wrong place. Runners are untrusted workloads. Model and provider selection is a governance decision. It belongs in the infrastructure layer, compiled from operator-authored pod contracts.

## Decision

### 1. Model and provider authority moves to cllama

The runner's requested model is a **hint**, not an authority. cllama evaluates it against a compiled per-agent model policy and makes the final dispatch decision. The runner cannot expand its own policy by requesting a model that was not declared.

This extends credential starvation from key-level isolation to model-level isolation. The combined guarantee: a runner cannot reach an undeclared provider (no key), and cllama will not forward traffic to an undeclared provider on the runner's behalf (policy enforcement).

### 2. Model policy is compiled by `claw up`

Pod authors declare the allowed model set through `Clawfile` `MODEL` directives. `claw up` compiles this into a structured `model_policy` object inside each agent's `metadata.json`. The compiled policy contains:

- `default` — the model to use when the runner's request is missing, malformed, or disallowed
- `allowed` — the full set of declared models, each with its slot name and full `provider/model` ref
- `failover` — the ordered chain cllama may automatically traverse when the chosen provider has no usable keys

`MODEL` directives shift in meaning. They are no longer runner configuration hints. They are pod author policy declarations: the models this agent is permitted to use and in what order failover should proceed.

### 3. Request normalization with clamping as the default mode

Before dispatching, cllama normalizes the runner's requested model against the compiled allowlist:

- Exact full ref match: use it unchanged
- Bare model that uniquely matches one allowed full ref: normalize to that ref (fixes the stripped-prefix incident class)
- Ambiguous or disallowed model: clamp to `default`
- Missing model: clamp to `default`

The default enforcement mode is **clamp**, not **reject**. Disallowed or malformed runner requests are silently rewritten to an allowed model and the request proceeds. The rewrite is logged as a policy intervention.

Clamping is the correct default because runner bugs are expected in practice. Production pods should continue operating while policy wins. A strict `reject` mode is reserved for future explicit opt-in once the ecosystem is stable.

### 4. Declared automatic failover

After normalization, cllama builds a candidate execution list:

1. The normalized requested model
2. If that model is in the failover chain, the remaining declared failover entries follow it

cllama tries each candidate in order. For each candidate, it selects an available key from the candidate's provider. If the provider has no usable keys (all dead or in cooldown), it advances to the next candidate. If no candidate is usable, it returns an infrastructure error.

Failover is strictly bounded to the declared failover chain. cllama never substitutes an undeclared provider.

### 5. Policy interventions are observable

Every time cllama rewrites a runner request because of policy, it emits a structured log event with the agent ID, the raw requested model, the effective model, and the reason for the rewrite (`bare_model_normalized`, `disallowed_clamped`, `ambiguous_clamped`, `provider_exhausted_failover`). Session history records both the original requested model and the effective dispatch.

This connects to ADR-014's telemetry contract: policy interventions are first-class events, not silent implementation details.

### 6. Backward compatibility during migration

If an agent's `metadata.json` does not contain `model_policy` (i.e., the agent was deployed before this change), cllama falls through to legacy behavior: it requires a full `provider/model` string from the runner and applies no policy. This preserves behavior for agents that have not been redeployed and allows rolling migration.

Once `claw up` is run for a pod, every agent in that pod gets a compiled `model_policy` and the new enforcement applies on the next container restart.

## Rationale

**Why is the runner the wrong place for model authority?**

Runners are untrusted by design. They execute agent workloads that process external input, including arbitrary user messages and tool results. A runner's model field can be influenced by a sufficiently crafted prompt. Even without adversarial input, runner bugs are common — serialization errors, wrong slot selection, version drift. Governance decisions (which providers are approved, what the cost envelope is, what the failover order is) belong to the operator and should be enforced by infrastructure, not trusted from the workload.

**Why compile-time policy rather than runtime lookup?**

`claw up` is already the compiler for feeds, auth, context, and surfaces. Model policy is the same class of operator decision: declared once, compiled into the artifact, enforced at runtime. A runtime lookup would require cllama to call back to some external policy store on every request, adding latency, a new failure mode, and a component that doesn't exist. The metadata.json context file is already loaded per-request for auth validation — adding compiled policy there is zero marginal cost.

**Why clamp instead of reject as the default?**

Runner bugs are a fact of life in any ecosystem with multiple driver implementations and active development. If the first production deployment of model policy causes production outages because runner model strings don't exactly match the allowlist, operators lose trust in the feature. Clamping fixes the production symptom (the request goes through) while enforcing the right governance outcome (only declared providers get traffic). The intervention log tells the operator what happened so the runner can be fixed. Reject mode can be added later as a configurable option once the ecosystem has stabilized.

**Why not just special-case the stripped-prefix bug?**

A special case for bare model strings would fix the immediate incident but leave the policy bypass class open. The right fix is the same fix — model policy authority — and it subsumes the special case. The bare-model incident is the motivating example, not the scope.

**Why no free-form "best available" routing?**

Operators declare the models their agents are allowed to use. If cllama were to substitute any available provider when the declared ones are exhausted, it would route traffic to providers the operator never approved. The value of declared failover is its boundary: automatic resilience within the declared policy, hard stop at the policy edge.

## Consequences

**Positive:**
- Runner bugs in model serialization are absorbed by policy rather than causing outages.
- Prompt-induced provider switching is blocked at the infrastructure layer.
- Declared fallback chains make credential starvation genuinely useful under provider exhaustion — not just per-key, but per-provider.
- `MODEL` directives in Clawfiles become authoritative governance declarations, not runner configuration hints.
- The operator's view of model policy is compile-time and inspectable in `metadata.json`.
- Policy interventions are visible in telemetry, connecting to `claw audit` (ADR-014).

**Negative:**
- `claw up` must be run to activate policy for a pod. Agents deployed before this change continue to run without enforcement until redeployed.
- The implied contract that a runner can self-select its model is broken. Runners that rely on sending arbitrary model strings will be clamped silently. This is correct behavior but may surprise operators who have not read this ADR.
- `dispatchWithRetry` in cllama requires structural changes to support cross-provider candidate traversal. The current function is provider-scoped; the new design is candidate-list-scoped.
- Clawfiles with no `MODEL` directives produce an empty policy. cllama treats an empty policy as unconstrained (legacy behavior). Operators who expect enforcement must declare at least one `MODEL` slot.

## Amendment (2026-08-03): Ordered Multi-Fallback Chains

cllama's declared failover originally consumed a single `fallback` slot. As of
cllama's managed read-failover work, `FailoverRefs` walks **every** allowed
entry whose slot is `fallback`, in declared order. Clawdapus now compiles full
chains:

- **Pod surface.** `x-claw.models.fallback` accepts a scalar (unchanged) or an
  ordered list. List entries normalize to reserved internal slot keys
  `fallback`, `fallback-2`, `fallback-3`, ... in declared order. Declaring the
  ordinal keys directly is rejected; the list is the only authoring surface.
- **Policy emission.** Every chain link is emitted into `model_policy.allowed`
  with slot name `fallback`, ordered primary → chain → other slots. Older
  cllama versions use only the first fallback entry and treat the rest as
  allowed models — graceful degradation, no compatibility break.
- **Atomic family merge.** The fallback family merges as one unit everywhere:
  a service-level `fallback` declaration (scalar or list) replaces the entire
  `models-defaults` chain, and a pod-declared chain replaces the entire
  image-label fallback family. Chains never interleave across layers.
- **Images stay scalar.** `MODEL fallback` in a Clawfile declares at most one
  fallback; repeated declarations fail with guidance toward the pod surface.
  Failover chains are deployment policy, not image authorship: the image
  author cannot know which providers a pod holds keys for. If image-declared
  chains become a real need, indexed labels (`claw.model.fallback-2`) are the
  planned encoding — deferred until a producer exists.
- **Slots stay purposeful.** Only the fallback family participates in
  failover. A model declared under any other slot (`analysis`, `cheap`, ...)
  is allowed but never a failover target, matching cllama's contract.
- **The runner ingress still bounds reachability.** An OpenAI-format runner
  enters through `/v1/chat/completions`; its candidates must be directly
  OpenAI-compatible, or an Anthropic ref must be bridged through a configured
  OpenRouter provider. An Anthropic-format runner enters through
  `/v1/messages`, where every candidate must be `anthropic/...`. Ordered
  policy does not imply arbitrary request-shape conversion. cllama fails
  closed when a chain cannot be encoded for the active ingress.
