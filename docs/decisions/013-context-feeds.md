# ADR-013: Context Feeds — Live Data Injection for Claws

**Date:** 2026-03-19
**Status:** Proposed
**Depends on:** ADR-004 (Service Surface Skills), ADR-008 (cllama Sidecar Standard)
**Consumed by:** ADR-012 (Master Claw)

## Context

Clawdapus currently injects static context at deploy time: `CLAWDAPUS.md`, skills, contracts, and `INVOKE` messages are all baked during `claw up` and mounted read-only. Claws that need live data — market summaries, fleet telemetry, system metrics, or news headlines — must know to call the right API themselves, relying on their contract or skills to tell them how.

This works, but it leaks plumbing into behavior:

- The claw's contract must explain which API to call, how often, and what to do when it fails.
- The same live-data pattern gets reinvented per use case instead of becoming infrastructure.
- The Master Claw use case in ADR-012 needs fleet context injection, but the need is broader than governance.

The repo also has a clear technical seam for this feature: cllama already intercepts every LLM request, already resolves caller identity, and already mounts per-agent context. What does **not** exist yet is a feed manifest, a fetch/cache path, or any prompt-decoration mechanism in cllama. The current proxy forwards request bodies unchanged. Implementing feeds therefore requires new request-rewrite logic for both OpenAI-style and Anthropic-style bodies, not just manifest plumbing.

## Decision

### 1. Context Feeds Are a Generic Primitive

A **context feed** is an operator-declared binding between a pod service endpoint and a claw's LLM context. Clawdapus fetches the endpoint and injects the response into the claw's context automatically. The claw sees the data, not the plumbing.

Context feeds are the data-plane counterpart to service surface skills:

- skills explain how to call a service
- feeds inject data from a service automatically

This ADR is intentionally generic. ADR-012 consumes feeds for fleet governance, but feeds are not a Master Claw feature.

### 2. V1 Scope: Operator-Declared Feeds + cllama Injection

V1 is deliberately narrow:

- feeds are declared explicitly in `x-claw.feeds`
- feed injection is implemented through cllama prompt decoration
- only cllama-enabled claws receive automatic feed injection in V1

Non-cllama claws continue to rely on explicit service calls until a later runner-hook design exists. Driver-level parity is a deferred extension, not part of this ADR's first implementation target, because the current driver interface has no per-turn context refresh hook.

### 3. Feed Manifest

Each claw may declare feeds in pod YAML:

```yaml
services:
  tiverton:
    x-claw:
      agent: ./agents/TIVERTON.md
      feeds:
        - source: trading-api
          path: /api/v1/market-summary
          ttl: 300
        - source: trading-api
          path: /api/v1/portfolio
          ttl: 30
```

`source` is a pod service name, resolved using the same service topology as `service://` surfaces. `path` is an HTTP GET endpoint. `ttl` is the maximum staleness window in seconds. `name` is optional in YAML; if omitted, Clawdapus derives it from the path.

At `claw up` time, Clawdapus resolves these declarations into a per-agent manifest:

```json
[
  {"name":"market-summary","source":"trading-api","path":"/api/v1/market-summary","ttl":300},
  {"name":"portfolio","source":"trading-api","path":"/api/v1/portfolio","ttl":30}
]
```

The manifest is written to:

- `.claw-runtime/context/<agent-id>/feeds.json`
- `/claw/context/<agent-id>/feeds.json` for cllama-enabled claws

No parser or runtime support for `feeds` exists yet. V1 implementation must add it through the `x-claw` parse path and the generated per-agent cllama context layout.

### 4. cllama Injection Path

When a claw is behind cllama, cllama handles feeds as part of request-time prompt decoration.

On each proxied request, cllama:

1. Reads `/claw/context/<agent-id>/feeds.json`
2. Checks the per-feed TTL cache
3. Fetches stale feeds with HTTP GET
4. Rewrites the outgoing request body to prepend the responses as clearly delimited context blocks before forwarding upstream

This requires two format-specific injection paths:

- OpenAI-compatible chat payloads
- Anthropic `/v1/messages` payloads

Example block:

```text
--- BEGIN FEED: fleet-alerts (from claw-api, refreshed 2026-03-19T14:32:00Z) ---

Fleet nominal. 7 agents healthy. No alerts.

--- END FEED: fleet-alerts ---
```

