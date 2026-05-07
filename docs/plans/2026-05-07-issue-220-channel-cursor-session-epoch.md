# Issue 220 — Channel-context cursor bootstrap on consumer session restart

**Author (draft):** claude (acc31fb0) — for adversarial review by codex before implementation.

**Revision history:**
- 2026-05-07 r1 — initial draft.
- 2026-05-07 r2 — codex (`codex:c646a5ba`) adversarial pass; folded in: header rename for clarity, single-snapshot store load, `*epochDecision` returned from prepare (caller stamps after fetch success), CAS on epoch overwrite, dropped the bootstrap log line (logger schema cost outweighs benefit at v1), Anthropic-format Hermes path explicitly out of scope, added two tests.
- 2026-05-07 r3 — codex r2 review; corrected: CAS semantics (use `*string` so empty-epoch is distinguishable from no-CAS — "" sentinel was a real bug, would let two concurrent first-bootstrap requests both win), empty-channel test assertion (next prepare must observe `Bootstrapped=false` with no `after=` param, since `encodeAfterCursors` of an empty cursor map yields empty), softened observability claim (claw-wall access-log grep is ambiguous between legacy/first-turn/empty/bootstrap — no reliable in-protocol bootstrap signal in v1).

**Issue:** https://github.com/mostlydev/clawdapus/issues/220

## Problem (one-line)

Persisted `channelCursorLedger` correctly tracks deltas turn-to-turn, but when the **consumer** process (Hermes or other cllama-fronted runtime) restarts, the first turn of the new session reuses the stored cursor and silently sees only the delta since the last turn of the previous session, instead of a full tail.

## Design summary

Decouple consumer session lifetime from cursor ledger lifetime via a **session epoch** that the consumer attaches to every cllama request. The proxy compares the incoming epoch to the per-agent stored epoch; mismatch ⇒ one-shot bootstrap (skip `after=` injection); match ⇒ normal delta path. Missing header ⇒ legacy delta path (back-compat).

## Wire format

- Header name: `X-Claw-Consumer-Session-Epoch` (verbose-but-precise; "consumer session epoch" is what this actually is — distinct from any cllama-internal session/history concept).
- Value: opaque ASCII (UUIDv4 in the Hermes implementation). Cllama does no parsing — only string-equality comparison and persistence.
- Scope: per-agent (the agentID is already authoritative from the bearer token). Two agentIDs sharing a process is fine; one agentID across two processes is undefined and we do not try to support it.
- Empty / whitespace-only header values are treated as **absent**, not as a distinct epoch value.

## Cllama side (`cllama/internal/proxy/`)

### 1. `channel_cursor.go`

Add `SessionEpoch` to the persisted ledger:

```go
type channelCursorLedger struct {
    Version      int                      `json:"version"`
    SessionEpoch string                   `json:"session_epoch,omitempty"`
    Channels     map[string]channelCursor `json:"channels"`
}
```

