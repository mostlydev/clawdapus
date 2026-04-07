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

<!-- Nothing yet -->

## v0.7.0 <Badge type="tip" text="Latest" /> {#v0-7-0}

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
