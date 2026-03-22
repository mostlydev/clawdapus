# ADR-014: Telemetry Normalization and `claw audit`

**Date:** 2026-03-19
**Status:** Accepted
**Depends on:** ADR-008 (cllama Sidecar Standard)
**Consumed by:** ADR-012 (Master Claw)
**Implementation:** Milestones 1-3 complete. `claw audit` CLI, normalization pipeline (`internal/audit/`), and `claw-api` read operations all implemented. Alert thresholds configurable via `clawapi.Thresholds` type and `CLAW_ALERT_*` env vars. `feed_fetch` events surfaced in audit schema.

## Context

Clawdapus needs a stable telemetry substrate for operator audit, anomaly detection, and fleet governance. That substrate exists in rough form today, but it is not yet coherent:

- cllama emits structured JSON to stdout
- the formal spec and the reference implementation do not match cleanly
- nothing in Clawdapus currently ingests or normalizes those records

Concretely:

- the spec talks about `timestamp` and `intervention_reason`
- the reference implementation emits `ts` and `intervention`
- the passthrough proxy emits `request`, `response`, and `error`
- the reference logger always includes `intervention: null` on non-intervention records
- the reference logger emits `error`, but `CLLAMA_SPEC.md` does not currently list `error` in its event-type prose
- richer future proxies may emit `intervention` and possibly `drift_score`

If raw proxy logs remain the only contract, every consumer ends up reimplementing compatibility logic. Clawdapus needs one normalization boundary and one internal schema that higher-level features can depend on.

## Decision

### 1. Raw cllama stdout is the durable source of truth

The raw event stream emitted by cllama to stdout is the only required telemetry source. Clawdapus reads it through Docker container logs. There is no second audit log path.

This preserves the existing operational model:

- cllama writes structured JSON to stdout
- Docker captures stdout
- Clawdapus ingests from Docker logs

### 2. `claw audit` is the normalization boundary

`claw audit` is not just a report command. It is the canonical ingestion and normalization layer for cllama telemetry inside Clawdapus.

All higher-level telemetry consumers should build on the normalized output of this ingestion path rather than depending directly on raw proxy log shape.

That includes:

- the `claw audit` CLI itself
- `claw-api` read operations such as `fleet.query_metrics`
- anomaly summarization such as `GET /fleet/alerts`
- any future drift-scoring or fleet-governance pipeline

### 3. Normalized event schema

`claw audit` MUST normalize raw log records into the following stable shape:

| Field | Notes |
|-------|-------|
| `timestamp` | Normalized from `ts` or `timestamp` |
| `claw_id` | Agent identity |
| `type` | Core set: `request`, `response`, `intervention`, `error` |
| `model` | Requested or routed model when available |
| `status_code` | Upstream/provider status when available |
| `latency_ms` | Request latency when available |
| `tokens_in` | Input tokens when available |
| `tokens_out` | Output tokens when available |
| `cost_usd` | Estimated cost when available |
| `intervention_reason` | Normalized from `intervention` or `intervention_reason` |
| `error` | Error string when present |

Not every event type populates every field. The schema is sparse by design.

Future extensions may add derived governance fields such as `drift_score`, but those are not part of the required core schema in V1.

### 4. Type compatibility rules

The normalized core type set is:

- `request`
- `response`
- `intervention`
- `error`

Compatibility rules:

- passthrough proxies emitting only `request`, `response`, and `error` are valid
- richer policy proxies may additionally emit `intervention`
- `drift_score` is an optional extension event or derived metric, not part of the required core type set
- the absence of `drift_score` or `intervention` in a given proxy is normal and must not be treated as malformed telemetry

### 5. Normalization rules

At minimum, the normalizer must tolerate:

- `ts` or `timestamp`
- `intervention` or `intervention_reason`
- `intervention: null`, which must be treated as "no intervention" rather than a meaningful field presence
- partial events that omit cost or token data
- proxies that never emit drift events

Unknown extra fields may be ignored by the normalized schema as long as they do not break ingestion.

### 6. CLI surface

ADR-014 owns the canonical `claw audit` surface. It reads normalized events and exposes them through a stable operator-facing interface:

```text
claw audit [--claw <id>] [--since <duration>] [--type request|response|intervention|error]
```

It should support summaries such as:

- per-agent cost
- request volume
- error counts and rates
- intervention counts
- drift history when present through optional extensions or higher-level scoring
- model usage breakdown

### 7. Relationship to `CLLAMA_SPEC.md`

This ADR deliberately does not force immediate convergence between:

- the current raw emitted wire shape
- the older prose in `CLLAMA_SPEC.md`
- the normalized internal audit schema

The immediate requirement is ingestion compatibility and a stable internal contract.

A follow-on spec update should align `CLLAMA_SPEC.md` either with:

1. the raw emitted cllama wire format, or
2. the normalized schema defined here

That alignment should happen explicitly, not implicitly through drift. In particular, the spec should explicitly account for `error` events and the current raw `intervention` field shape.

## Implementation Sequence

### Milestone 1: Ingestion
1. Read cllama JSON lines from Docker logs
2. Parse per-line records safely
3. Reject or annotate malformed lines without collapsing the full audit stream

### Milestone 2: Normalization
4. Normalize field names and type variants
5. Normalize sparse records into the stable event shape
6. Filter and aggregate by `claw_id`, time window, and event type

### Milestone 3: Reuse
7. Build the `claw audit` CLI on top of normalized events
8. Reuse the same normalized ingestion for `claw-api` read operations
9. Reuse the same normalized ingestion for anomaly summarization

## Rationale

This keeps the repo honest about where the real mismatch is.

The problem is not only that the current spec is stale. The larger issue is that raw proxy telemetry is a moving implementation surface while fleet features need a stable contract. Putting the normalization boundary inside Clawdapus gives the rest of the system a dependable substrate without forcing every proxy implementation detail to stabilize first.

It also preserves the simple operational story: stdout is the log, Docker captures it, Clawdapus reads it.

## Consequences

**Positive:**
- Gives fleet features a stable telemetry contract even while raw proxy output evolves
- Avoids duplicating compatibility logic across CLI, `claw-api`, and governance code
- Preserves stdout-only audit logging
- Makes ADR-012 smaller and clearer

**Negative:**
- Introduces an internal schema distinct from the current raw emitted shape
- Requires an explicit future pass to reconcile `CLLAMA_SPEC.md` with reality
- Some proxy-specific fields may be ignored until they are intentionally added to the normalized contract
