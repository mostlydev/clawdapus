# claw-api Write Plane Design (Issue #78)

## Scope

Implement write operations for `claw-api`. Four new POST endpoints alongside existing reads.

## API Surface

| Endpoint | Verb | Target | Effect |
|---|---|---|---|
| `POST /fleet/restart` | `fleet.restart` | compose service name | Docker ContainerRestart |
| `POST /fleet/quarantine` | `fleet.quarantine` | compose service name | Docker ContainerStop + quarantine marker |
| `POST /fleet/budget/set` | `fleet.budget.set` | `claw_id` | Write `budget.json` to governance dir |
| `POST /fleet/model/restrict` | `fleet.model.restrict` | `claw_id` | Write `model-restrict.json` to governance dir |

## Request Bodies

```json
// POST /fleet/restart
{"service": "trader-0"}

// POST /fleet/quarantine
{"service": "trader-0"}

// POST /fleet/budget/set
{"claw_id": "trader-0", "limit_usd": 2.00, "window": "1h", "behavior": "rate_limit"}
// behavior: "rate_limit" | "hard_stop" | "soft_alert" | "graceful_switch"

// POST /fleet/model/restrict
{"claw_id": "trader-0", "allowed_models": ["claude-haiku-4-5"]}
```

## Authorization

Reuses existing `authorize(w, r, verb, target)` pattern with new cases:

- `fleet.restart`, `fleet.quarantine`: call `principal.AllowsComposeService(podName, target)` — exact compose service name, fail closed
- `fleet.budget.set`, `fleet.model.restrict`: call `principal.AllowsClawID(podName, target)` — same dimension as metrics

Both dimensions already exist on `Principal`. Write verbs already defined in `internal/clawapi/principal.go`.

## Runtime Governance Files

**IMPORTANT:** `.claw-runtime/` is wiped wholesale by `claw up` (`resetRuntimeDir` = `RemoveAll` + `MkdirAll`). Governance override files must live outside it.

claw-api gains a `governanceDir` field pointing to `.claw-governance/` — a sibling of `.claw-runtime/` that `claw up` creates but **never resets**.

File layout:
```
.claw-governance/          ← survives claw up
  trader-0/
    quarantine.json      -- {"quarantined": true, "at": "2026-03-22T10:00:00Z", "by": "sentinel"}
    budget.json          -- {"limit_usd": 2.00, "window": "1h", "behavior": "rate_limit"}
    model-restrict.json  -- {"allowed_models": ["claude-haiku-4-5"]}
```

Files are written atomically (write temp, rename). Budget and model-restrict files are written now; cllama consumer wiring is deferred. Quarantine marker is written alongside the Docker stop.

## Docker Operations

- `fleet.restart`: `docker.ContainerRestart(ctx, containerID, nil)` for all containers matching the compose service name
- `fleet.quarantine`: `docker.ContainerStop(ctx, containerID, nil)` + write quarantine marker

Docker SDK used for both (read-only principle applies to `claw up` orchestration path; targeted container ops in claw-api are acceptable).

## Compose Wiring

`governanceDir` is passed from `main.go` via env var `CLAW_GOVERNANCE_DIR` (default `/claw-governance`). The dir is mounted into claw-api from host `.claw-governance/` in `compose_up.go`. `claw up` calls `os.MkdirAll(.claw-governance, 0o777)` to create it (same permissions as other runtime dirs) but does NOT call `RemoveAll` on it.

## Audit

Every write call is logged via existing `logDecision()`. Write verbs appear in the audit trail with the compose service or claw_id as the target.

## Testing

Unit tests for:
- handler routes 404 for unknown paths, 405 for wrong method
- `fleet.restart` authorization: wrong verb denied, out-of-scope service denied, in-scope service allowed
- `fleet.quarantine` same
- `fleet.budget.set`: validation (missing claw_id, unknown behavior value), file written with correct content
- `fleet.model.restrict`: validation, file written

Integration tests use Docker containers (spike tag).

## What Is NOT in Scope

- cllama reading governance override files (deferred)
- `fleet.scale` (explicitly deferred in ADR-012)
- Clearing/resetting overrides (follow-up)
- Fleet-level (non-ordinal) budget targets (follow-up)
