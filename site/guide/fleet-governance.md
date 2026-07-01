# Fleet Governance

Fleet governance is the closed loop that turns Clawdapus telemetry into scoped
runtime action:

```
cllama telemetry -> claw-api read plane -> governor decision -> claw-api write plane -> runtime effect
```

The loop is explicit. Clawdapus wires the surfaces and scopes; the policy is
operator-authored in the Master Claw contract.

## Master Claw Pattern

Declare one service as the pod's master:

```yaml
x-claw:
  pod: operations-room
  master: governor

services:
  governor:
    image: operations-governor:latest
    x-claw:
      agent: ./agents/GOVERNOR.md
      feeds: [fleet-alerts]
      surfaces:
        - service://claw-api
      invoke:
        - schedule: "*/5 * * * *"
          name: "Fleet review"
          message: "Read fleet alerts, apply the reference policy if needed, and report."
```

When `x-claw.master` is set, `claw up` auto-injects `claw-api`, generates a
master principal, and injects `CLAW_API_URL` plus `CLAW_API_TOKEN` into the
master service. The master principal can read fleet telemetry and use the write
verbs, subject to its scopes.

Use `examples/master-claw/` as the worked example. It demonstrates one policy:
if a worker crosses the configured cost threshold, the governor calls
`fleet.budget.set` for that claw.

## Read Plane

The read plane is available through `claw-api`:

| Endpoint | Purpose |
|---|---|
| `GET /fleet/status` | Service health, counts, and compose service names |
| `GET /fleet/metrics?claw_id=<id>&since=<window>` | Audit telemetry for one claw |
| `GET /fleet/logs?service=<name>&lines=<n>` | Recent container logs for one service |
| `GET /fleet/alerts?since=<window>` | Threshold-derived anomaly summaries |
| `GET /agents` and `GET /agents/<id>/context` | Compiled and live context visibility |
| `GET /schedule` | Scheduled invocation state |

`/fleet/alerts` is also exposed as the built-in `fleet-alerts` feed. Subscribe
the governor to that feed so each scheduled review starts with current anomaly
state.

## Alert Thresholds

Set thresholds on the host before `claw up`; Clawdapus forwards them into the
auto-injected `claw-api` container.

```bash
export CLAW_ALERT_ERROR_RATE_PERCENT=5.0
export CLAW_ALERT_MAX_COST_USD=10.0
export CLAW_ALERT_FEED_ERROR_RATE_PERCENT=20.0
export CLAW_ALERT_INTERVENTION_COUNT=5
claw up -d
```

Thresholds only decide when an alert is emitted. They do not apply enforcement
by themselves. The governor closes the loop by choosing whether to call a write
verb.

## Write Plane

The write plane is also served by `claw-api`:

| Verb | Endpoint | Scope checked | Runtime effect |
|---|---|---|---|
| `fleet.restart` | `POST /fleet/restart` | `compose_services` | Restarts matching compose containers |
| `fleet.quarantine` | `POST /fleet/quarantine` | `compose_services` | Writes a quarantine marker, then stops matching containers |
| `fleet.budget.set` | `POST /fleet/budget/set` | `claw_ids` | Writes `.claw-governance/<claw-id>/budget.json` |
| `fleet.model.restrict` | `POST /fleet/model/restrict` | `claw_ids` | Writes `.claw-governance/<claw-id>/model-restrict.json` |
| `schedule.control` | `POST /schedule/<id>/<action>` | `services` | Pauses, resumes, skips, clears, or fires a schedule |

The smallest useful governor uses one threshold and one write verb. For example:

```bash
curl -sS -H "Authorization: Bearer $CLAW_API_TOKEN" \
  "$CLAW_API_URL/fleet/alerts?since=15m"

curl -sS -X POST \
  -H "Authorization: Bearer $CLAW_API_TOKEN" \
  -H "Content-Type: application/json" \
  "$CLAW_API_URL/fleet/budget/set" \
  -d '{"claw_id":"worker-a","max_requests":20,"window":"1h","behavior":"hard_stop"}'
```

The budget write is immediately observable on the host under
`.claw-governance/worker-a/budget.json`, and cllama reads that override on
subsequent requests.

## Principal Scopes

`claw-api` principals combine verbs with scope dimensions:

| Scope | Used for |
|---|---|
| `pods` | Broad authority over all services and claws in a pod |
| `services` | Service-level read access and schedule control |
| `claw_ids` | Per-agent telemetry and claw-targeted write verbs |
| `compose_services` | Container lifecycle writes such as restart and quarantine |

A narrower master principal can override the auto-generated one by using the
same name as `x-claw.master`. The master service still receives
`CLAW_API_URL` and `CLAW_API_TOKEN`; omit `inject-into` because the master
injection point is reserved.

```yaml
x-claw:
  master: governor
  principals:
    - name: governor
      verbs:
        - fleet.status
        - fleet.alerts
        - fleet.query_metrics
        - fleet.budget.set
        - schedule.read
        - schedule.control
      claw_ids:
        - worker-a
        - worker-b
      services:
        - worker-a
        - worker-b
```

The built-in master principal is broader: it gets all read and write verbs for
the pod named by `x-claw.master`. Use explicit principals when you want a
smaller authority surface.

## Operating Contract

Keep the governor contract concrete:

- Name the threshold it is allowed to act on.
- Name the exact write verb it may call.
- Require a fresh read before any write.
- Require the report to include the alert, the target claw or service, and the
  write response.
- Keep escalation paths explicit for actions that stop containers or change
  model access.

This keeps the governor auditable. The model makes a decision, but the
authority boundary remains the scoped `claw-api` bearer token and the generated
compose/runtime artifacts.
