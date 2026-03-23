# ADR-015: `claw-api` Authentication and Authorization Scoping

**Date:** 2026-03-19
**Status:** Accepted
**Depends on:** ADR-002 (Runtime Authority), ADR-013 (Context Feeds)
**Consumed by:** ADR-012 (Master Claw)
**Implementation:** Full principal model implemented. Three principal categories: (1) master principal (all verbs, pod-wide, auto-generated from `x-claw.master`), (2) self principals (read verbs, service-scoped, auto-generated from `claw-api: self` on service x-claw), (3) explicit principals (`x-claw.principals` with verbs, scope, inject-into). Scope has four dimensions: `pods`, `services`, `claw_ids`, `compose_services` (last reserved for write-plane ordinal targeting). Verbs validated against known set at parse time. `inject-into` collision between distinct principals fails closed. Surfaces grant topology (reachability) only — `claw-api: self` is the explicit authority signal.

## Context

`claw-api` is the proposed control surface for fleet governance. Even in its read-only form it can expose fleet-wide health, logs, and telemetry. In its write form it can restart, quarantine, and eventually constrain agents. That makes it a high-blast-radius service.

The repo already has a strong general rule for services:

- `service://` grants topology and reachability
- the service itself owns authentication and authorization
- Clawdapus does not treat network presence as permission

That rule must hold for `claw-api` too, but `claw-api` has additional needs:

- the Master Claw must be able to call it safely
- feed fetches such as `/fleet/alerts` must authenticate too when cllama fetches them
- future non-governor automation may also call it
- hub-and-spoke federation needs the same model across pods

If this is left implicit, the project drifts toward ambient trust: "the governor can reach `claw-api`, therefore it is allowed." That would violate the service model and hide the real authority boundary.

## Decision

### 1. `claw-api` is a normal authenticated service

`claw-api` follows the standard service rule:

- `service://claw-api` grants network reachability only
- `x-claw.master` does not grant authority
- caller identity must come from explicit service credentials
- all authorization decisions happen inside `claw-api`

There is no implicit trust from:

- being on the internal pod network
- being the service named by `x-claw.master`
- presenting `X-Claw-ID`
- being behind cllama

`X-Claw-ID` and `X-Claw-Pod` are informational caller headers, not authentication.

### 2. Explicit principal credentials are required

Every `claw-api` caller authenticates as a principal. The credential material is delivered through standard service-auth mechanisms already used elsewhere in the repo:

- environment variables
- Docker secrets
- mounted credential files

V1 SHOULD use bearer tokens because they are simple for both REST and MCP-adjacent service patterns, but the deeper requirement is principal resolution, not a specific wire protocol. Any credential scheme is acceptable if it resolves to a principal identity and scope set.

The existing cllama bearer token in `metadata.json["token"]` is reserved for caller-to-cllama authentication and currently uses the `<agent-id>:<secret>` format. `claw-api` credentials MUST NOT reuse that field or assume the same token namespace. Service credentials are separate principal credentials, not proxy ingress secrets.

### 3. Feed fetches and tool calls use the same principal model

The same principal used for explicit `claw-api` tool calls must also be usable for feed fetches such as `GET /fleet/alerts`.

For cllama-enabled claws, that means the `claw-api` credential cannot live only inside the runner container. Clawdapus must project the credential into the cllama-side per-agent context so cllama can authenticate the feed request on behalf of the same principal.

The projection path is:

- host: `.claw-runtime/context/<agent-id>/service-auth/claw-api.json`
- mounted in cllama as: `/claw/context/<agent-id>/service-auth/claw-api.json`

V1 file shape:

```json
{
  "service": "claw-api",
  "auth_type": "bearer",
  "token": "capi_opaque_token",
  "principal": "octopus"
}
```

`auth_type` is intentionally explicit so the file can evolve without overloading cllama's own ingress token semantics. Tool-call runners may expose the same credential through env vars or secrets, but this per-agent file is the canonical projection seam for cllama-backed feed fetches.

This keeps the model coherent:

