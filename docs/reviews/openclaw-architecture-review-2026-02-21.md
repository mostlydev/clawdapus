# OpenClaw Architecture Review (2026-02-21)

Scope reviewed: current handle/directive + service surface implementation against local OpenClaw npm source/docs.

## Executive assessment

The direction is solid for context discovery and local interoperability:
- Service-emitted skills + fallback generated skill files.
- Pod-wide `CLAW_HANDLE_*` projection for identity routing.
- CLAWDAPUS + mounted per-claw skills.

Where it diverges from “fleet of claws talking publicly + API sidecar” is the control-plane layer:
- OpenClaw remains gateway-first and routing is configured in `openclaw.json` (`agents`, `channels`, `bindings`), not inferred from surface metadata alone.
- Current implementation is good for discovery/documentation, but not yet sufficient to fully model fleet-wide comms/routing without extra `CONFIGURE` data.

## Open issues

### 1) High — Service surface port data ignores `ports` definitions
- Severity: High
- File: `cmd/claw/compose_up.go`
- Issue: Enrichment uses only `targetSvc.Expose` and ignores service `ports:`.
- Impact:
  - Services that only expose `ports:` get no port hints in generated `surface-<target>.md`.
  - Public/API services are typically declared with `ports` in compose context, so fallbacks can be incomplete.
- Next step:
  - Parse and merge both `expose` and `ports` into `ResolvedSurface.Ports`.

### 2) Medium — `claw.skill.emit` failures are fail-fast
- Severity: Medium
- File: `cmd/claw/compose_up.go`, `internal/runtime/service_skill.go`
- Issue: If `claw.skill.emit` label exists but path is missing/invalid, `claw up` returns hard error.
- Impact:
  - Single bad label can block full pod startup even though a generated fallback skill is available.
- Next step:
  - Treat missing/invalid emitted-skill path as warn-and-fallback by default; reserve fail-fast only when strict mode is requested.

### 3) High — HANDLE enables transport channels, not routing/auth model
- Severity: High
- Files: `internal/driver/openclaw/config.go`, `internal/driver/openclaw/handle_skill.go`
- Issue: `HANDLE` currently maps to `channels.<platform>.enabled` booleans only.
- Impact:
  - No account binding (`channels.<platform>.accounts`), no bindings/agent routing materialization, no per-channel allow/deny policy by default.
  - For public Discord (or other) fleet use, this is insufficient without explicit `CONFIGURE` entries.
- Next step:
  - Document required `CONFIGURE` pattern in examples and/or add a dedicated operator-driven handle→binding contract.

### 4) Medium — Multi-claw public topology still needs explicit gateway architecture
- Severity: Medium
- Files: cross-cutting (compose and driver)
- Issue: One claw per service + local network surfaces is fine for reachability, but OpenClaw’s documented scaling model is gateway-hosted multi-agent routing.
- Impact:
  - Identity/context is available but runtime interoperability can fragment unless a shared gateway strategy is defined (shared accounts, deterministic `agentId`/`accountId` mapping, bindings, allowlists).
- Next step:
  - Add architecture ADR describing gateway topology for multi-claw public fleets and migration path.

### 5) Low — Service-emitted skill trust is implicit
- Severity: Low
- Files: `cmd/claw/compose_up.go`, `internal/runtime/service_skill.go`
- Issue: `claw.skill.emit` content is mounted from target image with no provenance checks.
- Impact:
  - Malicious/compromised image can inject misleading operational guidance in runtime agent context.
- Next step:
  - Add provenance/attestation considerations (allowlist image signatures, source-of-truth checks, optional disable flag, hash pinning in pod declaration).

## Alignment notes (what is working well)

- `CLAW_HANDLE_<SERVICE>_<PLATFORM>_*` environment emission is correct for cross-service identity sharing.
- Service-emitted skill extraction and generated fallback behavior match the desired “minimum viable context” model.
- CLAWDAPUS includes handles/surfaces/skills index and gives agents the local topology contract.

