# Phase 3 Slice 3: Channel Surface Bindings

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** `SURFACE channel://discord` in claw-pod.yml declares a full channel surface with optional routing config (DM policy, allowFrom, guild policy). The OpenClaw driver translates it into openclaw.json config. A skill file `surface-discord.md` is generated. The trading-desk spike test verifies end-to-end.

**Architecture:** Map-form channel surfaces require the pod YAML parser to handle mixed `[]interface{}` surface entries (strings and maps). Surfaces are fully parsed at the pod layer into `driver.ResolvedSurface` (with optional `ChannelConfig`). The HANDLE directive sets structural defaults (identity, mentionPatterns, guild membership, allowBots). SURFACE channel refines routing (DM policy, allowFrom, guild policy) — these are operator-declared constraints HANDLE can't infer. Surface config is applied to openclaw.json after HANDLE, so it takes precedence.

**Tech Stack:** Go, gopkg.in/yaml.v3, encoding/json, internal driver/pod/openclaw packages, Docker, Discord REST API.

**Spike framing:** We add a map-form `channel://discord` surface to tiverton in `claw-pod.yml` with a DM config that includes `allow_from: ["${OPERATOR_DISCORD_ID}"]`. The spike test then verifies `channels.discord.allowFrom` contains the operator ID in the generated `openclaw.json`, and that `surface-discord.md` appears in the skills directory inside the container.

---

## Task 1: Add ChannelConfig types and update ResolvedSurface

**Files:**
- Modify: `internal/driver/types.go`

This is types-only — no tests needed here, coverage comes from usages in later tasks.

**Step 1: Add three new structs before `ResolvedSurface` in `internal/driver/types.go`**

```go
// ChannelGuildConfig is the routing config for one guild in a channel surface.
type ChannelGuildConfig struct {
	Policy         string   // "allowlist", "denylist", or "" (inherit platform default)
	Users          []string // user IDs for the policy list
	RequireMention bool
}

// ChannelDMConfig is the DM routing config for a channel surface.
type ChannelDMConfig struct {
	Enabled   bool
	Policy    string   // "allowlist", "denylist", or ""
	AllowFrom []string // user IDs allowed to DM the bot
}

// ChannelConfig is the full routing config declared in a map-form channel surface.
// Non-nil only when the pod declares map form (channel://discord: {...}).
type ChannelConfig struct {
	Guilds map[string]ChannelGuildConfig // guild ID → routing config
	DM     ChannelDMConfig
}
```

**Step 2: Add `ChannelConfig *ChannelConfig` field to `ResolvedSurface`**

```go
type ResolvedSurface struct {
	Scheme        string
	Target        string
	AccessMode    string
	Ports         []string
	ChannelConfig *ChannelConfig // non-nil only for map-form channel surfaces
}
```

**Step 3: Verify it compiles**

```bash
go build ./internal/driver/...
```
Expected: no errors.

**Step 4: Commit**

```bash
git add internal/driver/types.go
git commit -m "feat: add ChannelConfig types to ResolvedSurface"
```

---

## Task 2: Parse map-form channel surfaces in the pod layer

**Files:**
- Modify: `internal/pod/types.go` — change `ClawBlock.Surfaces` from `[]string` to `[]driver.ResolvedSurface`
- Modify: `internal/pod/parser.go` — handle `[]interface{}` surfaces in rawClawBlock; call new helper
- Modify: `internal/pod/surface.go` — add `parseChannelSurfaceMap` and `parseChannelConfig`
- Create: `internal/pod/parser_channel_surface_test.go`

This is the structural heart of the feature. Surfaces must move from raw strings to parsed structs at the pod parse layer so map-form entries can carry `ChannelConfig`.

**Step 1: Write failing tests in `internal/pod/parser_channel_surface_test.go`**

```go
package pod

import (
	"strings"
	"testing"
)

const podWithStringChannelSurface = `
x-claw:
  pod: test-pod
services:
  svc:
    image: test:latest
    x-claw:
      agent: AGENTS.md
      surfaces:
        - "channel://discord"
`

const podWithMapChannelSurface = `
x-claw:
  pod: test-pod
services:
  svc:
    image: test:latest
    x-claw:
      agent: AGENTS.md
      surfaces:
        - channel://discord:
            dm:
              enabled: true
              policy: allowlist
              allow_from:
                - "167037070349434880"
`

const podWithMapChannelSurfaceGuilds = `
x-claw:
  pod: test-pod
services:
  svc:
    image: test:latest
    x-claw:
      agent: AGENTS.md
      surfaces:
        - channel://discord:
            guilds:
              "1465489501551067136":
                policy: allowlist
                require_mention: true
                users:
                  - "167037070349434880"
`

const podWithMixedSurfaces = `
x-claw:
  pod: test-pod
services:
  svc:
    image: test:latest
    x-claw:
      agent: AGENTS.md
      surfaces:
        - "service://trading-api"
        - channel://discord:
            dm:
              enabled: true
              policy: allowlist
              allow_from:
                - "111222333"
`

const podWithMapNonChannelSurface = `
x-claw:
  pod: test-pod
services:
  svc:
    image: test:latest
    x-claw:
      agent: AGENTS.md
      surfaces:
        - volume://cache:
            dm:
              enabled: true
