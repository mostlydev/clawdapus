# ADR-012: Master Claw and Fleet Governance

**Date:** 2026-03-19
**Status:** Proposed

## Context

The manifesto (Section XII) describes the Master Claw as an autonomous AI governor — the "Top Octopus" — that reads cllama telemetry, scores drift, shifts budgets, quarantines agents, and manages a neural fleet. The hub-and-spoke model extends this to multi-pod governance across infrastructure zones.

As of today, the infrastructure pieces exist but nothing connects them:

- cllama emits structured JSON logs to stdout (per CLLAMA_SPEC Section 5), but the formal spec only guarantees `timestamp`, `claw_id`, `type`, and `intervention_reason`. The reference implementation already emits richer fields (`cost_usd`, `tokens_in/out`, `model`, `latency_ms`, `status_code`) but these are not yet normative. Nothing consumes the logs.
- The Model Governance Layer (MGL) project implements the cllama policy pipeline (prompt decoration, response amendment, compliance checks) but has no fleet-level consumer.
- There is no `claw audit` CLI command, no drift scoring, no quarantine mechanism.
- No Master Claw example exists.

The Master Claw is the feature that elevates Clawdapus from "deploy and configure" to "autonomous fleet governance."

## Decision

### 1. The Master Claw Is a Pattern, Not a Driver

The Master Claw is a standard claw deployed with any runner (initially OpenClaw). It is not a special `CLAW_TYPE`. What makes it a Master Claw is its contract, its surfaces, and the tools it can reach. This is consistent with the principle that governance is orthogonal to the runner.

### 2. Telemetry: Push Anomalies, Pull Details (Sensory Input)

The Master Claw receives fleet telemetry via an exception-based monitoring pattern, not a periodic state dump. This preserves the Master Claw's context window for reasoning and scales to large fleets where 99% of agents are healthy at any given time.

**Telemetry sources:**
- cllama structured JSON logs (per-request: cost, tokens, model, latency, interventions)
- `claw health` output (container health, uptime)
- MGL compliance events (rule violations, amendments)

#### Push: Anomaly Alerts

The Master Claw declares a **context feed** (see ADR-013) from `claw-api`'s `/fleet/alerts` endpoint. The feed poller fetches this endpoint on each turn and injects the response into the Master Claw's context automatically — no runner-specific plugin needed. The `claw-api` endpoint returns **only anomalies** — threshold breaches, error spikes, intervention surges, health failures. When the fleet is nominal, the response is minimal (e.g., "Fleet nominal. 7 agents healthy. No alerts.").

**Alert examples:**
- `ALERT: crypto-crusher exceeded 1h budget threshold ($4.12 / $2.00 limit)`
- `ALERT: news-scanner drift score critical (0.87) — 12 interventions in last hour`
- `ALERT: trade-executor unhealthy for 3m — last healthy 14:02 UTC`

The `claw-api` endpoint uses configurable thresholds defined in the Master Claw's pod manifest or contract. The anomaly detection is simple and deterministic (threshold comparison, rate-of-change) — the Master Claw's LLM does the interpretation and decision-making.

#### Pull: On-Demand Detail via `claw-api`

When the Master Claw receives an anomaly alert and needs to investigate, it pulls detailed telemetry via tool calls to `claw-api`:

- `fleet.query_metrics(claw_id, window)` — detailed per-agent cost, token, latency, intervention history
- `fleet.logs(claw_id, lines)` — recent log output
- `fleet.status()` — full fleet health snapshot (used sparingly, for situational awareness)

This separation means the Master Claw's context stays lean during normal operations and only fills with detail when it's actively investigating a problem.

### 3. Unified Control Plane via `claw-api` (Motor Output)

The Master Claw acts on its decisions exclusively through tool calls to a single `claw-api` service. There is no split between "hard actions" and "soft actions" from the Master Claw's perspective — all governance operations are tool calls, and `claw-api` handles the underlying implementation.

#### `claw-api` Service Surface

A thin API service (MCP server or REST) that exposes fleet management as tool calls. `claw up` auto-injects `claw-api` when the pod contains a Master Claw (detected by `x-claw.role: master` or similar marker), the same way it auto-injects cllama sidecar services. The operator does not manually wire Docker socket mounts, policy volumes, or compose file paths — `claw up` handles the plumbing. The Master Claw auto-discovers `claw-api` capabilities via the standard `claw.skill.emit` label.

**Agent-to-service mapping:** Clawdapus already expands `count > 1` services into ordinal-named compose services (e.g., `crypto-crusher-0`, `crypto-crusher-1`), each with a `claw.ordinal` label and a distinct `claw_id` in cllama telemetry. Governance actions target these ordinal services, not the abstract agent definition. When the Master Claw sees `crypto-crusher-1` drifting, it targets that specific compose service — siblings are unaffected. For `count: 1` services (the common case), agent and service are 1:1.

