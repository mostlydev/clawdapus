# Phase 3.5: HANDLE Directive + Social Topology Projection

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** `HANDLE <platform>` in Clawfile declares an agent's public social identity. Two things happen: (1) the driver enables the platform in the runner's native config (so the agent can communicate on it), and (2) `claw up` aggregates handles across the pod and broadcasts `CLAW_HANDLE_<SERVICE>_<PLATFORM>_*` environment variables into every service — so a Rails API, another agent, or any pod member can route to a specific agent on a platform without hardcoding.

**The Leviathan Pattern:** A trading bot needs human approval before executing a large trade. A Rails API monitors positions and needs to ping the right agent on Discord. It reads `CLAW_HANDLE_TIVERTON_DISCORD_ID=123456789` and constructs `<@123456789>` in the right guild channel from `CLAW_HANDLE_TIVERTON_DISCORD_GUILDS`. The agent sees the mention, reviews, approves. No hardcoding, no service discovery — just env vars injected by Clawdapus.

**Architecture:**

- `HANDLE discord` in Clawfile → `LABEL claw.handle.discord=true` at build time
- `handles:` in x-claw service block → declares the agent's full contact card per platform
- At `claw up`: collect all handle contact cards, generate env vars, inject into every service
- OpenClaw driver: `HANDLE discord` → `channels.discord.enabled = true` in JSON5 config
- CLAWDAPUS.md: `## Handles` section with guild/channel membership tree
- `skills/handle-<platform>.md`: full contact card skill file per platform

**Data model:**

```go
// HandleInfo is the full contact card for an agent on a platform.
type HandleInfo struct {
    ID       string      // platform user ID for @mentions (required)
    Username string      // display name/handle (optional)
    Guilds   []GuildInfo // guild/server memberships for routing (optional)
}

type GuildInfo struct {
    ID       string        // guild/server/workspace ID
    Name     string        // optional human label
    Channels []ChannelInfo // channels within this guild the agent monitors
}

type ChannelInfo struct {
    ID   string // channel ID
    Name string // optional human label
}
```

**x-claw schema (two forms):**

```yaml
# String shorthand — just the platform user ID
handles:
  discord: "123456789"

# Full contact card
handles:
  discord:
    id: "123456789"
    username: "crypto-bot"
    guilds:
      - id: "111222333"
        name: "Crypto Ops HQ"
        channels:
          - id: "987654321"
            name: "bot-commands"
          - id: "555666777"
            name: "crypto-alerts"
```

**Env vars emitted to ALL pod services:**

```
CLAW_HANDLE_CRYPTO_CRUSHER_DISCORD_ID=123456789
CLAW_HANDLE_CRYPTO_CRUSHER_DISCORD_USERNAME=crypto-bot          # only if set
CLAW_HANDLE_CRYPTO_CRUSHER_DISCORD_GUILDS=111222333             # comma-sep, only if set
CLAW_HANDLE_CRYPTO_CRUSHER_DISCORD_JSON={"id":"123456789",...}  # always, full schema
```

Service name is uppercased, hyphens replaced with underscores.

**Key reference files:**
- `internal/clawfile/directives.go` — directive type definitions
- `internal/clawfile/parser.go` — Clawfile line parser
- `internal/clawfile/emit.go` — Dockerfile label emission
- `internal/inspect/inspect.go` — label parsing from built image
- `internal/pod/types.go` — ClawBlock, HandleInfo types
- `internal/pod/parser.go` — x-claw YAML parsing
- `internal/driver/types.go` — ResolvedClaw
- `cmd/claw/compose_up.go` — main orchestration
- `internal/pod/compose_emit.go` — compose YAML generation
- `internal/driver/openclaw/driver.go` — config injection
- `internal/driver/openclaw/clawdapus_md.go` — CLAWDAPUS.md generation
- `internal/driver/openclaw/handle_skill.go` — handle skill file generation (new)

---

## Task 1: Add HandleDirective to Clawfile parser ✅ DONE

**File:** `internal/clawfile/directives.go` — Added `Handles []string` to `ClawConfig`.
**File:** `internal/clawfile/parser.go` — Added `"handle"` to `knownDirectives`. Parses `HANDLE <platform>`, single arg, lowercased, appended to `config.Handles`.

---

## Task 2: Emit HANDLE labels in Dockerfile ✅ DONE

**File:** `internal/clawfile/emit.go` — Emits `LABEL claw.handle.<platform>=true` for each handle.

---

## Task 3: Parse claw.handle.* labels in inspect ✅ DONE

**File:** `internal/inspect/inspect.go` — Added `Handles []string` to `ClawInfo`. Collects `claw.handle.*` keys, sorts alphabetically.

---

## Task 4: Add HandleInfo type and x-claw parser support

**File:** `internal/pod/types.go`

Add `HandleInfo`, `GuildInfo`, `ChannelInfo` structs. Update `ClawBlock`:

```go
type HandleInfo struct {
    ID       string
    Username string
    Guilds   []GuildInfo
}

type GuildInfo struct {
    ID       string
    Name     string
    Channels []ChannelInfo
}

type ChannelInfo struct {
    ID   string
    Name string
}

// In ClawBlock:
Handles map[string]*HandleInfo // platform → contact card
```

**File:** `internal/pod/parser.go`

Parse `handles:` from x-claw. Support two forms:
- String: `discord: "123456789"` → `&HandleInfo{ID: "123456789"}`
- Map: `discord: {id: "...", username: "...", guilds: [...]}` → full struct

YAML guild/channel entries: `{id: "...", name: "..."}` or just `"<id>"` string shorthand for channels.