`

func TestParsePodStringChannelSurface(t *testing.T) {
	p := mustParsePod(t, podWithStringChannelSurface)
	svc := p.Services["svc"]
	if len(svc.Claw.Surfaces) != 1 {
		t.Fatalf("expected 1 surface, got %d", len(svc.Claw.Surfaces))
	}
	s := svc.Claw.Surfaces[0]
	if s.Scheme != "channel" {
		t.Errorf("expected scheme=channel, got %q", s.Scheme)
	}
	if s.Target != "discord" {
		t.Errorf("expected target=discord, got %q", s.Target)
	}
	if s.ChannelConfig != nil {
		t.Error("expected ChannelConfig=nil for string-form channel surface")
	}
}

func TestParsePodMapChannelSurfaceDM(t *testing.T) {
	p := mustParsePod(t, podWithMapChannelSurface)
	svc := p.Services["svc"]
	if len(svc.Claw.Surfaces) != 1 {
		t.Fatalf("expected 1 surface, got %d", len(svc.Claw.Surfaces))
	}
	s := svc.Claw.Surfaces[0]
	if s.Scheme != "channel" || s.Target != "discord" {
		t.Errorf("expected channel://discord, got %s://%s", s.Scheme, s.Target)
	}
	if s.ChannelConfig == nil {
		t.Fatal("expected non-nil ChannelConfig")
	}
	dm := s.ChannelConfig.DM
	if !dm.Enabled {
		t.Error("expected DM.Enabled=true")
	}
	if dm.Policy != "allowlist" {
		t.Errorf("expected DM.Policy=allowlist, got %q", dm.Policy)
	}
	if len(dm.AllowFrom) != 1 || dm.AllowFrom[0] != "167037070349434880" {
		t.Errorf("expected AllowFrom=[167037070349434880], got %v", dm.AllowFrom)
	}
}

func TestParsePodMapChannelSurfaceGuilds(t *testing.T) {
	p := mustParsePod(t, podWithMapChannelSurfaceGuilds)
	svc := p.Services["svc"]
	s := svc.Claw.Surfaces[0]
	if s.ChannelConfig == nil {
		t.Fatal("expected non-nil ChannelConfig")
	}
	g, ok := s.ChannelConfig.Guilds["1465489501551067136"]
	if !ok {
		t.Fatal("expected guild 1465489501551067136")
	}
	if g.Policy != "allowlist" {
		t.Errorf("expected guild policy=allowlist, got %q", g.Policy)
	}
	if !g.RequireMention {
		t.Error("expected guild.RequireMention=true")
	}
	if len(g.Users) != 1 || g.Users[0] != "167037070349434880" {
		t.Errorf("expected guild.Users=[167037070349434880], got %v", g.Users)
	}
}

func TestParsePodMixedSurfaces(t *testing.T) {
	p := mustParsePod(t, podWithMixedSurfaces)
	svc := p.Services["svc"]
	if len(svc.Claw.Surfaces) != 2 {
		t.Fatalf("expected 2 surfaces, got %d", len(svc.Claw.Surfaces))
	}
	if svc.Claw.Surfaces[0].Scheme != "service" {
		t.Errorf("expected first surface scheme=service, got %q", svc.Claw.Surfaces[0].Scheme)
	}
	if svc.Claw.Surfaces[1].Scheme != "channel" {
		t.Errorf("expected second surface scheme=channel, got %q", svc.Claw.Surfaces[1].Scheme)
	}
	if svc.Claw.Surfaces[1].ChannelConfig == nil {
		t.Error("expected second surface ChannelConfig non-nil")
	}
}

func TestParsePodMapNonChannelSurfaceErrors(t *testing.T) {
	_, err := parsePodString(podWithMapNonChannelSurface)
	if err == nil {
		t.Fatal("expected error for map-form non-channel surface")
	}
	if !strings.Contains(err.Error(), "channel") {
		t.Errorf("expected error mentioning 'channel', got: %v", err)
	}
}

// helpers shared with other test files
func mustParsePod(t *testing.T, yaml string) *Pod {
	t.Helper()
	p, err := parsePodString(yaml)
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	return p
}

func parsePodString(yaml string) (*Pod, error) {
	return Parse(strings.NewReader(yaml))
}
```

**Step 2: Run tests — expect compile errors**

```bash
go test ./internal/pod/... 2>&1 | head -30
```
Expected: compile errors because `svc.Claw.Surfaces` doesn't exist as `[]driver.ResolvedSurface` yet.

**Step 3: Change `ClawBlock.Surfaces` type in `internal/pod/types.go`**

Change:
```go
Surfaces []string
```
To:
```go
Surfaces []driver.ResolvedSurface
```

**Step 4: Change `rawClawBlock.Surfaces` in `internal/pod/parser.go`**

Change:
```go
Surfaces []string `yaml:"surfaces"`
```
To:
```go
Surfaces []interface{} `yaml:"surfaces"`
```

**Step 5: Add `parseChannelSurfaceMap` and `parseChannelConfig` to `internal/pod/surface.go`**

Add after the existing `ParseSurface` function:

```go
// parseChannelSurfaceMap parses a map-form channel surface entry.
// The map must have exactly one key: a channel:// URI.
// Only channel:// scheme is supported in map form.
func parseChannelSurfaceMap(m map[string]interface{}) (driver.ResolvedSurface, error) {
	if len(m) != 1 {
		return driver.ResolvedSurface{}, fmt.Errorf("map-form surface must have exactly one key (the URI), got %d", len(m))
	}
	for rawKey, rawVal := range m {
		s, err := ParseSurface(rawKey)
		if err != nil {
			return driver.ResolvedSurface{}, fmt.Errorf("invalid surface URI %q: %w", rawKey, err)
		}
		if s.Scheme != "channel" {
			return driver.ResolvedSurface{}, fmt.Errorf("map-form surface only supported for channel:// scheme, got %q — use string form for %s", s.Scheme, rawKey)
		}
		config, err := parseChannelConfig(rawVal)
		if err != nil {
			return driver.ResolvedSurface{}, fmt.Errorf("surface %q: %w", rawKey, err)
		}
		s.ChannelConfig = config
		return s, nil
	}
	return driver.ResolvedSurface{}, fmt.Errorf("empty surface map")
}

// parseChannelConfig converts a raw YAML map into a ChannelConfig.
// Returns nil (no error) if the raw value is nil — meaning just enable the channel.
func parseChannelConfig(raw interface{}) (*driver.ChannelConfig, error) {
	if raw == nil {
		return nil, nil
	}
	m, ok := raw.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("channel config must be a map, got %T", raw)
	}
	config := &driver.ChannelConfig{}

	if guildsRaw, ok := m["guilds"]; ok {
		guildsMap, ok := guildsRaw.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("channel config guilds must be a map, got %T", guildsRaw)
		}
		config.Guilds = make(map[string]driver.ChannelGuildConfig, len(guildsMap))
		for guildID, guildRaw := range guildsMap {
			gc, err := parseChannelGuildConfig(guildRaw)
			if err != nil {
				return nil, fmt.Errorf("guild %q: %w", guildID, err)
			}
			config.Guilds[guildID] = gc
		}
	}

	if dmRaw, ok := m["dm"]; ok {
		dm, err := parseChannelDMConfig(dmRaw)
		if err != nil {
			return nil, fmt.Errorf("dm config: %w", err)
		}
		config.DM = dm
	}

	return config, nil
}

func parseChannelGuildConfig(raw interface{}) (driver.ChannelGuildConfig, error) {
	m, ok := raw.(map[string]interface{})
	if !ok {
		return driver.ChannelGuildConfig{}, fmt.Errorf("guild config must be a map, got %T", raw)
	}
	gc := driver.ChannelGuildConfig{}
	if v, ok := m["policy"]; ok {
		s, ok := v.(string)
		if !ok {
			return gc, fmt.Errorf("guild policy must be a string")
		}
		gc.Policy = s
	}
	if v, ok := m["require_mention"]; ok {
		b, ok := v.(bool)
		if !ok {
			return gc, fmt.Errorf("guild require_mention must be a bool")
		}
		gc.RequireMention = b
	}
	if v, ok := m["users"]; ok {
		users, err := toStringSlice(v)
		if err != nil {
			return gc, fmt.Errorf("guild users: %w", err)
		}
		gc.Users = users
	}
	return gc, nil
}

func parseChannelDMConfig(raw interface{}) (driver.ChannelDMConfig, error) {
	m, ok := raw.(map[string]interface{})
	if !ok {
		return driver.ChannelDMConfig{}, fmt.Errorf("dm config must be a map, got %T", raw)
	}
	dm := driver.ChannelDMConfig{}
	if v, ok := m["enabled"]; ok {
		b, ok := v.(bool)
		if !ok {
			return dm, fmt.Errorf("dm.enabled must be a bool")
		}
		dm.Enabled = b
	}
	if v, ok := m["policy"]; ok {
		s, ok := v.(string)
		if !ok {
			return dm, fmt.Errorf("dm.policy must be a string")
		}
		dm.Policy = s
	}
	if v, ok := m["allow_from"]; ok {
		ids, err := toStringSlice(v)
		if err != nil {
			return dm, fmt.Errorf("dm.allow_from: %w", err)
		}
		dm.AllowFrom = ids
	}
	return dm, nil
}

// toStringSlice converts []interface{} (from YAML) to []string.
func toStringSlice(raw interface{}) ([]string, error) {
	slice, ok := raw.([]interface{})
	if !ok {
		return nil, fmt.Errorf("expected list, got %T", raw)
	}
	out := make([]string, 0, len(slice))
	for i, v := range slice {
		s, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("entry %d must be a string, got %T", i, v)
		}
		out = append(out, s)
	}
	return out, nil
}
```

**Step 6: Update the surface parsing loop in `internal/pod/parser.go`**

Replace the existing surface handling in `Parse()`. Currently:
```go
surfaces := svc.XClaw.Surfaces
if surfaces == nil {
    surfaces = make([]string, 0)
}
```

Replace with:
```go
parsedSurfaces := make([]driver.ResolvedSurface, 0, len(svc.XClaw.Surfaces))
for _, rawSurf := range svc.XClaw.Surfaces {
    switch v := rawSurf.(type) {
    case string:
        s, err := ParseSurface(v)
        if err != nil {
            return nil, fmt.Errorf("service %q: surface %q: %w", name, v, err)
        }
        parsedSurfaces = append(parsedSurfaces, s)
    case map[string]interface{}:
        s, err := parseChannelSurfaceMap(v)
        if err != nil {
            return nil, fmt.Errorf("service %q: map-form surface: %w", name, err)
        }
        parsedSurfaces = append(parsedSurfaces, s)
    default:
        return nil, fmt.Errorf("service %q: unsupported surface entry type %T", name, rawSurf)
    }
}
```

And update the ClawBlock construction:
```go
service.Claw = &ClawBlock{
    ...
    Surfaces: parsedSurfaces,
    ...
}
```

