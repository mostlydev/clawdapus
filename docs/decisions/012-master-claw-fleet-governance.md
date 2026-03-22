# ADR-012: Master Claw and Fleet Governance

**Date:** 2026-03-19
**Status:** Accepted
**Depends on:** ADR-013 (Context Feeds), ADR-014 (Telemetry Normalization and `claw audit`), ADR-015 (`claw-api` Authentication and Authorization Scoping)
**Implementation:** Milestones 1-2 complete (telemetry + claw-api read plane). Milestone 3 (Master Claw example) in `examples/master-claw/` and `examples/trading-desk/`. Write plane (Milestone 4) and hub-and-spoke (Milestone 5) deferred.

## Context

The manifesto (Section XII) describes the Master Claw as an autonomous AI governor — the "Top Octopus" — that reads cllama telemetry, scores drift, shifts budgets, quarantines agents, and manages a neural fleet. The hub-and-spoke model extends this to multi-pod governance across infrastructure zones.

As of today, the infrastructure pieces exist but nothing connects them:

- cllama emits structured JSON logs to stdout, but the formal spec and the reference implementation have drifted. The spec talks about `timestamp` and `intervention_reason`; the reference logger emits `ts`, `intervention`, a first-class `error` event, and richer response telemetry such as `cost_usd`, `tokens_in`, `tokens_out`, `model`, `latency_ms`, and `status_code`. Nothing in Clawdapus consumes these logs yet.
- The Model Governance Layer (MGL) project implements the cllama policy pipeline (prompt decoration, response amendment, compliance checks) but has no fleet-level consumer here.
- There is no `claw audit` CLI command, no `claw-api` service, no runtime governance override store, and no Master Claw example.
- The pod YAML parser recognizes raw `x-claw.master`, but the parsed `Pod` model does not carry it forward yet. Any auto-injection or Master Claw-specific runtime wiring therefore still needs parser/runtime plumbing before it can work.

The Master Claw is the feature that closes the largest gap between the repo's vision and the current implementation.

## Decision

### 1. The Master Claw Is a Pattern, Not a Driver

The Master Claw is a standard claw deployed with any runner (initially OpenClaw). It is not a special `CLAW_TYPE`. What makes it a Master Claw is its contract, its declared feeds, and the tools it can reach.

The pod selects its governor through the existing pod-level convention:

```yaml
x-claw:
  pod: trading-desk
  master: octopus
```

The named service is still just a normal claw. The declaration identifies which claw is expected to reason over the fleet.

### 2. Telemetry: Push Anomalies, Pull Details

The Master Claw receives fleet telemetry through an exception-based monitoring pattern, not a periodic full-state dump. This keeps the governor's context window lean and scales better than streaming every healthy agent on every turn.

Telemetry sources are:

- cllama structured logs
- `claw health` / container health state
- MGL compliance events when available

#### Push: Fleet Alerts Feed

ADR-013 provides the sensory substrate. The Master Claw consumes an operator-declared feed from `claw-api`:

```yaml
feeds:
  - source: claw-api
    path: /fleet/alerts
    ttl: 30
surfaces:
  - "service://claw-api"
```

`/fleet/alerts` returns anomalies only: budget threshold breaches, error spikes, intervention surges, health failures, and similar exception conditions. When the fleet is nominal, the response is intentionally minimal.

Examples:

- `ALERT: crypto-crusher-1 exceeded 1h budget threshold ($4.12 / $2.00 limit)`
- `ALERT: news-scanner derived drift score critical (0.87) — 12 interventions in last hour`
- `ALERT: trade-executor unhealthy for 3m — last healthy 14:02 UTC`

#### Pull: Investigation via `claw-api`

When the Master Claw needs detail, it pulls it through `claw-api`:

- `fleet.query_metrics(claw_id, window)`
- `fleet.logs(service, lines)`
- `fleet.status()`

This separation preserves the Master Claw's context during normal operation and only expands detail when it is actively investigating.

### 3. Unified Control Plane via `claw-api`

The Master Claw acts through a single service: `claw-api`. It should feel like one control surface from the governor's perspective even if the implementation talks to different backends underneath.

#### Service Model

