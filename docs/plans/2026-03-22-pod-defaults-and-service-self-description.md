# Pod Defaults, Service Self-Description, and Context Simplification

**Date:** 2026-03-22
**Status:** Draft (v3 — incorporates Codex and Gemini review feedback)
**Depends on:** ADR-004 (Service Surface Skills), ADR-013 (Context Feeds), ADR-014 (Telemetry Normalization)
**Related:** RailsTrail gem (tiverton-house/services/trading-api/gems/rails_trail)

## Problem

Clawdapus pods are operator-authored deployment contracts — inspectable, diffable, deterministic. But the current YAML surface forces operators to restate the same facts across every service, and services cannot contribute their own metadata to the system.

Three clusters of repetitiveness:

1. **Pod YAML boilerplate.** Every claw repeats `cllama`, `cllama-env`, `surfaces`, `feeds`, and `skills` even when they're identical across the pod.

2. **Feed wiring is backwards.** Feeds are declared per-consumer when they should be declared per-provider. The consumer shouldn't need to know a service's URL path or TTL.

3. **Service description is shallow.** `claw.skill.emit` extracts one markdown file. The fallback stub says "hostname + ports." Generated surface/handle skills duplicate CLAWDAPUS.md.

## Design Principles

1. **Compile-time, not runtime.** All wiring is resolved during `claw up`. No runtime self-registration.
2. **Provider-owns, consumer-subscribes.** Services declare what they offer. Consumers subscribe by name.
3. **Pod-level defaults, service-level overrides.** Shared config declared once. Services inherit, override, or extend.
4. **One canonical descriptor.** Declared once, projected into whatever artifacts need it.
5. **Simplicity over backward compatibility.** Tiverton-house is the only production pod. Breaking changes are acceptable.

---

## Phase 1: Pod-Level Defaults

**Goal:** Eliminate per-service YAML repetition.

### New Pod-Level `x-claw` Fields

```yaml
x-claw:
  pod: tiverton-house
  master: sentinel
  handles-defaults: { ... }          # existing
  cllama-defaults:                    # NEW
    proxy: [passthrough]
    env:
      OPENROUTER_API_KEY: "${OPENROUTER_API_KEY}"
      ANTHROPIC_API_KEY: "${ANTHROPIC_API_KEY}"
  surfaces-defaults:                  # NEW
    - "service://trading-api"
    - "channel://discord: ..."
  feeds-defaults:                     # NEW
    - name: market-context
      source: trading-api
      path: /api/v1/market_context/{claw_id}
      ttl: 180
  skills-defaults:                    # NEW
    - ./policy/risk-limits.md
```

### Override Rule: Replace-on-Declare with `...` Spread

**One rule:** if a service declares a list field, it replaces the defaults entirely. To extend instead, use `...`:

```yaml
# Replace entirely — sentinel wants different feeds:
feeds:
  - name: fleet-alerts
    source: claw-api
    path: /fleet/alerts
    ttl: 30

# Extend — coordinator appends escalation to default skills:
skills:
  - ...                           # defaults expand at this position
  - ./policy/escalation.md
```

- No `...` → full replacement
- `...` present → defaults splice at that position
- At most one `...` per list
- `skills: []` (explicit empty) means no skills, NOT "inherit defaults"

**Map fields:** `cllama-defaults.env` merges additively — default keys first, service wins on collision. `cllama-defaults.proxy` is inherited when service has no `cllama:` key; service replaces entirely.

**Empty vs omitted distinction:** The parser must check whether the YAML node was actually present, not just whether the parsed slice is empty. An omitted key inherits defaults; an explicit `feeds: []` means "no feeds."

### What Tiverton-House Looks Like After