- the Master Claw feed and its tool calls are authorized as the same principal
- `X-Claw-ID` is still sent for caller context
- service auth still relies on explicit credentials, not reachability

### 4. Authorization is deny-by-default and scope-based

Every authenticated principal receives an explicit allowlist of verbs and targets. No principal gets implicit wildcard authority.

The scope model is:

- **verbs**: what operation may be performed
- **targets**: which services, claws, or pods are in scope

Illustrative verb set:

- read: `fleet.status`, `fleet.query_metrics`, `fleet.logs`, `fleet.alerts`
- write: `fleet.restart`, `fleet.quarantine`, `fleet.budget.set`, `fleet.model.restrict`, `fleet.scale`

Illustrative target dimensions:

- compose service name
- base service name
- `claw_id`
- pod name

The exact config file shape is an implementation detail, but the semantics are not: verbs and targets are checked independently and both must match.

### 5. Read operations are filtered by scope

Authorization for read operations is not just allow/deny at endpoint level.

If a principal may call:

- `fleet.status`
- `fleet.query_metrics`
- `fleet.logs`
- `GET /fleet/alerts`

then the returned data must still be filtered to only the principal's allowed targets.

Examples:

- a principal scoped to `crypto-crusher-*` must not receive alerts for `trade-executor`
- `fleet.status` for a partially scoped principal returns a partial fleet view, not the full pod
- `fleet.query_metrics` for an out-of-scope `claw_id` is denied

### 6. Write operations require exact target validation

Write operations must validate both:

- the verb is allowed
- the requested target is in scope

This is stricter than read-path filtering. A write request against an out-of-scope target fails closed.

For write operations that mutate shared state, such as budget or model overrides, the target identity must be unambiguous. If the request addresses a base service but the action is defined at ordinal or `claw_id` granularity, `claw-api` must reject ambiguous requests rather than guessing.

### 7. Every `claw-api` call is auditable

`claw-api` must log:

- principal identity
- requested verb
- requested targets
- allow or deny result
- effective filtered scope for read operations where relevant

This audit trail is required for both:

- explicit tool calls
- feed-serving requests such as `/fleet/alerts`

### 8. Relationship to hub-and-spoke

The same principal-and-scope model applies to remote `claw-api` federation.

Hub-and-spoke should not invent a second authorization model. A remote Master Claw authenticates to a remote `claw-api` as a scoped principal in exactly the same way a local governor does, using normal service credentials and target filtering.

## Implementation Sequence

### Milestone 1: Principal Model
1. Define the internal principal and scope representation
2. Define how credentials resolve to principals
3. Make authorization deny-by-default

### Milestone 2: Credential Projection
4. Deliver `claw-api` credentials to callers through normal service-auth mechanisms
5. For cllama-enabled claws, project the same credential into `service-auth/claw-api.json` so `/fleet/alerts` can authenticate

### Milestone 3: Read-Path Enforcement
6. Enforce verb checks for read operations
7. Filter read responses to in-scope targets
8. Audit read calls and feed-serving calls

### Milestone 4: Write-Path Enforcement
9. Enforce exact target validation for write operations
10. Reject ambiguous target specifications
11. Audit all write attempts

## Rationale

This preserves the repo's simplicity rule instead of adding a special governor exception.

`claw-api` is just a service, albeit a powerful one. Treating it as a service keeps the trust model understandable:

- topology says who can reach it
- credentials say who the caller is
- `claw-api` policy says what that caller may do

Making feeds use the same principal model avoids a subtle hole where feed injection would otherwise become an ambient privileged side channel.

## Consequences

**Positive:**
- Preserves the existing service-auth model instead of creating a special trust path for governance
- Makes `claw-api` auditable and least-privilege by design
- Gives read and write operations one coherent scope model
- Keeps hub-and-spoke aligned with single-pod behavior

**Negative:**
- Adds credential projection work for cllama-backed feeds
- Requires response filtering logic for read endpoints
- Makes `claw-api` implementation more involved than a thin unauthenticated wrapper
