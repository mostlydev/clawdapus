# Clawdash `/schedule` UI Redesign

**Date:** 2026-04-05
**Status:** Draft
**Related:** `docs/plans/2026-04-04-conditional-invoke-scheduling.md` (parent feature)
**Tracking issue:** TBD (link after filing)

## Why

The first-pass `/schedule` page ships an operator control plane that *works* but doesn't *read*. During local demo review the page was called out as "scattered and busy" with unclear intent. The specific problems:

1. **Single 6-column table carries the whole page.** Each row stacks 15–20 lines of text across Invocation / Gate / Timing / Wake / State / Actions columns. Nothing anchors the eye.
2. **Technical fields leak into the hot path.** The "Wake" column renders the full `docker exec` command string. Operators never act on it — it belongs under a disclosure, not inline.
3. **Three columns cover one concept.** Gate, Timing, and Wake are all "when / how this fires." They should consolidate into a single "schedule" block with one hero answer: *when does this fire next?*
4. **Five action buttons per row, always visible.** Pause, Resume, Skip next, Fire now, Force fire. Pause + Resume are mutually exclusive. The red "Force fire" button sits inches from the green "Fire now" — one-click foot-gun.
5. **Summary strip duplicates the panel header.** The six-stat strip (Invocations / Services / Gated / Paused / Skip next / Degraded) competes with the `N visible` chip on the table header and doesn't lead anywhere.
6. **No focal point.** With one row the page is dense; with ten rows it becomes wall-of-text. No visual hierarchy tells the operator *which invocation needs attention right now*.

## Operator Mental Model

For one scheduled invocation, an operator needs to answer — in this order:

1. **Is this healthy?** One glance: scheduled / paused / skip-armed / degraded / failing.
2. **When does it fire next?** Relative ("in 2h 14m") plus absolute ("Mon 14:30 ET") plus the cron + calendar gate.
3. **What did it do last?** Status + timestamp of the last event, with detail on demand.
4. **What do I need to do?** Context-sensitive primary action (Pause *or* Resume), with destructive actions tucked behind disclosure + confirm.

For the pod as a whole:

- How many scheduled jobs are there, and is anything off-nominal? (One health strip, not six.)

## Proposed Shape

### Layout

Replace the monolithic table with a **stacked card list**, one card per invocation, plus a trimmed **health strip** above it.

```
┌─ Health strip (4 stats) ─────────────────────────────────┐
│  6 scheduled  ·  1 paused  ·  0 degraded  ·  next 14:30  │
└──────────────────────────────────────────────────────────┘

┌─ westin-open ─────────────────────  [● SCHEDULED] ───────┐
│                                                           │
│   Next fire   in 2h 14m                                   │
│                Mon 14:30 America/New_York                 │
│                ┌ 30 9 * * 1-5 · us-equities / regular    │
│                                                           │
│   Last event  fired  ·  3m ago                            │
│                                                           │
│   ▸ Details                                               │
│                                                           │
│   [ Pause ]                          ⋯ skip-next  fire   │
└───────────────────────────────────────────────────────────┘
```

### Card anatomy

| Region | Content |
|---|---|
| **Header** | Invocation name (left, bold) · Status pills (right). State is **multi-dimensional** — render up to two primary pills plus one optional chip: **Lifecycle pill** (scheduled / paused) + **Health pill** (shown only when degraded) + **skip-next chip** (shown only when armed). An invocation that's both paused and degraded shows both pills — no information is collapsed. Colors: neutral=scheduled, amber=paused, red=degraded, outlined=skip-next. |
| **Primary line** | "Next slot" — relative time is the hero string (largest type). Absolute timestamp + timezone under it. Cron + `calendar / session` as a subtitle below. **This is the next cron slot, not a guaranteed fire.** The scheduler only tracks `next_fire_at` as the upcoming cron tick; whether it actually fires depends on current state. Rendering adapts:<br>• **scheduled, no gate issues** → hero reads "Next fire — in 2h 14m"<br>• **paused** → hero grays out and reads "Next slot — in 2h 14m *(paused, will be skipped)*"; if `paused_until` is set and earlier than the slot, show "resumes in …" beneath<br>• **skip-next armed** → hero reads "Next slot — in 2h 14m *(skip-next armed, will be skipped)*"; compute and show the slot *after* that as "Next fire — in …"<br>• **degraded** → hero reads "Next slot — in 2h 14m *(degraded, ~10% fire chance)*" <br>• **paused + degraded / paused + skip-armed** → paused takes precedence in the hero copy; other modifiers show as subtitle lines. |
| **Secondary line** | "Last event" — `last_status` pill + relative timestamp. Truncated `last_detail` tooltip on hover. This is *orthogonal* to the header pills — header answers "current state," last-event line answers "what happened last time." |
| **Details disclosure** | Collapsed by default. Contains: full ID hash, `service → agent-id`, message body, target container, wake adapter, wake command (monospace). |
| **Actions** | Primary button toggles by lifecycle state: `Pause` when scheduled, `Resume` when paused. Secondary actions behind `⋯` menu: Skip next, Clear skip-next (shown only when skip-next armed), Fire now, Force fire. |

