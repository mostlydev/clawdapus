# ADR-013: Context Feeds — Live Data Injection for Claws

**Date:** 2026-03-19
**Status:** Proposed
**Depends on:** ADR-004 (Service Surface Skills), ADR-012 (Master Claw)

## Context

Clawdapus currently injects static context at deploy time: CLAWDAPUS.md, skills, contracts, and INVOKE messages are all baked during `claw up` and mounted read-only. Claws that need live data — stock prices, fleet telemetry, system metrics, news headlines — must know to call the right API themselves, relying on their contract or skills to tell them how.

This works but has limitations:

- **Runner-specific**: An OpenClaw plugin for market data doesn't help a Hermes or PicoClaw agent. Each runner has its own mechanism (if any) for per-turn context enrichment.
- **Operator burden**: The claw's contract must instruct it to call the API, parse the response, and decide what to do with it. The "plumbing" leaks into the behavioral contract.
- **No self-description**: Services in the pod may have live data feeds available, but there's no way for them to advertise this capability. `claw.skill.emit` describes *how to use* a service — it doesn't describe *what data the service can push to you*.

ADR-012 (Master Claw) identified the need for fleet telemetry injection. But the pattern is universal: any claw benefits from having live data appear in its context without needing to know the plumbing.

## Decision

### 1. Context Feeds as a Clawdapus Primitive

A **context feed** is a declared binding between a service endpoint and a claw's turn context. On each invocation (INVOKE cron, incoming message, or configurable interval), Clawdapus fetches the endpoint and injects the response as ephemeral context for that turn.

Context feeds are the data-plane counterpart to service surface skills: skills tell the claw *how to call* a service (pull), feeds *push data to* the claw automatically.

### 2. Three Layers of Feed Wiring

Feed wiring operates at three levels, from most explicit to most dynamic:

#### Layer 1: Operator-Declared Feeds (Pod YAML)

The operator explicitly wires a feed in the pod manifest. This is the simplest model — works with any existing HTTP API, no Clawdapus awareness needed on the service side.

```yaml
services:
  tiverton:
    x-claw:
      agent: ./agents/TIVERTON.md
      feeds:
        - source: trading-api
          path: /api/v1/market-summary
          schedule: "*/5 * * * *"    # every 5 minutes
        - source: trading-api
          path: /api/v1/portfolio
          trigger: per-turn          # refresh on every agent turn
```

`source` is a service name in the pod (resolved via `service://` scheme, same as surfaces). `path` is an HTTP GET endpoint. `schedule` is a 5-field cron expression (same syntax as INVOKE). `trigger: per-turn` refreshes on every agent invocation.

At `claw up` time, Clawdapus resolves feeds into the runtime config. No service needs to be running yet — the endpoint is just a contract.

#### Layer 2: Service-Advertised Feeds (Image Labels)

Services self-describe their available feeds, extending the `claw.skill.emit` pattern from ADR-004:

```dockerfile
LABEL claw.feed.0.path="/api/v1/fleet/alerts"
LABEL claw.feed.0.name="fleet-alerts"
LABEL claw.feed.0.description="Anomaly alerts for all claws in the pod"
LABEL claw.feed.0.format="markdown"
LABEL claw.feed.0.suggested-trigger="per-turn"
```

During `claw up`, Clawdapus inspects service images (same pass that extracts `claw.skill.emit`) and discovers advertised feeds. These appear in CLAWDAPUS.md's service surface section:

```markdown
## service://claw-api

**Available feeds:**
- `fleet-alerts` — Anomaly alerts for all claws in the pod (suggested: per-turn)
- `fleet-metrics` — Detailed per-agent telemetry (suggested: on-demand)
```

Advertised feeds are **not auto-subscribed**. They are discoverable — the operator wires them in the pod YAML, or claws request subscriptions at runtime (Layer 3).

#### Layer 3: Runtime Subscription Requests

Claws can request new feed subscriptions at runtime by calling a `feeds.subscribe` tool on `claw-api`:

```
feeds.subscribe(
  source: "trading-api",
  path: "/api/v1/sector/tech",
  trigger: "per-turn",
  reason: "Need live tech sector data for momentum analysis"
)
```

Subscription requests are **not self-service**. They go through a governance gate:

- **If a Master Claw is present**: the request is queued and the Master Claw reviews it on its next turn. The Master Claw can approve, deny, or modify (e.g., change `per-turn` to a 5-minute schedule to conserve resources). Approval writes the feed config to the requesting claw's runtime, and the feed activator picks it up on the next cycle.
- **If no Master Claw**: `claw-api` checks the requesting claw's contract for a `feeds.self-subscribe: true` permission. If present, the subscription is auto-approved. If absent, it's logged and denied.