`claw-api` is a thin fleet-management service exposed over MCP or REST. The intended steady-state is runtime injection when the pod declares `x-claw.master`. Because `master` is not yet carried through the parsed pod model, that injection path does not exist today; the first implementation step is to wire `master` through parse and compose generation. Until then, any manual service declaration is transitional rather than canonical. `claw-api` participates in the normal service-surface model: the Master Claw reaches it through `service://claw-api`, and service skills document its capabilities.

ADR-015 defines its auth and scope model. In short:

- `service://claw-api` grants reachability, not permission
- `x-claw.master` identifies the governor, not a trusted principal
- callers authenticate with explicit service credentials
- feed fetches and tool calls use the same scoped principal model
- read responses are filtered to scope and writes fail closed out of scope

#### Agent-to-Service Mapping

Clawdapus already expands `count > 1` claws into ordinal-named compose services such as `crypto-crusher-0` and `crypto-crusher-1`. Each ordinal has its own `claw_id` in cllama telemetry and its own compose service name. Governance actions target the ordinal service, not the abstract base definition.

For `count: 1`, agent and service are 1:1. For `count > 1`, lifecycle targeting is compose-service based and telemetry/policy targeting is `claw_id` based.

#### V1 Read Operations

V1 of `claw-api` is read-first:

| Action | What it does | Implementation |
|--------|-------------|----------------|
| `fleet.status` | Aggregated health and uptime for all claws in scope | Docker inspect plus compose metadata |
| `fleet.query_metrics` | Detailed per-agent telemetry for a time window | `claw audit` ingestion keyed by `claw_id` |
| `fleet.logs` | Tail recent logs for a specific service | `docker compose logs <service>` |
| `GET /fleet/alerts` | Summarized anomaly feed for ADR-013 | Thresholding over normalized audit data plus health state |

#### Deferred Write Operations

Write operations are part of the design, but they are deferred until the read path, audit schema, and auth model are proven:

| Action | Intended effect | Deferred implementation |
|--------|-----------------|-------------------------|
| `fleet.restart(service)` | Restart one ordinal service | `docker compose restart <service>` |
| `fleet.quarantine(service)` | Stop one ordinal service and mark it for review | `docker compose stop <service>` plus runtime governance state |
| `fleet.budget.set(claw_id, ...)` | Apply budget override to one claw or pod scope | runtime override files consumed by cllama/MGL |
| `fleet.model.restrict(claw_id, ...)` | Restrict or downgrade model access | runtime override files consumed by cllama/MGL |
| `fleet.scale(base, count)` | Change desired replica count | explicitly deferred until a durable desired-state mutation path exists |

Read-first is still a unified control-plane design. It simply acknowledges that telemetry, auth, scope enforcement, and auditability must exist before runtime control is exposed.

#### Privilege Model

`claw-api` is infrastructure, not a regular claw. It will need access to:

- the generated `compose.generated.yml`
- Docker status and log APIs
- `docker compose` for lifecycle actions when the write plane is enabled
- runtime governance state when soft overrides exist

The honest trade-off is that a raw Docker socket mount is extremely high privilege. In production, a scoped socket proxy is recommended. If `claw-api` is mounted against an unrestricted `/var/run/docker.sock`, it is effectively Docker-root inside the pod regardless of whether its own API exposes only read verbs.

This remains consistent with ADR-002: `docker compose` is the sole lifecycle authority. If and when write operations are enabled, `claw-api` shells out to `docker compose` against the generated compose file. Docker SDK usage remains read-oriented.

### 4. Telemetry Contract via ADR-014

ADR-014 defines the telemetry normalization boundary and the stable internal event schema used by governance features.

This ADR depends on that normalized substrate rather than the raw cllama wire shape. In particular:

- `claw audit` is the ingestion and normalization layer
- `fleet.query_metrics` reads normalized events
- `/fleet/alerts` thresholds over normalized events plus health state
- future drift-scoring logic may consume normalized telemetry without depending on proxy-specific field names

### 5. `claw audit` as the Operator Read Surface

ADR-014 owns the `claw audit` contract. This ADR consumes that command as the operator-facing read surface over normalized cllama telemetry.

