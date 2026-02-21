# Vertical Spike 1: Clawfile Parse + Build (OpenClaw)

## Outcome

Status: `completed` on 2026-02-20.

This spike proved the core compilation loop:

1. Parse Claw directives from Dockerfile-compatible source using BuildKit AST.
2. Emit deterministic standard Dockerfile output with Claw metadata/infrastructure primitives.
3. Build image with Docker.
4. Re-read `claw.*` metadata from image labels via `claw inspect`.

## What Was Delivered

Implemented files and packages:

- CLI: `cmd/claw/main.go`, `cmd/claw/root.go`, `cmd/claw/doctor.go`, `cmd/claw/build.go`, `cmd/claw/inspect.go`
- Parser/emitter: `internal/clawfile/*`
- Build pipeline: `internal/build/*`
- Image metadata inspection: `internal/inspect/*`
- Doctor checks with integration tag split: `internal/doctor/*`
- Example workload: `examples/openclaw/*`

Key behaviors now in place:

- Unknown `CLAW_*` directives fail with line-numbered parse errors.
- Duplicate singleton directives fail fast.
- Emission is deterministic (sorted map-derived labels).
- Injection point is runtime stage (after last `FROM`).
- `claw build` generates once then builds once (no duplicate generation path).
- Docker-coupled checks are isolated behind `//go:build integration`.

## Verification Completed

Executed successfully:

```bash
go test ./...
go test -tags=integration ./...
go build -o bin/claw ./cmd/claw
./bin/claw build -t claw-openclaw-example examples/openclaw
./bin/claw inspect claw-openclaw-example
```

Smoke output confirms labels round-trip for:

- `claw.type`
- `claw.agent.file`
- `claw.model.*`
- `claw.surface.*`
- `claw.privilege.*`

## Deferred From Spike 1

- Runtime enforcement framework
- `claw-pod.yml` parsing and compose generation
- Persona materialization
- Driver-managed policy/runtime lifecycle

---

## Phase 2 Plan: Runtime Driver + Pod Orchestration

## Goals

1. Convert parsed Claw intent into enforceable runtime state.
2. Introduce pod-level orchestration (`docker-claw up/down/ps/logs`) with generated compose.
3. Enforce contract authority (`AGENT` exists and mounts read-only) as fail-closed.
4. Support Phase 2 pod runtime scope: `count` scaling with stable ordinals, volume surfaces, and network restriction enforcement.
5. Enforce fail-closed checks both pre-apply and post-apply.

## Phase 2 Constraints

- Do not rely on repeated `openclaw config set` shellouts for runtime injection.
- Prefer Go-native JSON5 read/patch/write for OpenClaw config synthesis.
- Keep output deterministic and auditable (`compose.generated.yml`, generated config files).

## Workstream A: Driver Framework

Deliverables:

- `internal/driver/registry.go`
- `internal/driver/types.go`
- `internal/driver/openclaw/driver.go`

Driver contract:

```go
type Driver interface {
    Validate(*ResolvedClaw) error
    Materialize(*ResolvedClaw, MaterializeOpts) (*MaterializeResult, error)
    PostApply(*ResolvedClaw, PostApplyOpts) error
    HealthProbe(ContainerRef) (*Health, error)
}
```

Responsibilities:

- Resolve `CLAW_TYPE` to driver implementation.
- Validate required directives and runner prerequisites before startup (fail-closed preflight).
- Materialize runtime artifacts into deterministic host paths.
- Verify enforcement after compose apply (fail-closed post-apply).

Acceptance:

- Unknown `CLAW_TYPE` fails early.
- Driver selection and validation unit-tested without Docker.
- `PostApply` hook is invoked and failure aborts successful `claw up` completion.

## Workstream B: OpenClaw Runtime Config Materialization

Deliverables:

- `internal/driver/openclaw/config_patch.go`
- `internal/driver/openclaw/config_patch_test.go`
- `internal/driver/openclaw/materialize.go`

Implementation detail:

1. Parse baseline OpenClaw config (JSON5-aware parser).
2. Apply patches from `MODEL`, `CONFIGURE`, `SURFACE`, and relevant defaults.
3. Emit generated config file under managed runtime directory.
4. Mount emitted file read-only into target container.

Rules:

- Preserve unknown fields while patching known paths.
- Deterministic field ordering or stable serialization strategy.
- Never use `cmd.CombinedOutput()` for JSON responses in this path.

Acceptance:

- Golden tests for patch application.
- Repeated materialization with same input is byte-identical.

## Workstream C: Contract Enforcement (`AGENT`)

Deliverables:

- `internal/runtime/contract.go`
- `internal/runtime/contract_test.go`

Rules:

- Missing AGENT source file is hard error (`fail closed`).
- Contract is mounted read-only.
- Container startup is blocked if contract mount cannot be applied.

Acceptance:

- Unit tests for path resolution and fail-closed behavior.
- Integration smoke validates read-only mount semantics.

## Workstream D: Pod Parser + Compose Generator

Deliverables:

- `internal/pod/parser.go`
- `internal/pod/compose_emit.go`
- `internal/pod/compose_emit_test.go`
- `cmd/docker-claw/up.go`, `cmd/docker-claw/down.go`, `cmd/docker-claw/ps.go`, `cmd/docker-claw/logs.go`