### Actions — safe by default

- **Primary button** is state-aware: one button, not both. When `Paused=true` render `Resume`; otherwise `Pause`. When `SkipNext=true` the menu shows "Clear skip-next" instead of "Skip next."
- **Fire now** lives in the overflow menu, not as a top-level button. One click fires; no bypass.
- **Force fire (bypass)** requires a confirm dialog: a modal with the warning "This bypasses the calendar gate and pause state. Fire `<name>` now?" — two explicit buttons.
- Pause opens a small inline form for optional `until` (datetime-local) + `reason`, not a bare POST. See "Control-plane changes" below — the clawdash controller converts `datetime-local` values against the invocation's timezone and forwards RFC3339 UTC to claw-api.

### Health strip — 4 stats, not 6

- **Scheduled** (total) — neutral
- **Paused** — amber when > 0
- **Degraded** — red when > 0
- **Next slot** — relative time to the soonest upcoming cron slot across the pod ("in 2h 14m"). Label clarifies this is a slot, not a guaranteed fire, to match the card-level terminology. Computed only from non-paused invocations (paused ones would mislead this metric).

Drop: "Services," "Gated," "Skip next" as summary stats. They're per-invocation state, not pod-health signals. Gated count is static config; skip-next is ephemeral per-row state.

### Empty state

Keep the existing empty card ("No scheduler-owned invocations in scope") but replace with a calmer message that names the likely cause: "This scope has no pod-origin invocations. Pod-level `x-claw.invoke` entries appear here once `claw up` wires them."

### Sort order

Cards sort by **next fire time ascending** (soonest first), with paused/degraded pinned to the top regardless of schedule. Currently rows sort by `service → name → id` which doesn't match the operator's scanning order.

## Implementation Sketch

### Template

Replace `cmd/clawdash/templates/schedule.html` section `dash-panel > table` with a `dash-card-list` containing `scheduleCard` entries. Keep the existing `dash-shell`, topbar, nav, and banner structure.

### Data shape

Extend `scheduleRow` / rename to `scheduleCard`:

```go
type scheduleCard struct {
    ID              string
    Name            string

    // Header pills — multi-dimensional, not a single enum.
    LifecyclePill   scheduleBadge   // always present: "scheduled" or "paused"
    HealthPill      *scheduleBadge  // present only when Degraded=true
    SkipNextChip    *scheduleBadge  // present only when SkipNext=true

    NextSlot        nextSlotDisplay // hero copy adapts to paused/skip-armed/degraded
    LastEvent       lastEventDisplay
    Details         scheduleDetails // collapsed content

    Primary         scheduleAction  // Pause OR Resume (lifecycle-driven)
    Overflow        []scheduleAction // Skip next / Clear skip-next / Fire now
    BypassFire      scheduleAction  // gated behind confirm modal
    PauseForm       pauseFormFields // datetime-local + reason

    SortKey         time.Time       // NextFireAt UTC; zero for pinned rows
    Pinned          bool            // paused or degraded pin to top
}

type nextSlotDisplay struct {
    SlotRelative    string // "in 2h 14m"
    SlotAbsolute    string // "Mon 14:30 America/New_York"
    CronExpr        string
    GateSubtitle    string // "us-equities / regular" or "cron only"
    HeroLabel       string // "Next fire" | "Next slot"
    Modifier        string // "(paused, will be skipped)" | "(skip-next armed, will be skipped)" | "(degraded, ~10% fire chance)" | ""
    FollowupLabel   string // "Next fire" when skip-next armed; "resumes in" when paused-until; "" otherwise
    FollowupValue   string
    Dimmed          bool   // true when paused (grays out the hero)
}
```

Computed on the server; template stays dumb.

### Sort

In `buildSchedulePageData`, sort by:
1. `Pinned` desc (paused/degraded first)
2. `SortKey` asc (soonest next-fire first)
3. `Name` asc (stable tiebreaker)

### CSS

Add `cmd/clawdash/static/schedule.css` classes:
- `.dash-card-list` — vertical gap 0.75rem
- `.dash-schedule-card` — panel styling, padding 1.25rem
- `.dash-schedule-hero` — next-fire relative string, `font-size: 1.5rem`, monospace
- `.dash-schedule-meta-line` — subtitle rows (absolute time, cron)
- `.dash-schedule-details` — `<details>` element; `summary` is clickable
- `.dash-overflow-menu` — Alpine.js dropdown trigger + panel (already have `alpine.js` in page)
- `.dash-confirm-modal` — bypass-fire dialog

Drop `.dash-schedule-table`, `.dash-action-stack`, `.dash-action-row` — no longer used.

### Relative time rendering

Server-side "in 2h 14m" computed against a `now func() time.Time` injected into `buildSchedulePageData`. Production wires `time.Now`; tests inject a frozen clock. Without this injection the golden-file tests would churn on every run. Acceptable staleness for a refresh-on-action page; no client clock needed initially. If/when SSE lands, the relative string can be re-rendered client-side.

### Confirm modal

Alpine.js local state on the bypass-fire button. One modal element per card. Modal POSTs the same form; cancel closes it.