This solves the "claws requesting their own subscriptions" pattern without a formation race condition — the claw doesn't need the feed to exist at startup. It discovers what's available via CLAWDAPUS.md, then requests what it needs.

### 3. Feed Execution Model

#### Who Fetches?

A lightweight **feed poller** sidecar (or goroutine within `claw-api`) handles all feed fetching for the pod. This is NOT done by the runner — the runner never knows feeds exist as infrastructure.

The poller:
- Reads the resolved feed config (from `claw up` or runtime subscription)
- Fetches each endpoint on its schedule or trigger
- Writes the response to a per-claw, per-feed file in the claw's context directory
- The driver mounts this directory into the runner's context path

#### How Does the Runner See It?

The feed response lands as a file in the runner's skill/context directory, refreshed before each turn:

- **OpenClaw**: File in `/claw/feeds/fleet-alerts.md` — openclaw's `bootstrap-extra-files` hook picks it up
- **Hermes**: File in `/workspace/feeds/fleet-alerts.md` — inlined into AGENTS.md context
- **PicoClaw/Nanobot/etc.**: Same pattern, adapted to each runner's context path via `SkillDir` or equivalent

The driver doesn't need feed-specific logic. Feeds write to a directory the driver already knows how to mount. The only new work per driver is ensuring the feed directory is in the runner's context loading path.

#### Caller Identity

Feed requests include the requesting claw's identity:

```
GET /api/v1/fleet/alerts
X-Claw-ID: tiverton
X-Claw-Pod: trading-desk
```

Services that are pod-aware can use this to customize responses. A fleet alerts endpoint returns only alerts relevant to the requesting claw. A market data endpoint returns holdings specific to that trader. Services that are not pod-aware simply ignore the headers.

This extends the existing topology broadcasting pattern (`CLAW_HANDLE_*` env vars). Services already know who the claws are — now they also know who's asking for data.

### 4. Formation and Lifecycle

#### `claw build` Time

Nothing changes. Feed declarations are pod-level, not image-level. Service images can declare `claw.feed.*` labels for self-description.

#### `claw up` Time

1. **Parse feeds**: `claw up` reads `x-claw.feeds` from each service's claw block.
2. **Discover advertised feeds**: During the existing image inspection pass, extract `claw.feed.*` labels from service images. Record in CLAWDAPUS.md.
3. **Resolve feed config**: For each declared feed, resolve `source` to a service hostname. Validate `path`, `schedule`/`trigger`. Write resolved feed config to `.claw-runtime/<service>/feeds/`.
4. **Inject feed poller**: If any service has feeds, inject the feed poller sidecar (or start it as a goroutine in `claw-api` if present). The poller reads resolved feed configs from the runtime directory.
5. **Mount feed directory**: Add a bind-mount for `<runtime>/feeds/<service>/` into each claw's context directory. The directory starts empty — feeds populate after services are healthy.
6. **Emit compose**: Feed mounts appear in `compose.generated.yml`. The feed poller appears as a service (or as part of `claw-api`).

#### Service Startup (Runtime)

7. Services start. The feed poller waits for each source service to be healthy (using Docker healthcheck, same as `depends_on: condition: service_healthy`).
8. Once healthy, the poller fetches the first response and writes it to the feed directory. The claw now has live data.
9. On each subsequent trigger (schedule or per-turn signal), the poller refreshes the file.

**Formation race condition**: Feeds depend on services being healthy. This is the same dependency model as `depends_on` — Clawdapus already handles service ordering. A claw that starts before its feed source is healthy simply sees an empty (or stale) feed file. The contract should handle this gracefully ("if no feed data is available, state that and proceed").

#### Per-Turn Trigger

For `trigger: per-turn`, the feed poller needs to know when a claw's turn starts. Two options:

- **Schedule approximation**: Use a tight cron (e.g., every 30 seconds). Good enough for most cases. Simple. The feed file is "at most 30 seconds stale."
- **Driver-assisted signal**: The runner emits a signal (webhook, file touch, log line) that the poller watches. More precise, but runner-specific. Deferred.

V1 uses schedule approximation. `per-turn` is syntactic sugar for a tight cron interval (configurable, default 30s).

#### Runtime Subscription

10. A claw discovers available feeds in CLAWDAPUS.md.
11. It calls `feeds.subscribe(...)` on `claw-api`.
12. `claw-api` validates the request (source exists, path is reasonable, claw has permission).
13. If Master Claw is present: queue for review. Master Claw approves/denies on next turn.
14. If no Master Claw: auto-approve if claw's contract allows `feeds.self-subscribe`.
15. On approval: `claw-api` writes a new feed config to the runtime directory. The poller picks it up on its next cycle.
16. CLAWDAPUS.md is NOT regenerated at runtime — the feed just starts appearing in the feed directory.

#### `claw down` / Restart

