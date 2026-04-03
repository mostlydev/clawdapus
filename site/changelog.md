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
| Phase 4.6 -- Unified worker architecture (config, provision, diagnostic) | Design |
| Phase 5 -- Fleet governance: Master Claw, telemetry, context feeds | Design (ADRs 012-015) |
| Phase 6 -- Recipe promotion + worker mode | Planned |

## v0.5.0 <Badge type="tip" text="Latest" /> {#v0-5-0}

*2026-04-03*

- **Capability wave: compiled tools + memory manifests** (ADRs 020 and 021) — `claw up` now compiles `tools.json` and `memory.json` into each managed `cllama` context from descriptor `version: 2` service capabilities.
- **Managed tool mediation in cllama** — OpenAI-compatible and Anthropic-format requests now support bounded managed HTTP tool execution, structured tool error feedback, session-history `tool_trace`, and synthetic downstream SSE re-streaming when the runner requested streaming.
- **Cross-turn continuity for mediated tools** — hidden managed tool rounds are preserved across later turns so the upstream model sees the effective transcript that produced the runner-visible assistant reply.
- **Memory plane hardening** — pre-turn recall and post-turn retain hooks are live in `cllama`, with structured `memory_op` telemetry, stable session-history entry IDs, governed `claw memory forget`, tombstone-aware replay, indexed `--after` backfill reads, and retain/recall policy-removal accounting.
- **Reference memory adapter** — Clawdapus now ships a file-backed reference memory service under `examples/reference-memory` that dedupes by stable `entry.id`, honors forget tombstones, and is used by the rollcall example plus the capability-wave spike path.
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
