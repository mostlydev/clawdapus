# Fleet Governance ADRs Implementation Plan

## Goal

Ship the first working governance vertical slice implied by ADR-012 through ADR-015:

- a pod can declare a Master Claw via `x-claw.master`
- claws can declare `x-claw.feeds`
- Clawdapus writes per-agent feed and service-auth runtime files
- `claw audit` normalizes cllama stdout into a stable operator-facing read model
- `claw-api` exposes an authenticated, scoped, read-only fleet control surface
- cllama injects feed content into both OpenAI-style and Anthropic-style requests
- the `trading-desk` example demonstrates the full loop: push anomalies, pull details

This plan is intentionally read-plane-first. It does not try to ship write-plane governance, runtime override files, scaling, or federation in the first slice.

## Guiding Decisions

- Build bottom-up from substrate to example.
- Preserve current authority boundaries.
  - Docker SDK is fine for reads.
  - `docker compose` remains the sole lifecycle authority.
  - Write-plane actions stay deferred.
- Keep runtime state inspectable under `.claw-runtime/`.
- Prefer existing runtime-injected infrastructure patterns over user-authored special services.
- Treat `cllama/` as a git submodule in this workspace, not as a vague future dependency.
- Pick one transport for v1.
  - `claw-api` should be HTTP in v1.
  - Service-surface docs can describe the HTTP interface.
  - MCP can come later if it proves useful.

## Current Repo Reality

The ADR stack is directionally aligned now, but the repo is still missing the core substrate:

- `internal/pod/parser.go` deserializes raw `x-claw.master`, but `internal/pod/types.go` has no `Pod.Master`, so the value is dropped.
- `x-claw.feeds` is not parsed anywhere yet.
- `internal/cllama/context.go` only writes `AGENTS.md`, `CLAWDAPUS.md`, and `metadata.json`.
- `cllama/internal/agentctx/agentctx.go` only loads those same files.
- `cllama/internal/proxy/handler.go` forwards request bodies unchanged. Feed injection requires new mutation logic in both `handleOpenAI` and `handleAnthropicMessages`.
- cllama telemetry exists, but nothing in the main repo normalizes or consumes it yet.
- There is no `claw audit`, no `claw-api`, and no runtime injection path for governance infrastructure.

## Delivery Boundaries

**Main repo owns:**

- pod parsing and runtime generation
- normalized telemetry ingestion and the `claw audit` CLI
- the `claw-api` binary, image, auth model, and runtime injection
- example pods, pod manifest generation, and clawdash visibility

**`cllama/` submodule owns:**

- loading optional feed and service-auth runtime files
- fetching feeds and caching them by TTL
- prompt decoration for both OpenAI and Anthropic request shapes

Both repos are in this workspace. They should be planned together but landed in separable commits.

## Dependency Order

```text
Phase 1  Parser + runtime context substrate      (main repo)
Phase 2  Telemetry normalization + claw audit    (main repo)
Phase 3  claw-api auth + read plane              (main repo)
Phase 4  cllama feed injection                   (cllama submodule)
Phase 5  Runtime injection + example pod         (main repo)
```

Practical parallelism:

- Phase 1 and Phase 2 can start in parallel.
- Phase 3 depends on Phase 2 for normalized telemetry reuse.
- Phase 4 depends on Phase 1 for feed manifests and on Phase 3 for authenticated `claw-api` fetches.
- Phase 5 depends on Phase 3 and Phase 4.

## Phase 1: Parser And Runtime Context Substrate

**Outcome**

- `x-claw.master` survives parsing and can drive later runtime injection.
- `x-claw.feeds` is part of the typed pod model.
- `claw up` writes per-agent `feeds.json` into the existing cllama context tree.

**Likely files**

- `internal/pod/types.go`
- `internal/pod/parser.go`
- `internal/pod/feed.go` (new)
- `internal/pod/parser_test.go`
- `internal/cllama/context.go`
- `internal/cllama/context_test.go`
- `cmd/claw/compose_up.go`

**Work**

- Add `Master string` to `pod.Pod`.
- Validate that `x-claw.master`, when present, points at an existing claw service.
- Add a typed `FeedEntry` to `pod.ClawBlock`.
- Parse and validate `x-claw.feeds`.
  - require `source`
  - require `path`
  - require positive `ttl`
  - derive `name` when omitted
- Keep feeds declarative. Parsing should not attempt service discovery over the network.
- Extend `cllama.AgentContextInput` with feed manifest data.
- Write `.claw-runtime/context/<agent-id>/feeds.json` from `internal/cllama/context.go`.
- Wire feed manifests through the existing context generation loop in `cmd/claw/compose_up.go`.

**Notes**

- The correct manifest path is under the existing per-agent context tree:
  - host: `.claw-runtime/context/<agent-id>/feeds.json`
  - mounted in cllama: `/claw/context/<agent-id>/feeds.json`