```yaml
x-claw:
  pod: tiverton-house
  master: sentinel
  handles-defaults:
    discord:
      guilds: *floor_guild
  cllama-defaults:
    proxy: [passthrough]
    env: *cllama_env
  surfaces-defaults: *desk_surfaces
  feeds-defaults: *market_context_feed
  skills-defaults:
    - ./policy/risk-limits.md
    - ./policy/approval-workflow.md

services:
  tiverton:
    x-claw:
      agent: *shared_agent
      include: [...]
      skills:
        - ...                      # inherits risk-limits + approval-workflow
        - ./policy/escalation.md   # coordinator-only
      handles:
        discord:
          id: "${TIVERTON_DISCORD_ID}"
          username: "tiverton"
          guilds: *floor_infra_guild
      invoke: [...]

  weston:
    x-claw:
      agent: *shared_agent
      include: [...]
      handles:
        discord:
          id: "${WESTON_DISCORD_ID}"
          username: "weston"
      invoke: [...]
      # Inherits everything: cllama, surfaces, feeds, skills

  sentinel:
    x-claw:
      agent: *shared_agent
      include: [...]
      feeds:                       # no spread — replaces defaults
        - name: fleet-alerts
          source: claw-api
          path: /fleet/alerts
          ttl: 30
      surfaces:                    # no spread — replaces defaults
        - "service://claw-api"
        - *discord_floor_surface
      skills: []                   # explicit empty — no skills
      handles:
        discord:
          id: "${SENTINEL_DISCORD_ID}"
          username: "sentinel"
          guilds: *floor_infra_guild
      invoke: [...]
```

### Implementation: Spread Expansion at the Raw YAML Layer

**Critical constraint:** The current parser immediately parses surfaces into typed `ResolvedSurface` structs via `ParseSurface()` (parser.go:150-168). A literal `"..."` string would fail `ParseSurface()` before defaults ever run. Same issue with feeds — `parseFeeds()` validates `source` and `path` as required fields.

**Solution:** Spread expansion must happen on the raw YAML data, before typed parsing.

```go
// In Parse(), after unmarshalling raw YAML but before typed parsing:

// 1. Read raw defaults from rawPodClaw
defaults := extractDefaults(raw.XClaw)

// 2. For each service's raw x-claw block, expand spreads at the raw layer
for name, svc := range raw.Services {
    if svc.XClaw != nil {
        expandRawDefaults(svc.XClaw, defaults)
    }
}

// 3. Proceed with existing typed parsing (ParseSurface, parseFeeds, etc.)
```

`expandRawDefaults` operates on the raw `*rawClawBlock`:
- If `rawClawBlock.Surfaces` is nil and defaults exist → copy defaults
- If `rawClawBlock.Surfaces` contains `"..."` → splice defaults at that position
- If `rawClawBlock.Surfaces` is an explicit empty list → leave empty
- Same for Feeds (as `[]rawFeedEntry`), Skills (as `[]string`)
- Cllama: inherit proxy if nil; merge env map

The key insight: by the time `ParseSurface()` runs, every `"..."` has already been replaced with the actual default entries. The typed parsers never see the spread token.

**Empty vs nil detection:** Use `yaml.v3` node inspection or a sentinel approach. The `rawClawBlock` can use pointer fields (`Surfaces *[]interface{}`) to distinguish nil (omitted) from empty (explicit `[]`). Alternatively, do a second unmarshal pass with `yaml.Node` to check presence.

#### Tests

- Inheritance: service with no field inherits defaults
- Replacement: service with field (no spread) replaces defaults
- Spread: `...` at start, middle, end — defaults expand at correct position
- Spread error: two `...` entries → parse error
- Map merge: `cllama-defaults.env` keys merge, service wins
- Empty list: `skills: []` means no skills, not "inherit"
- Omitted key: no `skills:` at all means "inherit defaults"

---

## Phase 2: Service Self-Description (`claw.describe`)

**Goal:** Services advertise capabilities. `claw up` compiles descriptors into the pod. Consumers subscribe to feeds by name.

### The Descriptor Contract

A service image declares a structured descriptor:

```dockerfile
LABEL claw.describe=/app/.claw-describe.json
```

```json
{
  "version": 1,
  "description": "Algorithmic trading REST API with position management and order execution",
  "feeds": [
    {
      "name": "market-context",
      "path": "/api/v1/market_context/{claw_id}",
      "ttl": 180,
      "description": "Per-agent market context with positions, watchlist, and session state"
    }
  ],
  "endpoints": [
    {
      "method": "GET",
      "path": "/api/v1/positions",
      "description": "Current open positions for an agent"
    },
    {
      "method": "POST",
      "path": "/api/v1/trades/:id/confirm",
      "description": "Confirm a proposed trade"
    }
  ],
  "auth": {
    "type": "bearer",
    "env": "TRADING_API_TOKEN"
  },
  "skill": "/app/docs/skills/trading-api.md"
}
```