Behavior:

- Parse `claw-pod.yml` + `x-claw` extensions.
- Expand each Claw service into generated compose services + mounts + labels.
- Implement `count: N` scaling with stable zero-indexed ordinal identities (`service-0..N-1`).
- Scale-down/removal targets highest ordinals first to preserve stable identities.
- Translate `volume://` surfaces into top-level compose `volumes:` and service mounts with explicit `:ro`/`:rw`.
- Generate and enforce compose-level network restrictions from pod policy (`networks:`), fail-closed on invalid policy.
- Write deterministic `compose.generated.yml`.
- Execute lifecycle via `docker compose -f compose.generated.yml ...`, surfaced through `docker-claw ...` (with optional `docker claw ...` when installed as a Docker CLI plugin).

Acceptance:

- Golden tests for generated compose.
- Golden tests cover volume surfaces, access modes, and network restriction emission.
- Tests cover stable ordinal naming and highest-ordinal-first scale-down behavior.
- `docker-claw up` -> `docker-claw ps` -> `docker-claw down` smoke flow on sample pod.

## Workstream E: Health and Diagnostics

Deliverables:

- `internal/health/openclaw.go`
- `cmd/claw/doctor.go` refinements for structured diagnostics

Behavior:

- For JSON health probes, keep stdout/stderr separated.
- When parsing mixed streams from container logs, detect first JSON object boundary before decode.

Acceptance:

- Unit tests for noisy-output parsing.
- Integration check against intentionally noisy OpenClaw container.

---

## Phase 2 Exit Criteria

1. `docker-claw up` can launch an OpenClaw pod from `claw-pod.yml` with generated compose.
2. Driver enforces runtime config from Clawfile directives via generated JSON5 config.
3. AGENT contract is required and mounted read-only.
4. Driver preflight and post-apply checks both run fail-closed.
5. `count` scaling produces stable ordinals and deterministic compose output.
6. Volume surfaces are generated as compose volumes with explicit access modes.
7. Network restrictions are emitted/enforced at compose level.
8. `docker-claw ps` and `docker-claw logs` provide operational visibility.
9. Unit tests pass by default; integration tests pass with Docker enabled.

---

## Spike 1 Retrospective Notes

### What worked

- **BuildKit parser was the right bet.** Unknown instructions surface as lowercased AST nodes — zero custom lexing needed. Claw directives parse cleanly alongside standard Dockerfile.
- **`Runner` injection pattern** decoupled doctor tests from Docker completely. Reuse this for any future code that shells out.
- **`make([]T, 0)` nil-avoidance** prevented subtle JSON/test bugs. Adopt as project convention.
- **Determinism-by-default** (sorted map keys + byte-equality test) caught the problem structurally, not by inspection.
- **Last-FROM injection** was the correct fix for multi-stage builds. The original plan said "first FROM" — would have broken any multi-stage Clawfile.

### What to carry into Phase 2

- **Stderr separation is critical.** `CombinedOutput()` is fine for version strings (doctor), but the OpenClaw driver's health probe MUST use `Output()` (stdout only). Gemini's `openclaw health --json` stderr bleed finding applies to any JSON response from a runner CLI.
- **Go-native JSON5 patching, not shellouts.** The spike used `printf` to generate configure scripts (correct for build-time). Phase 2 must use a JSON5 library for host-side config assembly. Do not repeat the `openclaw config set` mistake — splash banners, doctor checks, `.bak` files, Node.js cold start per invocation.
- **Schema tracking.** OpenClaw's Zod validation rejects unknown keys at startup. The driver must inject only schema-valid keys. This is good (fail-closed) but means the driver needs a schema awareness mechanism that can evolve with OpenClaw releases.
- **Volume surfaces belong in Phase 2** per the architecture plan (not Phase 3). The spike plan's Phase 2 outline omits them — add workstream or fold into Workstream D.
- **`count` scaling and ordinal identity** are architecture Phase 2 scope. Add to Workstream D.
- **Preflight + post-apply verification** needs explicit coverage. The architecture plan requires fail-closed verification at both stages. The driver interface has `Validate` but no `PostApply` — add it.
- **Network restriction** at compose level is architecture Phase 2 scope. Add to Workstream D.

### Gaps between spike Phase 2 outline and architecture Phase 2

| Architecture requirement | Spike Phase 2 coverage | Action needed |
|--------------------------|----------------------|---------------|
| Volume surfaces (parse, generate, ACL) | Missing | Add workstream |
| `count` scaling + ordinal identity | Missing | Add to Workstream D |
| Network restriction enforcement | Missing | Add to Workstream D |
| Post-apply verification hook | Implicit in driver interface | Make explicit in driver contract |
| Preflight validation (fail-closed) | Covered in Workstream A | OK |
| Go-native JSON5 patching | Covered in Workstream B | OK |
| Contract fail-closed | Covered in Workstream C | OK |
| Pod parser + compose emit | Covered in Workstream D | Extend scope |
| Health stderr separation | Covered in Workstream E | OK |
