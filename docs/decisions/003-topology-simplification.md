# ADR-003: Topology Simplification and the HANDLE Directive

**Date:** 2026-02-21
**Status:** Accepted

## Context

We initially modeled external chat platforms (like Discord, Slack) and outbound network egress as `SURFACE` directives (`channel://discord`, `egress://api.example.com`). This overloaded the `SURFACE` concept, conflating infrastructure boundaries (network reachability, volume mounts) with social topology and platform identity configuration. 

## Decision

We have refined the topology model by strictly separating infrastructure surfaces from social identity:

1. **Surfaces are Infrastructure Only:** The `SURFACE` directive is now strictly for infrastructure boundaries: `volume://`, `host://`, and `service://`.
2. **Removal of Egress:** We removed `egress://` surfaces completely. Egress control is delegated entirely to the network layer (e.g. standard compose network profiles), simplifying Clawdapus's responsibilities.
3. **Introduction of the HANDLE Directive:** We introduced the `HANDLE` directive (e.g., `HANDLE discord`) to replace `channel://`. `HANDLE` specifically declares an agent's public identity and presence on a communication platform. 

## Rationale

Clawdapus needs to differentiate between "what services can this container route to" (Infrastructure) and "who is this agent talking to on chat platforms" (Identity). 

By extracting chat configuration into the new `HANDLE` directive, the driver can still perform the heavy lifting of translating chat platform configuration into runner-native schemas (e.g. OpenClaw's JSON5 `channels.discord.*` config). This lowers the barrier to entry so operators don't need to manually write complex `CONFIGURE` directives for standard chat platforms. 

Simultaneously, it enables the "Leviathan pattern": Clawdapus now parses `handles:` from the pod manifest and broadcasts them as pod-wide environment variables (e.g. `CLAW_HANDLE_<SERVICE>_<PLATFORM>=<ID>`). This allows non-claw API services in the pod to dynamically address and mention agents without parsing runner-specific configurations.

## Consequences

**Positive:**
- `SURFACE` taxonomy is strictly infrastructure-focused, aligning perfectly with Docker/Compose semantics.
- Drivers retain the abstraction for chat platforms (via `HANDLE`), keeping agent setup simple and preventing operators from needing to learn internal runner JSON schemas.
- Pod-wide environment variables (`CLAW_HANDLE_*`) enable standard API services to effortlessly discover and interact with agent identities.

**Negative:**
- Adds a new distinct directive (`HANDLE`) to the Clawfile syntax instead of reusing `SURFACE`.
- Drivers must still maintain translation logic for supported chat platforms.
