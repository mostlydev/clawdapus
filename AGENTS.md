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

## Compilation Principles

`claw up` is a compiler. These principles govern the pipeline and must not be violated by new features:

1. **Compile-time, not runtime.** All wiring — feeds, skills, identity, surfaces — is resolved during `claw up`. No runtime self-registration. The generated compose file is the single source of truth.
2. **Provider-owns, consumer-subscribes.** Services declare what they offer (feeds, endpoints, auth). Agents subscribe by name. Consumers never need to know a service's URL path or TTL.
3. **Pod-level defaults, service-level overrides.** Shared config is declared once at pod level. Services inherit by default, override or extend (`...` spread) as needed.
4. **One canonical descriptor.** A service's capabilities are declared once (via `claw.describe` in the image) and projected into whatever artifacts need them. No manual duplication across pod YAML, skills, and CLAWDAPUS.md.
5. **Services self-describe.** Images carry structured descriptors. `claw up` extracts and compiles them. Framework adapters (RailsTrail, etc.) generate descriptors from code introspection.

See ADR-017 for the full design and `docs/plans/2026-03-22-pod-defaults-and-service-self-description.md` for implementation details.

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
- `claw compose`

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
- `x-claw.master` is parsed in `rawPodClaw` but NOT propagated into `Pod` — the field is silently dropped. Any feature depending on it needs the parser→Pod→compose_up chain wired first.
- cllama wiring is resolved before materialization in a two-pass `claw up` flow.
- Generated runtime artifacts like `AGENTS.generated.md`, `CLAWDAPUS.md`, cllama context files, and runner configs are produced under runtime dirs during `claw up`.
- cllama context layout: host-side at `.claw-runtime/context/<agent-id>/` containing `AGENTS.md`, `CLAWDAPUS.md`, `metadata.json`. Mounted into cllama container at `/claw/context/<agent-id>/`. The `context/` directory segment is required.

## Repo-Specific Gotchas

- Bug fixes in one driver often apply to all 7. When fixing driver behavior (permissions, config defaults, env vars), check all drivers in `internal/driver/*/driver.go` and `config.go` — not just the one mentioned in the issue.
- Runtime directories created by `Materialize()` use `0o777` (not `0o700`) so container users with different uids can write. Do not regress this.
- All drivers set `mention_only` (or equivalent like `requireMention`, `DISCORD_REQUIRE_MENTION`) for Discord channels. Without this, multi-agent pods enter feedback loops.
- All drivers explicitly set `HOME` in the container env map to match their config mount path. Container base images may run as root or a different user than expected.
- `cllama/` is a git submodule pointing to a private SSH repo. Fresh `git clone` leaves it empty. Infra images (cllama, clawdash) are published to ghcr.io as public packages to avoid this for end users.
- `ensureImage()` in `compose_up.go` has a 3-step fallback: local image → `docker pull` → local Dockerfile build → git URL build. All steps must be considered when debugging image failures.
- Managed services require `claw up -d` because post-apply verification is fail-closed.
- Multi-proxy cllama is represented in the data model but runtime currently fails fast if more than one proxy type is declared.
- cllama proxy handler (`cllama/internal/proxy/handler.go`) is pure passthrough: it rewrites the `model` field and forwards. It does NOT touch the `messages` array. No prompt decoration, no system message injection, no middleware hooks exist. Two distinct code paths handle OpenAI format (`messages[]`) vs Anthropic format (top-level `system` field).
- cllama context mount (`agentctx`) currently holds only `AgentsMD`, `ClawdapusMD`, and `Metadata` (for bearer token auth). No outbound service credentials, no feed manifests, no decoration config.
- Provider API keys for cllama-managed services belong in `x-claw.cllama-env`, not regular agent `environment:` blocks.
- For cllama-enabled `count > 1` services, bearer tokens and context are per ordinal, not per base service.
- `compose.generated.yml` and `Dockerfile.generated` are generated artifacts. Inspect them, but do not hand-edit them as source.
- OpenClaw config and cron paths are mounted as directories, not single files, because the runtime performs atomic rewrites.
- OpenClaw `openclaw health --json` can emit noise to stderr. The repo handles it as a stdout-first parse path.
- cllama logger (`cllama/internal/logging/logger.go`): field `intervention *string` has no `omitempty` — every event emits `"intervention": null`. Emitted `type` values are `request`, `response`, `error`, `intervention`. No `drift_score` exists in the reference implementation. The spec (`CLLAMA_SPEC.md` §5) omits `error` from its type enum and uses `intervention_reason` where the logger uses `intervention`.
- Hermes SOUL.md identity: The Hermes runner seeds a default SOUL.md ("You are Hermes, made by Nous Research") on first boot via `hermes_cli/default_soul.py`. The Clawdapus Hermes driver writes its own `SOUL.md` to `hermes-home/` during `Materialize` to override this with the agent's contracted identity. Persona SOUL.md takes priority when configured.
- Hermes `.env` passthrough: Container env vars from compose `environment:` are NOT available in Hermes agent tool execution. Only vars in `allowedEnvPassthroughKeys()` (`internal/driver/hermes/config.go`) reach the tool runtime via the `.env` file. New env vars that agents need (e.g. `CLAW_API_TOKEN`) must be added to this list.
- Pod-level `x-claw` only accepts `pod`, `master`, and `handles-defaults`. Provider keys (`cllama-env`) must be service-level — use YAML anchors to stay DRY. See `examples/trading-desk/claw-pod.yml` for the pattern.