- Keep `Version: 1`. Field is additive-optional. Older cllama unmarshalling a ledger with `session_epoch` drops the field silently (no v1→v2 silent-failure analogue because the missing field never affects key-loading correctness — the worst case is one extra bootstrap on downgrade).
- Replace separate `Load` with a **single snapshot load** under the store mutex (codex r2): `LoadSnapshot(agentID string) (channelLedgerSnapshot, error)` returning `{ epoch string; cursors map[string]channelCursor }`. This rules out a torn read where prepare sees a freshly-committed epoch but stale cursors (or vice-versa). The existing `Load` becomes a thin wrapper for any non-feed-prep call sites that only want cursors.
- Combined commit with **CAS on epoch** (codex r2 → r3 correction):

  ```go
  type ledgerCommitInput struct {
      // ExpectedPreviousEpoch is nil for cursor-only commits (no epoch CAS).
      // For epoch-bearing commits, *ExpectedPreviousEpoch is the exact value
      // the caller's snapshot observed — including "" to mean "expect no
      // stored epoch yet". This distinguishes "first-time bootstrap"
      // (Expected=*"", New="abc") from "concurrent first-bootstrap winner"
      // (Expected=*"", New="xyz" arrives after the ledger already moved to "abc").
      ExpectedPreviousEpoch *string
      NewEpoch              string             // "" = leave epoch alone
      CursorUpdates         map[string]channelCursor
  }
  func (s *channelCursorStore) Commit(agentID string, in ledgerCommitInput) error
  ```

  Cursor updates always merge monotonically via `compareMessageID`. Epoch update applies only when `ExpectedPreviousEpoch != nil` AND `*ExpectedPreviousEpoch` equals the current on-disk `session_epoch`; otherwise the cursor merge still runs but the epoch field is left untouched. This prevents an in-flight bootstrap from a stale (older) request reverting an epoch that a newer restart already committed — including the case where two concurrent first-bootstrap requests race against an empty stored epoch.
  Empty `NewEpoch` means "leave existing epoch alone." `ExpectedPreviousEpoch == nil` skips the CAS check entirely (used by cursor-only commits).

- Extend `pendingChannelCursorCommit`:

  ```go
  type pendingChannelCursorCommit struct {
      newEpoch              string             // "" means no epoch update
      expectedPreviousEpoch *string            // nil = cursor-only / no CAS; *"" valid (expect empty)
      updates               map[string]channelCursor
  }
  ```

  Existing `Merge(updates ...)` keeps signature. New helper `SetEpoch(prev *string, next string)` records the CAS pair. Caller (handler.fetchFeeds) calls `SetEpoch` **only after** `feedFetcher.Fetch` returns nil for the channel-context entry; the prepare step returns the decision but does not mutate pending state. (codex r2)

### 2. `channel_context_feed.go`

`prepareChannelContextFeed` gains a third argument: the incoming epoch (empty string == header absent). It returns the decision so the caller can stamp pending state only on fetch success (codex r2):

```go
func (h *Handler) prepareChannelContextFeed(
    agentID string, entry feeds.FeedEntry, incomingEpoch string,
) (feeds.FeedEntry, channelContextPrepareDecision, error)

type channelContextPrepareDecision struct {
    AppliedAfter   bool   // true when the URL got `after=` (today's behavior)
    Bootstrapped   bool   // true when we suppressed `after=` due to mismatch
    PriorEpoch     string // exact value the snapshot saw (may be "" — caller wraps in *string for CAS)
    IncomingEpoch  string // value to write on success
}
```

The caller in `fetchFeeds` flow:
1. call `prepareChannelContextFeed(agentID, entry, incomingEpoch)` → entry, decision
2. `feedFetcher.Fetch(...)`; if it errors, **do nothing** to pending state for epoch
3. on success, if `decision.Bootstrapped`, call `pending.SetEpoch(&decision.PriorEpoch, decision.IncomingEpoch)` — note the `&`: empty `PriorEpoch` is a real expectation, not "no CAS"
4. cursor merging from response metadata stays as today

Decision matrix inside `prepareChannelContextFeed`:

| stored epoch | incoming epoch | action                                                                | Bootstrapped | PriorEpoch (CAS expected) | NewEpoch on success |
| ------------ | -------------- | --------------------------------------------------------------------- | ------------ | ------------------------- | ------------------- |
| —            | empty          | apply `after=` from cursor map (legacy, current behavior)             | false        | n/a (cursor-only commit)  | "" (no commit)      |
| empty        | non-empty      | first-time bootstrap: skip `after=`; commit incoming epoch on 2xx     | true         | `*""`                     | incoming            |
| `e`          | empty          | apply `after=` (legacy back-compat for older Hermes)                  | false        | n/a (cursor-only commit)  | "" (no commit)      |
| `e`          | `e`            | apply `after=` (delta path)                                           | false        | n/a (cursor-only commit)  | "" (no commit)      |
| `e`          | `e2 ≠ e`       | bootstrap: skip `after=`; commit `e2` on 2xx                          | true         | `*e`                      | `e2`                |