It reads cllama structured logs from Docker container logs (stdout), normalizes them per ADR-014, and outputs summaries such as:

- per-agent cost
- request and error rates
- intervention counts
- drift history when available from optional extensions or higher-level scoring
- model usage breakdown

The same ingestion and aggregation logic powers `claw-api` read operations and `/fleet/alerts`.

### 6. Drift Scoring (Deferred Details)

Drift scoring is the composition of:

- cllama telemetry such as intervention frequency, error rates, latency shifts, and cost anomalies
- MGL compliance events such as rule violations and response amendments
- behavioral signals such as output sampling and contract adherence checks

The exact scoring model is organization-specific. Clawdapus provides the telemetry substrate and the audit/alert plumbing. The scoring algorithm lives in the Master Claw's contract, tools, and operator policy.

### 7. Hub-and-Spoke (Future Extension)

Single-pod governance comes first. Multi-pod hub-and-spoke extends the same model:

- each pod runs its own cllama proxy and `claw-api`
- each `claw-api` exposes a service endpoint reachable through normal networking and service credentials
- a central Master Claw consumes remote `/fleet/alerts` feeds and calls remote `claw-api` read or write operations through the same interface

This uses the existing `service://` surface model plus ordinary service credentials. It is deferred until single-pod governance is proven.

## Implementation Sequence

### Milestone 1: `claw audit` + Telemetry Foundation
1. Implement raw cllama log ingestion from Docker stdout logs
2. Normalize raw records into the stable audit schema defined above
3. Aggregate per-agent cost, error counts, intervention counts, model usage, and drift history when present
4. Define anomaly-threshold configuration for alert generation

### Milestone 2: `claw-api` Read Plane
5. Build `claw-api` as a lightweight Go service or MCP server
6. Expose `fleet.status`, `fleet.query_metrics`, `fleet.logs`, and `GET /fleet/alerts`
7. Require explicit auth and per-target/per-verb scoping
8. Log every `claw-api` call as part of the governance audit trail
9. Add runtime injection for `claw-api` when `x-claw.master` is present, with a temporary manual example only if that plumbing is not yet merged

### Milestone 3: Master Claw Example
10. Write a Master Claw contract
11. Declare `x-claw.master: octopus` in pod YAML
12. Deploy `octopus` as a standard claw with `cllama`, the `/fleet/alerts` feed, and `service://claw-api`
13. Wire `INVOKE` for periodic fleet review so the governor still reasons when the fleet is quiet

### Milestone 4: Write Plane
14. Add `fleet.restart` and `fleet.quarantine`
15. Define runtime override files for `fleet.budget.set` and `fleet.model.restrict`
16. Wire cllama and MGL to consume runtime overrides
17. Defer `fleet.scale` until durable desired-state mutation and compose regeneration are defined

### Milestone 5: Hub-and-Spoke
18. Add remote telemetry ingestion via federated `claw-api`
19. Add cross-pod authentication
20. Add a central Master Claw example with multi-pod scope

## Rationale

The Master Claw as a pattern, not a driver, is the most Clawdapus-native design. It proves the system's own thesis: governance is just another workload governed by the same infrastructure.

Making ADR-013 the sensory substrate simplifies this ADR. The Master Claw does not need a special telemetry plugin. It consumes a feed like any other claw and uses a normal service surface for deeper investigation.

Making `claw-api` read-first keeps the plan honest. The repo already has strong seams for telemetry, context, and compose authority. It does not yet have a stable audit schema, a runtime override store, or a finished service-auth/scoping model for high-blast-radius control.

## Consequences

**Positive:**
- Closes the largest gap between the manifesto vision and the repo's implementation
- Keeps governance runner-agnostic
- Makes `claw audit` independently useful even without a Master Claw
- Reuses the normal service-surface and feed abstractions instead of inventing a bespoke governor path
- Preserves the compose-as-sole-lifecycle-authority invariant

**Negative:**
- `claw-api` remains a high-blast-radius service and needs careful auth and scoping
- Alert thresholds will require tuning
- Drift scoring remains intentionally operator-defined rather than built into infrastructure
- Full governance write operations arrive later than the read path