## Current Behavior Worth Knowing

- Lifecycle commands (`ps`, `logs`, `health`, `compose`) refuse to run if `claw-pod.yml` is newer than `compose.generated.yml`. `claw down` is exempt — you can always tear down a stale pod. Run `claw up` to regenerate.
- `claw compose <subcommand> [args...]` passes through to `docker compose -f compose.generated.yml`. Use it for any compose operation not covered by the named shortcuts (e.g. `claw compose exec analyst bash`, `claw compose restart cllama-passthrough`).
- `HANDLE` and channel `SURFACE` are different layers in current code. `HANDLE` is identity/bootstrap data; channel `SURFACE` is routing policy. If both are present, surface-level routing config is applied after handle defaults.
- Map-form channel surfaces are still real code paths at the pod layer; `ClawBlock.Surfaces` is parsed into `[]driver.ResolvedSurface`, not raw strings.
- Channel/service surface skills are generated and referenced through `CLAWDAPUS.md` plus mounted skill files.
- OpenClaw cllama wiring does not write to `agents.defaults.model.baseURL/apiKey`; the schema-valid rewrite path is `models.providers.<provider>.{baseUrl,apiKey,api,models}`.
- `PERSONA` is implemented as runtime materialization. Local refs are copied with traversal/symlink hardening; non-local refs are pulled as OCI artifacts. `CLAW_PERSONA_DIR` is only set when a persona is present.
- `x-claw.include` contract composition is live. `enforce` and `guide` content is inlined into generated `AGENTS.md`; `reference` content is mounted as read-only skill material.
- The `Driver` interface (`internal/driver/types.go`) has four methods: `Validate`, `Materialize`, `PostApply`, `HealthProbe`. All run once at deploy/startup. There is no per-turn or per-request hook — any per-request context enrichment must go through cllama or a runner-native mechanism.

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

- `TestSpikeRollCall` is the primary validation for cllama proxy enforcement. Every claw in the rollcall pod must make at least one real LLM call through cllama, and `claw audit` must show telemetry for all of them. If you change cllama wiring, driver materialization, feed injection, or telemetry normalization, this spike test is how you prove it works end-to-end.
- `docs_quickstart_spike_test.go` extracts shell blocks from README docs and runs them in a fresh Docker container. It removes infra images first to exercise the real pull path.

## Practical Guidance For Agents

- Multi-arch cllama image: `docker buildx build --platform linux/amd64,linux/arm64 -t ghcr.io/mostlydev/cllama:latest --push cllama/` using the `multiarch-builder` buildx builder.
- User-defined `healthcheck:` in `claw-pod.yml` takes precedence over driver defaults. The override happens in `compose_emit.go` — check `serviceOut["healthcheck"]` before applying `result.Healthcheck`.
- `Service.Compose` in the pod parser preserves all non-`x-claw` compose keys as a deep-copied `map[string]interface{}`. This is how user healthchecks, depends_on, command, etc. flow through.
- Releases: use `gh release create` with semver tags. cllama has its own tag namespace (e.g. `v0.1.0`) published from the submodule repo. ghcr.io packages default to private; must be set public via GitHub UI after first push.
- `claw-api` image is not published to ghcr.io. The `ensureImage` fallback tries a git URL build which fails because the Docker builder cannot access the private cllama submodule. Build it locally from the repo root: `docker build -t ghcr.io/mostlydev/claw-api:latest -f dockerfiles/claw-api/Dockerfile .`
- `x-claw.master` auto-injects a `claw-api` service into compose. The master claw gets a bearer token baked into its `feeds.json` via the `auth` field, and a `CLAW_API_URL` env var pointing at the in-pod API. Feed auth flows: `claw up` → `feeds.json` with `auth` → cllama fetcher sends `Authorization: Bearer` header → `claw-api` validates via principals.json.
- Alert thresholds are configurable via `CLAW_ALERT_*` env vars on the host. `claw up` forwards them into the auto-injected `claw-api` container.
- Prefer reading the code paths above before relying on plan documents.
- When changing runtime behavior, update tests in the same area if they exist.
- If a behavior is reflected in generated artifacts, inspect both the source logic and the generated output expectations in tests.
- Be careful with the working tree: this repo is often mid-change, and unrelated files may already be modified.
