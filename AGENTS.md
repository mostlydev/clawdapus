# AGENTS.md

This is the canonical repo-level agent guide for Clawdapus. `CLAUDE.md` should be a symlink to this file.

## What This Repo Is

Clawdapus is infrastructure-layer governance for AI agent containers. The `claw` CLI is a Go binary that treats agents as untrusted workloads: reproducible, inspectable, diffable, and killable.

Core docs:

- `MANIFESTO.md` — project vision
- `README.md` — current user-facing CLI and examples
- `docs/CLLAMA_SPEC.md` — cllama proxy contract
- `docs/decisions/` — ADRs
- `docs/plans/` — implementation plans and historical design notes

## Trust Order

There is some doc drift in the repo. When sources disagree, trust them in this order:

1. Current code in `cmd/claw/` and `internal/`
2. Current tests
3. Examples under `examples/`
4. ADRs in `docs/decisions/`
5. Plans/reviews in `docs/plans/` and `docs/reviews/`

Example: `TESTING.md` still talks about `e2e`, but the build tags currently in-tree are `integration` and `spike`.

## Actual CLI Surface

Current top-level commands are:

- `claw build`
- `claw up`
- `claw down`
- `claw ps`
- `claw logs`
- `claw health`
- `claw inspect`
- `claw doctor`
- `claw init`
- `claw agent add`

Useful current behavior:

- `claw up` writes `compose.generated.yml` next to the pod file.
- If the pod contains managed `x-claw` services, `claw up` currently requires detached mode: use `claw up -d`.
- `docker compose` is the sole lifecycle writer. Docker SDK usage is read-only.

## Start Here

If you are debugging or changing behavior, these are the main entry points:

- `cmd/claw/compose_up.go` — main runtime orchestration path
- `internal/pod/` — `claw-pod.yml` parsing and compose emission
- `internal/clawfile/` — Clawfile parsing and Dockerfile emission
- `internal/driver/` — driver registry and per-runner implementations
- `internal/cllama/` — cllama context generation and wiring helpers
- `internal/inspect/` — claw label parsing from images
- `internal/persona/` — persona materialization
- `cllama/` — proxy implementation source

The best end-to-end fixtures are:

- `examples/quickstart/`
- `examples/trading-desk/`
- `examples/rollcall/`

## Current Driver Set

Driver directories currently in-tree:

- `internal/driver/openclaw`
- `internal/driver/hermes`
- `internal/driver/nanobot`
- `internal/driver/nanoclaw`
- `internal/driver/picoclaw`
- `internal/driver/microclaw`
- `internal/driver/nullclaw`
- `internal/driver/shared`

Do not assume older docs mentioning only a subset are current.

## Runtime Model That Exists Today

- A `Clawfile` is parsed and emitted into a standard Dockerfile using image labels for Clawdapus directives.
- A `claw-pod.yml` is parsed from service-level `x-claw` blocks. Current parsed fields include `agent`, `persona`, `cllama`, `cllama-env`, `count`, `handles`, `include`, `surfaces`, `skills`, and `invoke`.
- `count > 1` expands into ordinal-named compose services like `svc-0`, `svc-1`, etc.
- cllama wiring is resolved before materialization in a two-pass `claw up` flow.
- Generated runtime artifacts like `AGENTS.generated.md`, `CLAWDAPUS.md`, cllama context files, and runner configs are produced under runtime dirs during `claw up`.

## Repo-Specific Gotchas

- Managed services require `claw up -d` because post-apply verification is fail-closed.
- Multi-proxy cllama is represented in the data model but runtime currently fails fast if more than one proxy type is declared.
- Provider API keys for cllama-managed services belong in `x-claw.cllama-env`, not regular agent `environment:` blocks.
- For cllama-enabled `count > 1` services, bearer tokens and context are per ordinal, not per base service.
- `compose.generated.yml` and `Dockerfile.generated` are generated artifacts. Inspect them, but do not hand-edit them as source.
- OpenClaw config and cron paths are mounted as directories, not single files, because the runtime performs atomic rewrites.
- OpenClaw `openclaw health --json` can emit noise to stderr. The repo handles it as a stdout-first parse path.

## Current Behavior Worth Knowing

- `HANDLE` and channel `SURFACE` are different layers in current code. `HANDLE` is identity/bootstrap data; channel `SURFACE` is routing policy. If both are present, surface-level routing config is applied after handle defaults.
- Map-form channel surfaces are still real code paths at the pod layer; `ClawBlock.Surfaces` is parsed into `[]driver.ResolvedSurface`, not raw strings.
- Channel/service surface skills are generated and referenced through `CLAWDAPUS.md` plus mounted skill files.
- OpenClaw cllama wiring does not write to `agents.defaults.model.baseURL/apiKey`; the schema-valid rewrite path is `models.providers.<provider>.{baseUrl,apiKey,api,models}`.
- `PERSONA` is implemented as runtime materialization. Local refs are copied with traversal/symlink hardening; non-local refs are pulled as OCI artifacts. `CLAW_PERSONA_DIR` is only set when a persona is present.
- `x-claw.include` contract composition is live. `enforce` and `guide` content is inlined into generated `AGENTS.md`; `reference` content is mounted as read-only skill material.

## Testing Reality

Current test layers:

- Unit: `go test ./...`
- Vet: `go vet ./...`
- Integration-tagged tests: `go test -tags integration ./...`
- Live/Docker spike tests: `go test -tags spike -run TestSpikeRollCall ./cmd/claw/...` or `go test -tags spike -run TestSpikeComposeUp ./cmd/claw/...`

Build tags currently present in the repo:

- `integration`
- `spike`

The spike tests are the heavy end-to-end path. They build images, run Docker, and in some cases require real Discord/provider credentials.

## Practical Guidance For Agents

- Prefer reading the code paths above before relying on plan documents.
- When changing runtime behavior, update tests in the same area if they exist.
- If a behavior is reflected in generated artifacts, inspect both the source logic and the generated output expectations in tests.
- Be careful with the working tree: this repo is often mid-change, and unrelated files may already be modified.