Note: **bootstrap commits the new epoch even when the response carries no `[channel-context cursor=…]` line** (e.g., empty channel). Otherwise the next turn within the same session would bootstrap again. Implementation: caller calls `pending.SetEpoch(&decision.PriorEpoch, decision.IncomingEpoch)` on fetch success even if `metadata.Cursor` is empty. `channelCursorStore.Commit` accepts `ledgerCommitInput{ExpectedPreviousEpoch: &"", NewEpoch: e2, CursorUpdates: nil}` as a valid epoch-only commit; the CAS check rejects a stale concurrent bootstrap that arrives after the ledger already moved.

### 3. `handler.go`

`fetchFeeds` gains a fourth parameter: `incomingEpoch string`. Caller (`ServeHTTP` / `handleOpenAI` / `handleAnthropicMessages`) extracts it once at the request boundary:

```go
incomingEpoch := strings.TrimSpace(r.Header.Get("X-Claw-Consumer-Session-Epoch"))
```

Pass to `fetchFeeds`, which passes to `prepareChannelContextFeed`, calls `Fetch`, then on success applies `SetEpoch(decision.PriorEpoch, decision.IncomingEpoch)` if `decision.Bootstrapped`. The cursor-from-metadata merge stays unchanged.

Existing `pendingChannelCursorCommit.Commit` becomes:

```go
func (p *pendingChannelCursorCommit) Commit(h *Handler, agentID, model string) {
    if p == nil || h == nil || h.channelCursors == nil { return }
    if p.newEpoch == "" && len(p.updates) == 0 { return }
    in := ledgerCommitInput{
        ExpectedPreviousEpoch: p.expectedPreviousEpoch,
        NewEpoch:              p.newEpoch,
        CursorUpdates:         p.updates,
    }
    if err := h.channelCursors.Commit(agentID, in); err != nil {
        h.logger.LogError(agentID, model, 0, 0, fmt.Errorf("channel context cursor commit: %w", err))
    }
}
```

### 4. Failure modes / observability

- **No bootstrap log event in v1 (codex r2).** The cllama logger `entry` struct (`cllama/internal/logging/logger.go`) is fixed-shape; `prior_epoch_present` and `channels` are not in the schema, and the existing `intervention` field has its own semantics (model-policy intervention) that this would muddy. Adding fields means audit-test churn and a logger schema bump for marginal value at v1.
- **No reliable in-protocol bootstrap signal in v1 (codex r3).** Earlier r2 wording suggested operators could detect bootstrap by grepping `claw-wall` for channel-context requests that lack `after=`. That signal is ambiguous — it also fires for legacy clients (no header), the very first turn against a fresh ledger, and turns where the cursor map happens to be empty. We accept this gap and revisit observability when there is a second producer of similar signal.
- Bootstrap is best-effort: if the no-`after=` fetch fails upstream, we do **not** commit the new epoch. Next turn retries bootstrap. This means a transient `claw-wall` outage does not silently wedge an agent into perpetual delta-from-stale-cursor.

## Hermes side (`dockerfiles/hermes-base/`)

### 1. `entrypoint.sh`

Generate the epoch at container start, before `exec`-ing the hermes runner:

```sh
if [ -z "${CLLAMA_CONSUMER_SESSION_EPOCH:-}" ]; then
    export CLLAMA_CONSUMER_SESSION_EPOCH="$(cat /proc/sys/kernel/random/uuid)"
fi
```

Single line, no extra deps. The `if` guard lets pods pin a stable epoch for testing if they want.

### 2. `patch-hermes-runtime.py`

Add a patch that monkeypatches the **highest-level Hermes-owned factory** through which all OpenAI-compatible calls flow (codex r2 — patch Hermes, not openai-python; lower drift surface). Concrete target requires a `replace_once` against current Hermes source — to be located during implementation. Likely candidates:

- `purelib / "agent" / "llm.py"` or whichever Hermes module wraps `openai.OpenAI(...)`.
- If Hermes builds a custom transport, prefer attaching the header at the transport layer over `default_headers=` (some openai-python transports drop default_headers). Verify during impl that the chosen injection point actually emits the header on the wire — a simple unit/contract test that exercises the patched factory and inspects an outgoing request settles it.

Pattern: read `os.environ.get("CLLAMA_CONSUMER_SESSION_EPOCH", "")` at request time, attach as `X-Claw-Consumer-Session-Epoch`. Empty value means do not add the header (legacy path on cllama side stays as today).

The `replace_once` discipline (per the Hermes-base patch convention) ensures the build fails loud on upstream drift — no silent header drop.

**Anthropic format is out of scope (codex r2).** The current Hermes driver (`internal/driver/hermes/config.go::resolveModelConfig`) routes all cllama traffic through `Provider: "custom"` with `BaseURL: cllama.ProxyBaseURL(...)`, i.e. the OpenAI-compatible `/v1` base URL. There is no driver path that points Hermes at cllama's `/v1/messages`. If that changes, this PR's Hermes patch will need a sibling for the Anthropic client constructor.

### 3. Image tag bump

This is a hermes-base content change → bump `DefaultHermesBaseTag` in `internal/infraimages/release_manifest.go` **only at release time** (per `CLAUDE.md` Release Discipline). The PR for #220 leaves the tag alone and notes in the body:

> hermes-base content changed; bumping `DefaultHermesBaseTag` and publishing `ghcr.io/mostlydev/hermes-base:vX.Y.Z` is a release-time step.

## Non-Hermes consumers

Other cllama-fronted runners that hit the proxy directly (custom user runners, etc.) keep working unchanged because the missing header path is preserved. Any new runner SHOULD attach the header; we will document this in `docs/CLLAMA_SPEC.md` as part of the same PR (small section: "Session epoch (optional)" under the existing client requirements).

## Tests

### `channel_cursor_test.go`

1. `TestChannelCursorStoreEpochRoundTrip` — write epoch + cursors, reload via snapshot, both survive.
2. `TestChannelCursorStoreEpochCASApplies` — Commit with `*ExpectedPreviousEpoch == "stored"` updates the epoch; cursors continue to advance monotonically.
3. `TestChannelCursorStoreEpochCASRejects` — Commit with stale `*ExpectedPreviousEpoch` leaves the stored epoch untouched; cursor updates still merge monotonically.
4. `TestChannelCursorStoreEpochCASFromEmptyApplies` — first-time bootstrap: `*ExpectedPreviousEpoch == ""`, ledger has no epoch yet → epoch is set.
5. `TestChannelCursorStoreEpochCASFromEmptyRejectsAfterRace` (codex r3) — two commits both pass `*ExpectedPreviousEpoch == ""`; first commit wins, second commit's epoch field is ignored, but its cursor updates still merge monotonically. This is the bug the `*string` CAS fixes vs r2's plain-string sentinel.
6. `TestChannelCursorStoreEpochUnchangedWhenNewEpochEmpty` — `NewEpoch == ""` does not clear an existing epoch.
7. `TestChannelCursorStoreEpochOnlyCommit` — `CursorUpdates == nil` + non-empty `NewEpoch` + matching CAS is a valid commit (covers the empty-channel bootstrap response path).
8. `TestChannelCursorStoreCursorOnlyCommitNoCAS` — `ExpectedPreviousEpoch == nil` + cursor updates only: epoch field on disk is left alone whether it is empty or non-empty.

### `channel_context_feed_test.go`

1. Header absent + cursor stored → URL has `after=`; decision: `AppliedAfter=true, Bootstrapped=false`. (regression guard)
2. Header present + no stored epoch → URL has **no** `after=`; decision: `Bootstrapped=true, PriorEpoch="", IncomingEpoch=hdr`.
3. Header matches stored epoch + cursor stored → URL has `after=`; decision: `Bootstrapped=false`.
4. Header differs from stored epoch → URL has no `after=`; decision: `Bootstrapped=true, PriorEpoch=stored, IncomingEpoch=hdr`.

