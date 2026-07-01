# Policy Plane Implementation — #308 → #307 → #309 (2026-07-01)

**Status:** DRAFT — awaiting adversarial review. Implements the ratified contract in
[ADR-025](../decisions/025-policy-plane.md); this plan does not re-decide the architecture, only
sequences and scopes the build.

**Depends on:** ADR-025 (Accepted). #310 (budget enforcement) already shipped as PR #322 (cllama
v0.7.4). This train has no dependency on the just-shipped v0.26.0 channel-compaction work.

## Why now

ADR-025 ratified the policy-plane design as a *fixed contract* precisely so #307/#308 could be built
without re-deriving it. The manifesto's seventh principle — governance by a separate process the agent
cannot reach — is realized in cllama today only as credential firewall + model clamp + tool mediation +
hard budgets. The adaptive/contract-derived layer (gate / decorate / amend / score) is reserved in
CLLAMA_SPEC §4B–D and unfilled. This train fills it.

## The load-bearing invariant (from ADR-025)

**A nil `PolicyEvaluator` must be byte-identical to today's passthrough.** This is the conformance test
#307 ships and the acceptance gate for the whole train: removing the policy service returns the proxy
to exact passthrough behaviour. Everything below is subordinate to this.

## Sequencing (agreed: #308 first)

ADR-025 §Sequencing permits #307/#308 in parallel (different files/repos, same contract). We deliberately
lead with **#308** because a deterministic, mounted `rules.json` gives the hook work (#307) a concrete
artifact to read and lets the rules format be reviewed *without* hot-path latency/failure semantics in
play. #309 lands after at least a thin #307/#308 slice exists to demonstrate.

```
#308 (clawdapus: rules artifact)  ──►  #307 (cllama: hooks that consume it)  ──►  #309 (demo + guide)
```

---

## Slice 1 — #308: Compile enforce/guide blocks into `rules.json` (clawdapus)

**Goal:** Emit a per-agent `RulesManifest v1` (`rules.json`) in the context mount, alongside
`metadata.json` / `tools.json`, compiled from ADR-009 `enforce`/`guide` include blocks (already
concatenated into `AGENTS.md`). `enforce` = the only enforced tier in v1; `guide` = advisory.

**Producer (clawdapus):**
- `internal/cllama/context.go` — add `RulesManifest` to `AgentContextInput`; write `rules.json` in
  `GenerateContextDir` **conditioned on non-empty rules** (mirror the `tools.json` emit path).
- `cmd/claw/compose_up.go` — populate the rules input from the parsed `include` blocks
  (`enforce`/`guide`) at the existing context-generation call sites. **Conflict note:** this edits
  `compose_up.go` — keep #312 (the compose_up split) out of this train.