**Step 7: Fix compilation errors in files that construct `ClawBlock.Surfaces` with `[]string`**

Search for breakage:
```bash
go build ./... 2>&1 | grep -v "^#"
```

Expected compile errors in test files that set `Surfaces: []string{...}`. For each, change to use `driver.ResolvedSurface`:

In `internal/pod/integration_test.go`, `compose_emit_service_test.go`, `compose_emit_test.go`, etc.:

Old:
```go
Surfaces: []string{"volume://research-cache read-write", "channel://discord"},
```

New:
```go
Surfaces: []driver.ResolvedSurface{
    {Scheme: "volume", Target: "research-cache", AccessMode: "read-write"},
    {Scheme: "channel", Target: "discord"},
},
```

Also fix `cmd/claw/compose_up.go`: the existing surface parsing loop (that calls `pod.ParseSurface(raw)`) must be replaced. The surfaces are now pre-parsed. Change:

```go
// OLD — remove this loop:
var surfaces []driver.ResolvedSurface
if svc.Claw != nil {
    for _, raw := range svc.Claw.Surfaces {
        s, err := pod.ParseSurface(raw)
        if err != nil {
            return fmt.Errorf("service %q: %w", name, err)
        }
        surfaces = append(surfaces, s)
    }
}
```

To:
```go
// NEW — surfaces already parsed by pod.Parse()
surfaces := svc.Claw.Surfaces
```

**Step 8: Run the tests**

```bash
go test ./internal/pod/... ./cmd/claw/...
```
Expected: new `parser_channel_surface_test.go` tests pass; existing tests pass.

**Step 9: Commit**

```bash
git add internal/driver/types.go internal/pod/types.go internal/pod/parser.go internal/pod/surface.go internal/pod/parser_channel_surface_test.go cmd/claw/compose_up.go
git commit -m "feat: parse map-form channel surfaces in pod layer"
```

---

## Task 3: Generate surface-discord.md skill + wire into compose_up

**Files:**
- Modify: `internal/driver/openclaw/surface_skill.go` — add `GenerateChannelSkill`
- Create: `internal/driver/openclaw/surface_skill_channel_test.go`
- Modify: `cmd/claw/compose_up.go` — add `resolveChannelGeneratedSkills` and wire it

**Step 1: Write failing tests in `internal/driver/openclaw/surface_skill_channel_test.go`**

```go
package openclaw

import (
	"strings"
	"testing"

	"github.com/mostlydev/clawdapus/internal/driver"
)

func TestGenerateChannelSkillSimple(t *testing.T) {
	surface := driver.ResolvedSurface{
		Scheme: "channel",
		Target: "discord",
	}
	skill := GenerateChannelSkill(surface)
	if !strings.Contains(skill, "Discord") {
		t.Error("expected platform name Discord in skill")
	}
	if !strings.Contains(skill, "DISCORD_BOT_TOKEN") {
		t.Error("expected token env var hint in skill")
	}
	if !strings.Contains(skill, "Usage") {
		t.Error("expected Usage section in skill")
	}
	// No DM or Guild sections for nil config
	if strings.Contains(skill, "Direct Messages") {
		t.Error("expected no DM section for nil ChannelConfig")
	}
}

func TestGenerateChannelSkillWithDM(t *testing.T) {
	surface := driver.ResolvedSurface{
		Scheme: "channel",
		Target: "discord",
		ChannelConfig: &driver.ChannelConfig{
			DM: driver.ChannelDMConfig{
				Enabled: true,
				Policy:  "allowlist",
			},
		},
	}
	skill := GenerateChannelSkill(surface)
	if !strings.Contains(skill, "Direct Messages") {
		t.Error("expected DM section when DM config present")
	}
	if !strings.Contains(skill, "allowlist") {
		t.Error("expected DM policy in skill")
	}
}

func TestGenerateChannelSkillWithGuilds(t *testing.T) {
	surface := driver.ResolvedSurface{
		Scheme: "channel",
		Target: "discord",
		ChannelConfig: &driver.ChannelConfig{
			Guilds: map[string]driver.ChannelGuildConfig{
				"1465489501551067136": {
					Policy:         "allowlist",
					RequireMention: true,
				},
			},
		},
	}
	skill := GenerateChannelSkill(surface)
	if !strings.Contains(skill, "Guild Access") {
		t.Error("expected Guild Access section when guilds present")
	}
	if !strings.Contains(skill, "1465489501551067136") {
		t.Error("expected guild ID in skill")
	}
}

func TestGenerateChannelSkillSlack(t *testing.T) {
	surface := driver.ResolvedSurface{Scheme: "channel", Target: "slack"}
	skill := GenerateChannelSkill(surface)
	if !strings.Contains(skill, "Slack") {
		t.Error("expected Slack in skill")
	}
	if !strings.Contains(skill, "SLACK_BOT_TOKEN") {
		t.Error("expected SLACK_BOT_TOKEN hint")
	}
}
```

