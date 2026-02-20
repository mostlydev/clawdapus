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
2. Introduce pod-level orchestration (`claw up/down/ps/logs`) with generated compose.
3. Enforce contract authority (`AGENT` exists and mounts read-only) as fail-closed.

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
    HealthProbe(ContainerRef) (*Health, error)
}
```

Responsibilities:

- Resolve `CLAW_TYPE` to driver implementation.
- Validate required directives and runner prerequisites before startup.
- Materialize runtime artifacts into deterministic host paths.

Acceptance:

- Unknown `CLAW_TYPE` fails early.
- Driver selection and validation unit-tested without Docker.

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
- `cmd/claw/up.go`, `cmd/claw/down.go`, `cmd/claw/ps.go`, `cmd/claw/logs.go`

Behavior:

- Parse `claw-pod.yml` + `x-claw` extensions.
- Expand each Claw service into generated compose services + mounts + labels.
- Write deterministic `compose.generated.yml`.
- Execute lifecycle via `docker compose -f compose.generated.yml ...`.

Acceptance:

- Golden tests for generated compose.
- `claw up` -> `claw ps` -> `claw down` smoke flow on sample pod.

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

1. `claw up` can launch an OpenClaw pod from `claw-pod.yml` with generated compose.
2. Driver enforces runtime config from Clawfile directives via generated JSON5 config.
3. AGENT contract is required and mounted read-only.
4. `claw ps` and `claw logs` provide operational visibility.
5. Unit tests pass by default; integration tests pass with Docker enabled.