- `claw down`: Feed poller stops with everything else. Feed files are ephemeral (in runtime dir).
- Service restart: Feed poller detects unhealthy source, stops fetching, resumes when healthy again. Stale feed file remains (last known good).
- Claw restart: Feed files persist in the bind-mounted runtime dir. The restarted claw immediately has the last-fetched data.

### 5. Feed Response Format

Feed endpoints return plain text or markdown by default. The response is written verbatim to the feed file — no transformation by the poller.

Services can include frontmatter for richer metadata:

```markdown
---
feed: fleet-alerts
refreshed: 2026-03-19T14:32:00Z
ttl: 60s
---

Fleet nominal. 7 agents healthy. No alerts.
```

The `ttl` hint tells the poller it can skip the next fetch if the interval is shorter than the TTL. This lets services control their own polling pressure.

JSON responses are supported but wrapped in a markdown code fence before writing to the feed file, so the runner's context loader handles them uniformly.

### 6. Relationship to ADR-012 (Master Claw)

Context feeds replace the "anomaly plugin" described in ADR-012 Section 2. Instead of a runner-specific OpenClaw plugin, the Master Claw declares:

```yaml
octopus:
  x-claw:
    role: master
    agent: ./agents/OCTOPUS.md
    feeds:
      - source: claw-api
        path: /fleet/alerts
        trigger: per-turn
    surfaces:
      - "service://claw-api"
```

The Master Claw wakes up on its INVOKE schedule, sees the latest fleet alerts in its context (refreshed by the feed poller), and uses `fleet.query_metrics` via tool call to investigate anomalies. The entire sensory pipeline is Clawdapus infrastructure — no runner-specific plugin needed.

## Implementation Sequence

### Milestone 1: Operator-Declared Feeds (Layer 1)
1. Add `feeds` to `x-claw` schema in pod parser
2. Resolve feeds during `claw up` — validate source, path, schedule/trigger
3. Build feed poller (lightweight Go process or goroutine in `claw-api`)
4. Write feed responses to per-claw feed directory
5. Mount feed directory into runner context via driver `SkillDir` or new `FeedDir`
6. Handle `per-turn` as configurable tight cron (default 30s)

### Milestone 2: Service-Advertised Feeds (Layer 2)
7. Add `claw.feed.*` label parsing to `inspect.ParseLabels`
8. Include advertised feeds in CLAWDAPUS.md generation
9. Generate feed skill files (like service surface skills) describing available feeds

### Milestone 3: Runtime Subscription (Layer 3)
10. Add `feeds.subscribe`, `feeds.list`, `feeds.unsubscribe` to `claw-api`
11. Implement governance gate (Master Claw review queue or `feeds.self-subscribe` permission)
12. Dynamic feed config writing and poller hot-reload

### Milestone 4: Caller Identity + Pod-Aware Feeds
13. Add `X-Claw-ID` and `X-Claw-Pod` headers to feed requests
14. Document service-side patterns for pod-aware feed responses
15. Example: `claw-api` fleet alerts filtered by requesting claw's scope

## Rationale

Context feeds extend Clawdapus's existing self-description philosophy (ADR-004) from "services tell claws how to use them" to "services push live data to claws automatically." The three-layer model mirrors how Clawdapus handles skills: operator-provided (explicit), service-emitted (self-describing), and fallback-generated. Adding a runtime subscription layer on top enables claws to adapt to their environment without operator intervention, governed by the Master Claw or explicit permissions.

The feed poller as infrastructure (not a runner feature) makes this universal across all 7+ driver types. The runner sees a file in its context directory — it doesn't know or care that a poller refreshed it 30 seconds ago. This is the same abstraction boundary that makes config injection work: Clawdapus handles the plumbing, the runner handles the thinking.

The formation race condition is handled the same way Docker handles service dependencies: health-gated startup with graceful degradation. A claw that starts before its feed source is simply told "no data yet" — the same way a human checks a dashboard that hasn't loaded.

## Consequences

**Positive:**
- Universal live data injection across all runner types — no per-driver plugin development.
- Services self-describe their feeds, extending the existing `claw.skill.emit` pattern.
- Master Claw telemetry becomes a feed, not a custom plugin — validates the abstraction.
- Runtime subscriptions enable adaptive claws without operator intervention, governed by the Master Claw.
- Caller identity headers enable pod-aware services to customize responses per claw.

**Negative:**
- Feed poller adds a new sidecar/process to the pod — resource overhead, failure mode to monitor.
- `per-turn` trigger is approximated via tight cron in v1 — up to 30s stale. True per-turn requires runner-specific signaling.
- Runtime subscriptions add state that doesn't survive `claw down`/`claw up` — subscriptions are ephemeral unless the operator promotes them to the pod YAML.
- Feed files in the context directory could grow large if endpoints return verbose responses. Need to document size guidance and consider truncation.