### `handler_test.go`

1. End-to-end: request with `X-Claw-Consumer-Session-Epoch` differing from stored value → upstream `claw-wall` request URL has no `after=` AND on response success, ledger reflects new epoch. Reuses the existing `weston` fixture.
2. **Fetch-failure invariant (codex r2):** request with epoch mismatch, but the upstream `claw-wall` fetch returns 5xx → ledger epoch is **unchanged**, next turn still bootstraps.
3. **Empty-channel bootstrap (codex r2 → r3 corrected):** epoch mismatch + upstream returns a valid response carrying zero `[channel-context cursor=…]` lines → ledger epoch is updated. Then, on a second prepare call with the same incoming epoch and the still-empty cursor map: `decision.Bootstrapped == false`, `decision.AppliedAfter == false` (since `encodeAfterCursors` of an empty cursor map yields empty), and no pending epoch rewrite. To prove the delta path resumes after a bootstrap that *did* learn cursors: a separate variant pre-seeds the ledger with one cursor entry and an old epoch, runs an epoch-mismatch bootstrap that returns no new cursor entries, and asserts the next prepare with matching epoch produces a URL with `after=<channel>:<seeded_id>` (epoch-only commit must leave the seeded cursor intact).

### Hermes-base contract (codex r2)

A small contract test that exercises the post-build image:

1. The image's `entrypoint.sh` exports `CLLAMA_CONSUMER_SESSION_EPOCH` to a non-empty UUID-shaped value when not pre-set.
2. The patched Hermes factory attaches `X-Claw-Consumer-Session-Epoch: $CLLAMA_CONSUMER_SESSION_EPOCH` to outgoing OpenAI-compatible requests. Test runs the patched factory in-process against a `httpx`/`httpserver` mock and asserts the header reached the wire.

Test placement: `dockerfiles/hermes-base/contract_test.py` or, if we don't already run pytest from there, a Go-level test that boots the image and inspects a captured request. Implementer's call which is more idiomatic.

### Spike (`cmd/claw/...`)

`TestSpikeRollCall` is the integration tripwire. The pod uses cllama + claw-wall; if we regress the legacy path, the spike fails. We do **not** add a new spike; the unit + handler + hermes-base contract tests above are sufficient.

## Migration / back-compat

- Empty stored epoch + non-empty incoming → first-time bootstrap, then steady-state delta path. No data migration.
- Older cllama image reading a v1 ledger written by a newer cllama with `session_epoch` field → the field is dropped on save (next save loses the field), but the agent's behavior is just one extra bootstrap on downgrade. Acceptable.
- Older Hermes against newer cllama → no header → delta path (current behavior). Operator workaround (clear `cursor.json`) still applies.
- Newer Hermes against older cllama → header is ignored upstream; delta path. No harm.

## Out of scope (deliberately)

- Per-message epoch rotation policies. Header is set once at consumer boot.
- Multi-consumer-per-agent semantics. Not supported.
- Migrating the ledger file format (no v2). The minimal additive change is enough.
- Operator-side `cursor.json` cleanup tooling. The whole point of this fix is to make that unnecessary.

## Open questions (resolved in r2)

1. ~~Header name~~ → `X-Claw-Consumer-Session-Epoch`. (codex r2)
2. ~~Returned decision struct vs widened `applied` bitfield~~ → `channelContextPrepareDecision` struct. (codex r2)
3. ~~Bootstrap log level~~ → no log emitted in v1; logger schema cost outweighs value. (codex r2)
4. ~~Counter metric~~ → out of scope; defer until there is a second producer of similar signal.
5. ~~Hermes monkeypatch on openai-python vs Hermes-owned factory~~ → patch the highest-level Hermes-owned factory; verify header survives transport. (codex r2)
