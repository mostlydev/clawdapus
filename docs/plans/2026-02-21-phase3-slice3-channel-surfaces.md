# Phase 3 Slice 3: Channel Surface Bindings

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** `SURFACE channel://discord` in Clawfile and claw-pod.yml declares a full channel surface with optional routing config. The OpenClaw driver translates the routing config into runner-native JSON5 patches. A skill file `skills/surface-discord.md` is generated and mounted. If the driver doesn't support the declared channel platform, preflight fails — the container doesn't start.

**Distinction from HANDLE:** `HANDLE discord` enables a platform with defaults and broadcasts the agent's identity. `SURFACE channel://discord` configures how the agent behaves on that platform (guilds, DM policies, allowlists). When both are present, the SURFACE config takes precedence for routing; HANDLE still fires for identity broadcasting.

**Implementation model:** v1 hardcoded driver ops (explicit JSON5 patches). This proves the channel surface flow end-to-end. Migrating to the LLM worker model (see `docs/plans/2026-02-21-llm-configuration-workers.md`) is a follow-up upgrade after this slice is proven.

**Key reference files:**
- `internal/clawfile/directives.go` — directive types
- `internal/pod/types.go` — ClawBlock, ResolvedSurface
- `internal/pod/parser.go` — x-claw YAML parsing
- `internal/driver/types.go` — ResolvedSurface, ChannelConfig
- `cmd/claw/compose_up.go` — surface resolution
- `internal/driver/openclaw/driver.go` — config injection
- `internal/driver/openclaw/surface_skill.go` — skill generation
- `internal/driver/openclaw/clawdapus_md.go` — CLAWDAPUS.md generation

---

## Task 1: Define ChannelConfig and update ResolvedSurface

**File:** `internal/driver/types.go`

Add `ChannelConfig` struct to represent the routing config declared in the surface:

```go
type ChannelGuildConfig struct {
    Policy         string   // "allowlist", "denylist", or ""
    Users          []string // user IDs for policy
    RequireMention bool
}

type ChannelDMConfig struct {
    Enabled   bool
    Policy    string
    AllowFrom []string
}

type ChannelConfig struct {
    Guilds map[string]ChannelGuildConfig // guild ID → config
    DM     ChannelDMConfig
}
```

Update `ResolvedSurface`:
```go
type ResolvedSurface struct {
    Scheme        string        // "channel", "service", "volume", "host"
    Target        string        // "discord", "fleet-master", etc.
    AccessMode    string        // for volume/host surfaces
    Ports         []string      // for service surfaces
    ChannelConfig *ChannelConfig // non-nil for channel surfaces
}
```

---

## Task 2: Parse channel surfaces from x-claw

**File:** `internal/pod/parser.go`

Channel surfaces can appear in two forms in the surfaces list:

**Simple string form** (just enable, no routing config):
```yaml
surfaces:
  - "channel://discord"
```

**Map form** (with routing config):
```yaml
surfaces:
  - channel://discord:
      guilds:
        "1465489501551067136":
          policy: allowlist
          users: ["167037070349434880"]
          require_mention: true
      dm:
        enabled: true
        policy: allowlist
        allow_from: ["167037070349434880"]
```

The current surface parser handles string entries. Extend it to also handle map entries for channel surfaces:

1. When iterating `surfaces:`, if an entry is a map (not a string), check the key for `channel://` prefix
2. Parse the YAML value as `ChannelConfig`
3. Build a `ResolvedSurface` with the parsed config

`ParseSurface` helper in `cmd/claw/compose_up.go` currently handles string parsing. Channel map surfaces bypass the string parser and go through a new `parseChannelSurface` helper.

**Test:** `internal/pod/parser_channel_test.go`

- String `"channel://discord"` → ResolvedSurface{Scheme: "channel", Target: "discord", ChannelConfig: nil}
- Map form with guild config → ChannelConfig populated correctly
- require_mention boolean parsed correctly
- Empty guilds map → no error
- Missing `dm:` section → DM config zero-value (disabled)

---

## Task 3: Wire channel surfaces through compose_up

**File:** `cmd/claw/compose_up.go`

Channel surfaces don't need compose-level wiring (no network or volume changes — they're driver-mediated). But they need to be passed to the driver in `ResolvedClaw.Surfaces`.

Ensure `ParseSurface` (or its replacement) correctly returns channel surfaces with their `ChannelConfig` attached. Channel surfaces with the map form in claw-pod.yml need special handling since they aren't plain strings.

Extend the surface parsing loop in compose_up to handle the map-form channel surface entries from the pod parser.

---

## Task 4: OpenClaw driver — translate channel surface to config ops

**File:** `internal/driver/openclaw/driver.go` (or new `channel.go`)

During `Materialize`, for each surface where `Scheme == "channel"`:

1. If `ChannelConfig == nil` (simple string form with no routing):
   - Apply only `channels.<platform>.enabled = true`
   - This is identical to what `HANDLE` does — simple enablement

2. If `ChannelConfig != nil` (map form with routing config):
   - Apply `channels.<platform>.enabled = true`
   - For each guild in `ChannelConfig.Guilds`, apply:
     - `channels.<platform>.guilds.<guildID>.requireMention = <bool>`
     - If policy: `channels.<platform>.guilds.<guildID>.policy = "<policy>"`
     - If users: `channels.<platform>.guilds.<guildID>.users = [...]`
   - If DM config:
     - `channels.<platform>.dmPolicy = "<policy>"`
     - If allow_from: `channels.<platform>.allowFrom = [...]`