**Step 2: Run tests — expect failure (function doesn't exist)**

```bash
go test ./internal/driver/openclaw/... -run TestGenerateChannelSkill
```
Expected: compile error `undefined: GenerateChannelSkill`.

**Step 3: Add `GenerateChannelSkill` and `platformTokenVar` to `internal/driver/openclaw/surface_skill.go`**

```go
// GenerateChannelSkill produces a markdown skill file for a channel surface.
// Describes the platform, token env var, routing config, and usage guidance.
func GenerateChannelSkill(surface driver.ResolvedSurface) string {
	var b strings.Builder
	platformTitle := strings.ToUpper(surface.Target[:1]) + surface.Target[1:]

	b.WriteString(fmt.Sprintf("# %s Channel Surface\n\n", platformTitle))
	b.WriteString(fmt.Sprintf("**Platform:** %s\n", platformTitle))
	if tokenVar := platformTokenVar(surface.Target); tokenVar != "" {
		b.WriteString(fmt.Sprintf("**Token env var:** `%s`\n", tokenVar))
	}
	b.WriteString("\n")

	if cc := surface.ChannelConfig; cc != nil {
		if len(cc.Guilds) > 0 {
			b.WriteString("## Guild Access\n\n")
			// Sort guild IDs for determinism
			guildIDs := make([]string, 0, len(cc.Guilds))
			for id := range cc.Guilds {
				guildIDs = append(guildIDs, id)
			}
			sort.Strings(guildIDs)
			for _, guildID := range guildIDs {
				g := cc.Guilds[guildID]
				line := fmt.Sprintf("- Guild `%s`", guildID)
				if g.Policy != "" {
					line += fmt.Sprintf(": %s policy", g.Policy)
				}
				if g.RequireMention {
					line += ", mentions required"
				}
				b.WriteString(line + "\n")
			}
			b.WriteString("\n")
		}

		if cc.DM.Enabled || cc.DM.Policy != "" || len(cc.DM.AllowFrom) > 0 {
			b.WriteString("## Direct Messages\n\n")
			line := "- DMs"
			if cc.DM.Enabled {
				line += " enabled"
			}
			if cc.DM.Policy != "" {
				line += fmt.Sprintf(": policy=%s", cc.DM.Policy)
			}
			b.WriteString(line + "\n\n")
		}
	}

	b.WriteString("## Usage\n\n")
	b.WriteString(fmt.Sprintf("Use the %s channel to send messages, receive commands, and interact with users.\n", platformTitle))
	b.WriteString("Messages arrive as agent invocations via the OpenClaw gateway.\n")
	if cc := surface.ChannelConfig; cc != nil && (cc.DM.Policy != "" || len(cc.Guilds) > 0) {
		b.WriteString("Only reply to users matching the configured policy.\n")
	}

	return b.String()
}

// platformTokenVar returns the conventional env var name for a platform's bot token.
func platformTokenVar(platform string) string {
	switch strings.ToLower(platform) {
	case "discord":
		return "DISCORD_BOT_TOKEN"
	case "slack":
		return "SLACK_BOT_TOKEN"
	case "telegram":
		return "TELEGRAM_BOT_TOKEN"
	default:
		return ""
	}
}
```

Add `"sort"` to the import block in `surface_skill.go`.

**Step 4: Run the tests**

```bash
go test ./internal/driver/openclaw/... -run TestGenerateChannelSkill
```
Expected: all 4 tests pass.

**Step 5: Add `resolveChannelGeneratedSkills` to `cmd/claw/compose_up.go`**

Add after `resolveServiceGeneratedSkills`:

```go
// resolveChannelGeneratedSkills generates surface-<platform>.md skill files for
// each channel surface and returns them as ResolvedSkills.
func resolveChannelGeneratedSkills(runtimeDir string, surfaces []driver.ResolvedSurface) ([]driver.ResolvedSkill, error) {
	surfaceSkillsDir := filepath.Join(runtimeDir, "skills")
	generated := make([]driver.ResolvedSkill, 0)
	seen := make(map[string]struct{})

	for _, surface := range surfaces {
		if surface.Scheme != "channel" {
			continue
		}
		name := fmt.Sprintf("surface-%s.md", strings.TrimSpace(surface.Target))
		if _, exists := seen[name]; exists {
			continue
		}
		seen[name] = struct{}{}

		skillPath := filepath.Join(surfaceSkillsDir, name)
		if err := os.MkdirAll(filepath.Dir(skillPath), 0700); err != nil {
			return nil, fmt.Errorf("create channel skill dir: %w", err)
		}
		content := openclaw.GenerateChannelSkill(surface)
		if err := writeRuntimeFile(skillPath, []byte(content), 0644); err != nil {
			return nil, fmt.Errorf("write channel skill %q: %w", name, err)
		}
		generated = append(generated, driver.ResolvedSkill{
			Name:     name,
			HostPath: skillPath,
		})
	}
	return generated, nil
}
```

**Step 6: Wire `resolveChannelGeneratedSkills` into the skill pipeline in `runComposeUp`**

In `runComposeUp`, after the call to `resolveServiceGeneratedSkills`:

```go
generatedSkills, err := resolveServiceGeneratedSkills(svcRuntimeDir, surfaces)
if err != nil {
    return fmt.Errorf("service %q: resolve generated service skills: %w", name, err)
}
// Add channel surface skills (surface-discord.md etc.)
channelSkills, err := resolveChannelGeneratedSkills(svcRuntimeDir, surfaces)
if err != nil {
    return fmt.Errorf("service %q: resolve generated channel skills: %w", name, err)
}
if len(channelSkills) > 0 {
    generatedSkills = mergeResolvedSkills(generatedSkills, channelSkills)
}
```

**Step 7: Run all tests**

```bash
go test ./...
```
Expected: pass.

**Step 8: Commit**

```bash
git add internal/driver/openclaw/surface_skill.go internal/driver/openclaw/surface_skill_channel_test.go cmd/claw/compose_up.go
git commit -m "feat: GenerateChannelSkill + resolveChannelGeneratedSkills"
```

---

## Task 4: Apply channel surface routing config to openclaw.json

**Files:**
- Modify: `internal/driver/openclaw/config.go` — add `applyDiscordChannelSurface` + wire into `GenerateConfig`
- Create: `internal/driver/openclaw/channel_config_test.go`

This is what actually changes the generated `openclaw.json`. HANDLE sets structural defaults. SURFACE channel refines routing. SURFACE runs after HANDLE so its values take precedence where they overlap (e.g. `dmPolicy`).

**Step 1: Write failing tests in `internal/driver/openclaw/channel_config_test.go`**

```go
package openclaw

import (
	"encoding/json"
	"testing"

	"github.com/mostlydev/clawdapus/internal/driver"
)

func TestGenerateConfigChannelSurfaceDMPolicy(t *testing.T) {
	rc := &driver.ResolvedClaw{
		Models:     make(map[string]string),
		Configures: []string{},
		Handles:    map[string]*driver.HandleInfo{"discord": {ID: "111"}},
		Surfaces: []driver.ResolvedSurface{
			{
				Scheme: "channel",
				Target: "discord",
				ChannelConfig: &driver.ChannelConfig{
					DM: driver.ChannelDMConfig{
						Enabled: true,
						Policy:  "denylist",
					},
				},
			},
		},
	}
	data, err := GenerateConfig(rc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var config map[string]interface{}
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	discord := config["channels"].(map[string]interface{})["discord"].(map[string]interface{})
	// SURFACE channel should override HANDLE's default dmPolicy
	if discord["dmPolicy"] != "denylist" {
		t.Errorf("expected dmPolicy=denylist from channel surface, got %v", discord["dmPolicy"])
	}
}

func TestGenerateConfigChannelSurfaceAllowFrom(t *testing.T) {
	rc := &driver.ResolvedClaw{
		Models:     make(map[string]string),
		Configures: []string{},
		Handles:    map[string]*driver.HandleInfo{"discord": {ID: "111"}},
		Surfaces: []driver.ResolvedSurface{
			{
				Scheme: "channel",
				Target: "discord",
				ChannelConfig: &driver.ChannelConfig{
					DM: driver.ChannelDMConfig{
						AllowFrom: []string{"167037070349434880", "999888777666"},
					},
				},
			},
		},
	}
	data, err := GenerateConfig(rc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var config map[string]interface{}
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	discord := config["channels"].(map[string]interface{})["discord"].(map[string]interface{})
	allowFrom, ok := discord["allowFrom"].([]interface{})
	if !ok {
		t.Fatalf("expected allowFrom to be an array, got %T", discord["allowFrom"])
	}
	if len(allowFrom) != 2 {
		t.Errorf("expected 2 allowFrom entries, got %d", len(allowFrom))
	}
	got := make(map[string]bool)
	for _, v := range allowFrom {
		got[v.(string)] = true
	}
	for _, expected := range []string{"167037070349434880", "999888777666"} {
		if !got[expected] {
			t.Errorf("expected %q in allowFrom, got %v", expected, allowFrom)
		}
	}
}

func TestGenerateConfigChannelSurfaceGuildPolicy(t *testing.T) {
	rc := &driver.ResolvedClaw{
		Models:     make(map[string]string),
		Configures: []string{},
		Handles: map[string]*driver.HandleInfo{
			"discord": {
				ID: "AAA",
				Guilds: []driver.GuildInfo{{ID: "GUILD1"}},
			},
		},
		Surfaces: []driver.ResolvedSurface{
			{
				Scheme: "channel",
				Target: "discord",
				ChannelConfig: &driver.ChannelConfig{
					Guilds: map[string]driver.ChannelGuildConfig{
						"GUILD1": {
							Policy:         "allowlist",
							RequireMention: true,
						},
					},
				},
			},
		},
	}
	data, err := GenerateConfig(rc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var config map[string]interface{}
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	discord := config["channels"].(map[string]interface{})["discord"].(map[string]interface{})
	guild := discord["guilds"].(map[string]interface{})["GUILD1"].(map[string]interface{})
	if guild["policy"] != "allowlist" {
		t.Errorf("expected guild policy=allowlist, got %v", guild["policy"])
	}
	if guild["requireMention"] != true {
		t.Errorf("expected guild requireMention=true, got %v", guild["requireMention"])
	}
}

func TestGenerateConfigChannelSurfaceNilConfigNoOp(t *testing.T) {
	// String-form channel surface (ChannelConfig == nil) should not error
	rc := &driver.ResolvedClaw{
		Models:     make(map[string]string),
		Configures: []string{},
		Handles:    map[string]*driver.HandleInfo{"discord": {ID: "111"}},
		Surfaces: []driver.ResolvedSurface{
			{Scheme: "channel", Target: "discord", ChannelConfig: nil},
		},
	}
	_, err := GenerateConfig(rc)
	if err != nil {
		t.Fatalf("expected no error for nil ChannelConfig, got: %v", err)
	}
}

func TestGenerateConfigChannelSurfaceUnknownPlatformSkipped(t *testing.T) {
	// Unknown channel platform should be silently skipped (no crash)
	rc := &driver.ResolvedClaw{
		Models:     make(map[string]string),
		Configures: []string{},
		Surfaces: []driver.ResolvedSurface{
			{
				Scheme: "channel",
				Target: "unknownplatform",
				ChannelConfig: &driver.ChannelConfig{
					DM: driver.ChannelDMConfig{Policy: "allowlist"},
				},
			},
		},
	}
	_, err := GenerateConfig(rc)
	if err != nil {
		t.Fatalf("unknown platform should be skipped, not error: %v", err)
	}
}
```

**Step 2: Run tests — expect failure**

```bash
go test ./internal/driver/openclaw/... -run TestGenerateConfigChannel
```
Expected: `FAIL` — config doesn't apply channel surface config yet.

**Step 3: Add `applyDiscordChannelSurface` to `internal/driver/openclaw/config.go`**

Add this function near `discordBotIDs`:

```go
// applyDiscordChannelSurface applies ChannelConfig to the openclaw config map
// for the discord channel. Runs after HANDLE so it can refine/override routing.
func applyDiscordChannelSurface(config map[string]interface{}, cc *driver.ChannelConfig) error {
	if cc.DM.Policy != "" {
		if err := setPath(config, "channels.discord.dmPolicy", cc.DM.Policy); err != nil {
			return err
		}
	}
	if len(cc.DM.AllowFrom) > 0 {
		if err := setPath(config, "channels.discord.allowFrom", stringsToIface(cc.DM.AllowFrom)); err != nil {
			return err
		}
	}
	for guildID, guildCfg := range cc.Guilds {
		base := fmt.Sprintf("channels.discord.guilds.%s", guildID)
		if guildCfg.Policy != "" {
			if err := setPath(config, base+".policy", guildCfg.Policy); err != nil {
				return err
			}
		}
		if guildCfg.RequireMention {
			if err := setPath(config, base+".requireMention", true); err != nil {
				return err
			}
		}
		if len(guildCfg.Users) > 0 {
			if err := setPath(config, base+".users", stringsToIface(guildCfg.Users)); err != nil {
				return err
			}
		}
	}
	return nil
}
```

**Step 4: Wire into `GenerateConfig` in `config.go`**

After the HANDLE loop (and after CONFIGURE commands), add:

```go
// Apply SURFACE channel directives — refine routing config set by HANDLE.
// SURFACE runs after HANDLE so it takes precedence where keys overlap.
for _, surface := range rc.Surfaces {
    if surface.Scheme != "channel" || surface.ChannelConfig == nil {
        continue
    }
    switch surface.Target {
    case "discord":
        if err := applyDiscordChannelSurface(config, surface.ChannelConfig); err != nil {
            return nil, fmt.Errorf("config generation: SURFACE channel://discord: %w", err)
        }
    // Other platforms: silently skip (unsupported = no config, not an error here)
    }
}
```

**Step 5: Run the tests**

```bash
go test ./internal/driver/openclaw/... -run TestGenerateConfigChannel
```
Expected: all 5 tests pass.

**Step 6: Run all tests**

```bash
go test ./...
```
Expected: pass.

**Step 7: Commit**

```bash
git add internal/driver/openclaw/config.go internal/driver/openclaw/channel_config_test.go
git commit -m "feat: apply channel surface routing config to openclaw.json"
```

---

## Task 5: Update CLAWDAPUS.md for channel surfaces

**Files:**
- Modify: `internal/driver/openclaw/clawdapus_md.go`
- Modify: `internal/driver/openclaw/clawdapus_md_test.go`

The existing `case "channel":` is empty — the Surfaces section shows nothing for channel surfaces. Fix it, and add channel surface skills to the Skills index.

**Step 1: Update the failing assertion in `clawdapus_md_test.go`**

In `TestGenerateClawdapusMD`, the test already has a `channel://discord` surface in its RC fixture. Add assertions for channel-specific content:

```go
// Channel surface should describe token and skill
if !strings.Contains(md, "DISCORD_BOT_TOKEN") {
    t.Error("expected token env var hint for channel surface")
}
if !strings.Contains(md, "skills/surface-discord.md") {
    t.Error("expected channel skill in Skills index")
}
```

**Step 2: Run the test — expect failure**

```bash
go test ./internal/driver/openclaw/... -run TestGenerateClawdapusMD
```
Expected: FAIL — channel case is empty, token and skill aren't mentioned.

**Step 3: Fill in the `case "channel":` in `GenerateClawdapusMD`**

In `clawdapus_md.go`, replace the empty `case "channel":` block:

```go
case "channel":
    if tokenVar := platformTokenVar(s.Target); tokenVar != "" {
        b.WriteString(fmt.Sprintf("- **Token:** `%s` (env)\n", tokenVar))
    }
    b.WriteString(fmt.Sprintf("- **Skill:** `skills/surface-%s.md`\n", s.Target))
```

**Step 4: Add channel surface skills to the Skills index**

In the Skills index section, after the service surface skills loop:

```go
// Channel surface skills
for _, s := range rc.Surfaces {
    if s.Scheme == "channel" {
        skillEntries = append(skillEntries,
            fmt.Sprintf("- `skills/surface-%s.md` — %s channel surface", s.Target, s.Target))
    }
}
```

**Step 5: Run the tests**

```bash
go test ./internal/driver/openclaw/... -run TestGenerateClawdapusMD
```
Expected: pass.

**Step 6: Run all tests**

```bash
go test ./...
```
Expected: pass.

**Step 7: Commit**

```bash
git add internal/driver/openclaw/clawdapus_md.go internal/driver/openclaw/clawdapus_md_test.go
git commit -m "feat: channel surface in CLAWDAPUS.md surfaces + skills index"
```

---

## Task 6: Update trading-desk example + spike test

**Files:**
- Modify: `examples/trading-desk/claw-pod.yml` — add map-form channel surface to tiverton
- Modify: `examples/trading-desk/.env.example` — add OPERATOR_DISCORD_ID
- Modify: `cmd/claw/spike_test.go` — verify allowFrom + surface-discord.md

**Step 1: Add `OPERATOR_DISCORD_ID` to `.env.example`**

```bash
# Your personal Discord user ID — injected into the bot's allowFrom list via channel surface.
export OPERATOR_DISCORD_ID=your_discord_user_id
```

**Step 2: Add map-form channel surface to tiverton in `claw-pod.yml`**

In the tiverton service `surfaces:` block, change:

```yaml
    surfaces:
      - "service://trading-api"
      - "volume://clawd-shared read-write"
```

To:
```yaml
    surfaces:
      - "service://trading-api"
      - "volume://clawd-shared read-write"
      - channel://discord:
          dm:
            enabled: true
            policy: allowlist
            allow_from:
              - "${OPERATOR_DISCORD_ID}"
```

**Step 3: Add spike test assertions in `cmd/claw/spike_test.go`**

After the existing `guilds` assertions in the openclaw.json verification block, add:

```go
// Channel surface routing config: allowFrom should contain operator ID if set.
// This proves the map-form channel surface is parsed and applied to openclaw.json.
if operatorID := env["OPERATOR_DISCORD_ID"]; operatorID != "" {
    allowFrom, _ := discord["allowFrom"].([]interface{})
    found := false
    for _, id := range allowFrom {
        if s, ok := id.(string); ok && s == operatorID {
            found = true
            break
        }
    }
    if !found {
        t.Errorf("openclaw.json: expected channels.discord.allowFrom to contain operator ID %q, got %v",
            operatorID, allowFrom)
    }
}
```

After the existing skills directory check, add a specific assertion:

```go
// Channel surface skill should be present (generated from map-form SURFACE channel://discord)
if strings.Contains(string(out3), "surface-discord.md") {
    t.Logf("surface-discord.md confirmed in skills")
} else {
    t.Errorf("expected surface-discord.md in /claw/skills/, got: %s", strings.TrimSpace(string(out3)))
}
```

**Step 4: Verify the pod parses cleanly**

```bash
go build -o bin/claw ./cmd/claw && ./bin/claw up -f examples/trading-desk/claw-pod.yml --dry-run 2>&1 | head -20
```

If `--dry-run` isn't a flag, just verify build succeeds:
```bash
go build -o bin/claw ./cmd/claw
```
Expected: no errors.

**Step 5: Commit**

```bash
git add examples/trading-desk/claw-pod.yml examples/trading-desk/.env.example cmd/claw/spike_test.go
git commit -m "feat(trading-desk): add channel surface to tiverton; spike verifies allowFrom"
```

---

## Task 7: Final verification

**Step 1: Run all unit tests**

```bash
go test ./...
```
Expected: all pass, no regressions.

**Step 2: Build the binary**

```bash
go build -o bin/claw ./cmd/claw
```
Expected: no errors.

**Step 3: Run vet**

```bash
go vet ./...
```
Expected: no warnings.

**Step 4: If Docker + .env available, run the spike**

```bash
go test -tags spike -v -run TestSpikeComposeUp ./cmd/claw/...
```

Expected spike results:
- `openclaw.json` has `channels.discord.allowFrom: ["<OPERATOR_DISCORD_ID>"]` ← new
- `/claw/skills/surface-discord.md` exists in the container ← new
- All existing assertions (guild config, jobs.json, bot greetings) still pass ← unchanged

**Step 5: Update docs**

- `CLAUDE.md` — mark Phase 3 Slice 3 as DONE in the status table; add settled decisions for `ChannelConfig`, `applyDiscordChannelSurface`, map-form surface parsing
- `docs/plans/phase2-progress.md` — add Phase 3 Slice 3 completion entry with commit hashes

**Step 6: Final commit (docs)**

```bash
git add CLAUDE.md docs/plans/phase2-progress.md
git commit -m "docs: mark Phase 3 Slice 3 complete"
```

---

## Settled Design Decisions

- **HANDLE vs SURFACE channel**: HANDLE = identity (bot ID, guild membership, mentionPatterns, peer bots, allowBots). SURFACE channel = routing policy (dmPolicy, allowFrom, guild policy, users). Both can coexist; SURFACE runs after HANDLE so routing config takes precedence over HANDLE defaults.
- **Map-form only for channel**: Other surface schemes (volume, service) don't have routing config and only appear as strings. Map-form is only valid for `channel://`.
- **`allowFrom` is the key spike proof**: The operator's Discord user ID can't be derived from the bot's identity. Channel surface is the declared mechanism for the operator to say "these users can DM this bot." The spike verifies this round-trips from pod YAML → openclaw.json.
- **Unknown platforms silently skipped in config**: An unrecognised channel platform (not discord/slack/telegram) has no config keys to write; silently skipping is safer than erroring, since future platforms may not have been added yet.
- **`platformTokenVar` shared**: The helper lives in `surface_skill.go` (used by skill generation) and is called from `clawdapus_md.go` (same package). No duplication.
- **Surface parsing moves to pod layer**: `ClawBlock.Surfaces` changes from `[]string` to `[]driver.ResolvedSurface`. All surface parsing (both string and map form) happens in `pod.Parse()`. `compose_up.go` just uses the pre-parsed slices. `pod.ParseSurface` stays public (utility; tested standalone).
