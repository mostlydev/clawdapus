---
outline: deep
---

# Changelog

[Latest release](https://github.com/mostlydev/clawdapus/releases/latest) | [All releases](https://github.com/mostlydev/clawdapus/releases)

## Roadmap

| Phase | Status |
|-------|--------|
| Phase 1 -- Clawfile parser + build | Done |
| Phase 2 -- Driver framework + pod runtime + OpenClaw + volume surfaces | Done |
| Phase 3 -- Surface manifests, service skills, CLAWDAPUS.md | Done |
| Phase 3.5 -- HANDLE directive + social topology (Discord, Telegram, Slack) | Done |
| Phase 3.6 -- INVOKE scheduling + Discord config wiring | Done |
| Phase 3.7 -- Social topology: mentionPatterns, allowBots, peer handle users | Done |
| Phase 3.8 -- Channel surface bindings | Done |
| Phase 4 -- Shared governance proxy integration + credential starvation | Done |
| Phase 4.5 -- Interactive claw init & claw agent add (canonical layout) | Done |
| Phase 4.7 -- Nanobot + PicoClaw + NullClaw + MicroClaw drivers | Done |
| Phase 4.8 -- Hermes driver + shared helper extraction | Done |
| Phase 4.9 -- Peer handles, mention safety, healthcheck passthrough | Done |
| Phase 4.10 -- Capability evolution wave: compiled tools + memory plane | Done (ADRs 020-021) |
| Phase 4.6 -- Unified worker architecture (config, provision, diagnostic) | Design |
| Phase 5 -- Fleet governance: Master Claw, telemetry, context feeds | Design (ADRs 012-015) |
| Phase 6 -- Recipe promotion + worker mode | Planned |

## Unreleased

- **Fix: OpenClaw scheduled jobs are materialized under the canonical cron store again** ([#159](https://github.com/mostlydev/clawdapus/issues/159)) — the OpenClaw driver now mounts a writable `~/.openclaw/cron/` directory and writes `jobs.json` there instead of under the config directory. Current OpenClaw builds resolve cron definitions from `~/.openclaw/cron/jobs.json`, so the previous layout left `openclaw cron list` empty and `openclaw cron run <id>` failed against jobs Clawdapus thought it had compiled. `claw up` now emits the native store where OpenClaw actually reads it, preserves the dedicated cron directory mount, and keeps pod-origin wakes targeting the runner-native `openclaw cron run <id>` contract.

## v0.8.11 <Badge type="tip" text="Latest" /> {#v0-8-11}

*2026-04-14*

- **Fix: `claw up -d` on a live pod can corrupt `.claw-runtime` and leave bind mounts as directories** ([#153](https://github.com/mostlydev/clawdapus/issues/153)) — the previous pipeline did `os.RemoveAll(.claw-runtime)` before `docker compose up -d` recreated the affected containers. That left a window where live containers were still bound to host paths that no longer existed, so when Docker recreated them it auto-created the missing bind-mount sources (`AGENTS.effective.md`, `CLAWDAPUS.md`, per-service config dirs) as *directories* on the host. The next `claw up -d` then crashed in materialization with `chmod config dir: operation not permitted` or `open AGENTS.generated.md: permission denied`, and the only recovery was `claw down`, move `.claw-runtime` aside, and redeploy. `runComposeUp` now rotates the runtime tree instead of destroying it: it renames the existing `.claw-runtime` to a generation-suffixed sibling, stages a fresh one for the new generation, and only removes the previous tree after compose apply succeeds. If apply fails, the old tree is restored in place so the live pod is unaffected and operators have a rollback target. Originally observed on the Tiverton trading-desk deployment. A new live spike (`TestSpikeComposeUpRuntimeRotation`) proves an in-place redeploy no longer leaves any bind-mount sources as directories.
- **Pinned cllama bumped to [v0.3.5](https://github.com/mostlydev/cllama/releases/tag/v0.3.5)** — the proxy now supports native tool handoff after managed rounds: once a managed tool mediation round completes, the runner's next turn can immediately drive a runner-native tool call without the proxy re-injecting managed tools into the outbound request. Builds on the v0.3.4 additive mediation contract.

## v0.8.10 {#v0-8-10}

*2026-04-13*

- Release note correction: the OpenClaw non-root canonical-home fix shipped in v0.8.9 requires two tmpfs mounts, not one. `/root` must be tmpfs-backed so non-root runtime users can traverse into the canonical home, and `/root/.openclaw` must also be tmpfs-backed so Docker does not leave the state root behind as `0755 root:root` when it creates the nested config bind mount. Without the second tmpfs, the first state write still fails with `EACCES: permission denied, mkdir '/root/.openclaw/agents'`. The runtime contract and code remain the same; this release makes the published changelog match the actual fix that is already on `master`.

## v0.8.9 {#v0-8-9}

*2026-04-13*

- **Fix: OpenClaw pods crash-loop on startup when the runtime container `USER` is not root** ([#149](https://github.com/mostlydev/clawdapus/pull/149) regression, fixed in this release) — v0.8.8's canonical-home rewrite mounted the writable tmpfs at `/root/.openclaw`, leaving `/root` itself at the image layer's baked-in `drwx------ root:root`. Any OpenClaw image whose runtime user is not root — including the upstream `ghcr.io/openclaw/openclaw` image and the documented "`RUN apt install ... && USER node`" pattern that lets agents add packages safely — could not even traverse `/root` to reach the writable subtree, so the gateway died on every restart with `Error: EACCES: permission denied, mkdir '/root/.openclaw/config'` and the pod cycled forever. The fix is two coordinated tmpfs mounts: `/root` itself at mode `1777` so non-root users can traverse the parent, and `~/.openclaw` (`/root/.openclaw`) at mode `1777` so the canonical state root stays writable after Docker creates the nested `/root/.openclaw/config` bind mount (without the second tmpfs, Docker leaves `/root/.openclaw` as `0755 root:root` and non-root users still fail on the first state write like `mkdir ~/.openclaw/agents`). The canonical `~/.openclaw` layout and `OPENCLAW_CONFIG_PATH`/`OPENCLAW_STATE_DIR` contract are unchanged, and any container `USER` (root or not) now both traverses in and writes state. The live regression spike `TestSpikeOpenClawNonRootHomeReachable` now proves both the config read path and the first state write before the gateway starts. Originally observed on the Tiverton trading-desk deployment within minutes of the v0.8.8 upgrade.

## v0.8.8 {#v0-8-8}

*2026-04-13*

- **Managed tool mediation is now additive** ([#151](https://github.com/mostlydev/clawdapus/issues/151), [#152](https://github.com/mostlydev/clawdapus/pull/152), [cllama#7](https://github.com/mostlydev/cllama/pull/7)) — when an upstream runner already declares its own tools, cllama no longer overwrites them on the way out to the model. Compiled managed tools are appended to the runner's outbound `tools[]` (OpenAI format) or Anthropic `tools` array, so OpenClaw and other drivers keep their native tool surface even when managed mediation is active. Runner-native tool calls in the model's response pass straight back to the runner unchanged, managed tool calls are still executed inside cllama as before, and a response that mixes both fail-closes with a precise error rather than silently dropping or replacing tools. ADR-020 is updated to reflect the additive contract, and a new live spike (`TestSpikeOpenClawAdditiveToolsLive`) proves an OpenClaw agent can hit a native tool followed by a managed tool in the same session. Pinned cllama image bumped to [v0.3.4](https://github.com/mostlydev/cllama/releases/tag/v0.3.4).
- **`claw-api` warns on principal verb skew and surfaces inert principals** ([#144](https://github.com/mostlydev/clawdapus/issues/144), [#150](https://github.com/mostlydev/clawdapus/pull/150)) — follow-up to the v0.8.6 fail-open fix. `claw up` now compares the verbs it emits in `principals.json` against the verb set known to be supported by the pinned `claw-api` image and prints a warning before deployment when a newer CLI is targeting an older API image. The `claw-api` `/health` endpoint now returns `principal_count`, `inert_principals` (principals that loaded with zero recognized verbs), and `normalization_warnings`, so operators can see normalization drops without tailing container stderr. Compile-time validation in `claw up` for user-declared verbs in `x-claw.principals` stays strict.
- **Honor service `TZ` in compiled schedules** ([#135](https://github.com/mostlydev/clawdapus/issues/135), [#145](https://github.com/mostlydev/clawdapus/pull/145)) — `claw up` now resolves a single service timezone from the Docker `TZ` env at compile time and uses it for pod-origin schedule manifest entries when no explicit calendar override is present. The same timezone flows into OpenClaw cron jobs and microclaw config instead of the previous hard-coded UTC. Schedules that depended on local-time semantics (e.g. `0 9 * * *` meaning 9am local) now fire when operators expect them to.
- **OpenClaw runtime uses the canonical `~/.openclaw` home** ([#121](https://github.com/mostlydev/clawdapus/issues/121), [#149](https://github.com/mostlydev/clawdapus/pull/149)) — the OpenClaw driver now mounts state and config under the canonical `~/.openclaw` layout inside the container (config at `/root/.openclaw/config/openclaw.json`), and the legacy `OPENCLAW_HOME` shim is gone. The base image and tmpfs config make the root-home contract explicit, removing a class of subtle path drift between Clawdapus and upstream OpenClaw.
- Doc fix: the `claw-api` principal verb validation behavior is now documented accurately — unknown verbs in `x-claw.principals` fail hard at pod parse time, while unknown verbs in `principals.json` are dropped with warnings at runtime.
- Test coverage: the OpenClaw provider config compilation matrix is now exercised end-to-end across the cllama-rewrite and direct-provider paths.

## v0.8.7 {#v0-8-7}

*2026-04-12*

- **Fix: `claw-wall` Discord rate limit handling** ([#147](https://github.com/mostlydev/clawdapus/issues/147), [#148](https://github.com/mostlydev/clawdapus/pull/148)) — `claw-wall` was polling duplicate channel/token pairs and retrying every non-200 response on the next tick, which could amplify Discord rate limits across multi-bot pods. Three coordinated fixes: (1) compile-time token selection now picks exactly one reader token per consumed Discord channel, preferring the master service's token when available, and fails hard if a consumed channel has no eligible reader; (2) the poller now parses Discord `429` responses into typed rate-limit data with channel-scoped and token-scoped cooldowns that honor `Retry-After` backoff; (3) the default poll interval increases from 15s to 30s, configurable via `CLAW_WALL_POLL_INTERVAL`. Originally observed on the Tiverton trading-desk deployment.

## v0.8.6 {#v0-8-6}

*2026-04-10*

- **Fix: `claw-api` no longer crash-loops on unknown principal verbs** ([#120](https://github.com/mostlydev/clawdapus/issues/120), [#143](https://github.com/mostlydev/clawdapus/pull/143)) — when a newer `claw` CLI compiled a `principals.json` containing verbs an older deployed `claw-api` image didn't recognize (e.g. `schedule.read` emitted by v0.6.1+ against a pre-`schedule.*` image), the API container hard-failed validation and crash-looped, taking the entire governance surface offline with only `unknown verb "..."` in container logs. The runtime loader is now tolerant: unrecognized verbs are filtered out of the in-memory principal store and logged as warnings on startup, and the service comes up normally. Compile-time validation in `claw up` stays strict — unknown verbs in `x-claw.principals` or hand-written `principals.json` still fail hard. Originally observed on the Tiverton trading-desk deployment.

## v0.8.5 {#v0-8-5}

*2026-04-10*

- **Formalize the cllama ingress surface matrix** ([#134](https://github.com/mostlydev/clawdapus/issues/134), [ADR-023](https://github.com/mostlydev/clawdapus/blob/master/docs/decisions/023-cllama-ingress-surface-matrix.md)) — `cllama` now has an explicit, minimum ingress surface contract: OpenAI Chat Completions (`POST /v1/chat/completions`) for non-Anthropic providers and Anthropic Messages (`POST /v1/messages`) for Anthropic-family providers. The previous spec described cllama as OpenAI-only even though the reference implementation has supported both surfaces for a while; `docs/CLLAMA_SPEC.md` and ADR-008 are reconciled with the runtime. Provider identity (`google/gemini-*`, `anthropic/*`, etc.) stays in operator-facing model refs — synthetic `cllama/<provider>` prefixes are explicitly rejected at parse time. The OpenClaw driver no longer owns the canonical provider-to-surface decision: it delegates to the shared `internal/cllama` contract and maps the resulting surface to its own `models.providers.*.api` enum, failing closed on unknown canonical surfaces. This is the architectural backstop behind the Gemini-behind-cllama fix that shipped in v0.8.2.
- **Spike-level regression coverage for ADR-023** ([#139](https://github.com/mostlydev/clawdapus/pull/139)) — `TestSpikeRollCall` is now a multi-model matrix: a dedicated `openclaw + google/gemini-2.5-flash` variant directly regression-tests the #127 incident that triggered the ADR, a new `openclaw + anthropic/claude-sonnet-4-6` variant covers the Anthropic Messages surface, and the stubs exercise distinct provider/model pairs across both surfaces. A final `ingress_surface_coverage` subtest fails the suite if a future change silently reroutes everything to a single surface. `pc-roll` is temporarily gated behind `CLAW_SPIKE_ENABLE_PICOCLAW` while [#137](https://github.com/mostlydev/clawdapus/issues/137) (pre-existing upstream picoclaw `gateway.port=0` crash, reproduces on master) is open.

## v0.8.4 {#v0-8-4}

*2026-04-09*

- **Fix: OpenClaw pod-origin cron schedules stop loading on openclaw ≥ 2026.3.24** ([#132](https://github.com/mostlydev/clawdapus/issues/132)) — OpenClaw `2026.3.24` changed its cron store contract: the jobs file is now resolved under `CONFIG_DIR/cron/jobs.json` using a versioned envelope (`{"version":1,"jobs":[...]}`), not the legacy bare array at `/app/state/cron/jobs.json`. Clawdapus was still emitting the old path and shape, so pod-origin schedules never loaded into OpenClaw's in-memory registry, `openclaw cron run <id>` returned `unknown cron job id`, and `claw-api` eventually marked affected schedules as degraded after repeated wake failures. The driver now writes `jobs.json` under the writable `/app/config/cron/` directory in the versioned envelope format, and the `examples/openclaw/` Clawfile is bumped to `openclaw@2026.4.9` so the example's `INVOKE` heartbeat actually loads.

## v0.8.3 {#v0-8-3}

*2026-04-09*

- **Fix: update-check notifier prints phantom "downgrade" after upgrading** — the update-available notifier used a plain string inequality instead of a semver comparison, so a freshly upgraded binary reading its pre-upgrade cache (within the 1 hour TTL) would print `Update available: v0.8.2 → v0.8.1`. The notifier now uses strict semver ordering, so a stale cache whose `latest_tag` is older than the running binary no longer triggers a bogus notice. As a one-time workaround on an already-upgraded host, `rm ~/.claw/.claw-update-check` clears the stale cache.

## v0.8.2 {#v0-8-2}

*2026-04-09*

- **Fix: OpenClaw Google models behind cllama** ([#127](https://github.com/mostlydev/clawdapus/issues/127)) — when `x-claw.cllama` is set and a `google/*` model is routed through the proxy, OpenClaw now compiles `models.providers.google.api` as `openai-completions` instead of the vendor-native `google-generative-ai` surface. Direct (non-cllama) Google routing is unchanged. This removes the `404 page not found` failure mode reported when running pods like `x-claw.cllama: passthrough` with `google/gemini-3-flash-preview`. Regression coverage added for both the cllama-rewrite case and the direct-provider case.

## v0.8.1 {#v0-8-1}

*2026-04-08*

- **Native Gemini provider support** ([#119](https://github.com/mostlydev/clawdapus/issues/119)) — cllama now supports direct `google/<model>` routing through Google's OpenAI-compatible endpoint. `GEMINI_API_KEY` is the primary seed env var, `GOOGLE_API_KEY` is accepted as a lower-priority alias, `claw up` compiles Google keys into `providers.json`, and the provider-key strip path now also removes xAI/Gemini secrets from agent envs. Pinned cllama image bumped to [v0.3.3](https://github.com/mostlydev/cllama/releases/tag/v0.3.3).

## v0.8.0 {#v0-8-0}

*2026-04-08*

- **Pod-level model slots** ([#118](https://github.com/mostlydev/clawdapus/issues/118)) — `claw-pod.yml` now supports service-level `x-claw.models` and pod-level `x-claw.models-defaults`. `claw up` compiles the merged slot map into each service's runtime config so operators can retarget models per pod without rebuilding shared images. Service-level overrides merge additively per slot over pod defaults, so you can replace `primary` without losing `fallback`. Pod slots still overlay image `MODEL` labels; `models: {}` and `models: null` suppress pod defaults only.
- **Cllama image bump to v0.3.2** — `DefaultCllamaTag` now pins [cllama v0.3.2](https://github.com/mostlydev/cllama/releases/tag/v0.3.2), which carries the cache-friendly feed injection ordering fix. Operators running `claw pull` will now actually receive the Anthropic prompt cache fix tracked in [#122](https://github.com/mostlydev/clawdapus/issues/122).

## v0.7.0 {#v0-7-0}

*2026-04-07*

- **Fix: cllama prompt cache efficiency** ([#122](https://github.com/mostlydev/clawdapus/issues/122)) — feed and time context is now appended after the system prompt rather than prepended, enabling Anthropic prompt cache reuse across requests.
- **Four-verb image lifecycle** — `claw pull` is now the explicit infra freshness command, `claw build` is pod-aware with no path, and `claw up` is strict by default with `--fix` as the opt-in auto-remediation path.
- **Pinned infra manifest plumbing** — release builds stamp first-party infra image tags into the `claw` binary, and source checkouts now use the same pinned refs with fail-closed behavior when a tag is unpublished.
- **Release-time infra verification** — the release workflow now verifies that pinned infra tags already exist in GHCR before publishing the `claw` binary, preventing a broken release from shipping a manifest that points at missing images.
- **Quickstart and operator docs sweep** — README, site quickstart/CLI docs, example READMEs, testing docs, and the embedded Clawdapus skill now teach the explicit `pull -> build -> up -> down` operator flow.

## v0.6.2 {#v0-6-2}

*2026-04-07*

- **Fix: cllama prompt cache efficiency** ([#122](https://github.com/mostlydev/clawdapus/issues/122)) — feed and time context is now appended after the system prompt rather than prepended, enabling Anthropic prompt cache reuse across requests.

## v0.6.1 {#v0-6-1}

*2026-04-05*

- **Managed tool manifest state in `claw audit`** ([#115](https://github.com/mostlydev/clawdapus/issues/115)) — proxy telemetry now emits `manifest_present` (bool) and `tools_count` (int) on every mediated request, and `claw audit` surfaces them in its JSON output. Operators can verify at runtime whether a compiled `tools.json` actually reached cllama for a given agent — closing an observability gap that made it hard to diagnose cases where tools were compiled on disk but not being injected into upstream LLM requests. Requires cllama v0.3.1 or newer.

## v0.6.0 {#v0-6-0}

*2026-04-05*

- **Conditional invoke scheduling control plane** ([#107](https://github.com/mostlydev/clawdapus/issues/107)) — pod-origin `x-claw.invoke` entries compile to an external schedule manifest with calendar-aware `when:` gates (e.g. `when: { calendar: us-equities, session: regular }`). A scheduler loop inside `claw-api` evaluates gates before wake and persists state under `.claw-governance/`. Wake adapters cover the full runner set; Clawfile/image-origin `INVOKE` continues to use runner-native cron unchanged.
- **`claw api schedule` operator CLI** — new subcommands (`list`, `show`, `pause`, `resume`, `skip-next`, `fire`) to inspect and control scheduled pod-origin invocations at runtime. Tunneled through `docker compose exec` so `claw-api` remains internal-only with no published host port. Operators can pause, resume, skip, or manually fire scheduled invocations without rebuilding images.
- **Clawdash Schedule page** — new card-based UI organized around operator mental model: health at a glance, next fire time, last event, and a context-sensitive primary action. Gate/Timing/Wake columns consolidated into a single schedule block; `docker exec` wake commands hidden behind disclosure; always-visible action buttons collapsed into primary + overflow.
- **Capability wave documentation** — new guides for Managed Tools (`site/guide/tools.md`) and Memory Plane; manifesto updated with compiled tool mediation section; ADRs 020 and 021 marked Implemented.
- **Site branding refresh** — new hero lockup (octopus glyph + wordmark) on the landing page.

## v0.5.2 {#v0-5-2}

*2026-04-05*

- **Fix: `claw.describe` discovery for build-only services** ([#112](https://github.com/mostlydev/clawdapus/issues/112)) — `claw up` now inspects locally built images for their `claw.describe` label even when the compose service uses `build:` without an explicit `image:`. Previously the in-image descriptor path was resolved against the host build context and quietly missed, which broke feed/tool subscriptions from sibling services.
- **Scheduler groundwork (internal)** — `claw-api` gains an externalized invoke scheduler and schedule state/control endpoints. These are wiring for upcoming v0.6.0 work; no new CLI or UI surfaces ship with this release.

## v0.5.1 {#v0-5-1}

*2026-04-03*

- **`body_key` in tool descriptors** — `claw.describe` v2 tool HTTP specs now support a `body_key` field that wraps tool arguments under a named JSON key in the request body (e.g. `"body_key": "trade"` sends `{"trade": {...args}}`). Validated at parse time for POST/PUT/PATCH only, propagated through the manifest, and executed by cllama proxy v0.3.0.
- **`claw skill install`** — new command installs the clawdapus-cli skill to `~/.claude/skills/` and `~/.agents/skills/`, giving Claude Code, Codex, Gemini, and OpenCode full operational knowledge of the claw CLI. Auto-updates on every `claw` invocation when a newer binary is installed.

## v0.5.0 {#v0-5-0}

*2026-04-03*

- **Capability wave: compiled tools + memory** (ADRs 020 and 021) — `claw up` now compiles `tools.json` and `memory.json` per agent from `claw.describe` version 2 service descriptors. Non-cllama services that declare `x-claw.tools` or `x-claw.memory` are a hard error at compile time.
- **Managed tool mediation** — cllama injects compiled managed tool schemas into upstream LLM requests (OpenAI-compatible and Anthropic formats), intercepts `tool_call` responses, executes them against the declared service, and loops until terminal text. Runners receive only the final text. Streaming runners receive synthetic SSE re-streaming after mediation completes; long mediated loops emit SSE keepalive comments.
- **Cross-turn tool continuity** — hidden managed tool rounds are reinjected into subsequent upstream requests so the LLM sees the effective transcript that produced each runner-visible reply.
- **Memory plane** — pre-turn recall and post-turn best-effort retain hooks live in cllama. `memory_op` telemetry events carry recall/retain outcome, latency, block count, injected bytes, and policy-removal counts. Secret-shaped values are scrubbed from both retain payloads and recalled blocks before they reach the model.
- **`claw memory backfill`** — replays the durable session ledger into a memory service's `retain` endpoint. Supports `--after` (indexed, no full-rescan), `--limit`, `--agent`, `--url`, and `--auth-token`. Tombstone-aware: forgotten entry IDs are skipped.
- **`claw memory forget`** — dispatches the service's `forget` endpoint and writes infra-owned tombstones under `.claw-memory-tombstones/`. Later backfill runs honor tombstones and skip re-retain.
- **`claw audit` tool events** — session-history `tool_call` events are merged with proxy log events so managed tool activity and failures are visible without manual ledger inspection.
- **Reference memory adapter** — `examples/reference-memory/` ships a file-backed reference implementation: idempotent retain by `entry.id`, tombstone-aware forget, recent/token-matching recall. Used by rollcall and capability-wave spike.
- **Site branding refresh** — new octopus glyph mark with regenerated favicons across all sizes.

## v0.4.3 {#v0-4-3}

*2026-03-28*

- **cllama model policy enforcement** — `claw up` compiles per-service model policies from pod YAML; cllama enforces allowed models, providers, and xAI seed support at the proxy layer.
- **`claw update` subcommand** — self-update via the install script with hourly check during active development.
- **`REPO_ROOT` surface placeholders** — volume surface paths now support `REPO_ROOT` substitution.
- Cross-runner memory portability formalized in docs.

## v0.4.2 {#v0-4-2}

*2026-03-27*

- **Session history** — `claw up` creates a persistent `.claw-session-history/` directory bind-mounted into cllama; proxy writes per-agent `history.jsonl` with `reported_cost_usd` on every 2xx completion. Survives container restarts and driver migrations.
- **Descriptor discovery from Dockerfile labels** — `claw inspect` reads `.claw-describe.json` from image labels, not just the filesystem.
- **Portable history importer** for trading-desk example feeds.
- Fixes: OpenClaw mention regex and workspace permissions, claw-api alert window, claw-wall quiet-turn context.
- ADR-018: session history and memory retention — two surfaces, two owners.

## v0.4.1 {#v0-4-1}

*2026-03-26*

- **Communication tools contract** — all 7 runtimes now enforce private thinking + explicit `send_message` delivery. Agent reasoning never reaches Discord automatically.
  - Hermes: `HERMES_TOOL_ONLY_MODE` injected by driver; runtime patches suppress text auto-routing
  - OpenClaw: already enforced natively
  - NullClaw, MicroClaw, NanoClaw, NanoBot, PicoClaw: `discord-responder.sh` passes a `send_message` tool to the LLM
- **Spike test hardened** — `TestSpikeRollCall` always rebuilds stub base images; 7/7 runtimes pass end-to-end.

## v0.4.0 {#v0-4-0}

*2026-03-26*

- **Hermes tool-only mode** — Hermes agents communicate exclusively via `send_message` tool calls when Discord handles are configured.
- **`hermes-base` image** — real runtime build replaces rollcall stub. `patch-hermes-runtime.py` applies compatibility fixes at build time: disabled intents, non-blocking slash-command sync, reply-mention suppression, `tool_choice=required` on first turn.
- **`claw up` auto-builds `hermes:latest`** via `ensureInfraImages`.
- **cllama v0.2.3** — unpriced request tracking, reported cost passthrough, timezone context injection.
- **clawdash** — surfaces `unpriced_requests` with amber warning in fleet page.
- **CLAWDAPUS.md** — adds `## Communication Tools` section with private-thinking policy when handles are configured.

## v0.3.6 {#v0-3-6}

*2026-03-23*

- **Conversation wall sidecar** (`claw-wall`, #71) — pod-level sidecar that polls Discord channels and serves incremental message history to cllama-enabled agents. Each agent gets a per-consumer cursor so it only sees new messages since its last turn. Auto-injected by `claw up` when any cllama-enabled service has Discord channel IDs; reserved service name `claw-wall` is enforced. `CLAW_WALL_TOKENS` carries `(channelID, token)` pairs supporting multi-bot pods with overlapping channels.
- **Empty-feed skip in cllama** — `FormatFeedBlock` now returns `""` for empty non-unavailable feeds, so quiet conversation wall turns produce no injected context block.
- **Sequential conformance opt-out** (#79) — `sequential-conformance: true` pod flag allows services to share Discord handle IDs (rollcall pattern). Cross-service uniqueness check is bypassed while `count > 1` rejection is still enforced.
- **Write plane security hardening** (#78) — path traversal rejected in governance target validation; master token read after merge to prevent pre-merge injection; reserved service name guard for inject-into; no-master guard rejects principals without `x-claw.master`.

## v0.3.5 {#v0-3-5}

*2026-03-22*

- **ADR-017: Pod defaults, service self-description & unified CLAWDAPUS.md** — `claw.describe` service descriptors, pod-level defaults with spread expansion, feed registry, and unified per-agent CLAWDAPUS.md generation
- **Master Claw API access** — `CLAW_API_TOKEN` injected automatically; feed auth via bearer tokens in `feeds.json`
- **`claw compose` passthrough** — `claw compose <subcommand>` passes through to `docker compose -f compose.generated.yml`
- **Stale guard** — lifecycle commands refuse to run if `claw-pod.yml` is newer than `compose.generated.yml`
- **Hermes SOUL.md** — default SOUL.md overrides runner identity with contracted agent identity; titlecased names
- **Hermes env passthrough** — `CLAW_API_URL` and `CLAW_API_TOKEN` added for master claw
- **Audit feed events** — feed fetch events surfaced with `feed_name` and `feed_url` fields
- **Configurable alert thresholds** — `CLAW_ALERT_*` env vars for error rate, cost, feeds, interventions; wired into `/fleet/alerts`
- **Documentation site** — [clawdapus.dev](https://clawdapus.dev) with full guide, reference, manifesto, and changelog
- OG social cards, favicons, web manifest, and complete meta tags

## v0.3.4 {#v0-3-4}

*2026-03-22*

- **cllama feed injection** (ADR-013 Milestone 2) — the proxy now supports runtime feed injection into LLM requests with TTL caching
- Updated AGENTS.md with cross-driver operational gotchas
- Multi-arch cllama image (amd64 + arm64)

## v0.3.3 {#v0-3-3}

*2026-03-21*

- **Auto-resolve base images** for all drivers — every driver now implements `BaseImageProvider`, eliminating manual `docker pull`
- Hermes first-class scaffold support in `claw init` and `claw agent add`
- README fully updated for v0.3.2+

## v0.3.2 {#v0-3-2}

*2026-03-21*

- Discord bot setup guide added to quickstart
- cllama image now multi-arch (amd64 + arm64)

## v0.3.1 {#v0-3-1}

*2026-03-20*

Bug fixes across all 7 drivers:

- Runtime directories use `0o777` for uid portability
- `mention_only` for Discord in all drivers (prevents feedback loops)
- Explicit `HOME` env var in all drivers
- Peer handles in CLAWDAPUS.md
- Healthcheck passthrough from `claw-pod.yml`
- ADRs 012--016 for fleet governance

## v0.3.0 {#v0-3-0}

*2026-03-19*

- **Hermes driver** added to trading-desk and rollcall examples
- Compose emitter preserves non-managed services/volumes/networks
- Published `ghcr.io/mostlydev/cllama:latest` as public image
- `CLAUDE.md` is now a symlink to `AGENTS.md`
- ADR-012 (Master Claw), ADR-013 (Context Feeds)

## v0.2.2 {#v0-2-2}

*2026-03-08*

- Docs: reorder driver matrix, clarify OpenClaw routing

## v0.2.1 {#v0-2-1}

*2026-03-08*

- Fix runtime regeneration and Discord guild policy handling
- Fix `CLAW_PERSONA_DIR` only set when persona is configured
- Review fixes (`dst.Close` error, `go mod tidy`, no-persona test)

## v0.2.0 {#v0-2-0}

*2026-03-08*

Major release bringing four new drivers, scheduling, channel surfaces, and the cllama proxy:

- **Interactive scaffold**: `claw init` + `claw agent add` with canonical layout
- **Persona materialization** (local refs + OCI artifacts)
- **x-claw include** composition (`enforce`, `guide`, `reference`)
- **Nanobot and PicoClaw drivers**
- **NullClaw driver** with `CONFIGURE` support
- **MicroClaw driver**
- Roll-call spike test covering 6 drivers
- **cllama sidecar wiring** (Phase 4 complete)
- **INVOKE scheduling** + Discord config wiring
- **Channel surface bindings** (map-form)
- Social topology: `mentionPatterns`, `allowBots`, peer handles
- Service surface skills, `SKILL` directive, CLAWDAPUS.md generation
- cllama dashboard (single-page with SSE live updates)
- Install script
- GoReleaser + release workflow

## v0.1.0 {#v0-1-0}

*2026-02-27*

Foundation release establishing the core compilation model:

- **Clawfile parser + build** (Phase 1)
- **OpenClaw driver + pod runtime** (Phase 2)
- Volume surfaces, service surfaces, CLAWDAPUS.md context injection
- `SKILL` directive
- `HANDLE` directive (Discord, Telegram, Slack)
- **NanoClaw driver** (Claude Agent SDK)
- Health probes, compose subcommands
- Manifesto, architecture plan, ADRs 001--009

## v0.0.1 {#v0-0-1}

*2026-02-11*

Initial pre-release tag. "Not quite ready, but tagging nonetheless."
