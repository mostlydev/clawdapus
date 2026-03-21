# Hermes First-Class Support

## Why This Exists

The tiverton-house `picoclaw -> hermes` migration draft surfaced a few concerns that should live in Clawdapus, not in a pod-local rollout spec:

1. Hermes exists as a runtime driver, but `claw init` / `claw agent add` still do not accept `--type hermes`.
2. The pod spec proposes a one-off published `ghcr.io/mostlydev/hermes-base` image, which is really a runtime packaging concern.
3. Upstream Hermes messaging/session behavior is configured partly through `~/.hermes/gateway.json`, while the current Clawdapus Hermes driver deliberately materializes only `config.yaml` + `.env`.

This plan tracks the Clawdapus-side work so pods do not each solve it differently.

## Scope

### 1. Make Hermes a first-class CLI/scaffolding target

Update the CLI so Hermes is treated like the other built-in claw types:

- add `hermes` to the accepted claw types in scaffold parsing
- add `hermes` to `claw init` and `claw agent add` prompts/help text
- choose a sane default base image for generated Hermes Clawfiles
- add/update tests covering scaffold output and validation

Candidate files:

- `cmd/claw/scaffold_helpers.go`
- `cmd/claw/init.go`
- `cmd/claw/agent.go`
- existing scaffold/init tests under `cmd/claw/`

### 2. Define the supported Hermes base image story

Pods should not need to invent their own ad hoc “real Hermes” base image and publish flow.

Decide one of:

- publish a Clawdapus-supported Hermes base image to GHCR
- ship a canonical Dockerfile/template in-tree and document the expected publish path
- support a repo-local base-image workflow well enough that pods do not need an external registry dependency for first deploy

Requirements:

- pin upstream Hermes by tag or commit, not `@main`
- document foreground startup semantics clearly
- keep compatibility with driver healthchecks
- document the exact dependency surface needed for cron + messaging

Likely touch points:

- `README.md`
- `examples/`
- release/docs pipeline for the published base, if we own one

### 3. Add a path for Hermes gateway policy

If operators need stable messaging-session policy, home-channel defaults, or other gateway-only settings, `config.yaml` + `.env` is not enough by itself.

Investigate a minimal Clawdapus-managed path for Hermes gateway config:

- either materialize `gateway.json` directly
- or define a narrower Clawdapus abstraction that renders the needed gateway fields

Initial target:

- session reset policy for messaging platforms
- settings that materially affect cron/proactive message delivery

Constraints:

- do not regress the current simpler Hermes v1 flow for pods that do not need gateway-specific tuning
- keep the config surface explicit; avoid magic derivation from partial handle data

Candidate files:

- `internal/driver/hermes/driver.go`
- `internal/driver/hermes/config.go`
- follow-on tests in `internal/driver/hermes/`

## Non-Goals

- Reworking the tiverton-house pod spec directly here
- Re-adding runner-specific fallback model semantics that Hermes itself does not support today
- Broad Clawdapus handle-model redesign beyond what Hermes gateway settings require

## Validation

- `go test ./...`
- Hermes-specific unit tests in `internal/driver/hermes/...`
- scaffold/init tests proving `--type hermes` works end-to-end
- spike coverage using a real Hermes-compatible image path rather than only the current stub
