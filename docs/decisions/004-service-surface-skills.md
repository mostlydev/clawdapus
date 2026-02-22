# ADR-004: Service Surface Skills Strategy

**Date:** 2026-02-21
**Status:** Accepted

## Context

When an agent is given access to a service surface (e.g., `SURFACE service://fleet-master`), it requires instructions on how to interact with that service. Relying solely on runtime discovery (like MCP) is insufficient because some services lack self-describing capabilities, or the agent needs "minimum viable context" (like ports, hostnames, and protocols) just to bootstrap the initial communication.

## Decision

We implement a two-tiered approach for service surface skills:

1. **Emitted Skills (Primary):** The target service's Docker image can declare a `claw.skill.emit` label pointing to a markdown skill file inside its container. Clawdapus extracts this file and mounts it into the consuming agent's context.
2. **Generated Fallbacks:** If no emitted skill is found, Clawdapus automatically generates a fallback markdown skill by parsing the `expose` and `ports` blocks from the target service's pod definition.

## Rationale

The provider of a service is best equipped to document how to use it, making emitted skills the ideal source of truth. However, when explicit documentation is absent, Clawdapus must guarantee that the agent receives at least the basic networking facts. This fallback mechanism ensures that every `service://` surface results in actionable context, preventing the agent from "flying blind."

## Consequences

**Positive:**
- Agents are guaranteed to receive context for every declared service surface.
- Decouples service documentation from the consumer, allowing service owners to update their interfaces independently.

**Negative:**
- Fallback skills lack semantic details and API specifics, potentially requiring operators to manually provide supplemental `SKILL` directives for undocumented services.
