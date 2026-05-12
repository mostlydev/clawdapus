# Plan: claw-wall startup backfill for restart-resilient 24h coverage

Issue: [#238](https://github.com/mostlydev/clawdapus/issues/238)
Related: [#232](https://github.com/mostlydev/clawdapus/issues/232) phase-1 raw-window (merged f88e4f6), [#164](https://github.com/mostlydev/clawdapus/issues/164) salience digest (long-term)
Status: implemented on branch `issue-238-claw-wall-restart-backfill` after codex review
Authoring agent: claude:328884a6 (after codex:629dcdf2 read-only investigation, room note ea13dbaa)

## Problem

After Tiverton deployed v0.16.0 (May 12, 2026), Boulton was asked about a Wojtek prompt from 09:50 ET and missed it — his `channel-awareness` window only covered 11:46..14:31. The phase-1 plumbing was working; the backing store could not satisfy `since=24h` after restart.

Root cause (verified in current `cmd/claw-wall/`):

- `discord.go:107-109` clamps `fetchLimit` to Discord's 100-msg API cap regardless of `CLAW_WALL_LIMIT`.
- `discord.go:137-165` `pollOnce` only paginates *forward* via `after=<latestByPair>`. On startup `latestByPair` is empty, so the first call is a single tail page of ≤100 messages.
- `discord.go:173` no `before=` pagination loop exists anywhere — there is no way today to walk back to the configured retention horizon.
- `store.go` is in-memory only, capped at `CLAW_WALL_LIMIT` messages per channel (default 500). Restart wipes everything.

PR #236 explicitly called out this gap: *"Phase 2 remaining: full acceptance guarantee for high-volume rooms beyond the phase-1 raw window cap."* The auto-close of #232 on merge was process drift, not a claim that the contract was fulfilled.

## Scope and non-goals

**In scope (this PR):**

- Startup backfill that paginates Discord history backward per channel until a configured retention horizon is covered, with bounded total page count and rate-limit awareness.
- Bounded poll-time gap recovery when a forward poll returns a full Discord page after an existing cursor.
- Time-based retention in the in-memory store: trim messages older than `CLAW_WALL_RETENTION` (default `24h`) in addition to the existing `CLAW_WALL_LIMIT` safety cap.
- Honest coverage reporting: a new `backfill_status` field in `[channel-awareness]` and `[channel-context]` headers so consumers can detect partial windows.

**Explicit non-goals (separate follow-up issues):**

- **Durable on-disk channel store** (would survive restart without re-paginating Discord). Worth doing for rate-limit hygiene on busy multi-pod hosts, but is a structurally larger change and doesn't fix #238 by itself; the in-memory wall + startup backfill closes #238 fully.
- **Salience/digest** (#164). Exact-source retrieval is what restores the 09:50 ET miss; digests are orthogonal.
- **Backfill on tool-call demand** (i.e. `search_channel_context` triggering Discord lookups for not-in-buffer queries). The store stays the source of truth; tools query what's retained.
- **Reopening #232.** Phase-1 API surface (raw window + retrieval tools + metadata) did ship in #236. #238 is the concrete implementation step that lets the phase-1 surface actually satisfy `since=24h`.

## Issue-board hygiene

1. #238 is the active implementation issue and belongs in `In Progress` while this branch is under active work.
2. #232 stays closed/Done. It shipped the phase-1 API surface; #238 is the correctness follow-through for restart-resilient 24h backing coverage.
3. A durable on-disk channel store remains separate follow-up work. Do **not** silently bake it into this PR.

## Design

### A. Retention model (`store.go`)

Today `merge()` trims by message count: `state.messages[len(state.messages)-s.limit:]`. Switch to a hybrid:

```
trim if msg.Timestamp < (now - retention) OR len(state.messages) > safetyCap
```

- `retention` is `time.Duration`, default `24h`, configured via `CLAW_WALL_RETENTION`.
- `safetyCap` is the existing `CLAW_WALL_LIMIT` semantic, raised default to e.g. `5000` per channel (memory upper bound).
- Trimming happens in `merge()` and again opportunistically in `tail()`/`scan()` when reading (cheap; messages are pre-sorted).

This means a 1000-msg/day channel under `retention=24h` retains ~1000 messages instead of being clamped at 500. The `safetyCap` is purely a memory-safety bound.

Edge case: messages with zero timestamps (parse failure in `convertDiscordMessage`) get treated as "outside time horizon" → drop. The existing fallback `timestamp = time.Time{}` (discord.go:233-234) becomes a real signal rather than silent retention.

### B. Startup backfill (`discord.go`)

Add `backfillAll` / `backfillChannel` methods on `discordPoller`. They walk Discord history backward from "newest" until either:

1. The oldest fetched message timestamp is before `now - horizon`, OR
2. A configured per-channel page budget is exhausted (default 25 pages = 2500 messages worst case), OR
3. Discord returns an empty page (channel exhausted), OR
4. Rate limit is hit.

API: `GET /channels/{id}/messages?limit=100&before=<oldestSeenID>`. Discord returns newest-first within a page; the store normalizes pages oldest-to-newest and advances `before` from the oldest seen ID.

Wire it into `Run()`:

```go
func (p *discordPoller) Run(ctx context.Context, interval time.Duration, logWriter io.Writer) {
    p.backfillAll(ctx, logWriter)  // new
    p.pollOnce(ctx, logWriter)
    ticker := ...
}
```

`backfillAll` walks each target sequentially and respects the existing `cooldowns` rate-limit tracker. Rate-limit hit during backfill → record per-channel `backfillStatus = "rate_limited"`, partial coverage retained.

After backfill, the existing `pollOnce` runs as before; `latestByPair` gets populated by backfill so the first forward poll picks up only post-backfill messages.

### C. Coverage status surfacing

The store gains a per-channel `backfillStatus` map: `{ "complete" | "partial" | "rate_limited" | "in_progress" | "unavailable" }`. Set by the poller after `backfillAll` completes (or partials out).

`formatChannelAwareness` and `formatTailContext` add a `backfill_status=<status>` token after `buffer_range`. Existing fields are unchanged so v0.16 consumers don't regress. If multiple channels have different statuses, emit `backfill_status=channelID:status,channelID:status` - same encoding as the existing cursor map.

This is the operator-visible signal the issue body asked for: *"If the backing store cannot actually satisfy 24h, the feed/tool should make that limitation explicit enough for operators and tests to catch."*

### D. Config surface (additive only, no pod YAML changes)

New env vars on `claw-wall`:

| Var | Default | Meaning |
|---|---|---|
| `CLAW_WALL_RETENTION` | `24h` | Time horizon for store retention and startup backfill. |
| `CLAW_WALL_BACKFILL_MAX_PAGES` | `25` | Per-channel page budget for the initial backfill walk. |
| `CLAW_WALL_LIMIT` | `5000` (raised from 500) | Per-channel safety cap on retained messages. |

No `claw-pod.yml` schema changes; the auto-injected `claw-wall` sidecar reads these from its own env. `claw up` now emits defaults for `CLAW_WALL_RETENTION` and `CLAW_WALL_BACKFILL_MAX_PAGES` and lets host env override them, matching the existing generated sidecar-env pattern.

### E. Tests (all `cmd/claw-wall/*_test.go`, no new build tag)

Reuse the existing `httptest.NewServer` pattern from `main_test.go:198+` and `discord.go` tests at `main_test.go:503+`.

1. `TestDiscordPollerBackfillsPastFirstPageToHorizon`: fake Discord with 360 messages spread over 36h. Backfill with `retention=24h`. Assert store contains exactly the messages within the 24h window, and that the fake server paginated with `before=`.
2. `TestDiscordPollerBackfillStopsAtPageBudget`: 500 messages all inside 24h, `maxPages=2`. Assert backfill stops at 200 messages and `backfillStatus="partial"`.
3. `TestDiscordPollerBackfillRespectsRateLimit`: fake Discord 429s on the 2nd page. Assert backfill records `backfillStatus="rate_limited"`, retains the first 100, and does not retry inside the same backfill call.
4. `TestChannelAwarenessHeaderReportsBackfillStatus`: store with `backfillStatus="partial"` -> response includes `backfill_status=partial`.
5. `TestConversationStoreTrimsBySinceHorizon`: insert messages from 30h ago, 12h ago, and now; with `retention=24h` only the latter two should remain after `merge`.
6. `TestConversationStoreSafetyCapStillApplies`: insert messages within 24h with `safetyCap=500`; assert store keeps newest 500 and reports `backfill_status=partial`.
7. Search/coverage: `search_channel_context` for a message inside the backfilled window returns `status=ok`; for a message outside the backfill horizon returns `status=not_in_buffer` with the existing hint.

No spike test changes required — `TestSpikeRollCall` doesn't exercise channel-context heavily. Could add a `TestSpikeChannelBackfill` later; not part of this PR.

### F. Files touched (rough)

- `cmd/claw-wall/discord.go` — add `backfillAll`, `backfillChannel`, helper to walk `before=` pages
- `cmd/claw-wall/store.go` — switch retention model, add `backfillStatus` field, update header formatters
- `cmd/claw-wall/main.go` — read new env vars, wire into poller and store
- `cmd/claw-wall/main_test.go` — new tests
- `cmd/claw/compose_up.go` — emit new generated claw-wall env defaults and raise the generated safety-cap default
- `cmd/claw/compose_up_test.go` — generated env expectations
- `site/changelog.md` — entry under `## Unreleased`
- `site/guide/cllama.md` (or wherever `channel-awareness` is documented) — short note on backfill semantics + new env vars
- AGENTS.md — one bullet under "Repo-Specific Gotchas" noting startup backfill happens before first poll and that rate-limited backfill is surfaced via `backfill_status=` header

No changes to `claw-api` or the pod parser. No release-pin bumps.

## Sequencing

1. **Adversarial review** (codex): poke holes; agree on retention default, page budget default, edge cases (zero-timestamp messages, channels with messages older than 24h with no traffic since).
2. **Implementation** (codex): writes code + tests against the converged plan.
3. **Implementation review** (claude): full repo test + go vet, read diff, mark PR ready.
4. **Release**: maintainer cuts `v0.17.0` via the `clawdapus-release` skill. claw-wall is a coordinated infra image, so its workflow republishes on master merge but the operator-facing release tag is the trigger.

## Codex review decisions

1. Leave #232 closed and track this correction in #238.
2. Raise `CLAW_WALL_LIMIT` default 500 -> 5000 and mention it in release notes/docs.
3. Count cap wins over retention for memory safety; if the cap evicts in-window messages, coverage is `partial`.
4. Emit `backfill_status=complete` when all visible channels share complete status; emit per-channel `channel_id:status` pairs only for mixed statuses.
5. Include bounded poll-time gap recovery in this PR.
6. Keep startup backfill sequential for v1; it fits the existing token cooldown model.

## Do not

- Do not bump `internal/infraimages/release_manifest.go` pins, the cllama submodule pointer, or the changelog `Latest` badge in this PR. Release belongs to the maintainer.
- Do not skip rate-limit handling during backfill. Tiverton has multiple pods that could be restarted in quick succession; hitting Discord 429 on every restart hurts the whole bot account.
- Do not assume zero-timestamp messages should be kept "to be safe" — they can't be filtered by window and they accumulate forever. Drop them.
- Do not introduce a new pod-YAML key for backfill config in this PR. The `claw-wall` env surface is the right layer for an auto-injected sidecar.