- This phase should not yet invent a generic driver-level feed hook. That is explicitly deferred by ADR-013.

**Exit criteria**

- parser tests cover `master` success, empty default, and invalid references
- parser tests cover feed parsing and validation
- context tests confirm `feeds.json` is written only when feeds exist
- `cmd/claw/compose_up.go` still generates valid cllama context for existing cases

## Phase 2: Telemetry Normalization And `claw audit`

**Outcome**

- The main repo has one normalization boundary for cllama telemetry.
- Operators can inspect normalized fleet telemetry with `claw audit`.
- Later `claw-api` read endpoints reuse the same package rather than reparsing raw logs.

**Likely files**

- `internal/audit/event.go` (new)
- `internal/audit/normalize.go` (new)
- `internal/audit/collect.go` or similar (new)
- `internal/audit/*_test.go`
- `cmd/claw/compose_audit.go` or `cmd/claw/audit.go` (new)

**Work**

- Introduce a normalized `Event` type matching ADR-014.
- Normalize these raw differences:
  - `ts` vs `timestamp`
  - `intervention` vs `intervention_reason`
  - `intervention: null` meaning “no intervention”
  - sparse token/cost/status fields
  - `error` as a first-class event
- Treat `drift_score` as optional extension data, not as a required event.
- Keep Docker collection separate from normalization logic.
  - normalizer should work over `io.Reader` or raw lines
  - Docker access should be a thin collection layer
- Build `claw audit` on top of normalized events.
- Support filtering by `--claw`, `--since`, and `--type`.
- Provide summaries that matter for governance:
  - request volume
  - error counts/rates
  - intervention counts
  - model usage
  - token and cost totals when present

**Notes**

- Reuse existing Docker SDK patterns from `cmd/claw/compose_health.go` and `cmd/clawdash/handler.go`.
- Do not bake Docker-specific concerns into the normalizer.
- If a scanner is used for log lines, increase its buffer or avoid the default 64 KiB limit.

**Exit criteria**

- unit tests cover each raw event family emitted today by cllama
- malformed lines are skipped or annotated without collapsing the whole read
- `claw audit` can summarize live proxy logs from a running pod

## Phase 3: `claw-api` Auth, Read Plane, And Credential Projection

**Outcome**

- `claw-api` exists as a real read-only fleet service.
- auth is explicit and deny-by-default
- the service has an inspectable principal source of truth
- the same principal can later be projected into cllama for feed fetches

**Likely files**

- `internal/clawapi/principal.go` (new)
- `internal/clawapi/scope.go` (new)
- `internal/clawapi/*_test.go`
- `cmd/claw-api/main.go` (new)
- `cmd/claw-api/handler.go` (new)
- `cmd/claw-api/*_test.go`
- `dockerfiles/claw-api/Dockerfile` (new)
- `internal/pod/types.go`
- `internal/pod/compose_emit.go`
- `cmd/claw/compose_up.go`
- `cmd/claw/compose_manifest.go`
- `internal/cllama/context.go`

**Work**

- Implement the ADR-015 principal model:
  - explicit principal identity
  - deny-by-default verb/target checks
  - filtered read responses
  - exact validation for later write-path reuse
- Use HTTP for v1.
- Build `claw-api` read operations:
  - `fleet.status`
  - `fleet.logs`
  - `fleet.query_metrics`
  - `GET /fleet/alerts`
- Read from Docker SDK and the shared audit package.
- Keep reads Docker-SDK-based. Do not shell out to `docker compose` for the read plane.
- Add JSON audit logging for every `claw-api` decision:
  - principal
  - verb
  - target
  - allow/deny
  - filtered scope where applicable

**Runtime state**

- Add a runtime-only config for `claw-api`, analogous to `pod.ClawdashConfig`.
  - `pod.ClawAPIConfig` is the right shape for this repo.
- Store the service’s principal source of truth in an inspectable runtime file, for example:
  - `.claw-runtime/claw-api/principals.json`
- Project per-agent service credentials into:
  - `.claw-runtime/context/<agent-id>/service-auth/claw-api.json`

That projection should be generated here, once the principal model is real, not earlier as placeholder data.

**Repo-alignment choices**

- Put the image build under `dockerfiles/claw-api/Dockerfile`, matching `dockerfiles/clawdash/Dockerfile`.
- Extend `ensureInfraImages(...)` so `claw-api` is treated like other injected infrastructure images.
- Expose `claw-api` to agents through the existing service-surface documentation path.
  - V1 can use a generated manual or service-skill fallback.
  - Do not block on a custom `claw.skill.emit` path for the first slice.

**Exit criteria**

- `claw-api` can answer scoped read requests against a live pod
- principal config is generated under `.claw-runtime/claw-api/`
- per-agent `service-auth/claw-api.json` files are generated for authorized callers
- no code path reuses cllama’s `<agent-id>:<secret>` bearer token as a `claw-api` credential