### Descriptor Fields

| Field | Required | Description |
|-------|----------|-------------|
| `version` | Yes | Schema version. Currently `1`. Unknown fields ignored. |
| `description` | No | One-liner for CLAWDAPUS.md and skill generation |
| `feeds` | No | Feeds this service provides |
| `feeds[].name` | Yes | Feed name for consumer subscription |
| `feeds[].path` | Yes | URL path template (may contain `{claw_id}`) |
| `feeds[].ttl` | Yes | Default refresh interval in seconds |
| `feeds[].description` | No | What the feed contains |
| `endpoints` | No | Structured endpoint list (machine-readable) |
| `endpoints[].method` | Yes | HTTP method |
| `endpoints[].path` | Yes | URL path (may contain `:param` placeholders) |
| `endpoints[].description` | No | What the endpoint does |
| `auth` | No | Authentication requirements |
| `auth.type` | Yes | `bearer`, `header`, `none` |
| `auth.env` | No | Env var name containing the credential |
| `skill` | No | Path to skill file inside image (supersedes `claw.skill.emit`) |

**No `name` field.** Service identity comes from the compose service key, not from the image. One image can back multiple services.

**`endpoints` field** provides machine-readable route data. This is the structured contract that RailsTrail's introspection produces. It flows into skill generation (both CLAWDAPUS.md surface sections and any generated skill content). Without this field, the only way to describe endpoints is the freeform `skill` file.

### Consumer-Side Feed Subscription

```yaml
# Short form — resolved from descriptor registry:
feeds: [market-context]

# Explicit form — bypasses registry:
feeds:
  - name: custom-feed
    source: some-service
    path: /custom
    ttl: 60

# Mixed:
feeds:
  - market-context
  - name: custom-feed
    source: some-service
    path: /custom
    ttl: 60
```

### Two-Phase Feed Resolution

Feed names cannot be resolved in the parser because the feed registry doesn't exist until images are inspected.

**Phase 1 — Parse (in `pod.Parse`):**
- Feeds list accepts both string and struct entries
- String entries stored as unresolved: `FeedEntry{Name: "market-context", Unresolved: true}`
- Struct entries parsed as today (source/path/ttl validated)
- No source/path validation for unresolved entries

**Phase 2 — Resolve (in `claw up`, after image inspection):**
- Build feed registry from:
  1. Service descriptors (`claw.describe` → `feeds[]`)
  2. claw-api auto-entries (when `x-claw.master` is set)
- Walk all services. For each unresolved feed:
  - Look up name in registry → populate source, path, ttl
  - If not found → hard error
- URL resolution proceeds as today via `resolveFeedURL()`

### Feed Auth: Credential Projection

**The problem:** The descriptor's `auth.env: "TRADING_API_TOKEN"` is just a name. It tells `claw up` which env var holds the credential, but nothing currently projects the actual token value into `feeds.json` for cllama to use.

Today, feed auth only works for claw-api because `claw up` explicitly generates bearer tokens and injects them via `serviceAuth` entries. For arbitrary service feeds, there's no equivalent mechanism.

**Solution:** When building feed manifest entries, if a feed's source service has a descriptor with `auth.env`, `claw up` reads the actual value from the consuming claw's compose environment:

```go
// In buildFeedManifestEntries, after resolving feed URL:
if descriptor != nil && descriptor.Auth.Env != "" {
    // Look up the credential from the consuming service's environment
    token := svc.Environment[descriptor.Auth.Env]
    if token != "" {
        entry.Auth = token
    }
}
```

This works because the consuming service already has `TRADING_API_TOKEN` in its compose `environment:` block (it needs it for direct API calls too). `claw up` just reads it and copies it into the feed manifest so cllama can send it as a bearer header.

For claw-api feeds, the existing mechanism (auto-generated principals + token injection) continues unchanged.

### Descriptor Extraction: Build-Context Fallback

**The problem:** Services defined with `build: ./path` don't have images to inspect until after `docker compose build` runs. But `claw up` needs to scan descriptors before compose generation.

