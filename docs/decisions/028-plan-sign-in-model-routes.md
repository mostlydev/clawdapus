# ADR-028: Plan Sign-In Model Routes

**Date:** 2026-11-10
**Status:** Accepted
**Depends on:** ADR-007 (LLM Isolation via Credential Starvation), ADR-019 (Model Policy Authority & Declared Failover), ADR-027 (Task-Home Profile)
**Tracks:** #398, mostlydev/cllama#70

## Context

cllama authenticates to model providers with API keys. An operator who also holds a ChatGPT
plan wants their own agents served from that plan first, with an API key as a declared
fallback, and wants to choose cheaper models through a gateway. ADR-027 left model access
through a subscription sign-in undecided.

Two facts shape the decision:

- OpenAI documents plan usage for open-source, locally hosted apps as
  [Sign in with ChatGPT](https://developers.openai.com/siwc/token-sharing-open-source): the app
  registers its own client, the user signs in through a loopback callback, and the app calls
  the Responses API with the resulting token.
- Other vendors' terms differ. A design that relays a plan must be per vendor and must follow
  each vendor's terms as written.

## Decision

1. **A plan is a provider.** cllama gains a provider, `chatgpt`, with a fixed endpoint and a
   credential of kind `subscription`. A model reference `chatgpt/<model>` routes through it.
2. **Order and fallback are the declared model chain** (ADR-019). `chatgpt/…` then `openai/…`
   is plan first, key second. One entry is plan only. There is no separate routing setting,
   and a launch carries no routing.
3. **The chain is compiled.** It is part of the cllama context a run template's ID binds, so a
   run accepted under one chain cannot spawn under another.
4. **cllama holds the sign-in, and only cllama.** It lives in a private store inside cllama's
   auth directory. It is never in an agent's context, environment, home or mounts, and never
   in a log, a trace record or a callback. A worker still holds only its cllama bearer.
5. **The operator signs in, with one command on the host.** No agent signs in, and nothing
   imports another tool's credentials.
6. **Plan-route failures have their own reasons**, distinct from model and transport errors.
   A missing or expired sign-in, a spent plan and a temporary fault move a request to the
   next entry. Eligibility and unsupported-feature refusals fail the request: they never move
   it to another billing path.
7. **Every model call records its credential class.** cllama names it on each upstream
   attempt in the trace journal and on each response log event. For a task-home run the
   supervisor reports `served_by`, `served_by_complete` and `last_plan_failure` in the status
   and the `end` callback, taken from cllama's revocation acknowledgement.
8. **Sign-in state is readable without a run**, through the run supervisor and
   `claw runs doctor`. It never carries a token.

## Vendor positions (November 2026)

| Vendor | Position | Decision |
|---|---|---|
| OpenAI | Documents plan usage for open-source, locally hosted apps | Built, as the `chatgpt` provider |
| Anthropic | Plan sign-in is for unmodified Claude Code; third-party harnesses and credential intermediation are not permitted | Not a cllama provider. A per-run worker type that runs Claude Code itself is separate work |
| Google | Antigravity terms forbid third-party tools using Antigravity OAuth credentials | Not built |
| xAI | Documents native sign-in for its own tools; its terms do not establish permission to relay a plan | Not built |
| Meta | Coding-plan keys are restricted to the specified harness | Not built |

Primary sources checked for these positions: [Anthropic Claude Code terms](https://code.claude.com/docs/en/legal-and-compliance),
[Google Antigravity terms](https://antigravity.google/terms),
[xAI native sign-in documentation](https://docs.x.ai/build/enterprise) and [consumer terms](https://x.ai/legal/terms-of-service),
and [Meta developer terms](https://dev.meta.ai/legal/terms-of-service).
Google's restriction cited here is specifically for Antigravity credentials; it does not
rule out Google API keys or separately authorized enterprise integrations.


The provider kind is general. A further plan is added only when its vendor documents this use.

## Scope of the OpenAI route

The documented use is one operator's plan on their own host, serving their own agents. Paid
or hosted multi-user use is outside OpenAI's documentation and outside this decision.

## Alternatives considered

- **A per-slot credential preference.** A new setting beside the chain saying which credential
  to try first. It duplicates what the chain already orders and adds a second place where
  routing could disagree with itself.
- **A subscription entry inside the `openai` provider's key pool.** The plan route has its own
  endpoint rules, request shape and failure classes. Sharing a provider would let an
  operator-edited base URL carry a sign-in token, and would hide which credential a model
  reference means.
- **Signing in inside the running proxy container and pasting the redirect back.** It is not
  the documented loopback flow and loses the state check on the callback.
- **Counting served requests in the supervisor from the trace journal.** It needs the journal
  on and a second parser of its format. The revocation acknowledgement already ends a run and
  is always present; a boot ID makes partial counts explicit.

## Consequences

- A pod can name `chatgpt/…` only with a cllama release that includes plan routes. An older
  proxy does not know the provider, and requests that name it fail.
- The plan route sends no sampling or length fields and refuses hosted tools and a
  `tool_choice` that names one tool. Agents that rely on those need a keyed route.
- `served_by` is counted in memory by one cllama process. A proxy restart during a run makes
  the counts partial, which the report states. The trace journal is the durable per-call
  record.
- Homes and sign-ins are both long-lived pod state that `claw up` and `claw down` leave in
  place.

## What is proven, and what is not

- **Proven by test:** the chain binding, the callback fields, the failure reasons, token
  redaction, and plan serving through a real Hermes task-home worker against a fixture that
  stands in for the plan endpoint (`TestSpikeTaskHomeRoutes`).
- **Not proven:** a request authorized by a real ChatGPT sign-in. No agent signs in. The
  callback parameter names, the namespaced tool shape and the vendor's error bodies are taken
  from OpenAI's documentation.