Platform-to-config-key mapping (OpenClaw driver knows these):
- `discord` → `channels.discord.*`
- `slack` → `channels.slack.*`
- `telegram` → `channels.telegram.*`

**Unsupported platform:** If the platform is not in the driver's supported list, preflight should fail with a clear error: `"openclaw driver: unsupported channel platform: webhooks"`. Add supported channel platforms to `OpenClawDriver.Capabilities().Surfaces`.

**Preflight check:** In `Preflight()`, iterate `rc.Surfaces` and fail on unsupported channel schemes.

**Test:** `internal/driver/openclaw/channel_test.go`

- Simple `channel://discord` → config has `channels.discord.enabled = true`, no guild keys
- Map form with guild → correct nested JSON keys set
- require_mention: true → correct key
- DM allowlist config → correct key
- Unknown platform → preflight error
- Two channel surfaces (discord + slack) → both applied

---

## Task 5: Generate surface skill for channel surfaces

**File:** `internal/driver/openclaw/surface_skill.go`

Extend `GenerateServiceSkill` (or add `GenerateChannelSkill`) to handle channel surfaces:

```go
func GenerateChannelSkill(surface driver.ResolvedSurface) string {
    // Returns markdown describing the channel surface:
    // - Platform name
    // - Guild IDs the agent has access to
    // - DM configuration
    // - What credential env vars are expected (DISCORD_TOKEN, etc.)
    // - Short "how to use" section
}
```

Output example for `surface-discord.md`:
```markdown
# Discord Channel Surface

**Platform:** Discord
**Token env var:** `DISCORD_TOKEN`

## Guild Access
- Guild `1465489501551067136`: allowlist policy, mentions required

## Direct Messages
- DMs enabled: policy=allowlist

## Usage
Use the Discord channel to send messages, receive commands, and interact
with users. Messages arrive as agent invocations via the OpenClaw gateway.
Only reply to users matching the configured policy.
```

**File:** `cmd/claw/compose_up.go`

Wire channel surface skill generation into the skill resolution pipeline. Add a `resolveChannelGeneratedSkills` function following the same pattern as `resolveServiceGeneratedSkills`:
1. Iterate surfaces where `Scheme == "channel"`
2. Call `GenerateChannelSkill`
3. Write to runtime dir as `surface-<platform>.md`
4. Return as `[]driver.ResolvedSkill`

Merge channel generated skills into the skill set with the same precedence: generated < operator-provided.

**Test:** `internal/driver/openclaw/surface_skill_channel_test.go`

- Channel surface with routing → skill contains guild ID, DM config
- Simple channel surface → skill contains platform name and token env var hint
- Skill file named `surface-discord.md`

---

## Task 6: CLAWDAPUS.md — channel surface section

**File:** `internal/driver/openclaw/clawdapus_md.go`

Add channel surface handling to the Surfaces section:

```markdown
### discord (channel)
- **Platform:** Discord
- **Token:** `DISCORD_TOKEN` (env)
- **Skill:** `skills/surface-discord.md`
```

Add to the Skills index:
```markdown
- `skills/surface-discord.md` — Discord channel constraints and routing
```

**Test:** `internal/driver/openclaw/clawdapus_md_test.go`

- Channel surface → appears in Surfaces section with platform and token hint
- Channel skill appears in Skills index

---

## Task 7: Preflight — driver capability check

**File:** `internal/driver/openclaw/driver.go`

`DriverCapabilities.Surfaces` should list the supported channel platforms:
```go
Surfaces: []string{"channel:discord", "channel:slack", "channel:telegram"},
```

In `Preflight()`, check each surface in `rc.Surfaces`:
- If `Scheme == "channel"` and `Target` not in supported list → return error
- Generic driver: `Surfaces: []string{}` → all channel surfaces fail preflight with clear error

**Test:** Preflight returns error for unsupported channel scheme.

---

## Task 8: Update the openclaw example

**File:** `examples/openclaw/claw-pod.yml`

The example already has `channel://discord` as a simple string. Leave it as-is for the simple case. The plan doc (this file) and MANIFESTO show the full routing config form for documentation purposes.

---

## Task 9: Final verification

```bash
go test ./...
go build -o bin/claw ./cmd/claw
go vet ./...
```

Confirm:
- Channel surface with routing config → correct JSON5 keys in generated openclaw.json
- Skill file `skills/surface-discord.md` generated and mounted
- CLAWDAPUS.md references channel skill
- Unsupported channel platform → preflight error, container doesn't start
- All existing tests pass

---

## Success Criteria

- `SURFACE channel://discord` (string form) → enables Discord with defaults, generates skill
- `SURFACE channel://discord:` (map form) → full routing config applied to runner config
- Preflight fails for unsupported channel platforms (generic driver)
- `skills/surface-discord.md` generated and mounted into claw container
- CLAWDAPUS.md lists discord channel in Surfaces and Skills sections
- All tests pass

---

## After This Slice: LLM Worker Upgrade

Channel surface config translation (Task 4) is a candidate for the first LLM worker migration. The driver currently hardcodes `channels.discord.guilds.<id>.requireMention` key paths. When OpenClaw updates its schema, the driver breaks.

The upgrade path (after LLM worker infrastructure is built — see `docs/plans/2026-02-21-llm-configuration-workers.md`):
1. Keep `ChannelConfig` parsing (Tasks 1-3) — the intent format
2. Replace Task 4 (hardcoded driver ops) with `GenerateIntents` → worker applies → `Verify`
3. Driver shrinks to intent generator + verifier
4. Schema drift is handled by the worker, not the driver