**Solution:** Two-path extraction:

1. **Image exists** → extract `.claw-describe.json` from image (existing `ExtractServiceSkill` pattern)
2. **Image doesn't exist but `build.context` is set** → look for `.claw-describe.json` on the local filesystem relative to the build context

```go
func extractDescriptor(svc *pod.Service) (*ServiceDescriptor, error) {
    // Try image first
    if imageExists(svc.Image) {
        return extractFromImage(svc.Image, describePath)
    }
    // Fall back to build context
    if buildCtx := svc.Compose["build"]; buildCtx != nil {
        return extractFromFilesystem(buildCtx, describePath)
    }
    return nil, nil // no descriptor available
}
```

For `build:` services, the expectation is that `RUN bundle exec rake rails_trail:claw_describe` (or equivalent) runs during `docker build`, so the file exists both in the image and in the build output. But on the first `claw up` before any build, the file may exist in the build context if it's checked in (or generated by a pre-build step).

**Malformed descriptors:** If `.claw-describe.json` exists but is invalid JSON or fails schema validation, `claw up` fails hard. A promised contract that can't be read is a deployment error, not a fallback condition.

### RailsTrail Integration

1. Add `rails_trail:claw_describe` rake task that writes `.claw-describe.json`:
   - Routes → `endpoints[]`
   - Feed-tagged endpoints → `feeds[]`
   - `config.service_name` → informational (not binding)
   - `rails_trail:describe` LLM output path → `skill` field

2. In Dockerfile:
```dockerfile
RUN bundle exec rake rails_trail:claw_describe
LABEL claw.describe=/app/.claw-describe.json
```

3. The contract is the JSON file, not the generation mechanism. Other frameworks write `.claw-describe.json` their own way.

### Implementation

#### New Code

1. `internal/describe/descriptor.go` — `ServiceDescriptor` type, JSON parsing, version validation
2. `internal/describe/registry.go` — `FeedRegistry` from descriptors + claw-api
3. `internal/describe/extract.go` — Two-path extraction (image or filesystem)

#### Modified Code

1. `internal/inspect/inspect.go` — Parse `claw.describe` label (`DescribePath` field)
2. `internal/pod/parser.go` — `parseFeeds()` accepts mixed string/struct, `Unresolved` flag
3. `cmd/claw/compose_up.go`:
   - Descriptor scan phase after image inspection
   - Feed resolve phase before feed manifest building
   - Richer skill generation from descriptor `endpoints` + `description`

---

## Phase 3: Unified Context Document

**Goal:** Reduce generated artifacts per agent. One context document instead of N redundant files.

### Current State

Each agent gets:
- `CLAWDAPUS.md` — identity, surface list, peer handles, feeds, proxy info
- `surface-<target>.md` — per-service skill (hostname, ports, extracted content)
- `handle-<platform>.md` — per-handle skill (ID, username, guilds)
- `surface-<platform>.md` — per-channel skill (token, guild access)

Surface and handle skills duplicate CLAWDAPUS.md content.

### Change: Two Tiers of Service Description

The review identified a real tension: inlining a 500-line trading-api skill into CLAWDAPUS.md bloats context. And the current prompt assembly has a specific order — guide injection runs before CLAWDAPUS.md exists, then drivers append the full CLAWDAPUS.md.

**Solution: structured metadata inlined, large skills mounted separately.**

CLAWDAPUS.md inlines:
- Surface connection metadata (host, port, auth env var)
- Descriptor `description` one-liner
- Descriptor `endpoints[]` (structured, machine-readable)
- Handle metadata (ID, username, guilds, channels)
- Feed descriptions
- Peer handles

Large service skill files (from `claw.describe.skill` or `claw.skill.emit`) remain as separate mounted files at `/claw/skills/`. CLAWDAPUS.md includes a pointer:

```markdown
### trading-api (service)
Host: trading-api:4000 | Auth: $TRADING_API_TOKEN

Algorithmic trading REST API with position management and order execution.

Endpoints:
- GET /api/v1/positions — Current open positions
- POST /api/v1/trades/:id/confirm — Confirm a proposed trade
- GET /api/v1/market_context/{claw_id} — Per-agent market context (also feed)

Full service manual: `skills/trading-api.md`
```