Numeric values (unquoted IDs) coerced to string.

---

## Task 5: Add Handles to ResolvedClaw

**File:** `internal/driver/types.go`

```go
// In ResolvedClaw:
Handles map[string]*HandleInfo // platform → contact card (from x-claw)
```

Where `HandleInfo`, `GuildInfo`, `ChannelInfo` are imported from `internal/pod` or moved to `internal/driver` (prefer driver package to avoid import cycle).

> **Note on package placement:** If `ResolvedClaw` is in `internal/driver` and `ClawBlock` is in `internal/pod`, define `HandleInfo` in `internal/driver/types.go` and have the pod parser construct `driver.HandleInfo` values. Check for import cycles before deciding.

---

## Task 6: Wire handles into ResolvedClaw in compose_up

**File:** `cmd/claw/compose_up.go`

When building `ResolvedClaw` for each service, copy `svc.Claw.Handles` into `rc.Handles`.

---

## Task 7: Generate CLAW_HANDLE_* env vars and inject into all services

**File:** `cmd/claw/compose_up.go`

After all services are resolved, compute handle broadcast env vars:

```go
handleEnvs := map[string]string{}
for name, rc := range resolvedClaws {
    prefix := "CLAW_HANDLE_" + envName(name) + "_"
    for platform, info := range rc.Handles {
        pfx := prefix + strings.ToUpper(platform)
        handleEnvs[pfx+"_ID"] = info.ID
        if info.Username != "" {
            handleEnvs[pfx+"_USERNAME"] = info.Username
        }
        if len(info.Guilds) > 0 {
            ids := collectGuildIDs(info.Guilds)
            handleEnvs[pfx+"_GUILDS"] = strings.Join(ids, ",")
        }
        jsonBytes, _ := json.Marshal(info)
        handleEnvs[pfx+"_JSON"] = string(jsonBytes)
    }
}
```

Where `envName` uppercases and replaces hyphens with underscores.

Pass `handleEnvs` to compose emitter — injected into ALL services (claw and non-claw). Handle env vars are lower-priority: they never override existing env vars. Sort keys for determinism.

**File:** `internal/pod/compose_emit.go`

Accept and merge handle env vars into every service's environment block.

---

## Task 8: OpenClaw driver — HANDLE enables platform config

**File:** `internal/driver/openclaw/driver.go` (or `handle.go`)

During `Materialize`, for each platform in `rc.Handles`, apply:
- `discord` → `channels.discord.enabled = true`
- `slack` → `channels.slack.enabled = true`
- `telegram` → `channels.telegram.enabled = true`
- Unknown platform → log warning, continue

Token/credentials come from env vars (`DISCORD_TOKEN`, etc.) — Clawdapus doesn't manage them.

When `SURFACE channel://discord` is also declared (Phase 3 Slice 3), it applies full routing config on top. `HANDLE` alone enables with defaults.

---

## Task 9: CLAWDAPUS.md — Handles section

**File:** `internal/driver/openclaw/clawdapus_md.go`

Add `## Handles` section after Identity, before Surfaces. Emit nested guild/channel tree:

```markdown
## Handles
### discord
- **ID:** 123456789
- **Username:** @crypto-bot
- **Guilds:**
  - Crypto Ops HQ (111222333)
    - #bot-commands (987654321)
    - #crypto-alerts (555666777)
```

Only emit when `rc.Handles` is non-empty.

---

## Task 10: Generate skills/handle-<platform>.md skill files

**File:** `internal/driver/openclaw/handle_skill.go` (new)

For each platform in `rc.Handles`, generate `skills/handle-<platform>.md`:

```markdown
# Discord Handle: @crypto-bot

## Identity
- **User ID:** 123456789
- **Username:** crypto-bot

## Guild Membership

### Crypto Ops HQ (111222333)
- **#bot-commands** (987654321)
- **#crypto-alerts** (555666777)

## How to Mention
Mention this agent with `<@123456789>`.

## How to Route a Message to This Agent
Post in one of the channels above and include `<@123456789>` in your message.
DMs: send a direct message to user ID 123456789.
```

Wire `resolveHandleSkills` into `Materialize` — mount each generated skill file read-only into the runner's `SkillDir`. Add to CLAWDAPUS.md skills index.

---

## Task 11: Update the openclaw example

**File:** `examples/openclaw/claw-pod.yml`

Update crypto-crusher x-claw block with structured handles:

```yaml
handles:
  discord:
    id: "${DISCORD_USER_ID}"
    username: "crypto-crusher-bot"
    guilds:
      - id: "${DISCORD_GUILD_ID}"
        name: "Crypto Ops"
        channels:
          - id: "${DISCORD_CHANNEL_ID}"
            name: "bot-commands"
```

---

## Task 12: Final verification

```bash
go build ./...
go test ./...
go vet ./...
```

All pass. Inspect the openclaw example image, verify handle labels. Check a generated compose.generated.yml for `CLAW_HANDLE_*` env vars in all services.

---

## Success Criteria

- `HANDLE discord` in Clawfile → `LABEL claw.handle.discord=true` in built image
- `handles: { discord: "123" }` (string form) → `CLAW_HANDLE_<SVC>_DISCORD_ID=123` + `CLAW_HANDLE_<SVC>_DISCORD_JSON={"id":"123",...}` in every service
- Full structured handles with guilds → `_GUILDS` var populated, `_JSON` includes full tree
- OpenClaw driver enables platform in config when HANDLE declared
- CLAWDAPUS.md includes `## Handles` section with guild/channel tree
- `skills/handle-discord.md` generated and mounted, indexed in CLAWDAPUS.md
- All existing tests continue to pass
