# ADR-002: Runtime Authority

**Date:** 2026-02-18
**Status:** Accepted
**Decided during:** 3-agent deliberation (arch-review room)

## Context

Clawdapus needs to manage container lifecycle (start, stop, restart) and also inspect running containers (status, logs, events). Docker provides two interfaces: the `docker compose` CLI and the Docker Go SDK (`github.com/docker/docker/client`). Using both for lifecycle operations creates a dual-write problem — compose and SDK can disagree about container state.

## Decision

**`docker compose` is the sole lifecycle authority. Docker SDK is read-only.**

- **Lifecycle operations** (start, stop, restart, scale, remove): always via `docker compose` CLI, operating on the generated `compose.generated.yml`
- **Read operations** (inspect, logs, events, status): via Docker Go SDK for structured data and streaming
- **Never** use Docker SDK for start/stop/restart/remove

`claw up` emits `compose.generated.yml` and runs `docker compose up`. `claw down` runs `docker compose down`. `claw ps` uses Docker SDK to inspect running containers. There is exactly one source of truth for "what should be running" (the generated compose file) and one authority for making it so (`docker compose`).

## Consequences

**Positive:**
- Single source of truth eliminates state drift
- Compose handles networking, volume mounts, dependency ordering, health checks — no reimplementation
- Generated compose file is inspectable and debuggable
- Users can run `docker compose` directly against the generated file in emergencies

**Negative:**
- Shelling out to `docker compose` is slower than SDK calls for lifecycle ops
- Error handling requires parsing compose CLI output (structured JSON output helps but isn't perfect)
- Some advanced lifecycle operations (rolling restarts, canary deploys) may be awkward through compose

**Risks:**
- Compose CLI breaking changes could affect `claw` (mitigated by pinning minimum compose version via `claw doctor`)
- If Clawdapus ever needs sub-second lifecycle operations, the compose CLI overhead may become a bottleneck (unlikely for agent workloads)

## Alternatives Considered

1. **SDK-only** — use Docker Go SDK for everything. Eliminates compose dependency but means reimplementing networking, volume mounts, dependency ordering, and every compose feature we rely on. Rejected: too much reimplementation.

2. **Dual authority** — compose for initial deploy, SDK for runtime adjustments. Creates state drift between what compose thinks is running and what SDK has done. Rejected: the exact problem this ADR prevents.

3. **Compose as a library** — import compose's Go packages directly instead of shelling out. More tightly coupled, version-locked to compose internals, and compose's library API is not stable. Rejected: fragile coupling.