This keeps CLAWDAPUS.md lean (structured metadata + endpoint list) while the deep operational manual stays in a separate skill file that the agent can reference when needed.

### Prompt Assembly: Avoiding Duplication

**The problem:** Today, `materializeServiceSurfaceGuides()` injects service manuals into `AGENTS.generated.md` as guide content (first pass, before CLAWDAPUS.md exists). Then drivers append the full CLAWDAPUS.md into the effective contract (second pass). If CLAWDAPUS.md also contains service descriptions, the agent sees them twice.

**Solution: choose one injection path, not both.**

Service descriptions flow through CLAWDAPUS.md only. `materializeServiceSurfaceGuides()` no longer injects separate surface guides into `AGENTS.generated.md`. Instead:
- CLAWDAPUS.md carries the structured metadata + endpoint list for all surfaces
- Large skill files are mounted and referenced by pointer
- Drivers append CLAWDAPUS.md into the effective contract as they do today
- `materializeServiceSurfaceGuides()` is removed or repurposed to only handle `include` entries with `mode: guide`

This means service descriptions are no longer double-injected. They appear once, in CLAWDAPUS.md, which is always in context.

**Trade-off:** Service manuals move from explicitly labeled guide blocks in `AGENTS.generated.md` to sections in CLAWDAPUS.md. Agents may weight them slightly differently. In practice, CLAWDAPUS.md is always present in context and drivers already treat it as authoritative infrastructure context. The structured endpoint list in CLAWDAPUS.md is more useful than a freeform guide block anyway.

### Generated handle/channel skills

Handle skills (`handle-discord.md`) and channel surface skills (`surface-discord.md`) are fully replaced by CLAWDAPUS.md sections. These are pure metadata — there's no large freeform content to keep separate. They are deleted as generated artifacts.

### Impact on cllama

Context mount simplifies:
- Before: `AGENTS.md` + `CLAWDAPUS.md` + `metadata.json` + `feeds.json` + N generated skills
- After: `AGENTS.md` + `CLAWDAPUS.md` + `metadata.json` + `feeds.json` + service skill files (when large) + operator skills

cllama reads `metadata.json` (auth) and `feeds.json` (injection). No cllama changes.

### Implementation

1. `internal/driver/shared/clawdapus_md.go` — Extend `GenerateClawdapusMD` to accept descriptors, handle info, feed descriptions. Render structured metadata inline. Add pointers to mounted skill files.
2. `cmd/claw/compose_up.go`:
   - Remove `resolveChannelGeneratedSkills()` and `resolveHandleSkills()` as file generators
   - Remove or repurpose `materializeServiceSurfaceGuides()` — no more surface guide injection into AGENTS.generated.md
   - Large service skills (from descriptor `skill` field or `claw.skill.emit`) still extracted and mounted at `/claw/skills/`
3. Update tests for generated artifact expectations.

---

## Phase 3B: Driver Base Template (Optional, Independent)

**Goal:** Reduce shared Materialize scaffolding across drivers.

### Scoped Narrower Than Originally Sketched

The review correctly identified that drivers diverge on critical details:
- nullclaw registers cron in `PostApply()`, not `Materialize()`
- nanoclaw and microclaw ignore invocations entirely
- openclaw needs a writable directory mount for cron state
- hermes has mandatory SOUL.md / .env sequencing

A monolithic `BaseMaterialize` that tries to handle all of this is the wrong abstraction.

**Instead, extract targeted shared helpers:**

```go
// shared/materialize_helpers.go

// CreateRuntimeDirs creates the standard directory structure with 0o777.
func CreateRuntimeDirs(homeDir string, extras ...string) error

// WriteEnvFile writes a filtered .env file from the service environment.
func WriteEnvFile(homeDir string, env map[string]string, allowedKeys []string) error

// WriteJobsJSON writes invocations as a JSON job manifest.
func WriteJobsJSON(homeDir string, invocations []driver.Invocation) error

// StandardMounts returns the common mount set (home, workspace, skills).
func StandardMounts(homeDir, workspaceDir, skillsDir string) []driver.Mount

// StandardEnv returns the common env map (HOME, CLAW_MANAGED).
func StandardEnv(homeDir string) map[string]string
```