**Operations exposed:**
| Action | What it does | Implementation |
|--------|-------------|----------------|
| `fleet.status` | Aggregated health, cost, uptime for all claws in scope | Docker inspect + cllama log aggregation |
| `fleet.query_metrics` | Detailed per-agent telemetry for a time window | cllama log query (keyed by `claw_id`) |
| `fleet.restart(service)` | Restart a specific ordinal service | `docker compose restart <service>` |
| `fleet.scale(base, count)` | Scale a base service up or down | Deferred — requires compose regeneration via `claw up`, not a runtime action |
| `fleet.quarantine(service)` | Stop a specific ordinal service and flag for review | `docker compose stop <service>` + quarantine flag to policy volume |
| `fleet.budget.set(claw_id, ...)` | Adjust per-agent or pod-wide compute budget | Writes budget override to policy volume (keyed by `claw_id`); cllama picks up on next request |
| `fleet.model.restrict(claw_id, ...)` | Restrict or downgrade an agent's model access | Writes model policy to policy volume (keyed by `claw_id`); MGL enforces on next inference |
| `fleet.logs(service, lines)` | Tail recent logs for a specific service | `docker compose logs <service>` |

The key insight: the `claw-api` owns the `policy` volume internally. Soft actions (budgets, model restrictions) and hard actions (restart, quarantine) are both tool calls to the Master Claw. The `claw-api` decides whether to talk to Docker or write a policy file — that's an implementation detail hidden behind a uniform interface. All operations use compose service names for lifecycle and `claw_id` for telemetry/policy, which are 1:1 for ordinal-expanded services.

**Benefits of unification:**
- Single audit trail: every governance decision is a logged tool call, not a mix of tool calls and file writes
- The Master Claw's LLM operates in one mode (tool calling), not two (tool calling + file I/O)
- `claw-api` can validate, rate-limit, and log all actions consistently
- Policy volume is an internal implementation detail, not a surface the Master Claw needs to understand

**Privilege model:** `claw-api` is not a regular claw — it is a privileged infrastructure service, like cllama. It is effectively Docker-root within the pod. It requires:
- The Docker socket (`/var/run/docker.sock`) mounted into its container — this grants full Docker API access
- Read access to `compose.generated.yml` (to know what services exist and their configuration)
- The `docker compose` binary (or equivalent) for lifecycle operations
- Write access to the internal policy volume for soft governance actions

This is an honest trade-off: `claw-api` needs Docker access to perform lifecycle operations, and Docker's socket API does not support granular verb-level permissions natively. Scoped socket proxies (e.g., Tecnativa/docker-socket-proxy) can restrict API endpoints and are RECOMMENDED for production deployments, but are not required by this ADR.

This is consistent with ADR-002: `docker compose` remains the sole lifecycle authority. `claw-api` shells out to `docker compose` for all lifecycle operations against the generated compose file. Docker SDK is used only for read operations (inspect, log collection for `fleet.query_metrics`).

Critically, the Master Claw itself does NOT have these privileges. The Master Claw is a standard, unprivileged claw that reaches `claw-api` via `service://claw-api` — the same way any claw reaches any pod service. The `claw-api` authenticates the Master Claw and enforces the operator's scoping (which actions are allowed, which agents can be targeted). The Master Claw cannot grant itself new capabilities.

### 4. CLLAMA_SPEC Log Contract Extension

The current CLLAMA_SPEC (Section 5) requires only four fields: `timestamp`, `claw_id`, `type`, and `intervention_reason`. The reference cllama implementation already emits richer telemetry, but the fields are not normative. This ADR depends on a richer log contract and formally extends the CLLAMA_SPEC required fields:

| Field | Type | Required | Notes |
|-------|------|----------|-------|
| `timestamp` | ISO-8601 string | YES | Already in spec |
| `claw_id` | string | YES | Already in spec |
| `type` | enum | YES | Already in spec: `request`, `response`, `intervention`, `drift_score` |
| `intervention_reason` | string | conditional | Already in spec; required when `type=intervention` |
| `model` | string | YES (new) | Model ID requested (e.g., `anthropic/claude-sonnet-4-6`) |
| `status_code` | int | YES (new) | HTTP status code of upstream response; required for `type=response` |
| `latency_ms` | int | YES (new) | Round-trip latency to upstream provider; required for `type=response` |
| `tokens_in` | int | SHOULD (new) | Input token count; required when provider returns usage data |
| `tokens_out` | int | SHOULD (new) | Output token count; required when provider returns usage data |
| `cost_usd` | float | SHOULD (new) | Estimated cost; required when proxy can compute it from token counts and pricing |
| `error` | string | conditional (new) | Error description; required when request failed |

This extension must be applied to CLLAMA_SPEC.md as part of Milestone 1. The reference implementation already emits all these fields — the change is making them normative.

### 5. `claw audit` CLI Command

A new CLI command that reads and queries cllama structured logs. This serves both human operators and the Master Claw's telemetry plugin.

```
claw audit [--claw <id>] [--since <duration>] [--type request|response|intervention|drift_score]
```

The `--type` filter matches the standardized cllama log taxonomy (CLLAMA_SPEC Section 5): `request`, `response`, `intervention`, and `drift_score`. Outputs structured summaries: per-agent cost, intervention counts, drift score history, model usage breakdown. The same logic that powers the CLI also powers the `claw-api`'s `fleet.query_metrics` and the telemetry plugin's anomaly detection.

