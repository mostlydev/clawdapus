# ADR-025: Policy Plane — cllama PolicyEvaluator Architecture and Contract

**Date:** 2026-06-23
**Status:** Accepted (design ratified; hook implementation #307/#308 deferred)
**Depends on:** ADR-009 (Contract Composition and Policy), ADR-019 (Model Policy Authority), ADR-020 (cllama Compiled Tool Mediation), ADR-023 (cllama Ingress Surface Matrix)
**Tracks:** #302 (policy plane epic) → #306 (this ADR) → #307 (PolicyEvaluator hooks), #308 (rules artifact), #310 (proxy-enforced budgets — the first concrete enforcement, shipped independently)

## Context

The manifesto's seventh principle is *governance by a separate process*: an agent's executive policy
is enforced by infrastructure the agent cannot reach, not by the agent's own goodwill. Today the
`cllama` proxy realizes part of that promise — it is a credential firewall, a model-policy clamp
(ADR-019), a managed-tool mediator (ADR-020), and, as of cllama v0.7.4 (#310), a **per-agent budget
and rate enforcer**. But it is otherwise a pure passthrough: it rewrites the `model` field and
forwards. It does **not** consult a contract-derived policy engine, decorate prompts, gate or amend
responses, or score drift. CLLAMA_SPEC §4B–D reserve those slots; nothing fills them.

#310 closed the most prominent honesty gap (hard budget caps now enforce, not merely meter). It did so
as **core hard-caps** that need no external policy service — the right split: an *infrastructure
guarantee* lives in the proxy, while *adaptive, contract-derived* governance belongs to a separate
policy process. This ADR ratifies the architecture and contract for that policy process — the **policy
plane** — so #307 (the hook interface) and #308 (the compiled rules artifact) can be built against a
fixed design rather than discovered during implementation.

The design below is grounded in a full map of the current proxy (`cllama/internal/proxy/handler.go`
at v0.7.4): the dual OpenAI (`/v1/chat/completions`, `messages[]`) and Anthropic (`/v1/messages`,
top-level `system`) ingress paths, the plain vs managed-tool fork (`hasManagedTools`), and the four
egress functions (`streamResponse`, `forwardManagedToolAwareResponse`, and the two managed terminal
writes in `toolmediation.go`).

## Decision

**A policy *sidecar* consulted by cllama over HTTP through an optional `PolicyEvaluator`, not a second
proxy image.** A second proxy would duplicate dual ingress, provider key custody, 600+ lines of
managed mediation, feeds, history, and telemetry. The sidecar consumes only the decision surface.

Wiring follows the existing `HandlerOption` pattern (`handler.go`): `WithPolicyEvaluator(p)`, stored
nil-safe on the `Handler` like `sessionRecorder`/`feedFetcher`/`accumulator`. **A nil evaluator makes
every hook a guard-clause early return → bit-identical passthrough.** This is the load-bearing
invariant and the conformance test #307 must ship: removing the policy service returns the proxy to
exact passthrough behaviour, byte for byte.

Configuration (parsed at `NewHandler`, mirroring `CLLAMA_*` env conventions):

- `CLLAMA_POLICY_URL` — unset ⇒ `WithPolicyEvaluator(nil)` ⇒ passthrough.
- `CLLAMA_POLICY_TOKEN` — bearer for the sidecar.
- `CLLAMA_POLICY_TIMEOUT_MS` — per-hook timeout.
- `CLLAMA_POLICY_FAIL_MODE` — `open` | `closed`. **Gates fail-closed, decoration/score fail-open** by
  default (a decoration or scoring outage must not break traffic; a gate outage is the operator's
  risk choice). This mirrors the `CLLAMA_BUDGET_FAIL_MODE` precedent (#310): default fail-open for
  measurement-style hooks, explicit `closed` for strict enforcement.

### The five interception points

| # | Hook | Call site (v0.7.4) | Effect |
|---|------|--------------------|--------|
| 1 | **Pre-flight gate** | top of `handleOpenAI` / `handleAnthropicMessages`, after context load + secret validation, before dispatch | allow / deny the whole request → `policy_denied` |
| 2 | **Tool filter** | before `injectManagedOpenAITools` / `injectManagedAnthropicTools` | drop disallowed managed tools from the manifest for this request |
| 3 | **Prompt decoration** | at `feeds.AppendLateContext` / `AppendAnthropicLateContext` (the existing infra-context seam) | mutate outbound `messages[]` (OpenAI) / `system`+`messages` (Anthropic) → `policy_decorated` |
| 4 | **Response gate** | reachable from `forwardResponse`; managed terminal writes in `toolmediation.go` | allow / deny / amend the final response → `policy_amended` |
| 5 | **Score / drift log** | `recordResponse` (the single chokepoint every success path calls) | fire-and-forget verdict/score on the captured response → `policy_flagged` |

Hooks 1–3 require touching **two** functions (the OpenAI and Anthropic handlers) — the duplicated
format paths are the main implementation hazard. Hook 5 converges cleanly at `recordResponse`, which
every successful path already calls.

### The hook-order matrix and its two invariants

The `{OpenAI, Anthropic} × {plain, managed-tool} × {streaming, non-streaming}` matrix collapses on two
structural facts in the current code:

1. **Managed-tool mode is always non-stream upstream.** `injectManagedOpenAITools` /
   `injectManagedAnthropicTools` force `stream=false`; the downstream SSE is *synthesized* from a
   fully-buffered JSON final. So **every managed-tool response can be hard-gated and amended** at the
   terminal-assistant point, regardless of what the runner requested. The only true streaming egress
   is **plain + stream**.
2. **Decoration (hook 3) runs exactly once per runner request**, before the mediation loop. The policy
   service does **not** see intermediate managed-tool rounds in v1; per-round visibility is deferred.

### v1 streaming semantics

The streaming-stall work (cllama v0.7.4, #319) made `streamResponse` defer `writeHeaders()` until the
first body byte. This is favourable for policy: a **gate-before-stream** decision (hook 4) can run on
the upstream response head before any downstream byte is committed — deny is clean (no partial leak).
Once the first chunk flushes, the response is irretractable.

- **v1:** plain+stream gets *gate-before-stream (deny only) + score-on-complete* (the full SSE text is
  captured in `responseBuf` and passed to `recordResponse`). Managed (always) and plain+non-stream get
  *full allow / deny / amend* (bodies are fully buffered).
- **Deferred:** mid-stream amendment; an opt-in pod-level "buffer-and-gate" mode for plain+stream;
  per-tool-round visibility; compliance-retry orchestration.

### Governor-principal non-recursion

A policy service that itself makes LLM calls (LLM-as-judge) routes them through cllama, which would
re-invoke the hooks → infinite regress. Resolution is **by construction**: the governor gets its own
agent context dir whose `metadata.json` carries `"policy_exempt": true`. `agentctx.Load` already
parses arbitrary metadata, so this is a one-method addition (`AgentContext.PolicyExempt()`); the
pre-flight hook checks it and structurally skips hooks 2–5. Because the token→context binding is
compile-time and validated, a runner cannot spoof it. A defence-in-depth loop-breaker header
(`X-Cllama-Policy-Origin`, added to the upstream skip-list) is a cheap safety net.

### The contract (CLLAMA_SPEC §4B–D fill)

Four `POST` endpoints, each carrying agent identity, `format` (`openai`|`anthropic`), `mode`
(`plain`|`managed`), `stream` (bool), and a reference to the compiled rules artifact (by path+digest):

- `/policy/gate-request` → `{verdict: allow|deny, reason, intervention}` (hooks 1+2)
- `/policy/decorate` → `{messages_patch | system_patch, intervention}` (hook 3)
- `/policy/gate-response` → `{verdict: allow|deny|amend, amended_body?, reason, intervention}` (hook 4;
  **v1 = allow/deny/amend only — no compliance-retry orchestration**, which would multiply dispatches
  and entangle failover/tool-budgets/history)
- `/policy/score` → `202`, fire-and-forget (hook 5)

The sidecar must honour the §streaming constraints — it must not request `amend` on a
`{stream:true, mode:plain}` response.

### Telemetry and the rules artifact

Verdicts reuse `LogIntervention(clawID, model, reason)` verbatim — `policy_denied`, `policy_amended`,
`policy_decorated`, `policy_flagged`. **Zero new log columns, zero `claw audit` changes** (the
`Intervention *string` field already serialises on every event). Drift stays an intervention string +
optional score; the spec's stale `drift_score` field is not reintroduced.

The compiled rules (#308, "RulesManifest v1") are emitted by `GenerateContextDir` as `rules.json`
alongside `metadata.json`/`tools.json` (conditioned on non-empty rules), and loaded by `agentctx`
mirroring `loadToolsManifest`. The policy hooks pass it **by path+digest** (the ContextDir is already
on `AgentContext`), so the sidecar reads it directly. Schema: ordered raw-text rules with stable IDs,
mode, and provenance; compiled pod rules win; `enforce` blocks are the only enforced tier in v1,
`guide` blocks advisory. Source: ADR-009's `enforce`/`guide`/`reference` modes, already concatenated
into `AGENTS.md`.

## Scope: v1 vs deferred

**v1 (what #307/#308 build):** the four HTTP hooks; nil-passthrough conformance; gate/decorate/amend
for non-stream + all managed; gate-before-stream + score-on-complete for plain+stream; governor
principal-skip; the `rules.json` artifact.

**Deferred:** mid-stream amend; buffer-and-gate mode; per-tool-round visibility; compliance-retry
orchestration; cross-turn decoration caching.

## Relationship to #310 (budget enforcement)

#310 ships **ahead of and independent of** this ADR: budget hard-caps compiled into `metadata.json`,
checked at the pre-flight point, rejecting with `429` + `budget_exceeded`/`rate_limited`, using the
session-history window as the ledger — **none** of the `PolicyEvaluator` HTTP machinery. This is the
manifesto's split made concrete: *infrastructure guarantees* (hard caps, identity, model authority)
live in cllama core and work with no policy service attached; *adaptive/conditional governance* (rules
evaluation, response gating, decoration, drift) is the policy plane. `graceful_switch`/adaptive
budgeting are the seam where the two meet, deferred to the policy service.

## Sequencing

**#306 (this ADR) is the convergence gate — it lands before any #307/#308 code.** Then #307 (cllama
`handler.go`/`toolmediation.go` hooks) and #308 (clawdapus `context.go` + cllama `agentctx.go` rules
artifact) proceed in parallel — different files, the same contract. #310 already shipped.

## Status notes

This ADR was converged adversarially (Claude draft + Codex review) during the 2026-06-23 governance
milestone and ratifies the design only. The PolicyEvaluator hook implementation (#307) and the rules
artifact (#308) are deferred to a later milestone; this document is their fixed contract.