Each driver calls the helpers it needs. No forced lifecycle. hermes calls `WriteEnvFile` with its passthrough keys; openclaw calls `CreateRuntimeDirs` with its extra cron dir; nullclaw skips `WriteJobsJSON` because it handles invocations in `PostApply`.

### Implementation

1. Extract helpers from existing driver code into `internal/driver/shared/materialize_helpers.go`
2. Convert drivers one at a time, verify tests: nullclaw → picoclaw → microclaw → nanoclaw → nanobot → openclaw → hermes

---

## Implementation Sequence

### Milestone 1: Pod-Level Defaults (Phase 1)
1. Add default fields to `rawPodClaw`
2. Implement raw-layer spread expansion before typed parsing
3. Handle empty-vs-omitted key distinction
4. Unit tests: inheritance, replacement, spread positions, map merge, empty list, omitted key, double-spread error
5. Rewrite examples to use defaults

### Milestone 2: Service Descriptor Contract (Phase 2A)
6. Define `ServiceDescriptor` type with `version`, `endpoints`, `feeds`, `auth`, `skill`
7. Add `claw.describe` label parsing to `internal/inspect`
8. Two-path descriptor extraction (image + filesystem fallback)
9. Malformed descriptor → hard error
10. Build `FeedRegistry` from descriptors + claw-api

### Milestone 3: Feed Resolution Pipeline (Phase 2B)
11. Extend `parseFeeds()` for mixed string/struct with `Unresolved` flag
12. Add resolve phase in `claw up` after descriptor scan
13. Wire feed auth credential projection from consuming service env
14. Rewrite examples to use short-form feeds
15. Tests: resolution, missing names, collisions, auth projection

### Milestone 4: RailsTrail Bridge (Phase 2C)
16. Add `rails_trail:claw_describe` rake task
17. Update tiverton-house trading-api Dockerfile
18. End-to-end: descriptor extraction → feed resolution → skill generation

### Milestone 5: Unified CLAWDAPUS.md (Phase 3)
19. Extend `GenerateClawdapusMD` to inline structured metadata + endpoint lists
20. Remove surface/handle skill file generation
21. Remove `materializeServiceSurfaceGuides()` surface guide injection
22. Large service skills → mounted files with CLAWDAPUS.md pointers
23. Update tests

### Milestone 6: Driver Helpers (Phase 3B — independent)
24. Extract targeted shared helpers (not monolithic base)
25. Convert drivers one at a time: nullclaw → ... → hermes

## Resolved Questions

1. **Feed name collisions:** Hard error. Feed names are pod-global and must be unique.
2. **Descriptor versioning:** `"version": 1` required. Unknown fields ignored.
3. **Descriptor `name` field:** Removed. Identity from pod YAML, not image. Shared images work.
4. **Merge semantics:** Replace-on-declare + `...` spread. One rule.
5. **Spread expansion timing:** Raw YAML layer, before typed parsing. Parsers never see `...`.
6. **Feed auth projection:** `claw up` reads credential from consuming service's env using `auth.env` name.
7. **Build-context services:** Filesystem fallback for descriptor extraction.
8. **Malformed descriptors:** Hard error, not silent fallback.
9. **Large service skills:** Mounted separately, CLAWDAPUS.md carries structured metadata + pointer.
10. **Service manual injection:** Through CLAWDAPUS.md only. No double injection.
11. **Driver base template:** Targeted helpers, not monolithic base. Drivers diverge too much.
12. **Empty list vs omitted key:** Explicit `[]` means empty. Omitted means inherit.

## Open Questions

1. **`claw.skill.emit` deprecation.** Once `claw.describe` with `skill` field is live, `claw.skill.emit` is redundant. Deprecate immediately or keep as alias?

2. **Endpoint field completeness.** Should `endpoints[]` support request/response schemas, or is method+path+description enough for v1? Current thinking: keep it minimal. RailsTrail can always put rich docs in the freeform skill file.

3. **Non-image services.** `postgres:16-alpine` can't have descriptors. Operators describe them via explicit feed declarations or custom wrapper images. A future `x-claw-describe` block on non-claw services is possible but deferred.