## Phase 4: cllama Feed Injection

**Outcome**

- cllama can read feed manifests and service-auth files
- cllama fetches feed content with TTL caching
- feed content is injected into both supported request shapes

**Likely files in `cllama/`**

- `cllama/internal/agentctx/agentctx.go`
- `cllama/internal/feeds/loader.go` (new)
- `cllama/internal/feeds/cache.go` (new)
- `cllama/internal/feeds/*_test.go`
- `cllama/internal/proxy/handler.go`
- `cllama/internal/proxy/*_test.go`

**Work**

- Extend `agentctx.AgentContext` to load optional:
  - `feeds.json`
  - `service-auth/<service>.json`
- Implement feed loading and TTL caching.
- Fetch feeds with:
  - `X-Claw-ID`
  - `X-Claw-Pod`
  - service Authorization data from `service-auth/claw-api.json` when required
- Add explicit mutation paths for:
  - OpenAI-compatible chat/completions payloads
  - Anthropic `/v1/messages` payloads
- Preserve existing provider-routing logic while injecting context.
- Enforce:
  - per-feed size cap
  - total injected-feed size cap
  - truncation markers
  - stale-data or unavailable-feed markers on fetch failure

**Notes**

- This is the main implementation work for ADR-013. It is not a small afterthought.
- Tests must assert both request families, because the injection seam is different in each.

**Exit criteria**

- cllama unit tests prove feed injection for OpenAI and Anthropic paths
- feed fetches can authenticate using projected `claw-api` credentials
- feed failures degrade cleanly instead of breaking all inference

## Phase 5: Runtime Injection And Example Pod

**Outcome**

- `x-claw.master` has a visible runtime effect
- `claw-api` is injected automatically when a pod declares a Master Claw
- the `trading-desk` example proves the end-to-end loop

**Likely files**

- `internal/pod/types.go`
- `internal/pod/compose_emit.go`
- `internal/pod/compose_emit_*_test.go`
- `cmd/claw/compose_up.go`
- `cmd/claw/compose_manifest.go`
- `examples/trading-desk/claw-pod.yml`
- `examples/trading-desk/agents/OCTOPUS.md`

**Work**

- Inject `claw-api` as runtime infrastructure when `p.Master != ""`.
- Follow the same pattern already used for cllama proxies and clawdash:
  - runtime-only config on `pod.Pod`
  - compose emission in `internal/pod/compose_emit.go`
  - image assurance in `ensureInfraImages(...)`
- Mount the minimum required runtime state into `claw-api`:
  - Docker socket
  - generated pod manifest
  - `claw-api` principal config
  - any audit/runtime directories needed for reads
- Include injected `claw-api` in the generated pod manifest so clawdash/topology views stay truthful.
- Generate or inject the `service://claw-api` manual so the Master Claw gets a usable service surface.
- Update `trading-desk`:
  - add `x-claw.master: octopus`
  - add `octopus` as a normal claw service
  - declare the `/fleet/alerts` feed
  - declare `service://claw-api`
  - add `invoke` schedule for periodic fleet review

**Target end state**

The YAML declares the Master Claw and its feed usage. It should not need a manually authored `claw-api` service block in steady state.

**Exit criteria**

- `claw up` with `x-claw.master` injects `claw-api` automatically
- generated runtime state includes feed manifests and `claw-api` credentials for the Master Claw
- generated compose includes `claw-api` and clawdash can see it
- the `trading-desk` example is the first working demonstration pod

## Suggested Acceptance Checks

Use these after the vertical slice lands:

- `go test ./...` in the main repo
- `(cd cllama && go test ./...)` in the submodule
- bring up a pod with cllama enabled and confirm:
  - `.claw-runtime/context/<agent-id>/feeds.json` exists
  - `.claw-runtime/context/<agent-id>/service-auth/claw-api.json` exists
  - `claw audit --since 1h` returns normalized events
  - `claw-api` serves scoped `fleet.status`, `fleet.logs`, `fleet.query_metrics`, and `/fleet/alerts`
  - cllama tests prove feed blocks are injected into outgoing requests

An end-to-end spike is useful after the unit seams are in place, but it should not be the first validation step.

## Explicitly Deferred

These stay out of the first slice:

- write-plane `claw-api` operations
- budget/model runtime override files
- `fleet.scale`
- hub-and-spoke federation
- service-advertised feed discovery
- runtime feed subscriptions
- generic non-cllama feed parity
- event-driven trigger routing

## Implementation Summary

If this plan is followed cleanly, the repo ends up with:

1. one typed governance declaration path in pod YAML
2. one normalized telemetry substrate
3. one authenticated read-plane service
4. one inspectable runtime state layout under `.claw-runtime/`
5. one concrete example proving the Master Claw pattern without inventing a new runner or authority model