### 6. Drift Scoring (Deferred Details)

Drift scoring is the composition of:
- **cllama telemetry** — intervention frequency, error rates, cost anomalies
- **MGL compliance events** — rule violations, response amendments
- **Behavioral signals** — output sampling, contract adherence checks

The exact scoring model is organization-specific (as the manifesto states). Clawdapus provides the raw signals via `claw audit` and the telemetry plugin. MGL provides compliance scoring. The Master Claw synthesizes these into decisions. The scoring algorithm lives in the Master Claw's contract and tools, not in Clawdapus infrastructure.

### 7. Hub-and-Spoke (Future Extension)

Single-pod governance is the first milestone. Multi-pod hub-and-spoke extends naturally:
- Each pod runs its own cllama proxy emitting local telemetry
- Each pod's `claw-api` exposes a `service://claw-api` surface that a remote Master Claw can reach (via standard network routing and service credentials)
- A central Master Claw pod declares `service://` surfaces pointing to remote `claw-api` instances, pulling telemetry and issuing commands through the same tool interface used for local governance

This uses the existing `service://` surface scheme (ADR-003) and credential model. The only new work is network routing between pods and cross-pod authentication on `claw-api`. Deferred until single-pod governance is proven.

## Implementation Sequence

### Milestone 1: `claw audit` + Telemetry Foundation
1. Extend CLLAMA_SPEC Section 5 with the normative fields defined in Section 4 of this ADR
2. Implement `claw audit` CLI — collects cllama structured JSON from Docker container logs (stdout), per CLLAMA_SPEC. No second log path; Docker's log driver is the single source of truth
3. Parse and aggregate: per-agent cost, intervention counts, drift scores, model usage breakdown
4. Define anomaly threshold format (configurable per-agent budget limits, error rate ceilings, intervention frequency caps)

### Milestone 2: `claw-api` Service
4. Build `claw-api` as a lightweight Go service (or MCP server)
5. Expose read operations first: `fleet.status`, `fleet.query_metrics`, `fleet.logs`
6. Emit skill via `claw.skill.emit` label
7. Add to trading-desk example as a pod service

### Milestone 3: Master Claw Example
8. Write a Master Claw contract (the governor's behavioral rules)
9. Declare `feeds: [{source: claw-api, path: /fleet/alerts, trigger: per-turn}]` in pod YAML (ADR-013)
10. Deploy as a standard claw in the trading-desk pod with `service://claw-api`
11. Wire `INVOKE` schedule for periodic fleet review (heartbeat check even when no anomalies)

### Milestone 4: Write Operations + Policy Volume
12. Add write operations to `claw-api`: `fleet.restart`, `fleet.quarantine`, `fleet.scale`
13. Implement internal policy volume: `fleet.budget.set`, `fleet.model.restrict` write to it
14. Wire MGL/cllama to watch policy volume for runtime overrides
15. Master Claw contract includes budget management and quarantine escalation rules

### Milestone 5: Hub-and-Spoke (Future)
16. Remote telemetry ingestion via `claw-api` federation
17. Cross-pod `claw-api` authentication
18. Central Master Claw example with multi-pod scope

## Rationale

The Master Claw as a pattern (not a driver) is the most Clawdapus-native design. It proves the system's own thesis: governance is just another workload, governed by the same infrastructure. The Master Claw has a contract it cannot modify, surfaces it must declare, and a cllama proxy watching its own cognition. It's turtles all the way down.

The unified `claw-api` control plane means the Master Claw operates in a single mode (tool calls) with a single audit trail. The policy volume is an internal implementation detail of `claw-api`, not a surface the Master Claw manages directly. This mirrors the manifesto's "cllama is the sensory organ, Master Claw is the brain" model — the brain sends motor commands through a single nerve bundle, not two.

The anomaly-based telemetry model (push alerts, pull details) scales to large fleets by keeping the Master Claw's context lean during normal operations. Context feeds (ADR-013) make telemetry injection a universal Clawdapus primitive — the Master Claw doesn't need a runner-specific plugin, just a feed declaration in the pod YAML. The same feed infrastructure that gives the Master Claw fleet alerts gives a trader claw live market data.

## Consequences

**Positive:**
- Closes the biggest gap between the manifesto vision and the implementation.
- `claw audit` is independently useful for human operators even without a Master Claw.
- `claw-api` is reusable for any automation, not just the Master Claw.
- No new driver type — validates the runner-agnostic governance model.
- Incremental: each milestone is independently valuable and deployable.

**Negative:**
- `claw-api` has a significant blast radius — it can restart and quarantine claws. Requires careful auth and contract scoping.
- Anomaly thresholds require tuning — too sensitive and the Master Claw drowns in alerts, too lax and it misses real issues. Sensible defaults with per-agent overrides are essential.
- Drift scoring algorithm is explicitly deferred — the Master Claw ships with operator-defined heuristics in its contract, not a built-in scoring engine.
