# claw-wall Discord Rate Limit Fix — Implementation Plan

**Issue:** mostlydev/clawdapus#147

## Goal

Prevent `claw-wall` from triggering Discord global rate limits by:

1. Polling each consumed channel with exactly one reader token.
2. Honoring Discord `429` backoff instead of retrying on the next tick.
3. Reducing steady-state poll pressure with an explicit wall poll interval.

## Design Constraints

- Fix the current compile-time bug in `cmd/claw/compose_up.go` where wall readers are derived from all Discord-handled services before the `cllama` check.
- Keep runtime retry behavior narrow: this change is about `429` handling, not generic retry middleware.
- Preserve deterministic output in `compose.generated.yml`.
- Keep operator behavior obvious:
  - `claw up` should fail if a consumed channel has no eligible reader token.
  - non-`429` Discord failures should stay visible and should not create cooldown state.

## Non-Goals

- Do not add generic backoff for `401`, `403`, `5xx`, or network errors.
- Do not introduce multiple wall reader tokens per channel as a fallback.
- Do not invent new CLI flags for this work.

## Target Behavior

### Compile-Time

- `claw-wall` is injected only when at least one `cllama` service consumes Discord channel context.
- The wall polls the union of Discord channel IDs declared by `cllama` services only.
- For each consumed channel, `claw up` picks exactly one reader token.
- Reader selection order:
  1. Master service token, if `p.Master` is set and the master actually declares that channel.
  2. Otherwise the lexicographically first eligible service name that declares the channel and has `DISCORD_BOT_TOKEN`.
- A non-`cllama` service may serve as the reader for a consumed channel, but it must not add extra channels that no `cllama` service consumes.
- If a consumed channel has no eligible reader token, `claw up` fails with the channel ID in the error.
- The injected wall service sets both:
  - `CLAW_WALL_TOKENS`
  - `CLAW_WALL_POLL_INTERVAL`
- Default wall poll interval becomes `30` seconds unless the host environment already sets `CLAW_WALL_POLL_INTERVAL`.

### Runtime

- A local `429` blocks only that token/channel pair until the backoff expires.
- A global or token-wide `429` blocks all channels using that token until the backoff expires.
- `pollOnce()` skips cooled-down targets without calling Discord again.
- Cooldown creation is logged once when the `429` is recorded.
- Skipped polls during the cooldown window are silent.
- Non-`429` failures are still logged and retried on the next normal tick.

## Implementation Outline

## Task 1: Rewrite compile-time channel and token selection

**Files**

- `cmd/claw/compose_up.go`
- `cmd/claw/compose_up_test.go`

### Required structure in `injectConversationWall()`

1. Pass 1: build `consumedChannels` from resolved claws with `len(rc.Cllama) > 0`.
2. In the same pass, build `triggerServices` for feed injection.
3. Pass 2: scan all services for eligible reader candidates:
   - service declares the consumed channel
   - service has `DISCORD_BOT_TOKEN`
4. Select one reader token per consumed channel using the precedence rules above.
5. Inject `claw-wall` with one `channel:token` pair per consumed channel.
6. Inject `CLAW_WALL_POLL_INTERVAL`, using host env override first and otherwise `"30"`.

### Test requirements

Replace the old expectation that allowed duplicate `chan-2` entries. The test set should cover:

- Consumer-only channel selection:
  - a non-`cllama` service shares one consumed channel and also has an extra unconsumed channel
  - only consumed channels appear in `CLAW_WALL_TOKENS`
- One reader token per channel.
- Master preference when the master declares the shared channel.
- Correct fallback when the master exists but does not declare a consumed channel.
- Hard failure when a consumed channel has no eligible reader.
- Existing reserved-name rejection still passes.
- `CLAW_WALL_POLL_INTERVAL` is injected.

### Notes

- Keep the output deterministic by sorting consumed channel IDs and candidate service names.
- Do not require `DISCORD_BOT_TOKEN` on services that are not needed as readers.
- Feed injection remains only for the `cllama` services that triggered wall injection.

## Task 2: Add typed 429 parsing and cooldown tracking

**Files**

- `cmd/claw-wall/ratelimit.go`
- `cmd/claw-wall/ratelimit_test.go`

### Required runtime types

- A typed `rateLimit` value with:
  - scope
  - retry duration
  - recorded-at time
- A typed error for `429` responses so `pollOnce()` can distinguish rate limits from ordinary failures without string matching.
- A tracker with two scopes:
  - channel scope keyed by `token + channel`
  - token/global scope keyed by `token`

### Parsing rules

When Discord returns `429`, parse timing from all available sources:

1. `Retry-After`
2. `X-RateLimit-Reset-After`
3. JSON body `retry_after`

Use the largest valid duration found. If Discord omits all timing hints, use a conservative `5s` fallback.

Determine scope from:

- body `global: true`, or
- `X-RateLimit-Scope: global`

Everything else is treated as local channel scope.

### Test requirements

- header-only `429`
- body-only `429`
- `X-RateLimit-Reset-After`
- global scope from body
- global scope from header
- fallback duration when timing data is missing
- non-`429` returns no rate-limit value
- channel-scoped block affects only that channel
- token/global block affects all channels for that token

## Task 3: Wire cooldown tracking into the poller

**Files**

- `cmd/claw-wall/discord.go`
- `cmd/claw-wall/main_test.go`

### Required changes

- Extend `discordPoller` with:
  - cooldown tracker
  - optional base URL override for tests
- In `pollOnce()`:
  - skip blocked targets before making a request
  - log non-`429` failures normally
  - log the typed rate-limit error when the cooldown is first recorded
- In `fetchMessages()`:
  - detect `429`
  - parse it into typed rate-limit data
  - record the cooldown
  - return a typed rate-limit error

### Test requirements

- A `429` on one channel blocks the next poll for that same channel/token pair.
- A global/token-scoped `429` on one channel blocks all channels using that token.
- A non-`429` failure does not create cooldown state.
- The second `pollOnce()` after a `429` does not hit the test server again while the cooldown is active.

## Task 4: Verification

Run verification in this order:

```bash
go test ./cmd/claw-wall/... -count=1
go test ./cmd/claw/... -count=1
go test ./... -count=1
go vet ./...
```

Then inspect generated output on a representative pod:

1. Run a real `claw up -d` against a pod with Discord channel context.
2. Inspect `compose.generated.yml`.
3. Verify:
   - `CLAW_WALL_TOKENS` has one entry per consumed channel
   - unconsumed channels are absent
   - `CLAW_WALL_POLL_INTERVAL` is present

`claw up --dry-run` is not part of the current CLI surface, so do not rely on it in this work.

## Suggested Commit Shape

1. `test(claw): rewrite conversation wall expectations around consumer-only channels`
2. `feat(claw): select one wall reader token per consumed channel`
3. `test(claw-wall): add rate limit parser and cooldown coverage`
4. `feat(claw-wall): honor Discord 429 cooldowns`

## Summary

| Area | File(s) | Outcome |
|------|---------|---------|
| Compile-time selection | `cmd/claw/compose_up.go` | One reader token per consumed channel, master-aware, deterministic |
| Compose tests | `cmd/claw/compose_up_test.go` | Old duplicate-token expectation removed, new reader selection cases covered |
| Rate-limit model | `cmd/claw-wall/ratelimit.go` | Typed cooldown parsing and tracking |
| Poller runtime | `cmd/claw-wall/discord.go` | `429` backoff honored without noisy re-polling |
| Verification | tests + `compose.generated.yml` inspection | Confirms behavior against current repo surface |
