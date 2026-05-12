# Issue 232 — Bounded 24h channel awareness independent of cursors and restarts

**Author (draft):** claude (`claude:dbaba2df`) on branch `issue-232-channel-24h-awareness` — for adversarial review by codex before any implementation.

**Revision history:**
- 2026-05-12 r1 — initial draft.
- 2026-05-12 r2 — codex (`codex:27f8b72e`) r1 adversarial pass; folded in: (1) header rewrite now targets the real after-bound tail path (kind discriminator keyed on `after=` presence, not on `mode=delta` — which is no longer the default per #201); (2) explicit tool ACL/auto-subscription via compose-materialized per-agent channel allowlist; (3) v2 producer model clarified — `claw-channel-memory` emits the channel-awareness wire surface and may use #164 internals, but channel events do **not** flow through ADR-021's `/recall` in any phase; (4) v1 default flipped from opt-in to default-on with smaller per-Discord-channel caps, and #232 explicitly stays open through v2 since v1 is not acceptance-complete; (5) feed ordering enforced via compose-emitted manifest order with regression test; (6) retrieval out-of-buffer semantics spelled out (structured `not_in_buffer` response with retained-coverage range).
- 2026-05-12 r3 — codex r2 adversarial pass; folded in: (a) auth model rewritten — claw-wall does **not** validate bearer auth on `/channel-context`; feeds remain generated-URL-only on the compose network; tools are strictly cllama-mediated with cllama validating agent identity + per-agent channel allowlist before forwarding to claw-wall (claw-wall enforces defense-in-depth via a service token + `X-Claw-ID` header); (b) tool auto-registration spelled out — compose-up emits a synthetic `claw.describe` v2 descriptor on the auto-injected claw-wall service that routes through the existing extraction path; (c) #220 epoch-bootstrap path gets its own `kind=bootstrap_tail` via a cllama-passed `context_kind=` URL hint; (d) ACL allowlist switched from env-var to a mounted JSON config file (`claw-wall-agent-channels.json`); claw-wall recreates on config change, no hot reload in v1; (e) `channel_context_op` telemetry spec'd explicitly for both feed and tool paths; (f) stale text scrubbed (§4.1 caps, §6 ">120 messages" wording, §7.5 spike vs integration, §4.1 "mode=delta header rewrite" residue).

**Issue:** https://github.com/mostlydev/clawdapus/issues/232

**Predecessors:**
- #201 (channel-context tail mode, v0.13.7) — flipped delta→tail as default, dual cap.
- #204 (cursored append-only context deltas) — late-context placement, no first-system mutation.
- #220 (channel-cursor session epoch, v0.14.7) — restart bootstrap via `X-Claw-Consumer-Session-Epoch`.

**Sibling:** #164 (salience-memory adapter) — owns LLM-derived compaction. This plan does **not** duplicate that work; v2 of #232 consumes #164's adapter.

## 1. Problem (one paragraph)

#220 fixed *across-restart* cursor bootstrap. #201 fixed tail vs delta semantics. The remaining failure mode is *within an active session*: the agent's cursor legitimately advances past a same-day floor message, then a human prompt refers to that older message, and the agent answers "I don't see it in the last 24h" because its prompt only contains the post-cursor delta. The live Tiverton incident (May 11, 2026, boulton on `#trading-floor`) is the canonical case: Logan posted a CMCSA read at 07:47 ET, boulton's cursor advanced normally, and at 09:50 ET the operator asked "wanna pick up CMCSA on Logan's read?" — boulton's prompt contained only the immediate ping thread because the cursor was already past Logan's message.

This is not a cursor bug. It is a missing *awareness layer*: cursorized delta is the right product for live continuity but the wrong product for "what does this agent know about the last 24h of the room?".

## 2. Contract (the four parts from issue #232 comment r2)

The operator's acceptance contract has four parts. They are not independently dispensable; they compose into "guaranteed bounded 24h awareness":

1. **Raw recent window** — exact messages for immediate continuity, bounded.
2. **Rolling 24h digest** — LLM-derived, source-backed compaction of older same-day material, regenerated under buffer churn and after restart.
3. **Retrieval path** — explicit fetch of exact source messages by author/ticker/time/id, so a digest can cite without restating.
4. **Provider-visible metadata** — every channel-context block in the prompt declares whether it is `delta` / `raw_window` / `digest`, with retained-coverage so the model never claims "I don't see the last 24h" when only a delta was injected.

## 3. Phasing

The plan splits along a hard dependency: part (2) needs a memory-grade producer, and the closest fit is #164's salience-memory adapter. v1 ships parts (1), (3), (4) and an explicit stopgap label on (2). v2 retires the stopgap once #164 lands.

| Part | v1 producer | v2 producer | Wire surface in both phases |
|------|-------------|-------------|------------------------------|
| (1) Raw recent window | `claw-wall` (new `channel-awareness` endpoint, uncursored, dual-capped) | unchanged | feed: `channel-awareness` |
| (2) Rolling 24h digest | **stopgap: omitted** with explicit metadata label `digest=unavailable`; busy rooms rely on retrieval | `claw-channel-memory` service (new) — subscribes to claw-wall events, builds rolling digest (may use #164 salience-memory internals as a library, but channel events do **not** transit ADR-021 `/recall`); emits the **same** `channel-awareness` wire surface with `digest=ready` and an appended digest section | feed: `channel-awareness` (composite block, same name) |
| (3) Retrieval path | `claw-wall` exposes managed tools (ADR-020) `search_channel_context` and `get_channel_messages`, gated by per-agent channel allowlist (see §4.2) | `claw-channel-memory` may offer richer retrieval (e.g., digest-cited source pull); v1 tools remain | `tools[]` |
| (4) Metadata | header-line discipline keyed on `after=` presence (see §4.4), new `channel_context_op` telemetry distinguishing `delta_tail` / `raw_window` / `digest` / `tool_call` | unchanged | feed header + telemetry |

The v1 channel-awareness block is **not** rebranded as "the 24h fix". The plan explicitly labels it as bounded raw-window + retrieval; full 24h coverage under busy-room load is only guaranteed once digest lands. **#232 itself stays open through v2** (codex r1) — v1 ships a meaningful improvement but is not acceptance-complete against the issue body's r2 contract.

**Architectural invariant (codex r1):** in no phase do channel events flow through ADR-021's per-agent `/recall`. `/recall` payload is `messages[]`/`system`/metadata — agent/session shaped, not room-shaped. Channel awareness is its own producer/consumer pair with its own wire surface. #164's salience-memory adapter may be used as an internal library by `claw-channel-memory` (sharing LLM prompts, dedupe heuristics, etc.) but the two adapters do not share the recall/retain wire endpoints.

## 4. Wire surfaces

### 4.1 New feed: `channel-awareness` (v1, producer = claw-wall)

```
GET /channel-awareness?channels=<csv>&since=24h&limit=60&max_chars=8192&context_kind=raw_window
```

- **No cursor**. No `consumer=` param. No `mode=delta` path. This feed exists precisely to be cursor-independent.
- Same channel-authorization model as `channel-context` (caller is upstream-restricted to surface-derived channels), PLUS the per-agent ACL of §4.2.1 (the same allowlist guards both feed and tool paths).
- Reuses `consumeTail` semantics from #201 (newest-first walk, dual cap, sort ASC for output) but rebranded.
- v1 defaults (r2): `since=24h`, `limit=60`, `max_chars=8192` (8KB, aligned with cllama's `MaxFeedResponseBytes` so we don't double-truncate). Operator-tunable via `x-claw.context.channel-awareness` (parallel of #201's `x-claw.context.channel`).
- Generated URL example: `/channel-awareness?channels=14645…,14647…&since=24h&limit=60&max_chars=8192&context_kind=raw_window`.
- Header line is explicit about layer kind, exactly so the model cannot conflate it with a delta:

```
[channel-awareness] kind=raw_window since=24h messages=87 range=2026-05-11T05:42Z..2026-05-12T05:42Z channels=14645…,14647… retained=87/since-24h digest=unavailable
```

`digest=unavailable` in v1 documents the known gap. In v2 the same feed emits `digest=ready` and a digest section is appended below the raw window.

`channel-context` (the existing delta feed) keeps its `mode=tail` request shape from #201 verbatim. #232 lands a **header rewrite** keyed on cllama's per-fetch `context_kind=` URL hint (or, as a fallback, the presence of `after=`). See §4.4 for the full kind matrix including the `#220` epoch-bootstrap path.

### 4.2 New tools: retrieval (v1, producer = claw-wall, ADR-020 managed)

`claw.describe` v2 on the `claw-wall` image declares two managed tools:

```yaml
tools:
  - name: search_channel_context
    description: "Search recent channel buffer (up to retained-coverage window) for messages matching a query."
    params:
      channels: { type: array, items: string, required: true }
      query: { type: string, required: true }
      since: { type: string, default: "24h" }
      author: { type: string, required: false }
      limit: { type: integer, default: 20 }
    http: { path: /search_channel_context }
  - name: get_channel_messages
    description: "Fetch exact channel messages by ID range, or by author+time window."
    params:
      channels: { type: array, items: string, required: true }
      message_ids: { type: array, items: string, required: false }
      after: { type: string, required: false }    # snowflake id or ISO ts
      before: { type: string, required: false }
      limit: { type: integer, default: 20 }
    http: { path: /get_channel_messages }
```

Both are **mediated** by cllama (ADR-020). Mediation is what makes the auth model work — see §4.2.1.

#### 4.2.1 Auth and per-agent channel ACL (codex r1+r2 must-fix)

**Current state (must not be misrepresented):** `claw-wall` today does **not** validate bearer auth on `/channel-context`. Auto-generated feed URLs are safe only because (a) `claw-wall` is reachable only on the compose Docker network, and (b) compose-up writes surface-derived channel IDs into the URL, so the request itself names exactly what the agent legitimately has access to. There is no token check.

**v1 contract:**

| Endpoint | Caller | Auth on claw-wall | Channel ACL site |
|----------|--------|-------------------|------------------|
| `/channel-context` (delta feed) | cllama fetcher with generated URL | none (network-scoped trust) | URL is single-writer compose-emitted |
| `/channel-awareness` (raw window feed) | cllama fetcher with generated URL | none (network-scoped trust) | URL is single-writer compose-emitted |
| `/search_channel_context` (tool) | cllama-mediated, agent on model side | service token (Bearer) + `X-Claw-ID` header (the validated agent) | cllama enforces; claw-wall re-checks (defense-in-depth) |
| `/get_channel_messages` (tool) | cllama-mediated, agent on model side | service token (Bearer) + `X-Claw-ID` header | cllama enforces; claw-wall re-checks |

The model never speaks to claw-wall directly. Tool calls go: model → cllama validates agent bearer (existing per-agent token from ADR-015 / claw-api auth pattern) → cllama checks per-agent channel allowlist → cllama forwards to claw-wall with the claw-wall service token + `X-Claw-ID: <agentID>` → claw-wall validates service token, re-checks `X-Claw-ID` against its mounted allowlist, runs the query.

**Where the allowlist lives:** compose-up materializes a single JSON config at `claw up` time (codex r2: prefer file over env var) and mounts it read-only into `claw-wall`:

```json
// claw-wall-agent-channels.json (mounted at /etc/claw-wall/agent-channels.json)
{
  "version": 1,
  "agents": {
    "boulton-0": ["1464509330731696213", "1464796137893662843"],
    "logan-0":   ["1464509330731696213"]
  }
}
```

The same allowlist is also projected into each agent's cllama context dir (`channels-allowlist.json`) so cllama can pre-check before forwarding. Two-lock model: cllama is the primary gate (correctness), claw-wall is the defense-in-depth (containment if cllama is bypassed or misconfigured).

**No hot reload in v1** (codex r2): claw-wall reads the file at startup. `claw up` recreates the claw-wall container when the generated config changes (compose detects the file-mount diff). Operators who add a channel surface mid-life must `claw up` to re-materialize.

**claw-wall service token:** compose-up generates a per-pod service token at `claw up` time (same pattern as the `claw-api` token from #44/`x-claw.master`). The token is mounted into cllama's per-agent context as part of the tool's auth descriptor (existing ADR-020 path) and into claw-wall's env as `CLAW_WALL_TOOL_TOKEN`. claw-wall validates incoming tool requests have `Authorization: Bearer <CLAW_WALL_TOOL_TOKEN>`; without it, 401.

**Tool request handler order:**
1. Validate `Authorization` header against `CLAW_WALL_TOOL_TOKEN`. Miss → 401 `{"error":"unauthenticated"}`.
2. Read `X-Claw-ID: <agentID>` header. Miss → 400 `{"error":"missing_agent_id"}`.
3. Confirm `agentID` exists in `agents` map of the mounted JSON. Miss → 403 `{"error":"unknown_agent"}`.
4. Parse requested `channels[]` from body.
5. Reject any channel not in `agents[agentID]` → 403 `{"error":"channel_not_allowed", "agent":"...", "channel":"..."}`.
6. Pass to store with validated channel set.

cllama surfaces the 403 case to the model with a short hint ("This agent has no surface to channel X; ask the operator to add it.").

#### 4.2.2 Tool auto-registration (codex r2 must-fix)

Auto-injected `claw-wall` is not a user-declared `x-claw` provider, so the existing descriptor extraction path needs an explicit hook. The mechanism:

1. compose-up generates the `claw-wall` service as today (including image, env, healthcheck).
2. compose-up additionally writes a **synthetic `claw.describe` v2 descriptor** to the runtime artifact dir (`.claw-runtime/describe/claw-wall.json`) declaring the two tool entries with their HTTP paths and `auth: {type: bearer, env: CLAW_WALL_TOOL_TOKEN}`.
3. compose-up adds a label to the claw-wall service: `claw.describe.path=/etc/claw-wall/describe.json`, and bind-mounts the synthetic descriptor file at that path. This routes auto-injected claw-wall through the existing `internal/describe/` extraction code path without a new code branch — the descriptor extractor reads from image labels OR from the filesystem-fallback path that already exists for build-context descriptors.
4. The normal compile pipeline (`internal/describe/registry.go`) projects the two tool entries into the `tools.json` of each agent whose `x-claw` consumes a channel surface (the same auto-subscription trigger that injects `claw-wall` itself).
5. Per-agent `tools.json` gets the standard ADR-020 mediated tool entry shape, with `http.base_url=http://claw-wall:8080`, `http.path=/search_channel_context` (or `/get_channel_messages`), and `auth.token=<service-token-value>`.

This means **no new code in the describe extractor or tool-manifest compiler** — we use the existing label + filesystem-fallback path, and the new code is confined to compose-up (synthetic descriptor emission) and claw-wall (the two new HTTP handlers + auth).

Regression test (§7.3 `TestComposeMaterializesAutoRegisteredToolsForChannelConsumers`): a pod with two agents — one consuming a Discord channel surface, one not — produces `tools.json` for the first agent containing both `search_channel_context` and `get_channel_messages` entries with the correct base URL and bearer token; the second agent's `tools.json` contains neither.

#### 4.2.3 Auto-subscription trigger

Tools are auto-registered for any service whose `x-claw` consumes channel surfaces (i.e., the same condition that auto-injects `claw-wall`). No separate pod knob. Rationale: if the agent has channel surfaces declared, it already needs the tools to recover from cursor/buffer-bound gaps. Forcing operators to opt-in twice (channel surface + retrieval tool) is friction without value.

#### 4.2.4 Out-of-buffer semantics (codex r1 must-fix)

claw-wall can only search/return messages currently in its in-process ring buffer (size from `CLAW_WALL_LIMIT`, default 50, configurable up to operator-set ceiling). When a query falls outside that window — either by `since` reach or by `message_ids` referring to evicted ids — the response is structured:

```json
{
  "messages": [],
  "retained_coverage": {
    "oldest_ts": "2026-05-12T01:14Z",
    "newest_ts": "2026-05-12T05:42Z",
    "buffer_size": 500
  },
  "status": "not_in_buffer",
  "hint": "Requested window extends before buffer's oldest_ts. Operator can widen CLAW_WALL_LIMIT or rely on v2 digest once available."
}
```

`status: "ok"` for normal hits, `"empty"` for in-buffer-but-no-match, `"not_in_buffer"` for the eviction case. cllama renders this as a tool response with header `[channel-tool] kind=tool_call name=... status=not_in_buffer retained_coverage=...` so the model can see explicitly that the gap is buffer-bound, not absence-of-message.

Tool responses include the same `kind=tool_call source=claw-wall channels=…` metadata header as the feed, so the model treats tool output and feed injection symmetrically. Every returned message carries its Discord snowflake `id` so the model can chain `search_channel_context` → `get_channel_messages` deterministically.

### 4.3 Late-context placement and feed ordering (per #204)

The `channel-awareness` block is appended to the late runtime-context system message (OpenAI) or final user content block (Anthropic), alongside the existing `channel-context` delta block and the memory recall block.

Order inside the late block (deterministic for cache-friendliness):

1. memory recall (existing, from ADR-021)
2. `channel-awareness` (new — broadest layer, anchors the model in the last 24h)
3. `channel-context` delta (existing, narrowest layer, fresh post-cursor)
4. current time line (existing)

Rationale: digest/awareness first sets the model's frame of "what the room knows"; delta then signals "what's new since you last looked"; time anchors the moment. Reversing this lets a stale-but-large window dominate fresh signal.

#### 4.3.1 Manifest-order enforcement (codex r1 must-fix)

cllama's `fetchFeeds` / `FormatAllFeeds` (`cllama/internal/feeds/inject.go`) iterate the feed manifest in order. There is no sort step today. The r1 plan said "order inside the late block" without saying who enforces it; codex flagged this. v1 enforces ordering at the **compose-up emission** side:

- `appendConversationWallAwarenessFeed` emits the awareness entry **before** `appendConversationWallFeed` emits the delta entry, both immediately after any memory recall slot.
- Regression test `TestConversationWallFeedsEmittedInAwarenessBeforeDeltaOrder` asserts the manifest order matches the §4.3 list.
- A cllama-side deterministic sort for channel-flavored feeds is **deferred** (would require feed-name introspection in `inject.go`). Manifest-order discipline is sufficient because compose-up is the single writer.

### 4.4 Header discipline (part 4 of contract)

Every channel-flavored block carries an explicit `kind=` field in its header. The implicit assumption "channel-context = full room" is the bug we are killing.

**Discriminator (codex r1+r2 must-fix):** the kind is **passed explicitly by cllama via a `context_kind=` URL query param**, not inferred from `after=` presence alone. r2 made the case: claw-wall cannot distinguish "first/cold tail" from "#220 epoch-bootstrap tail" from the URL alone — both arrive without `after=`, but the meanings differ (the bootstrap case means cllama *intentionally* suppressed a stored cursor due to an epoch change). cllama owns that distinction in `channelContextPrepareDecision` (`Bootstrapped`, `AppliedAfter`), so cllama stamps the URL accordingly.

cllama-side mapping (`cllama/internal/proxy/channel_context_feed.go::prepareChannelContextFeed`):

| Decision state | Appended URL param |
|----------------|--------------------|
| `AppliedAfter == true` | `context_kind=delta_tail` (also keeps `after=`) |
| `Bootstrapped == true` (epoch change suppressed cursor) | `context_kind=bootstrap_tail` |
| neither (first turn, empty cursor, legacy) | `context_kind=tail` |

For the new `channel-awareness` feed, compose-up writes `context_kind=raw_window` directly into the URL (no cllama logic needed; awareness is always raw_window in v1).

claw-wall's response formatter reads `context_kind` and emits the corresponding header line. If the param is missing (older cllama against newer claw-wall), claw-wall falls back to `delta_tail` if `after=` is present, `tail` otherwise. The fallback is intentional for back-compat but never used by current cllama.

| `context_kind` | Block kind | Header line |
|----------------|------------|-------------|
| `delta_tail` | `delta_tail` | `[channel-context delta] kind=delta_tail cursor=<ch>:<id>,...` |
| `bootstrap_tail` | `bootstrap_tail` | `[channel-context bootstrap] kind=bootstrap_tail reason=epoch_changed` |
| `tail` | `tail` | `[channel-context] kind=tail` |
| `raw_window` | `raw_window` | `[channel-awareness] kind=raw_window since=...` |
| `raw_window+digest` (v2) | composite | `[channel-awareness] kind=raw_window+digest since=... digest_source_count=...` |
| (tool response) | `tool_call` | `[channel-tool] kind=tool_call name=... status=...` |

v2 of #204's `coverage_partial=true` annotation is preserved verbatim across all kinds.

**Back-compat note:** model prompts that contained the old `[channel-context]` header for delta will now see `[channel-context delta] kind=delta_tail` once v1 ships. This is the intended behavior — the old prefix was the bug. No tool/agent code parses this header; only the model reads it.

### 4.5 Telemetry (part 4 of contract)

New normalized event kind: `channel_context_op` emitted on the cllama side (which already has the agent/pod identity and request context). It is **not** an existing event renamed — the audit/session-history paths today use distinct `feed_fetch` and `memory_op` events; tool calls flow through tool-trace normalization. r2 raised the concern that "fold into existing tool_call_result" assumed an event that doesn't exist. v1 lands `channel_context_op` as a new normalized record alongside `feed_fetch` and `memory_op`.

Schema (cllama records, claw-api surfaces):

```json
{
  "type": "channel_context_op",
  "agent_id": "boulton-0",
  "pod": "tiverton-house",
  "kind": "raw_window | delta_tail | bootstrap_tail | tail | tool_call | digest_built | digest",
  "channels": ["1464…", "1464…"],
  "retained": 60,           // messages in claw-wall's window that matched
  "returned": 47,           // after caps
  "omitted": 13,            // retained - returned
  "latency_ms": 24,
  "source": "claw-wall",    // or "claw-channel-memory" in v2
  "status": "ok | empty | not_in_buffer | error",
  "tool_name": "search_channel_context"  // present only for kind=tool_call
}
```

Emission sites:
- every `channel-awareness` fetch → `kind=raw_window`
- every `channel-context` fetch → `kind` mirrors the cllama-side `context_kind=` decision (`delta_tail` / `bootstrap_tail` / `tail`), so the audit view shows exactly which path served each turn
- every retrieval tool call → `kind=tool_call`, `status` reflects the §4.2.4 enum, `tool_name` is the called tool
- v2: digest production → `kind=digest_built`; digest injection → `kind=digest`

The retrieval `status` is also embedded in the session-history tool-trace record (per r2: agents/audits that look at session history can see "the tool was called, it returned not_in_buffer" without re-correlating two streams). The cllama tool-trace shim writes `status` into the existing trace JSON alongside the request/response pair.

`claw audit` surfaces `channel_context_op` alongside `memory_op` and `feed_fetch`.

## 5. Compose-up wiring

Pod-level config extends #201's `x-claw.context.channel` with a sibling block:

```yaml
x-claw:
  context:
    channel:                    # existing delta feed (from #201)
      since: 24h
      limit: 40
      max_chars: 8192
    channel-awareness:          # NEW — defaults applied automatically; pod block only needed for overrides
      since: 24h
      limit: 60                 # default (r2: tightened from 120)
      max_chars: 8192           # default (r2: tightened from 12288)
```

Service-level overrides via the same pod-defaults pattern (`x-claw.context.channel-awareness` on individual services). There is **no** `enabled` knob — see §8; the feed is emitted automatically when the service consumes any channel surface, matching the auto-injection of `claw-wall` itself.

`appendConversationWallFeed` (in `cmd/claw/compose_up.go`) gains a sibling `appendConversationWallAwarenessFeed` that emits `/channel-awareness?channels=...&since=...&limit=...&max_chars=...&context_kind=raw_window` whenever the service has any consumed channel IDs. Three additional emissions:

1. The synthetic `claw.describe` v2 descriptor at `.claw-runtime/describe/claw-wall.json` declaring the two retrieval tools (per §4.2.2). The claw-wall service gets a `claw.describe.path` label and a bind-mount for that file.
2. The per-agent channel allowlist at `.claw-runtime/claw-wall-agent-channels.json` (per §4.2.1). Bind-mounted into claw-wall at `/etc/claw-wall/agent-channels.json`.
3. The per-agent allowlist is also projected into each agent's cllama context dir as `channels-allowlist.json` so cllama can pre-check before forwarding tool calls.

claw-wall reloads neither file at runtime in v1: a generated-config change triggers compose recreate-on-diff (Docker compose hashes mounted file content), so `claw up` after any pod edit gives claw-wall the fresh allowlist. No SIGHUP path needed.

## 6. Stopgap discipline (codex challenge #2)

This plan **must not** ship as "the 24h fix" without phase 2. The framing in the PR body, the release note, and the on-call runbook reads:

> v1 of #232 ships bounded raw-window awareness, retrieval tools, and metadata. **Busy rooms — >60 messages OR >8KB of message content in 24h — will exceed the v1 dual cap.** When that happens, the agent will see the most recent N messages plus retrieved older ones on demand; full 24h compaction is delivered in v2 once #164's salience-memory adapter lands. The `digest=unavailable` header line documents this in-prompt so the model can decide whether to call retrieval.

The acceptance criterion in the issue body — "later Boulton turn still includes Logan's CMCSA read in the 24h awareness layer without requiring the human to restate it" — is **partially satisfied** in v1: Logan's read is reachable via retrieval (and likely also visible in the raw 24h window for a not-too-busy floor), but not guaranteed-injected. v2 closes the gap.

## 7. Tests

### 7.1 claw-wall (`cmd/claw-wall/`)

- `TestChannelAwarenessHandlerReturnsRawWindow` — `since=24h`, no cursor write, header reads `kind=raw_window`, `digest=unavailable` in v1.
- `TestChannelAwarenessHandlerDualCap` — 200-message buffer, `limit=50&max_chars=…` → returns newest 50 sorted ASC, header shows `retained=50/...`.
- `TestChannelAwarenessHandlerColdStart` — buffer holds 10 minutes worth → header reads `retained=N/buffer range=...` honest about coverage.
- `TestChannelAwarenessHandlerDoesNotMutateCursor` — interleave with `channel-context` delta consumer; awareness fetch leaves delta cursor untouched.
- `TestChannelContextHeaderByContextKindParam` — three requests on the same handler with `context_kind=delta_tail` / `bootstrap_tail` / `tail` produce the three distinct header lines from §4.4. (codex r2 must-fix)
- `TestChannelContextHeaderFallbackInfersFromAfter` — when `context_kind=` is missing, presence of `after=` yields `delta_tail`, absence yields `tail`. Back-compat for older cllama.
- **Tool auth tests (codex r2 must-fix):**
  - `TestToolRequestRejectsMissingBearer` — no Authorization header → 401.
  - `TestToolRequestRejectsWrongServiceToken` — Bearer present but wrong value → 401.
  - `TestToolRequestRejectsMissingClawID` — Bearer ok, no `X-Claw-ID` → 400.
  - `TestToolRequestRejectsUnknownAgent` — Bearer ok, `X-Claw-ID` not in mounted allowlist → 403.
  - `TestToolRequestRejectsDisallowedChannel` — Bearer ok, agent known, requested channel not in agent's allowlist → 403 with `{"error":"channel_not_allowed", ...}`.
  - `TestToolRequestAcceptsAllowedAgentAndChannel` — full happy path → 200.
- `TestToolRequestLoadsAllowlistFromMountedFile` — claw-wall reads `/etc/claw-wall/agent-channels.json` at startup; a malformed file produces a startup failure (fail-closed).
- `TestRetrievalToolNotInBufferStatus` — buffer has 10min; tool query with `since=12h` returns `status=not_in_buffer` + `retained_coverage` shape per §4.2.4.
- `TestRetrievalToolEmptyVsNotInBuffer` — distinguishes `status=empty` (in-buffer, no match) from `status=not_in_buffer` (out-of-buffer).
- `TestGetChannelMessagesByIDRange` — fetch by id range returns exact messages, ordered, each carrying its snowflake id.

### 7.2 cllama (`cllama/internal/proxy/`)

- `TestChannelAwarenessFeedIsFetchedWithoutCursor` — register a `channel-awareness` feed entry; assert URL has no `after=` and `X-Claw-Consumer-Session-Epoch` is **not** consulted for it (orthogonal to #220's epoch).
- `TestChannelAwarenessAndDeltaCoExist` — pod wires both feeds; both blocks appear in the late-context section, in the §4.3 order, with their respective `kind=` headers.
- **`context_kind=` stamping tests (codex r2 must-fix):**
  - `TestPrepareChannelContextStampsDeltaTail` — `AppliedAfter == true` → URL gains `context_kind=delta_tail`.
  - `TestPrepareChannelContextStampsBootstrapTail` — `Bootstrapped == true` (epoch mismatch) → URL gains `context_kind=bootstrap_tail` and **no** `after=`.
  - `TestPrepareChannelContextStampsTail` — neither (first turn, empty cursor) → URL gains `context_kind=tail`.
- `TestChannelContextHeaderFlowsThroughCllama` — claw-wall returns `[channel-context delta] kind=delta_tail`; cllama formats into late context without mutation.
- `TestToolMediationChecksChannelAllowlist` — cllama loads per-agent `channels-allowlist.json`; mediated tool call with disallowed channel is rejected by cllama before forwarding (returns a tool error to the model with hint per §4.2.1).
- `TestToolMediationForwardsValidatedRequest` — happy path: cllama forwards with `Authorization: Bearer <CLAW_WALL_TOOL_TOKEN>` and `X-Claw-ID: <agent>`; mocked claw-wall sees both headers.
- `TestChannelContextOpTelemetryNormalized` — fetch each kind, assert `audit.go`-style normalized records for `kind=raw_window`, `kind=delta_tail`, `kind=bootstrap_tail`, `kind=tail`, `kind=tool_call` with `status`.

### 7.3 compose_up (`cmd/claw/`)

- `TestAppendChannelAwarenessFeedHonorsPodConfig` — pod-level `x-claw.context.channel-awareness` overrides flow into generated URL.
- `TestChannelAwarenessFeedDefaultOnWithChannelSurface` — service consumes a Discord channel surface; pod has no explicit `channel-awareness` block; feed entry IS emitted with default `since=24h&limit=60&max_chars=8192&context_kind=raw_window`. (codex r1, default-on)
- `TestChannelAwarenessFeedAbsentWithoutChannelSurface` — service has no channel surface; no awareness feed entry emitted.
- `TestConversationWallFeedsEmittedInAwarenessBeforeDeltaOrder` — manifest-order regression: when both feeds are emitted, awareness appears before delta in the materialized `feeds.json`. (codex r1 must-fix §4.3.1)
- `TestComposeMaterializesAgentChannelAllowlistJSON` — when claw-wall is auto-injected, the runtime dir contains `claw-wall-agent-channels.json` with the per-agent allowlist, bind-mounted at `/etc/claw-wall/agent-channels.json`. (codex r2 must-fix §4.2.1)
- `TestComposeProjectsAllowlistIntoCllamaContext` — each consuming agent's cllama context dir contains `channels-allowlist.json` mirroring the same surface-derived channels.
- `TestComposeEmitsSyntheticClawWallDescriptor` — `.claw-runtime/describe/claw-wall.json` exists with the two tool entries; the claw-wall service has a `claw.describe.path` label pointing at the bind-mounted descriptor. (codex r2 must-fix §4.2.2)
- `TestComposeMaterializesAutoRegisteredToolsForChannelConsumers` — pod with two agents (one channel-consuming, one not); the channel-consuming agent's `tools.json` contains both `search_channel_context` and `get_channel_messages` with `http.base_url=http://claw-wall:8080` and the service token in `auth.token`; the non-consumer's `tools.json` contains neither.
- `TestComposeEmitsClawWallToolTokenEnv` — auto-injected claw-wall service env contains `CLAW_WALL_TOOL_TOKEN=<generated>`; the same value appears in the consuming agent's `tools.json` auth descriptor.

### 7.4 Pod parser (`internal/pod/`)

- `TestParseChannelAwarenessConfig` — accepts `since` / `limit` / `max_chars`; rejects negatives; rejects unparseable durations. No `enabled` field (default-on).
- `TestPodDefaultsChannelAwareness` — service-level overrides pod defaults using the standard pattern.

### 7.5 Integration (no spike)

Codex r1+r2: plain integration tests against a faked claw-wall are sufficient; spike-tag inflates CI cost and requires real provider credentials this scenario does not need.

- `TestChannelAwarenessEndToEndAgainstFakeClawWall` (in `cmd/claw/` or `cllama/internal/proxy/`) — a real cllama instance fronting a faked claw-wall (httptest server). Pod wires both `channel-context` and `channel-awareness` feeds. Assert the cllama-produced upstream request contains both header lines (`kind=delta_tail` from delta, `kind=raw_window` from awareness) in the late-context block in the §4.3 order.
- `TestTivertonReproductionFixture` (in `cmd/claw/` or as a fixture-driven harness test) — the issue-body scenario, fully reproducible without external services:
  1. Pre-seed the fake claw-wall buffer with messages spanning a 24h window, including a "Logan CMCSA read" at T-2h.
  2. Run cllama turn #1 with a routine prompt; the `channel-context` delta advances the cursor past Logan's message.
  3. Run cllama turn #2 with a human-mention prompt ("wanna pick up CMCSA on Logan's read?").
  4. Assert the upstream request for turn #2 contains the Logan message in the `[channel-awareness] kind=raw_window` block, even though the `[channel-context delta] kind=delta_tail` block has only post-cursor traffic.
  5. Assert the per-agent allowlist enforcement: the same turn issuing a `search_channel_context(channels=["<other-channel>"])` tool call is rejected by cllama before forwarding.

`TestSpikeRollCall` is **not** extended; it remains the live-Docker validation for cllama proxy enforcement and stays scoped to existing concerns.

## 8. Default behavior and ramp (revised r2)

**Codex r1 made the v1-opt-in posture untenable:** if v1 is opt-in and explicitly partial, then operators who don't flip the knob get nothing, and #232 closes on a stopgap that pods haven't actually adopted. The new default is **on**, with smaller caps tuned to Discord channel density, and `digest=unavailable` honestly documenting the gap in v1.

v1 defaults (active when the service consumes any channel surface, no explicit opt-in needed):

| Knob | v1 default | r1 draft | Rationale |
|------|-----------|----------|-----------|
| `enabled` | `true` (implicit) | `false` (explicit opt-in) | codex r1: opt-in fails the issue's acceptance contract |
| `since` | `24h` | `24h` | unchanged |
| `limit` | `60` | `120` | r1 caps were generous; smaller default reflects "raw window + retrieval pressure valve" model |
| `max_chars` | `8192` (8KB) | `12288` (12KB) | aligned with cllama's `MaxFeedResponseBytes` so we don't risk double-truncation |

Per-pod overrides still available via `x-claw.context.channel-awareness`. Operators with low-density rooms can raise the caps; high-traffic rooms keep the smaller defaults and lean on retrieval until v2.

**#232 stays open through v2.** Per codex r1: v1 is a meaningful improvement (raw-window + retrieval + metadata) but does not satisfy the issue body's r2 acceptance contract ("guaranteed 24h awareness regardless of session state and trigger"). The PR that lands v1 is titled `#232 phase 1: raw-window + retrieval + metadata` and closes #232 only when v2 (digest via `claw-channel-memory`) ships.

The PR body explicitly says:
> Phase 1 of #232. Lands the raw-window feed, retrieval tools, and metadata header discipline; busy rooms still rely on retrieval to close gaps. Phase 2 (rolling digest via `claw-channel-memory`) lands when #164 is far enough along to share its salience primitives. #232 stays open through both phases.

## 9. Out of scope (deliberately)

- **Cross-channel digest correlation.** v1 and v2 both keep channel-awareness scoped per-channel within a single feed call. Cross-channel synthesis is a future memory adapter feature.
- **Affect/salience scoring** of channel messages. Same #164 territory.
- **Discord history backfill** beyond claw-wall's in-process buffer. Per #201 §3 Q3, the wider-continuity feeds cover cold-start. Operators who need a true 24h window today can raise `CLAW_WALL_LIMIT`/buffer; v2's digest reduces the urgency further.
- **Per-channel awareness/delta toggles.** v1 keeps the pod/service knob granularity from #201.
- **Anthropic `cache_control` markers** for awareness blocks. Same deferral as #204 v2.

## 10. r1 open questions — codex answers and r2 resolution

(Original five questions kept for traceability; codex r1 answers and r2 outcome inline.)

1. **Composite vs co-injection.** r1 leaned two-feed.
   - **Codex r1:** two-feed OK for v1 *if* rendered/ordered as one coherent channel layer.
   - **r2 resolution:** two feeds (`channel-awareness`, `channel-context`) with compose-emitted manifest order enforced (§4.3.1). Telemetry treats them as one logical layer via `channel_context_op`. v2 can collapse to one feed if useful but is not required.

2. **Header rewrite on `channel-context`.** r1 said "change to `[channel-context delta] kind=delta`".
   - **Codex r1 must-fix:** rewrite targeted the wrong path (`mode=delta` is no longer the default since #201). The real delta path is `mode=tail` + `after=`.
   - **r2 resolution:** rewrite is keyed on `after=` presence. See §4.4 for the four kinds (`delta_tail`, `tail`, `raw_window`, `tool_call`).

3. **v1 retrieval tools live where?** r1 leaned claw-wall-direct.
   - **Codex r1:** claw-wall-direct OK *only after* ACL/auto-subscribe is solved.
   - **r2 resolution:** claw-wall-direct with §4.2.1 per-agent channel allowlist materialized at compose time + §4.2.2 auto-subscription. ACL is the gating condition; not a separate phase.

4. **v1 default opt-in vs opt-out.** r1 leaned opt-in for token cost discipline.
   - **Codex r1:** opt-in means v1 is not acceptance-complete; either split issues or make default-on with smaller caps.
   - **r2 resolution:** default-on with smaller caps (`limit=60`, `max_chars=8192`), and #232 stays open through v2 (§8). The smaller caps keep token-cost discipline without requiring operators to flip a knob.

5. **Spike test scope.** r1 leaned plain integration over spike-tagged.
   - **Codex r1:** plain integration, fake claw-wall, no spike-tag.
   - **r2 resolution:** confirmed — §7.5 targeted reproduction is a normal integration test.

## 10b. r2 open questions — codex answers and r3 resolution

1. **CLAW_WALL_AGENT_CHANNELS env shape.** r2 leaned env-var.
   - **Codex r2:** prefer JSON config file mounted into claw-wall (less fragile, future reload/diagnostic friendly). No hot reload needed; `claw up` recreates claw-wall on config change.
   - **r3 resolution:** switched to `claw-wall-agent-channels.json` bind-mounted at `/etc/claw-wall/agent-channels.json` (§4.2.1, §5). Also projected into cllama context as `channels-allowlist.json` for cllama-side pre-check.

2. **Tool error rendering.** r2 wanted `status` separate from generic tool_call_result.
   - **Codex r2:** there is no existing `tool_call_result` event. Don't assume. Add explicit `channel_context_op` normalization for feed/tool ops; embed retrieval status in session-history tool-trace as structured data.
   - **r3 resolution:** §4.5 rewritten — `channel_context_op` is a new normalized event alongside `feed_fetch`/`memory_op`; tool calls also write `status` into the session-history tool-trace record so audit reconstruction is single-stream.

3. **`digest=unavailable` signaling.** r2 leaned "once in awareness only".
   - **Codex r2:** confirmed — once in `channel-awareness` header only; repeating in delta blocks would invite the bug we're killing.
   - **r3 resolution:** header tables in §4.4 only carry `digest=unavailable` on the `raw_window` line. Delta/bootstrap/tail headers say nothing about digest.

4. **`claw-channel-memory` service location.** r2 leaned `examples/`.
   - **Codex r2:** start under `examples/channel-memory/` (or #164-aligned path); avoid published infra image and release pinning in phase 1.
   - **r3 resolution:** §3 v2 row, §11 build sequence, and §12 non-goals all say `examples/channel-memory/`. No `release_manifest.go` entry; no ghcr.io publication in phase 1.

## 10c. Open questions for codex r3 review

1. **`context_kind=` URL hint vs request header.** §4.4 uses a URL query param (`context_kind=delta_tail` etc.) because it's the lowest-friction change at compose-up/cllama and survives cache-keys naturally. Alternative: an `X-Claw-Context-Kind` request header. Header is cleaner if the value should never affect upstream caching; query param is cleaner if claw-wall ever wants to log per-kind hit ratios via existing access-log infrastructure. Lean query param.
2. **Defense-in-depth allowlist on feed paths.** §4.2.1 says feeds remain unauthenticated-on-compose-net. claw-wall could *additionally* check that `channels=` matches the agent's allowlist if it received `X-Claw-ID` on feed requests too — but feed requests don't carry `X-Claw-ID` today. Adding it would tighten the model but require cllama to send the header (small change). Lean "leave feeds as today" because the single-writer compose-URL invariant is already enforced; revisit if/when claw-wall ever gains a non-compose ingress.
3. **`claw.describe.path` label semantics.** §4.2.2 uses this label to point at a bind-mounted descriptor file. Is the existing descriptor extractor (`internal/describe/registry.go`) actually wired for label-based path indirection, or does it only consume `claw.describe` as the JSON inline? If the latter, the alternative is to embed the descriptor JSON directly in the label value. Lean inline JSON to avoid filesystem indirection for a small descriptor.

## 11. Build sequence (assuming codex r3 sign-off)

1. **claw-wall** —
   - `channel-awareness` endpoint reusing `consumeTail`.
   - Mounted allowlist loader (`/etc/claw-wall/agent-channels.json`), fail-closed on parse error.
   - Tool handlers `search_channel_context`, `get_channel_messages` with the §4.2.1 auth order (Bearer service token + `X-Claw-ID` + allowlist check) and §4.2.4 `not_in_buffer` semantics.
   - Header rewrite keyed on `context_kind=` URL param (with `after=` fallback) per §4.4.
   - Tests in §7.1.
2. **compose_up wiring** —
   - `appendConversationWallAwarenessFeed` emits awareness URL **before** the existing delta feed (manifest order).
   - Pod parser for `x-claw.context.channel-awareness`.
   - Per-agent allowlist materialization → `.claw-runtime/claw-wall-agent-channels.json`, bind-mounted into claw-wall + projected into cllama context as `channels-allowlist.json`.
   - Generated `CLAW_WALL_TOOL_TOKEN` env on claw-wall; same value flowed into per-agent `tools.json` auth descriptor.
   - Synthetic `claw.describe` v2 descriptor for claw-wall written to `.claw-runtime/describe/claw-wall.json`, with `claw.describe.path` label or inline JSON (§10c.3 pending).
   - Tests in §7.3, §7.4.
3. **cllama side** —
   - `prepareChannelContextFeed` stamps `context_kind=` into the URL based on `channelContextPrepareDecision`.
   - cllama tool-mediation enforces per-agent channel allowlist before forwarding (loads `channels-allowlist.json` from context).
   - cllama tool mediator forwards `Authorization: Bearer <CLAW_WALL_TOOL_TOKEN>` + `X-Claw-ID` to claw-wall.
   - Late-context injection unchanged (manifest order does the work).
   - Tests in §7.2.
4. **Telemetry normalization** — `channel_context_op` event kind, schema per §4.5, `claw audit` rendering; session-history tool-trace records `status`.
5. **Docs sweep** — `docs/CLLAMA_SPEC.md`, new ADR-025 once codex agrees, `skills/clawdapus/SKILL.md` mirror.
6. **Integration test** — Tiverton-reproduction fixture (§7.5), plain integration, no spike-tag.
7. **PR body** explicitly labels phase 1 vs phase 2 per §6; does **not** close #232.

## 12. Non-goals for this PR (clean cut-line)

- No #164 implementation. This PR depends on #164 *contract* only at the descriptor level; v1 wire surface has no digest section.
- **No channel events flow through ADR-021's `/recall` in any phase.** v1 produces only through the `channel-awareness` feed and the retrieval tools. v2 produces only through the same `channel-awareness` feed and the same retrieval tools, via `claw-channel-memory` as the new producer; the v2 producer may use #164's salience-memory primitives **as a library**, but channel events do not transit per-agent `/recall`/`/retain`.
- No release artifact changes (no `release_manifest.go` bumps, no changelog `Latest` badge, no nav dropdown). Per repo CLAUDE.md release discipline.