Multiple feeds are concatenated in manifest order.

### 5. Caller Identity and Fetch Contract

Feed requests include caller identity:

```text
GET /api/v1/fleet/alerts
X-Claw-ID: tiverton
X-Claw-Pod: trading-desk
```

This lets pod-aware services customize responses per caller while staying within the existing service model: topology grants reachability, and the service still owns authorization and response semantics.

For authenticated feed sources, the fetch path must carry explicit service credentials rather than relying on reachability alone. For cllama-enabled claws, those credentials are projected into `/claw/context/<agent-id>/service-auth/<service>.json`. ADR-015 defines this requirement and the concrete `claw-api` file shape.

### 6. Feed Response Format and Limits

Feed endpoints return plain text or markdown. JSON is allowed; cllama wraps it in a fenced block before injection.

Feeds may include frontmatter:

```markdown
---
feed: fleet-alerts
refreshed: 2026-03-19T14:32:00Z
ttl: 60
---

Fleet nominal. 7 agents healthy. No alerts.
```

The `ttl` hint may override the manifest TTL for that fetch.

Every implementation MUST:

- enforce a per-feed size cap
- enforce a total injected-feed size cap per request
- annotate truncation in the injected block
- on fetch failure, inject either a stale cached response with age warning or a clear unavailability placeholder

### 7. Relationship to ADR-012 (Master Claw)

ADR-012 depends on this primitive for its sensory path.

The Master Claw pattern is:

```yaml
x-claw:
  pod: trading-desk
  master: octopus

services:
  octopus:
    x-claw:
      agent: ./agents/OCTOPUS.md
      cllama: passthrough
      feeds:
        - source: claw-api
          path: /fleet/alerts
          ttl: 30
      surfaces:
        - "service://claw-api"
```

The feed gives the Master Claw anomaly context. The service surface gives it pull-based investigation tools.

### 8. Deferred Extensions

The following are explicitly out of scope for V1 and are deferred until the basic feed substrate is working:

- service-advertised feeds via `claw.feed.*` labels
- runtime feed subscriptions such as `feeds.subscribe(...)`
- non-cllama driver fallback
- event-driven triggers such as `POST /triggers/<claw-id>`

These are valuable, but they should be layered on top of a working operator-declared, cllama-backed feed path rather than shipped as one bundled feature.

## Implementation Sequence

### Milestone 1: Feed Manifest Plumbing
1. Add `feeds` to the `x-claw` parser schema
2. Validate `source`, `path`, and `ttl` during `claw up`
3. Write per-agent feed manifests into `.claw-runtime/context/<agent-id>/feeds.json`
4. Include `feeds.json` in the cllama context mount for cllama-enabled claws

### Milestone 2: cllama Feed Injection
5. Implement feed loading in cllama
6. Implement TTL caching
7. Fetch with `X-Claw-ID` and `X-Claw-Pod` headers
8. Add OpenAI and Anthropic request-rewrite paths for feed injection
9. Implement graceful degradation and truncation markers

### Milestone 3: First Consumer
10. Use this mechanism in ADR-012 via `source: claw-api`, `path: /fleet/alerts`
11. Validate the operational loop: feed for anomaly push, tool calls for detail pull

### Future Milestones
12. Add service-advertised feed discovery
13. Add runtime subscription APIs
14. Add non-cllama driver parity
15. Add event-driven triggers

## Rationale

This keeps the concept simple and native to the repo.

cllama is already the place where per-agent identity, context, and LLM request flow meet. Using it for feed injection makes feeds runner-agnostic for cllama-enabled claws without inventing a runner-specific plugin system.

Narrowing V1 matters. The repo does not yet have a generic driver hook for per-turn context refresh, a runtime subscription state model, or a trigger dispatch layer. Treating those as future layers keeps the ADR honest and buildable.

## Consequences

**Positive:**
- Introduces a reusable live-context primitive without tying it to the Master Claw
- Uses the strongest existing implementation seam in the repo: cllama request interception
- Gives ADR-012 a clean sensory substrate instead of a custom telemetry plugin
- Keeps the first implementation target narrow and inspectable

**Negative:**
- V1 only helps cllama-enabled claws
- Feed fetches add latency on cache miss
- Feed content consumes tokens and requires truncation discipline
- Discovery, runtime subscription, triggers, and non-cllama parity remain future work