## Control-plane changes

This is **not** a template-only change. Two small backend additions are required for the UX to work correctly:

### 1. `POST /schedule/:id/clear-skip-next` (new endpoint, claw-api)

The current contract only sets `skip_next = true`; it clears implicitly when the scheduler consumes it on the next tick. The UI needs an explicit clear so an operator can disarm a skip they set by mistake or no longer want.

- Verb: reuses `schedule.control` (no new verb).
- Semantics: idempotent — sets `state.SkipNext = false` and persists.
- Handler lives next to the existing `handleScheduleSkipNext` in `cmd/claw-api/handler.go`.
- Clawdash controller gains a matching `/schedule/:id/clear-skip-next` POST that forwards to the API.

Alternative considered: overloading `/skip-next` with a JSON body `{clear: true}`. Rejected — a dedicated path is clearer in audit logs and matches the existing verb-per-endpoint pattern (pause/resume are separate endpoints for the same reason).

### 2. Clawdash-side timezone conversion for `pause.until`

`<input type="datetime-local">` submits `2026-04-05T14:30` (no seconds, no offset). claw-api's `handleSchedulePause` requires RFC3339 with timezone.

Fix lives entirely in clawdash (`schedule_page.go` / `schedule.go`):

- When the pause form submits, the clawdash controller parses the raw value as `2006-01-02T15:04` against the **invocation's declared timezone** (available on `scheduleInvocationView.Timezone`), converts to UTC, formats as RFC3339, and forwards to claw-api.
- If the invocation timezone is empty, fall back to UTC.
- Invalid values surface as a `?error=...` flash on the redirect, same as today.

No change to `cmd/claw-api/handler.go` for this one — the API contract stays RFC3339, the clawdash controller does the translation.

### 3. Controller CLI (`claw api`) unaffected

`claw api schedule pause --until 2026-04-05T14:30:00Z ...` already takes RFC3339 directly. Only the browser form needs conversion. The new `clear-skip-next` endpoint gains a matching `claw api schedule clear-skip-next <id>` subcommand for parity.

## Build Sequence

Done on a feature branch — **direct replacement**, not an in-page A/B. The existing `/schedule` page is small enough that a query-param flag adds more complexity (redirect preservation, dual CSS maintenance) than it saves.

1. **Control-plane additions** — add `POST /schedule/:id/clear-skip-next` to `cmd/claw-api/handler.go` (handler + authz wired to `schedule.control`); add matching `claw api schedule clear-skip-next` CLI subcommand; unit tests for the new endpoint including idempotency and scope enforcement.
2. **Clock injection** — thread a `now func() time.Time` into `buildSchedulePageData` and `renderSchedule`; default to `time.Now` in production wiring. Existing tests keep passing.
3. **Timezone-aware pause form** — clawdash controller parses `datetime-local` against the invocation's declared timezone, converts to RFC3339 UTC before forwarding. Flash an `?error=` on parse failure. Test with a non-UTC fixture.
4. **Data shape refactor** — introduce `scheduleCard` replacing `scheduleRow`, populated from `scheduleInvocationView`. Sort by next-fire ascending with paused/degraded pinned. Unit-test sort/pin logic with the frozen clock.
5. **New template + CSS** — write `schedule.html` card list, new CSS classes (`dash-card-list`, `dash-schedule-card`, `dash-schedule-hero`, `dash-overflow-menu`, `dash-confirm-modal`). Drop `dash-schedule-table`, `dash-action-stack`, `dash-action-row`.
6. **State-aware primary action + overflow menu** — Pause/Resume toggle; Skip next / Clear skip-next toggle (uses the new endpoint); Fire now plain; Force fire behind Alpine.js confirm modal.
7. **Health strip trim** — 4 stats (Scheduled / Paused / Degraded / Next slot), with the last one computed as the soonest upcoming cron slot across non-paused visible invocations.
8. **Snapshot tests** — render card list with a representative fixture (scheduled, paused, degraded, paused+degraded, skip-armed, recently-fired) under a frozen clock; golden-file the HTML.

## Out of Scope

- **Live updates (SSE).** Parent plan calls for SSE on the `/schedule` page; do that after the static layout lands.
- **Edit schedule** (cron string, calendar, timezone). Read/control only.
- **Audit trail view.** `last_event` summary only; no history list.
- **Multi-pod view.** Single-pod scope matches claw-api's reality.

## Open Questions

1. **Per-card color accents by status?** Would paused cards get an amber left border? Degraded cards a red one? Probably yes — cheap visual pre-attention cue.
2. **Stale-state indicator.** If `last_evaluated_at` is older than the expected cadence (scheduler hung, not firing), do we flag it? Requires knowing expected cadence from the cron — easy enough, but out of scope for this pass?
3. **Principal name surfaced on page.** Small footer line ("acting as `claw-scheduler`") useful for audit, or clutter? Lean toward small gray footer text.
4. **Demo fixtures from live session** — Codex mentioned two runtime gaps (OpenClaw fixture entrypoint under `/claw` tmpfs; claw-api image missing tzdata). Those get their own issue, not this one.