- Schema (`RulesManifest v1`): ordered array of
  `{id, mode: "enforce"|"guide", text, source, content_sha256}` with **stable IDs** derived from the
  service-level include ID/mode/ordinal, plus provenance (`source` = which include produced it). Preserve
  current service-level declaration order; do not add include inheritance in this slice. Add a max-char
  validation mirroring the context-blocks primitive (#325) so oversized rules fail fast at `claw up`.
- Staleness/regen: `rules.json` regenerates on `claw up` like the other context artifacts; a changed
  digest must force a fresh mount.

**Consumer boundary:** do **not** make #308 a cllama change. #308 should stop at producing and
documenting the deterministic artifact. The cllama loader/accessors belong in #307 as the first
consumer commit, immediately before the hook wiring that uses them. That keeps #308 clawdapus-only and
avoids a cllama release for an inert stub.

**Docs:** document `RulesManifest v1` in `docs/CLLAMA_SPEC.md` (the §4B–D contract slot) — schema,
placement, digest semantics.

**Tests:** context-gen unit tests + a **golden `rules.json` fixture** (deterministic byte-for-byte
emit) covering: empty rules → no file; enforce+guide → ordered stable IDs + provenance; current
service-level include ordering; max-char → hard error at `claw up`.

**Release coupling:** producer side is clawdapus-only. It can land before cllama consumes it, but should
not claim runtime policy enforcement until #307 ships and the cllama submodule/tag are bumped.

---

## Slice 2 — #307: PolicyEvaluator hooks at the five interception points (cllama)

**Goal:** Load `rules.json` in `cllama/internal/agentctx/agentctx.go` and add an optional
`PolicyEvaluator` (HandlerOption pattern, stored nil-safe like `sessionRecorder`/`feedFetcher`/
`accumulator`). Five hooks; nil = guard-clause early return = bit-identical passthrough.

**Rules consumer:** load `rules.json` mirroring `loadToolsManifest`, expose it on `AgentContext` by
**path + digest** (`RulesPath()/RulesDigest()` accessors), and include the digest in every evaluator
request. Missing `rules.json` is valid for agents without compiled rules.

**Config (parsed at `NewHandler`, mirroring `CLLAMA_*`):**
- `CLLAMA_POLICY_URL` (unset ⇒ `WithPolicyEvaluator(nil)` ⇒ passthrough)
- `CLLAMA_POLICY_TOKEN`, `CLLAMA_POLICY_TIMEOUT_MS`
- `CLLAMA_POLICY_FAIL_MODE` = `open|closed`; **gates fail-closed, decorate/score fail-open** by default
  (mirrors `CLLAMA_BUDGET_FAIL_MODE`).

**The five hooks (call sites per ADR-025 §"five interception points", cllama `handler.go`/`toolmediation.go`):**
| # | Hook | Site | Effect |
|---|------|------|--------|
| 1 | Pre-flight gate | top of `handleOpenAI`/`handleAnthropicMessages`, after context+secret, before dispatch | allow/deny → `policy_denied` |
| 2 | Tool filter | before `injectManagedOpenAITools`/`injectManagedAnthropicTools` | drop disallowed managed tools |
| 3 | Prompt decoration | at `feeds.AppendLateContext`/`AppendAnthropicLateContext` | mutate outbound messages/system → `policy_decorated` |
| 4 | Response gate | `forwardResponse` + the two managed terminal writes in `toolmediation.go` | allow/deny/amend → `policy_amended` |
| 5 | Score/drift | `recordResponse` (single chokepoint) | fire-and-forget → `policy_flagged` |

**Call sites verified against current cllama v0.7.6 (`9054875`), 2026-07-01:** `handleOpenAI`
(handler.go:340), `handleAnthropicMessages` (:431); `AppendLateContext` (:374) /
`AppendAnthropicLateContext` (:466) — hook 3, immediately before tool injection; `injectManagedOpenAITools`
(:375, def :1256), `injectManagedAnthropicTools` (:468, def :1282) — hook 2; `forwardResponse` (:845)
and `streamResponse` (:881) both converge on `recordResponse` (:914) — hooks 4+5. The
`HandlerOption`/`WithX` nil-safe field pattern is confirmed (handler.go:72; siblings `accumulator`,
`feedFetcher`, `sessionRecorder`) → `WithPolicyEvaluator` drops in. `agentctx.Load` (:114) already
parses `Metadata map[string]any` → `PolicyExempt()` is a one-method add. ADR-025's contract still
matches the code — no drift since v0.7.4.

**Contract (4 HTTP endpoints, ADR-025 §contract):** `/policy/gate-request` (hooks 1+2),
`/policy/decorate` (3), `/policy/gate-response` (4; **v1 = allow/deny/amend only, no compliance-retry**),
`/policy/score` (5, `202` fire-and-forget). Each carries agent identity, `format`, `mode`, `stream`,
and the `rules.json` path+digest.

`/policy/gate-request` must be made explicit before coding hook 2: either return
`allowed_tools`/`denied_tools` for the filtered managed-tool manifest, or split tool filtering into a
separate evaluator method. The current ADR text names hooks 1+2 but only sketches an allow/deny verdict;
implementation cannot infer how a tool-filter result is represented.

**Sidecar visibility requirement:** ADR-025 says hooks pass `rules.json` by path+digest and the sidecar
reads it directly. That only works if the policy sidecar has the same context mount path as cllama
(for example `/claw/context/<agent-id>/...`). #307 must either document this as an operator wiring
requirement for `CLLAMA_POLICY_URL`, or #308/#307 must add compiler support for mounting the context
root into a declared policy service. Do not start #309's end-to-end demo until this is settled.

**The two structural facts that collapse the {OpenAI,Anthropic}×{plain,managed}×{stream,non-stream}
matrix (ADR-025):**
1. Managed-tool mode is always non-stream upstream (tools force `stream=false`; SSE is synthesized) →
   every managed response is hard-gateable/amendable at the terminal write. The only true streaming
   egress is **plain+stream**.
2. Decoration (hook 3) runs exactly once per runner request, before the mediation loop.

**v1 streaming semantics:** plain+stream gets *gate-before-stream (deny only) + score-on-complete*
(v0.7.4's deferred `writeHeaders()` makes deny clean — no partial leak). Managed + plain+non-stream get
*full allow/deny/amend* (fully buffered). **Deferred:** mid-stream amend, buffer-and-gate mode,
per-tool-round visibility, compliance-retry.

**Failure semantics (front-and-center — every branch decided + tested):**
| Condition | Behaviour |
|-----------|-----------|
| `CLLAMA_POLICY_URL` unset / disabled | nil evaluator → passthrough, no hooks (the load-bearing invariant) |
| sidecar timeout / conn error / 5xx | per `CLLAMA_POLICY_FAIL_MODE`: gates (1,2,4) **fail-closed** by default (deny + `policy_denied`), decorate (3) + score (5) **fail-open** (proceed undecorated / drop the score); `open` flips gates to allow |
| hook returns 4xx (proxy-side bad request) | treat as evaluator error → same fail-mode branch; log once, don't spin |
| invalid/missing `rules.json` (bad digest, parse error) | fail-closed for gates when rules were expected for this agent; otherwise passthrough. Never crash the request path |
| plain+stream, first byte already flushed | hook-4 amend impossible → score-on-complete only; a late deny is a no-op recorded as `policy_flagged` |
| governor `policy_exempt` | skip hooks 2–5 structurally, regardless of fail-mode |

**Governor non-recursion:** an LLM-judge policy service routes its own calls through cllama → regress.
Resolution (ADR-025): governor's `metadata.json` carries `"policy_exempt": true`; add
`AgentContext.PolicyExempt()` (one method — `agentctx.Load` already parses arbitrary metadata); the
pre-flight hook checks it and structurally skips hooks 2–5. Defence-in-depth: `X-Cllama-Policy-Origin`
loop-breaker header on the upstream skip-list.

**Telemetry:** reuse `LogIntervention(clawID, model, reason)` verbatim — `policy_denied`,
`policy_amended`, `policy_decorated`, `policy_flagged`. **Zero new log columns, zero `claw audit`
changes.**

**The main hazard:** hooks 1–3 touch **two** duplicated format functions (OpenAI + Anthropic). This is
where passthrough-equivalence is easiest to break. Mitigation: a shared hook-dispatch helper both paths
call, plus the conformance test below.

**Tests (the acceptance gate):**
- **Nil-passthrough conformance:** with no `PolicyEvaluator`, a representative request matrix
  ({OpenAI,Anthropic}×{plain,managed}×{stream,non-stream}) produces byte-identical output to a
  passthrough build. This is the load-bearing test.
- Per-hook behaviour against a fake policy sidecar: deny → `policy_denied`; decorate patches
  messages/system; managed amend rewrites the terminal assistant; plain+stream deny-before-first-byte
  is clean; score fires `202` without blocking.
- Governor `policy_exempt` skips hooks 2–5; loop-breaker header respected.
- Fail-mode: gate outage fail-closed (default) vs `open`; decorate/score outage fail-open.

**Release coupling:** cllama change → cllama release (next tag after v0.7.6, i.e. **v0.7.7**), then
submodule bump.

---

## Slice 3 — #309: Master Claw worked example + Fleet Governance guide

**Goal:** Demonstrate the governor closing the loop — read telemetry → threshold check → fire write
verbs — turning already-shipped Master Claw infra into something operators can use. This is the
manifesto's "governor" promise, currently undemonstrated.

- `examples/master-claw/` (extend) or a sibling: a pod where a master claw consumes telemetry and
  fires `fleet.budget.set` / `fleet.model.restrict` / `fleet.quarantine` on a threshold breach.
- `site/guide/fleet-governance.md` (new) + nav entry: the read-plane → decision → write-plane loop,
  principal scopes (pods/services/claw_ids/compose_services), and the five write verbs.
- A spike/integration test proving the closed loop (telemetry crosses threshold → write verb fires →
  effect observable), not just a doc walkthrough.

**Release coupling:** clawdapus + docs; site auto-deploys. No cllama change of its own (consumes #307
once available, but the demo can also stand on the already-shipped budget/model/quarantine verbs).

---

## Conflicts / landmines

- **#312 (split `compose_up.go`) stays OUT** — #308 edits `compose_up.go` + `context.go`; running #312
  concurrently guarantees conflicts. #312 goes on a quiet tree after this train.
- **cllama handler is the shared hot path** — #307 and the deferred #261 both touch it; do not run a
  second handler change in parallel. #261 stays deferred.
- **Passthrough-equivalence** is the whole ballgame for #307 — the dual-format duplication is the risk;
  the conformance test is non-negotiable.

## Open questions for review

1. **#308 rules ID scheme:** derive stable IDs from source-block content hash, or from
   contract-name + ordinal? (Determinism vs. readability.)
2. **#308/#307 parallel vs strict-serial:** ADR-025 allows parallel; do we want to start #307's
   nil-passthrough scaffold + conformance test in parallel with #308 (they don't share files), or hold
   strict #308→#307 to keep one reviewer focus at a time?
3. **#307 fake-sidecar test harness:** in-process `httptest` fake policy server in the cllama test
   suite — agree that's the right fixture for the per-hook + conformance tests?
4. **#309 scope:** minimum viable closed-loop demo for v1 (one threshold, one write verb), or a fuller
   multi-verb governor? Recommend MVP closed-loop first.
5. **Release shape:** one cllama v0.7.7 (#307 loader + hooks) plus one clawdapus release (#308 producer +
   submodule bump), or include #309 in the same release? Recommend #308+#307 in one train, #309 as the
   demonstrating fast-follow so the hooks can land without waiting on the example/guide/spike.

---

## Codex adversarial review (2026-07-01)

### Verdict

The plan is directionally right and publishable. I would treat it as **approved after the edits above**:
#308 first, #307 second, #309 as a fast-follow demonstration. The two material corrections are:

1. Keep #308 clawdapus-only. Move cllama `rules.json` loading/accessors into #307.
2. Resolve the sidecar context-mount and tool-filter response-shape gaps before implementation.

### Findings

- **#308 scope:** current `x-claw.include` is service-level only. `parseIncludes` returns each service's
  declared include list; there is no include-default inheritance surface today. The rules manifest should
  preserve current service-level declaration order and provenance. Do not invent include inheritance in
  #308 unless a separate issue explicitly adds that surface.
- **Rules identity:** use readable, source-stable IDs from the include ID/mode/ordinal, and store a
  separate content digest. Content-hash-only IDs are deterministic but make every text edit look like a
  different rule to policy telemetry and review tools.
- **Tool filtering:** ADR-025 collapses hooks 1+2 into `/policy/gate-request`, but the response shape only
  says allow/deny. #307 must define the filter result explicitly before touching managed-tool injection.
- **Policy sidecar access:** path+digest references are not sufficient unless the sidecar can read the
  same context mount. For v1, document the required mount path and add one fake-sidecar test that proves
  the evaluator receives a path that exists under the mounted context root. Compiler-managed policy
  service injection can remain a later issue if manual sidecar wiring is the v1 contract.
- **#309 nuance:** issue #309 says the Master Claw example is standalone-valuable and has no policy-plane
  dependency. It should still fast-follow #307 for this train only because this scoped train chose the
  policy-plane order, not because #309 is architecturally blocked.

### Open-Question Answers

1. **Rules ID scheme:** `include.<include_id>` for one block per include today; if a future parser splits
   a file into multiple rules, use `include.<include_id>.<ordinal>`. Add `content_sha256` separately.
2. **#308/#307 sequencing:** strict serial for implementation. Land/freeze the #308 manifest schema first,
   then implement #307 loader + hooks against it. Do not run parallel handler work while the artifact
   shape is still moving.
3. **Fake sidecar harness:** yes. Use `httptest.Server` for the HTTP evaluator and table-driven handler
   tests for outage/status/fail-mode branches. Keep a direct nil-evaluator conformance test separate.
4. **#309 scope:** MVP closed loop: one threshold, one write verb, one observable effect. A fuller
   multi-verb governor belongs after the thin loop proves the pattern.
5. **Release shape:** #308 producer can merge without a cllama release, but the user-facing release should
   wait until #307's cllama `v0.7.7` and submodule bump make the artifact active. #309 should be a
   fast-follow release unless it is trivial after #307.

### Acceptance Criteria To Add Before Coding

- `rules.json` schema includes `version`, ordered `rules[]`, `id`, `mode`, `text`, `source`, and
  `content_sha256`.
- #307 contract tests cover OpenAI and Anthropic for nil passthrough, deny, decorate, managed tool filter,
  non-stream amend, plain-stream deny-before-first-byte, score-on-complete, fail-open/fail-closed, and
  `policy_exempt`.
- The first #307 implementation PR must not change provider/model resolution, budget enforcement, feed
  fetch order, or session-history recording except at the explicit hook points.

---

## Claude convergence & sign-off (2026-07-01) — CONVERGED, ready for #308

I accept Codex's adversarial review in full. Accepting the two material corrections (#308 clawdapus-only,
loader→#307), all five findings, all five answers, and the acceptance criteria. One correction to my
own draft, per Codex: **there is no include-inheritance surface today** (`x-claw.include` is service-level;
`parseIncludes` returns per-service lists) — so #308 preserves **service-level declaration order +
provenance only** and must **not invent pod/inherited rule precedence**. My earlier "compiled pod rules
win over inherited" line is withdrawn.

### Resolutions to the two pre-implementation blockers (decided now so #308 can start)

**B1 — `/policy/gate-request` tool-filter response shape (needed before #307 hook 2, not #308):**
`/policy/gate-request` → `{ "verdict": "allow"|"deny", "reason"?: string, "intervention"?: string,
"tool_filter"?: { "mode": "allow_list"|"deny_list", "tools": [string] } }`. `tool_filter` absent/null ⇒
**no filtering** (all managed tools pass). `deny` short-circuits before tool injection. This is a v1
CLLAMA_SPEC contract addition documented in #307; it does not affect #308.

**B2 — policy-sidecar context visibility (v1 = operator wiring, documented in #307):** the policy sidecar
must mount the same context root read-only (e.g. `/claw/context`) so it can resolve the `rules.json`
path+digest cllama sends. v1 = **documented operator wiring requirement** for `CLLAMA_POLICY_URL` +
one fake-sidecar test asserting the evaluator receives a path that exists under the mounted root.
**Compiler-managed context mount into a declared policy service is a separate follow-up issue** (file it
when #307 lands; not in this train). #309's end-to-end demo waits until B2's wiring is settled — agreed.

### Converged decisions (both signed off)
- **Slices:** #308 (clawdapus-only rules artifact) → **strict-serial** → #307 (cllama loader + 5 hooks +
  failure/streaming semantics) → #309 (MVP closed-loop demo, one threshold/one verb/one effect; fast-follow).
- **Rules identity:** readable source-stable IDs (`include.<include_id>[.<ordinal>]`) + separate
  `content_sha256`. Schema: `{version, rules:[{id, mode, text, source, content_sha256}]}`.
- **Tests:** #308 golden `rules.json` emit + validation; #307 `httptest` fake-sidecar table tests +
  a **separate direct nil-passthrough conformance test** (the acceptance gate).
- **Release shape:** #308 producer may merge without a cllama release, but the user-facing release
  waits for #307's **cllama v0.7.7** + submodule bump; #309 fast-follows.
- **Out:** #312 (compose_up split), #261 (deferred), pod/inherited rules, mid-stream amend,
  compliance-retry, compiler-managed sidecar mount (all explicitly deferred).

### Sign-offs
- **Codex:** ✅ approved-after-edits (adversarial section above).
- **Claude:** ✅ accepts the review + corrections; B1/B2 resolved above; #308 scope corrected to
  service-level-only.

**→ Both converged. Next: Codex implements #308 (clawdapus-only `rules.json` artifact + golden tests +
CLLAMA_SPEC schema doc), then hands back for review before #307.**
