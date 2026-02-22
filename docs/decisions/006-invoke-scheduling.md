# ADR-006: INVOKE Scheduling Mechanism

**Date:** 2026-02-21
**Status:** Accepted

## Context

The initial architecture plan stated that the `INVOKE` directive would be implemented via a system cron daemon (`/etc/cron.d/`) running alongside the agent, combined with a gateway wake RPC mechanism to trigger invocations. This approach required installing and managing the `cron` daemon inside the container and building a separate network interface for the wake RPC.

## Decision

We have updated the enforcement mechanism for the `INVOKE` directive. Instead of relying on system cron, the `INVOKE` directive bakes scheduled tasks into the OCI image as labels (`claw.invoke.N`). At runtime, the driver extracts these labels and translates them into a runner-native scheduling format. For example, the OpenClaw driver generates a `jobs.json` file and mounts it directly into OpenClaw's `/app/state/cron/` directory, allowing OpenClaw's internal scheduler to pick it up automatically.

## Rationale

Utilizing the runner's native scheduling capabilities (like OpenClaw's internal job scheduler) is significantly more robust and secure. It eliminates the need to run an external cron daemon as a secondary process inside the container and removes the complexity of maintaining a dedicated RPC wake interface. This aligns better with the philosophy of using the runner's native tools whenever possible.

## Consequences

**Positive:**
- Removes the dependency on the `cron` daemon and RPC interfaces, reducing the container's attack surface and complexity.
- Integrates more deeply with the runner's native lifecycle and logging.

**Negative:**
- Drivers must now understand and translate cron schedules into runner-specific job formats, which slightly increases the complexity of the driver implementation.
