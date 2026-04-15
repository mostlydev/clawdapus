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
- [GitHub Project Board](https://github.com/users/mostlydev/projects/2) — prioritized roadmap (kanban).
  Workflow:
  - move an issue to `In progress` when actively working on it
  - use `Ready` only for work that is queued up but not yet being worked
  - move an issue to `In review` when implementation, docs, and verification are complete
  - move an issue to `Done` only after review/acceptance, not immediately after coding

## Issue-First Workflow

All planned work is tracked as GitHub issues and prioritized on the [project board](https://github.com/users/mostlydev/projects/2). This applies to agents and human collaborators alike.

**Before starting any non-trivial task:**
1. Find the relevant issue on the project board. If none exists, **create one first** — do not start work without an issue.
2. The issue must contain enough context for any model (or human) to resume cold: motivation, constraints, key design decisions, and open questions. A future collaborator with no prior context should be able to pick it up from the issue alone.
3. Move the issue to **In Progress** before beginning.
4. Do the work on a branch or worktree tied to that issue.
5. Move the issue to **Done** (or close it) when the work lands on master.

**Planning goes into issues.** Design notes, constraints, open questions, and sub-tasks belong in the issue body or comments — not in local plan files, unless the plan is large enough to warrant a `docs/plans/` document (linked from the issue).

**The project board is the single source of truth for priorities.** Column order within a status column reflects relative priority. Work the top item unless there is a blocking reason not to.

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

- `claw pull`
- `claw build`
- `claw up`
- `claw down`
- `claw ps`
- `claw logs`
- `claw health`
- `claw inspect`
- `claw doctor`
- `claw init`
- `claw api` (`schedule` subcommands)
- `claw agent add`
- `claw compose` (use this liberally instead of invoking docker directly)

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
- `internal/describe/` — `claw.describe` service descriptor extraction, parsing, and feed registry
- `internal/persona/` — persona materialization
- `cllama/` — proxy implementation source

The best end-to-end fixtures are:

- `examples/quickstart/`
- `examples/trading-desk/`
- `examples/rollcall/` (this must remain a full spike test)

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
- A `claw-pod.yml` is parsed from service-level `x-claw` blocks. Current parsed fields include `agent`, `persona`, `cllama`, `cllama-env`, `count`, `handles`, `include`, `surfaces`, `skills`, and `invoke`. Pod-level `x-claw` also accepts `sequential-conformance: true`.
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
- `cllama/` is a git submodule pointing to a public SSH repo. Fresh `git clone` leaves it empty. Infra images (cllama, clawdash) are published to ghcr.io as public packages to avoid this for end users. `cllama/` has its own `.git` — changes require two commits: one inside `cllama/` (for feeds/proxy code), then `git add cllama && git commit` in the repo root to update the pointer. Shell working directory can silently drift to `cllama/` between commands — use absolute paths for git operations or verify with `pwd` first.
- `internal/feeds/` and other cllama internals live at `cllama/internal/`, not at the repo root.
- Infra image remediation is explicit now: `claw pull` owns pinned infra freshness, `claw build` owns pod `build:` services, and `claw up` points at one or the other when something is missing. `claw up --fix` is the opt-in auto-remediation path.
- Managed services require `claw up -d` because post-apply verification is fail-closed.
- Multi-proxy cllama is represented in the data model but runtime currently fails fast if more than one proxy type is declared.
- cllama proxy handler (`cllama/internal/proxy/handler.go`) is pure passthrough: it rewrites the `model` field and forwards. It does NOT touch the `messages` array. No prompt decoration, no system message injection, no middleware hooks exist. Two distinct code paths handle OpenAI format (`messages[]`) vs Anthropic format (top-level `system` field).
- cllama providers.json: v1 format uses top-level `api_key`; v2 uses `version: 2` + `keys[]` array with `state`/`secret`/`id`. Old cllama images (pre-v0.2.2) silently load v2 files with empty key pools → every request returns "missing API key for {provider}" 502. Refresh through the four-verb flow: `claw pull`, then `claw up -d` (or `claw up --fix -d`) so Docker recreates the proxy container from the pulled image.
- cllama SSE endpoint for debugging provider key state: `curl -N -H "Authorization: Bearer <ui_token>" http://<host>:<port>/events` — the initial `data:` payload has `providers[name].maskedKey`; empty string means no active key loaded.
- Hermes gateway log (inside container): `/root/.hermes/logs/gateway.log` — shows all received Discord events. Zero entries after startup means the bot is connected but not receiving messages (stale gateway session or missing `MESSAGE_CONTENT` intent).
- cllama context mount (`agentctx`) currently holds only `AgentsMD`, `ClawdapusMD`, and `Metadata` (for bearer token auth). No outbound service credentials, no feed manifests, no decoration config.
- cllama session history: `claw up` bind-mounts `.claw-session-history/` → `/claw/session-history` in the cllama container when cllama is enabled. cllama writes `<dir>/<agent-id>/history.jsonl` — one entry per successful 2xx completion. This is infrastructure-owned (proxy-written). Agents have no read API against it in Phase 1. Distinct from `/claw/memory`, which is runner-owned. Both surfaces are persistent across container restarts AND driver migrations (`CLAW_TYPE` changes).
- Provider API keys for cllama-managed services belong in `x-claw.cllama-env`, not regular agent `environment:` blocks. Native Gemini uses `GEMINI_API_KEY` as the primary env name and also accepts `GOOGLE_API_KEY` as a lower-priority alias.
- For cllama-enabled `count > 1` services, bearer tokens and context are per ordinal, not per base service.
- `compose.generated.yml` and `Dockerfile.generated` are generated artifacts. Inspect them, but do not hand-edit them as source.
- OpenClaw config and cron paths are mounted as directories, not single files, because the runtime performs atomic rewrites.
- OpenClaw `openclaw health --json` can emit noise to stderr. The repo handles it as a stdout-first parse path.
- cllama logger (`cllama/internal/logging/logger.go`): field `intervention *string` has no `omitempty` — every event emits `"intervention": null`. Emitted `type` values are `request`, `response`, `error`, `intervention`. No `drift_score` exists in the reference implementation. The spec (`CLLAMA_SPEC.md` §5) omits `error` from its type enum and uses `intervention_reason` where the logger uses `intervention`.
- Hermes SOUL.md identity: The Hermes runner seeds a default SOUL.md ("You are Hermes, made by Nous Research") on first boot via `hermes_cli/default_soul.py`. The Clawdapus Hermes driver writes its own `SOUL.md` to `hermes-home/` during `Materialize` to override this with the agent's contracted identity. Persona SOUL.md takes priority when configured.
- Hermes `.env` passthrough: Container env vars from compose `environment:` are NOT available in Hermes agent tool execution. Only vars in `allowedEnvPassthroughKeys()` (`internal/driver/hermes/config.go`) reach the tool runtime via the `.env` file. New env vars that agents need (e.g. `CLAW_API_TOKEN`) must be added to this list.
- Pod-level `x-claw` accepts `pod`, `master`, `handles-defaults`, `cllama-defaults`, `models-defaults`, `surfaces-defaults`, `feeds-defaults`, `skills-defaults`, and `principals`. Service-level fields inherit pod defaults; declaring a field replaces defaults unless `...` spread is used to extend. `x-claw.models` merges additively over `models-defaults`, then the merged pod slots overlay image `MODEL` labels at compile time. `models: {}` and `models: null` suppress pod defaults only; they do not remove image-declared slots. See `examples/master-claw/claw-pod.yml` for the general defaults pattern.
- `claw-api: self` on a service's `x-claw` block is an authority signal — it auto-generates a read-only bearer token scoped to that service and injects `CLAW_API_URL` + `CLAW_API_TOKEN`. This is separate from `surfaces: [service://claw-api]` which only grants network reachability. Both are needed for full access.
- claw-api principal scopes have four dimensions: `pods`, `services` (base service names), `claw_ids` (ordinal IDs), `compose_services` (compose service names). `compose_services` is reserved for write-plane ordinal targeting (#78). Write verb constants (`fleet.restart` etc.) are defined but handlers are not yet implemented.
- `sequential-conformance: true` at pod level allows services to share the same Discord handle ID (rollcall pattern). Without it, duplicate handle IDs across services are a hard error. The `count > 1` rejection is still enforced even in sequential-conformance pods.
- `claw-wall` is an infrastructure service auto-injected by `claw up` when any cllama-enabled service has Discord channel IDs. The service name `claw-wall` is reserved — declaring it in `claw-pod.yml` is a hard error. Credentials are passed as `CLAW_WALL_TOKENS=channelID:token,...` pairs (not a map), with exactly one reader token selected per consumed channel. Cursor state is in-memory and resets on wall restart — agents may see some message overlap after redeploy.
- Verb validation is split: unknown verbs in `x-claw.principals` fail hard at pod parse time, while unknown verbs in `principals.json` are dropped with warnings; a principal left with no recognized verbs still loads but authorizes nothing.
- `claw.describe` is the structured service descriptor label. `claw up` extracts `.claw-describe.json` from images (or falls back to the build context filesystem). Descriptors declare feeds, endpoints, auth, and skill file paths. Feed names from descriptors populate a pod-global feed registry; consumers subscribe by name via short-form `feeds: [name]`.
- Feed resolution is two-phase: the parser stores short-form feed names as `FeedEntry{Unresolved: true}` (no source/path validation). `claw up` resolves them after image inspection against the feed registry. Unresolved feeds that aren't in the registry are hard errors.
- Spread expansion (`...`) in pod default lists happens at the raw YAML layer in `expandPodDefaults()`, BEFORE typed parsing (`ParseSurface`, `parseFeeds`). The typed parsers never see the `...` token. This ordering is critical — moving spread expansion after typed parsing will break.

## Current Behavior Worth Knowing

- Lifecycle commands (`ps`, `logs`, `health`, `compose`) refuse to run if `claw-pod.yml` is newer than `compose.generated.yml`. `claw down` is exempt — you can always tear down a stale pod. Run `claw up` to regenerate.
- `claw compose <subcommand> [args...]` passes through to `docker compose -f compose.generated.yml`. Use it for any compose operation not covered by the named shortcuts (e.g. `claw compose exec analyst bash`, `claw compose restart cllama-passthrough`).
- `HANDLE` and channel `SURFACE` are different layers in current code. `HANDLE` is identity/bootstrap data; channel `SURFACE` is routing policy. If both are present, surface-level routing config is applied after handle defaults.
- Map-form channel surfaces are still real code paths at the pod layer; `ClawBlock.Surfaces` is parsed into `[]driver.ResolvedSurface`, not raw strings.
- CLAWDAPUS.md is the single generated context document per agent. Surface metadata (service endpoints, channel config, handles) is inlined into CLAWDAPUS.md sections. Separate `surface-*.md` and `handle-*.md` skill files are no longer generated. Large service skill files (from `claw.describe` or `claw.skill.emit`) are still mounted separately at `/claw/skills/` with a pointer in CLAWDAPUS.md.
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

- Contributor-only release work: cllama, claw-api, clawdash, claw-wall, and hermes-base still use Docker buildx workflows for publishing. Operators should not reach for raw docker commands; they should use `claw pull`, `claw build`, `claw up`, and `claw down`.
- User-defined `healthcheck:` in `claw-pod.yml` takes precedence over driver defaults. The override happens in `compose_emit.go` — check `serviceOut["healthcheck"]` before applying `result.Healthcheck`.
- `Service.Compose` in the pod parser preserves all non-`x-claw` compose keys as a deep-copied `map[string]interface{}`. This is how user healthchecks, depends_on, command, etc. flow through.
- Releases: use `gh release create` with semver tags. cllama has its own tag namespace (e.g. `v0.1.0`) published from the submodule repo. ghcr.io packages default to private; must be set public via GitHub UI after first push. Pre-built `claw` binaries are published via `goreleaser` (`.goreleaser.yml` is in-tree) — do not suggest adding goreleaser, it already exists. `install.sh` downloads the latest release with checksum verification; `claw update` re-runs it.
- Before release work: `git fetch --tags`. Local tag list can lag GitHub releases significantly (e.g. v0.5.0 lived on GitHub while local tags stopped at v0.2.5).
- `cmd/claw/skill_data/SKILL.md` is a tracked mirror of `skills/clawdapus/SKILL.md` for `go:embed` in CI. Keep in sync via `go generate ./cmd/claw/...` (also runs in the goreleaser before hook). Do not add it back to `.gitignore` — `go vet`/CI builds need it present at checkout time.
- `claw-api` now has its own publication workflow (`.github/workflows/claw-api-image.yml`). Source checkouts now use the same pinned refs as releases; if a pinned repo-local infra tag is not published yet, contributors must build/tag that image explicitly instead of relying on hidden fallback behavior.
- `claw-wall` now has a publication workflow alongside the existing injected sidecar path. Keep end-user docs on the four-verb surface; raw buildx commands are contributor-only release tooling.
- `hermes-base` image is built from `dockerfiles/hermes-base/` and published to `ghcr.io/mostlydev/hermes-base:<tag>` (e.g. `v2026.3.17`). It installs `hermes-agent[messaging,cron]` from the pinned upstream tag, then runs `patch-hermes-runtime.py` to apply compatibility fixes. The patch disables the `members` and `voice_states` Discord intents, makes slash-command sync non-blocking (best-effort with timeout), and — critically — sets `allowed_mentions=discord.AllowedMentions(replied_user=False)` on all `channel.send()` calls that carry a reply reference. Without this last fix, Hermes's reply feature auto-pings the original author, which in multi-agent pods creates mention loops even when `DISCORD_REQUIRE_MENTION=true`. Build: `docker buildx build --platform linux/amd64,linux/arm64 -t ghcr.io/mostlydev/hermes-base:v2026.3.17 --push dockerfiles/hermes-base/`.
- `x-claw.master` auto-injects a `claw-api` service into compose. The master claw gets a bearer token baked into its `feeds.json` via the `auth` field, and a `CLAW_API_URL` env var pointing at the in-pod API. Feed auth flows: `claw up` → `feeds.json` with `auth` → cllama fetcher sends `Authorization: Bearer` header → `claw-api` validates via principals.json.
- Alert thresholds are configurable via `CLAW_ALERT_*` env vars on the host. `claw up` forwards them into the auto-injected `claw-api` container.
- Prefer reading the code paths above before relying on plan documents.
- When changing runtime behavior, update tests in the same area if they exist.
- If a behavior is reflected in generated artifacts, inspect both the source logic and the generated output expectations in tests.
- Be careful with the working tree: this repo is often mid-change, and unrelated files may already be modified.

## Public Site (clawdapus.dev)

- Site is a VitePress app at `site/`. Not a submodule — it lives in this repo.
- Auto-deploys to `clawdapus.dev` on merge to master when `site/**` changes (`.github/workflows/deploy-site.yml`).
- Sidebar/nav config: `site/.vitepress/config.mts`. Guide pages: `site/guide/`. Top-level pages: `site/index.md`, `site/manifesto.md`, `site/changelog.md`.
- **Changelog:** `site/changelog.md` is manually maintained. Release notes do NOT sync from GitHub releases automatically — add an entry to `site/changelog.md` when cutting a release or landing significant features on master.
