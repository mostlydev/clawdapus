# Communication Tools: Handle-as-Capability Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Transform the HANDLE directive from an output pipe into a capability grant across OpenClaw, Nullclaw, and Picoclaw. Agents receive `post_message` and `reply_to` tool definitions; no channel receives agent text unless the agent explicitly calls a tool. Thinking is always private. Nanobot and microclaw are removed first.

**Architecture:** Nanobot and microclaw are deleted with all references cleaned up. A new `shared/communication_tools.go` generates canonical, platform-filtered tool schemas from `ResolvedClaw.Handles`. The function accepts a `supportedPlatforms` slice so each driver only advertises platforms it can actually route. Each in-scope driver config gains tool definitions plus a "tools-only" mode flag that suppresses auto-routing. `CLAWDAPUS.md` gains a `## Communication Tools` section stating policy and tool API. HANDLE syntax is unchanged; only what it generates changes.

**Drivers in scope:** OpenClaw, Nullclaw, Picoclaw.

**Hermes:** Excluded from this plan. Hermes tool-mode requires live investigation of the upstream runtime to identify the exact send-dispatch code to patch. Tracked as a follow-up plan.

**Tech Stack:** Go, existing `shared.SetPath` / `driver.ResolvedClaw` patterns, JSON/YAML config generation.

---

## Task 0: Remove nanobot and microclaw

These drivers are unused in production and would be semantically inconsistent after this change. Full blast radius:

**Directories to delete:**
- `internal/driver/nanobot/`
- `internal/driver/microclaw/`

**Files to modify:**
- `internal/build/build.go` — remove blank imports
- `internal/build/build_test.go` — remove acceptance tests for these types
- `cmd/claw/compose_health.go` — remove blank imports
- `cmd/claw-api/handler.go` — remove blank imports
- `cmd/claw/scaffold_helpers.go` — remove from type list and default image switch
- `cmd/claw/scaffold_helpers_test.go` — remove scaffold tests for these types
- `internal/pod/driver_newtypes_integration_test.go` — remove nanobot portion
- `cmd/claw/agent_test.go` — remove references
- `cmd/claw/compose_test.go` — remove references
- `cmd/claw/init_test.go` — remove references
- `cmd/claw/spike_test.go` — remove references
- `cmd/claw/spike_rollcall_test.go` — remove references
- `cmd/claw/spike_mixed_managed_test.go` — remove references
- `internal/driver/shared/config_test.go` — remove references
- `examples/trading-desk/claw-pod.yml` — remove `micro` service (microclaw)
- `examples/trading-desk/Clawfile.microclaw` — delete file
- `examples/trading-desk/agents/MICRO.md` — delete file if it exists

**After deletion:** any example that uses nanobot in rollcall or spike fixtures must be updated to remove or replace the nanobot service with a supported driver type.

### Step 1: Find every reference

```bash
grep -r "nanobot\|microclaw" --include="*.go" --include="*.yml" --include="*.yaml" \
  --include="*.md" -l . | sort
```

Record the full list. Every file returned must be cleaned up before the task is done.

### Step 2: Delete driver directories

```bash
rm -rf internal/driver/nanobot internal/driver/microclaw
```

### Step 3: Remove blank imports

In each of these files, remove the two blank import lines for nanobot and microclaw:

**`internal/build/build.go`:**
```go
// Remove these two lines:
_ "github.com/mostlydev/clawdapus/internal/driver/microclaw"
_ "github.com/mostlydev/clawdapus/internal/driver/nanobot"
```

**`cmd/claw/compose_health.go`** (around line 17):
```go
// Remove these two lines:
_ "github.com/mostlydev/clawdapus/internal/driver/microclaw"
_ "github.com/mostlydev/clawdapus/internal/driver/nanobot"
```

**`cmd/claw-api/handler.go`** (around line 28):
```go
// Remove these two lines:
_ "github.com/mostlydev/clawdapus/internal/driver/microclaw"
_ "github.com/mostlydev/clawdapus/internal/driver/nanobot"
```

### Step 4: Update scaffold_helpers.go

**`cmd/claw/scaffold_helpers.go`** — three changes:

1. Remove `"microclaw"` and `"nanobot"` from `scaffoldClawTypes`:
```go
// Before:
var scaffoldClawTypes = []string{"openclaw", "hermes", "nanoclaw", "microclaw", "nullclaw", "nanobot", "picoclaw", "generic"}

// After:
var scaffoldClawTypes = []string{"openclaw", "hermes", "nanoclaw", "nullclaw", "picoclaw", "generic"}
```

2. Remove the `microclaw` and `nanobot` cases from `parseClawType()` (around line 158).

3. Remove the `microclaw` and `nanobot` cases from `defaultBaseImageForClawType()` (around lines 173-178):
```go
// Remove these two cases:
case "microclaw":
    return "microclaw:latest"
case "nanobot":
    return "nanobot:latest"
```

### Step 5: Remove tests in build_test.go

**`internal/build/build_test.go`** — delete the entire bodies of:
- `TestGenerateAcceptsMicroclawType` (around line 75)
- `TestGenerateAcceptsNanobotType` (around line 103)

### Step 6: Remove nanobot from driver_newtypes_integration_test.go

**`internal/pod/driver_newtypes_integration_test.go`** — `TestNanobotAndPicoclawMaterializeAndCompose` tests both nanobot and picoclaw. Split it:
- Delete the nanobot-specific assertions (any block that references nanobot driver, `nanobot-home/config.json`, etc.)
- Rename the remaining test to `TestPicoclawMaterializeAndCompose`
- Keep the picoclaw assertions intact

### Step 7: Remove scaffold_helpers_test.go references

**`cmd/claw/scaffold_helpers_test.go`** — remove any test cases that scaffold a microclaw or nanobot Clawfile. If entire test functions only test those types, delete the functions.

### Step 8: Remove references from cmd/claw test files

For each of `agent_test.go`, `compose_test.go`, `init_test.go`, `spike_test.go`, `spike_rollcall_test.go`, `spike_mixed_managed_test.go`:
- Remove test cases that use a nanobot or microclaw claw type
- If a test function only uses those types, delete the whole function
- If a function tests multiple types, remove only the nanobot/microclaw entries from the type list

### Step 9: Remove from shared/config_test.go

**`internal/driver/shared/config_test.go`** — remove any test cases parameterized over microclaw or nanobot driver types.

### Step 10: Clean up examples/trading-desk

```bash
rm -f examples/trading-desk/Clawfile.microclaw
rm -f examples/trading-desk/agents/MICRO.md   # if it exists
```

In `examples/trading-desk/claw-pod.yml`, remove the `micro:` service block entirely (around lines 199-217). If `micro` appears in any `depends_on:` lists for other services, remove those references too.

### Step 11: Check and update rollcall and other examples

```bash
grep -r "nanobot\|microclaw" examples/ --include="*.yml" --include="*.yaml" -l
```

For each hit, remove or replace the nanobot/microclaw service with a supported driver type. The rollcall spike test may reference a nanobot service — update the fixture to use nullclaw or picoclaw.

### Step 12: Verify build and tests are green

```bash
go build ./...
go test ./...
go vet ./...
```

All three must pass with zero errors before proceeding. The integration and spike tests are excluded from `go test ./...` by build tags, but verify that `go test ./...` itself passes.

### Step 13: Commit

```bash
git add -A
git commit -m "chore(drivers): remove nanobot and microclaw drivers and all references"
```

---

## Task 1: Shared — CommunicationTools types and generator

**Files:**
- Create: `internal/driver/shared/communication_tools.go`
- Create: `internal/driver/shared/communication_tools_test.go`

**Design note on `supportedPlatforms` parameter:** The function takes a `[]string` of platform names the calling driver can actually route. This prevents the tool description from advertising platforms (e.g., a hypothetical HANDLE wecom on OpenClaw) that the driver skips with a warning and has no channel backend for. Picoclaw passes its full 13-platform set; OpenClaw and Nullclaw pass `["discord", "telegram", "slack"]`.

**Design note on `platform` in tool schema:** Channel IDs are platform-scoped. Discord channel `1234` and Telegram chat `1234` are unrelated. Both `post_message` and `reply_to` require `platform` to unambiguously route delivery. `reply_to` also requires `channel_id` because Discord's reply API requires both the channel and the message ID.

### Step 1: Write the failing tests

`internal/driver/shared/communication_tools_test.go`:

```go
package shared

import (
	"strings"
	"testing"

	"github.com/mostlydev/clawdapus/internal/driver"
)

func TestCommunicationTools_NoHandles(t *testing.T) {
	rc := &driver.ResolvedClaw{}
	if CommunicationTools(rc, []string{"discord"}) != nil {
		t.Fatal("expected nil for no handles")
	}
}

func TestCommunicationTools_NoSupportedHandles(t *testing.T) {
	// Handle present, but not in the supported list → nil (no channel backend).
	rc := &driver.ResolvedClaw{
		Handles: map[string]*driver.HandleInfo{
			"wecom": {ID: "x"},
		},
	}
	if CommunicationTools(rc, []string{"discord", "telegram", "slack"}) != nil {
		t.Fatal("expected nil when no supported handle is present")
	}
}

func TestCommunicationTools_FiltersBySupported(t *testing.T) {
	rc := &driver.ResolvedClaw{
		Handles: map[string]*driver.HandleInfo{
			"discord": {ID: "111"},
			"wecom":   {ID: "999"}, // unsupported by this driver
		},
	}
	tools := CommunicationTools(rc, []string{"discord", "slack"})
	if tools == nil {
		t.Fatal("expected tools when at least one supported handle is present")
	}
	desc := tools[0].InputSchema.Properties["platform"].Description
	if strings.Contains(desc, "wecom") {
		t.Errorf("unsupported platform wecom must not appear in tool description, got: %s", desc)
	}
	if !strings.Contains(desc, "discord") {
		t.Errorf("supported platform discord must appear in tool description, got: %s", desc)
	}
}

func TestCommunicationTools_PlatformRequired(t *testing.T) {
	rc := &driver.ResolvedClaw{
		Handles: map[string]*driver.HandleInfo{
			"discord":  {ID: "111"},
			"telegram": {ID: "222"},
		},
	}
	tools := CommunicationTools(rc, []string{"discord", "telegram", "slack"})
	if len(tools) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(tools))
	}

	for _, toolDef := range tools {
		if _, ok := toolDef.InputSchema.Properties["platform"]; !ok {
			t.Errorf("tool %q: expected platform property", toolDef.Name)
		}
		found := false
		for _, r := range toolDef.InputSchema.Required {
			if r == "platform" {
				found = true
			}
		}
		if !found {
			t.Errorf("tool %q: platform must be in Required", toolDef.Name)
		}
	}
}

func TestCommunicationTools_ReplyToRequiresChannelAndMessageID(t *testing.T) {
	rc := &driver.ResolvedClaw{
		Handles: map[string]*driver.HandleInfo{"discord": {ID: "123"}},
	}
	tools := CommunicationTools(rc, []string{"discord"})
	var reply *ToolDef
	for i := range tools {
		if tools[i].Name == "reply_to" {
			reply = &tools[i]
		}
	}
	if reply == nil {
		t.Fatal("expected reply_to tool")
	}
	for _, field := range []string{"platform", "channel_id", "message_id", "content"} {
		if _, ok := reply.InputSchema.Properties[field]; !ok {
			t.Errorf("reply_to: expected %q property", field)
		}
		found := false
		for _, r := range reply.InputSchema.Required {
			if r == field {
				found = true
			}
		}
		if !found {
			t.Errorf("reply_to: %q must be required", field)
		}
	}
}

func TestCommunicationTools_ChannelListInDesc(t *testing.T) {
	rc := &driver.ResolvedClaw{
		Handles: map[string]*driver.HandleInfo{
			"discord": {
				Guilds: []driver.GuildInfo{{
					ID:       "gid1",
					Channels: []driver.ChannelInfo{{ID: "ch1", Name: "trading-floor"}},
				}},
			},
		},
	}
	tools := CommunicationTools(rc, []string{"discord"})
	desc := tools[0].InputSchema.Properties["channel_id"].Description
	if !strings.Contains(desc, "ch1") {
		t.Errorf("expected channel ID in desc, got: %s", desc)
	}
	if !strings.Contains(desc, "#trading-floor") {
		t.Errorf("expected #trading-floor in desc, got: %s", desc)
	}
}

func TestCommunicationTools_ChannelWithoutName(t *testing.T) {
	rc := &driver.ResolvedClaw{
		Handles: map[string]*driver.HandleInfo{
			"discord": {
				Guilds: []driver.GuildInfo{{
					Channels: []driver.ChannelInfo{{ID: "ch99"}},
				}},
			},
		},
	}
	tools := CommunicationTools(rc, []string{"discord"})
	desc := tools[0].InputSchema.Properties["channel_id"].Description
	if !strings.Contains(desc, "ch99") {
		t.Errorf("expected channel ID in desc, got: %s", desc)
	}
	if strings.Contains(desc, "#") {
		t.Errorf("expected no # prefix when channel name empty, got: %s", desc)
	}
}

func TestCommunicationToolsAsConfig_RoundTrip(t *testing.T) {
	rc := &driver.ResolvedClaw{
		Handles: map[string]*driver.HandleInfo{
			"discord": {
				Guilds: []driver.GuildInfo{{
					Channels: []driver.ChannelInfo{{ID: "c1", Name: "general"}},
				}},
			},
		},
	}
	out, err := CommunicationToolsAsConfig(rc, []string{"discord"})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(out))
	}
	first, ok := out[0].(map[string]interface{})
	if !ok {
		t.Fatalf("expected map, got %T", out[0])
	}
	if first["name"] != "post_message" {
		t.Errorf("expected post_message, got %v", first["name"])
	}
	schema := first["input_schema"].(map[string]interface{})
	if schema["type"] != "object" {
		t.Errorf("expected schema type=object, got %v", schema["type"])
	}
}

func TestCommunicationToolsAsConfig_NilWhenNoSupportedHandle(t *testing.T) {
	rc := &driver.ResolvedClaw{}
	out, err := CommunicationToolsAsConfig(rc, []string{"discord"})
	if err != nil {
		t.Fatal(err)
	}
	if out != nil {
		t.Fatalf("expected nil, got %v", out)
	}
}
```

### Step 2: Run tests to verify they fail

```bash
go test ./internal/driver/shared/... -run TestCommunicationTools -v
```

Expected: FAIL — `CommunicationTools undefined`

### Step 3: Write the implementation

`internal/driver/shared/communication_tools.go`:

```go
package shared

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/mostlydev/clawdapus/internal/driver"
)

// ToolPropertyDef describes a single parameter in a tool's input schema.
type ToolPropertyDef struct {
	Type        string `json:"type"`
	Description string `json:"description"`
}

// ToolInputSchemaDef is the JSON Schema for tool input parameters.
type ToolInputSchemaDef struct {
	Type       string                     `json:"type"`
	Properties map[string]ToolPropertyDef `json:"properties"`
	Required   []string                   `json:"required"`
}

// ToolDef is a canonical tool definition consumable by any driver.
type ToolDef struct {
	Name        string             `json:"name"`
	Description string             `json:"description"`
	InputSchema ToolInputSchemaDef `json:"input_schema"`
}

// CommunicationTools returns the canonical set of communication tool definitions
// for the given ResolvedClaw, filtered to supportedPlatforms.
//
// supportedPlatforms is the list of platform names this driver can route to
// (e.g. []string{"discord", "telegram", "slack"}). Handles on platforms not
// in this list are excluded from tool descriptions so the agent is never told
// to post to a platform the driver cannot deliver to.
//
// Returns nil if no supported handles are configured.
func CommunicationTools(rc *driver.ResolvedClaw, supportedPlatforms []string) []ToolDef {
	supported := make(map[string]bool, len(supportedPlatforms))
	for _, p := range supportedPlatforms {
		supported[strings.ToLower(p)] = true
	}

	// Collect the intersection of configured handles and supported platforms.
	var platforms []string
	for p := range rc.Handles {
		if supported[strings.ToLower(p)] {
			platforms = append(platforms, p)
		}
	}
	if len(platforms) == 0 {
		return nil
	}
	sort.Strings(platforms)

	platformDesc := "Platform to post on. Configured: " + strings.Join(platforms, ", ") + "."

	// Build channel list for the channel_id description (across all supported handles).
	type channelEntry struct{ id, name string }
	var entries []channelEntry
	for _, p := range platforms {
		h := rc.Handles[p]
		if h == nil {
			continue
		}
		for _, guild := range h.Guilds {
			for _, ch := range guild.Channels {
				entries = append(entries, channelEntry{id: ch.ID, name: ch.Name})
			}
		}
	}
	channelDesc := "Channel ID on the target platform."
	if len(entries) > 0 {
		var parts []string
		for _, e := range entries {
			if e.name != "" {
				parts = append(parts, fmt.Sprintf("%s (#%s)", e.id, e.name))
			} else {
				parts = append(parts, e.id)
			}
		}
		channelDesc += " Available: " + strings.Join(parts, ", ") + "."
	}

	return []ToolDef{
		{
			Name: "post_message",
			Description: "Post a message to a channel. Use this when you have something meaningful " +
				"to communicate. Do not call this to narrate your reasoning or to confirm you have nothing " +
				"to say. Silence is the correct response when you have nothing actionable to add.",
			InputSchema: ToolInputSchemaDef{
				Type: "object",
				Properties: map[string]ToolPropertyDef{
					"platform":   {Type: "string", Description: platformDesc},
					"channel_id": {Type: "string", Description: channelDesc},
					"content":    {Type: "string", Description: "The message content. Plain text or markdown."},
				},
				Required: []string{"platform", "channel_id", "content"},
			},
		},
		{
			Name: "reply_to",
			Description: "Reply to a specific message. Prefer this over post_message when responding " +
				"to a message directed at you — it preserves threading context in Discord and Telegram.",
			InputSchema: ToolInputSchemaDef{
				Type: "object",
				Properties: map[string]ToolPropertyDef{
					"platform":   {Type: "string", Description: platformDesc},
					"channel_id": {Type: "string", Description: channelDesc},
					"message_id": {Type: "string", Description: "The ID of the message to reply to."},
					"content":    {Type: "string", Description: "Your reply content."},
				},
				Required: []string{"platform", "channel_id", "message_id", "content"},
			},
		},
	}
}

// CommunicationToolsAsConfig returns communication tools as []interface{} ready
// for use with SetPath in any driver config.
// Returns nil, nil if no supported handles are configured.
func CommunicationToolsAsConfig(rc *driver.ResolvedClaw, supportedPlatforms []string) ([]interface{}, error) {
	tools := CommunicationTools(rc, supportedPlatforms)
	if tools == nil {
		return nil, nil
	}
	b, err := json.Marshal(tools)
	if err != nil {
		return nil, fmt.Errorf("communication tools marshal: %w", err)
	}
	var out []interface{}
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, fmt.Errorf("communication tools unmarshal: %w", err)
	}
	return out, nil
}
```

### Step 4: Run tests to verify they pass

```bash
go test ./internal/driver/shared/... -run TestCommunicationTools -v
```

Expected: all PASS

### Step 5: Run full suite

```bash
go test ./...
```

Expected: all PASS

### Step 6: Commit

```bash
git add internal/driver/shared/communication_tools.go internal/driver/shared/communication_tools_test.go
git commit -m "feat(driver/shared): add platform-filtered CommunicationTools tool schema generator"
```

---

## Task 2: Shared — CLAWDAPUS.md Communication Tools section

**Files:**
- Modify: `internal/driver/shared/clawdapus_md.go`
- Modify: `internal/driver/shared/clawdapus_md_test.go`

`GenerateClawdapusMD` does not know each driver's supported platform set, so it calls `CommunicationTools` with all configured handles (no filtering). The filtering happens at the driver config layer. The CLAWDAPUS.md is informational context for the agent; the runner enforces routing.

### Step 1: Write the failing tests

Add to `internal/driver/shared/clawdapus_md_test.go`:

```go
func TestGenerateClawdapusMD_CommunicationTools(t *testing.T) {
	rc := &driver.ResolvedClaw{
		ServiceName: "dundas",
		ClawType:    "nullclaw",
		Handles: map[string]*driver.HandleInfo{
			"discord": {
				ID:       "111",
				Username: "dundas",
				Guilds: []driver.GuildInfo{{
					ID:       "gid1",
					Channels: []driver.ChannelInfo{{ID: "ch1", Name: "trading-floor"}},
				}},
			},
		},
	}
	md := GenerateClawdapusMD(rc, "trading-pod")

	if !strings.Contains(md, "## Communication Tools") {
		t.Error("expected Communication Tools section")
	}
	if !strings.Contains(md, "post_message") {
		t.Error("expected post_message tool")
	}
	if !strings.Contains(md, "reply_to") {
		t.Error("expected reply_to tool")
	}
	if !strings.Contains(md, "platform") {
		t.Error("expected platform parameter documented")
	}
	if !strings.Contains(md, "ch1") {
		t.Error("expected channel ID in description")
	}
	if !strings.Contains(md, "Think privately") {
		t.Error("expected private-thinking policy statement")
	}
}

func TestGenerateClawdapusMD_NoCommunicationToolsWithoutHandles(t *testing.T) {
	rc := &driver.ResolvedClaw{
		ServiceName: "headless",
		ClawType:    "openclaw",
	}
	md := GenerateClawdapusMD(rc, "my-pod")
	if strings.Contains(md, "## Communication Tools") {
		t.Error("expected no Communication Tools section when no handles configured")
	}
}
```

### Step 2: Run tests to verify they fail

```bash
go test ./internal/driver/shared/... -run TestGenerateClawdapusMD_Communication -v
```

Expected: FAIL

### Step 3: Add the Communication Tools section to clawdapus_md.go

In `GenerateClawdapusMD`, insert after the Handles block (after line ~150, before Peer Handles):

```go
	// Communication Tools section — only when handles are configured.
	// All configured platforms are listed here. The runner filters to only those
	// it can route; CLAWDAPUS.md is informational context for the agent.
	if allPlatforms := func() []string {
		ps := make([]string, 0, len(rc.Handles))
		for p := range rc.Handles {
			ps = append(ps, p)
		}
		return ps
	}(); len(allPlatforms) > 0 {
		if tools := CommunicationTools(rc, allPlatforms); tools != nil {
			b.WriteString("## Communication Tools\n\n")
			b.WriteString("Your channels are communication tools, not output pipes. ")
			b.WriteString("Think privately. Speak deliberately.\n\n")
			b.WriteString("**Policy:** Your reasoning is always private and never reaches any channel. ")
			b.WriteString("To communicate, call one of the tools below explicitly. ")
			b.WriteString("If you have nothing meaningful to say, return without calling any tool — silence is correct.\n\n")

			for _, t := range tools {
				b.WriteString(fmt.Sprintf("### `%s`\n\n", t.Name))
				b.WriteString(t.Description + "\n\n")
				b.WriteString("**Parameters:**\n")

				propNames := make([]string, 0, len(t.InputSchema.Properties))
				for k := range t.InputSchema.Properties {
					propNames = append(propNames, k)
				}
				sort.Strings(propNames)

				for _, propName := range propNames {
					prop := t.InputSchema.Properties[propName]
					req := ""
					for _, r := range t.InputSchema.Required {
						if r == propName {
							req = " *(required)*"
							break
						}
					}
					b.WriteString(fmt.Sprintf("- `%s` (`%s`)%s: %s\n",
						propName, prop.Type, req, prop.Description))
				}
				b.WriteString("\n")
			}
		}
	}
```

### Step 4: Run tests to verify they pass

```bash
go test ./internal/driver/shared/... -run TestGenerateClawdapusMD -v
```

Expected: all PASS

### Step 5: Run full suite

```bash
go test ./...
```

### Step 6: Commit

```bash
git add internal/driver/shared/clawdapus_md.go internal/driver/shared/clawdapus_md_test.go
git commit -m "feat(clawdapus-md): add Communication Tools section with private-thinking policy"
```

---

## Task 3: OpenClaw — tool definitions and responseDelivery

**Files:**
- Modify: `internal/driver/openclaw/config.go`
- Modify: `internal/driver/openclaw/config_test.go`

**Supported platforms for this driver:** `discord`, `telegram`, `slack` (mirrors the existing platform switch in `GenerateConfig`).

**Runner contract:**
- `agents.defaults.tools`: list of tool schemas (Claude tool_use format with `input_schema`)
- `agents.defaults.responseDelivery: "tool"`: suppresses auto-routing of text output. Requires openclaw gateway >= v0.4.0.

### Step 1: Write the failing tests

Add to `internal/driver/openclaw/config_test.go`:

```go
func TestGenerateConfigCommunicationTools(t *testing.T) {
	rc := &driver.ResolvedClaw{
		Handles: map[string]*driver.HandleInfo{
			"discord": {
				ID:       "botid1",
				Username: "analyst",
				Guilds: []driver.GuildInfo{{
					ID:       "guild1",
					Channels: []driver.ChannelInfo{{ID: "ch1", Name: "trading-floor"}},
				}},
			},
		},
	}
	data, err := GenerateConfig(rc)
	if err != nil {
		t.Fatal(err)
	}

	var cfg map[string]interface{}
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}

	toolsRaw := nestedGet(cfg, "agents", "defaults", "tools")
	tools, ok := toolsRaw.([]interface{})
	if !ok || len(tools) != 2 {
		t.Fatalf("expected agents.defaults.tools=[2], got %#v", toolsRaw)
	}
	first := tools[0].(map[string]interface{})
	if first["name"] != "post_message" {
		t.Errorf("expected post_message, got %v", first["name"])
	}
	schema := first["input_schema"].(map[string]interface{})
	props := schema["properties"].(map[string]interface{})
	if _, ok := props["platform"]; !ok {
		t.Error("expected platform property in post_message")
	}

	delivery := nestedGet(cfg, "agents", "defaults", "responseDelivery")
	if delivery != "tool" {
		t.Errorf("expected responseDelivery=tool, got %v", delivery)
	}
}

func TestGenerateConfigNoToolsWithoutSupportedHandle(t *testing.T) {
	rc := &driver.ResolvedClaw{} // no handles
	data, err := GenerateConfig(rc)
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]interface{}
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	if v := nestedGet(cfg, "agents", "defaults", "tools"); v != nil {
		t.Errorf("expected no tools without handles, got %v", v)
	}
	if v := nestedGet(cfg, "agents", "defaults", "responseDelivery"); v != nil {
		t.Errorf("expected no responseDelivery without handles, got %v", v)
	}
}

// nestedGet walks a map[string]interface{} by key segments.
func nestedGet(m map[string]interface{}, keys ...string) interface{} {
	var cur interface{} = m
	for _, k := range keys {
		cm, ok := cur.(map[string]interface{})
		if !ok {
			return nil
		}
		cur = cm[k]
	}
	return cur
}
```

### Step 2: Run tests to verify they fail

```bash
go test ./internal/driver/openclaw/... -run TestGenerateConfigCommunicationTools -v
go test ./internal/driver/openclaw/... -run TestGenerateConfigNoToolsWithoutSupportedHandle -v
```

Expected: FAIL

### Step 3: Add tool generation to config.go

In `internal/driver/openclaw/config.go`, define the driver's supported platforms as a package-level var (place near the top, after imports):

```go
// openclawSupportedPlatforms are the HANDLE platform names this driver can route to.
var openclawSupportedPlatforms = []string{"discord", "telegram", "slack"}
```

Insert after the `agents.list` write block (after line ~235, before the `// Apply CONFIGURE directives` comment):

```go
	// Communication tools: emit post_message/reply_to when at least one supported HANDLE
	// platform is present. agents.defaults.responseDelivery="tool" tells the gateway to
	// suppress text auto-routing; delivery flows only through tool call results.
	// Requires openclaw gateway >= v0.4.0.
	tools, err := shared.CommunicationToolsAsConfig(rc, openclawSupportedPlatforms)
	if err != nil {
		return nil, fmt.Errorf("config generation: communication tools: %w", err)
	}
	if tools != nil {
		if err := setPath(config, "agents.defaults.tools", tools); err != nil {
			return nil, fmt.Errorf("config generation: agents.defaults.tools: %w", err)
		}
		if err := setPath(config, "agents.defaults.responseDelivery", "tool"); err != nil {
			return nil, fmt.Errorf("config generation: agents.defaults.responseDelivery: %w", err)
		}
	}
```

### Step 4: Run tests to verify they pass

```bash
go test ./internal/driver/openclaw/... -v
```

Expected: all PASS

### Step 5: Run full suite

```bash
go test ./...
```

### Step 6: Commit

```bash
git add internal/driver/openclaw/config.go internal/driver/openclaw/config_test.go
git commit -m "feat(driver/openclaw): add communication tools and responseDelivery:tool config"
```

---

## Task 4: Nullclaw — tool definitions and gateway.response_mode

**Files:**
- Modify: `internal/driver/nullclaw/config.go`
- Modify: `internal/driver/nullclaw/config_test.go`

**Supported platforms for this driver:** `discord`, `telegram`, `slack` (mirrors the existing validation switch in `driver.go`).

**Runner contract:**
- `agents.defaults.tools`: list of tool schemas
- `gateway.response_mode: "tool"`: suppresses text auto-routing. Requires nullclaw >= v0.3.0.

### Step 1: Write the failing tests

Add to `internal/driver/nullclaw/config_test.go`:

```go
func TestGenerateConfigCommunicationTools(t *testing.T) {
	rc := &driver.ResolvedClaw{
		Handles: map[string]*driver.HandleInfo{
			"discord": {
				ID:       "bot1",
				Username: "logan",
				Guilds: []driver.GuildInfo{{
					ID:       "gid1",
					Channels: []driver.ChannelInfo{{ID: "ch1", Name: "desk"}},
				}},
			},
		},
		Environment: map[string]string{
			"DISCORD_BOT_TOKEN": "tok",
		},
	}
	data, err := GenerateConfig(rc)
	if err != nil {
		t.Fatal(err)
	}

	toolsRaw, ok := getPath(data, "agents.defaults.tools")
	if !ok {
		t.Fatal("expected agents.defaults.tools")
	}
	tools, ok := toolsRaw.([]interface{})
	if !ok || len(tools) != 2 {
		t.Fatalf("expected 2 tools, got %#v", toolsRaw)
	}
	first := tools[0].(map[string]interface{})
	if first["name"] != "post_message" {
		t.Errorf("expected post_message, got %v", first["name"])
	}
	schema := first["input_schema"].(map[string]interface{})
	props := schema["properties"].(map[string]interface{})
	if _, ok := props["platform"]; !ok {
		t.Error("expected platform property in tool schema")
	}

	mode, ok := getPath(data, "gateway.response_mode")
	if !ok || mode != "tool" {
		t.Errorf("expected gateway.response_mode=tool, got %v", mode)
	}
}

func TestGenerateConfigNoToolsWithoutSupportedHandle(t *testing.T) {
	rc := &driver.ResolvedClaw{}
	data, err := GenerateConfig(rc)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := getPath(data, "agents.defaults.tools"); ok {
		t.Error("expected no tools without handles")
	}
	if v, ok := getPath(data, "gateway.response_mode"); ok {
		t.Errorf("expected no response_mode without handles, got %v", v)
	}
}
```

### Step 2: Run tests to verify they fail

```bash
go test ./internal/driver/nullclaw/... -run TestGenerateConfigCommunicationTools -v
go test ./internal/driver/nullclaw/... -run TestGenerateConfigNoToolsWithoutSupportedHandle -v
```

Expected: FAIL

### Step 3: Add tools to config.go

Add to `internal/driver/nullclaw/config.go`:

```go
// nullclawSupportedPlatforms are the HANDLE platform names this driver can route to.
var nullclawSupportedPlatforms = []string{"discord", "telegram", "slack"}
```

Insert after the HANDLE loop and before the CONFIGURE loop:

```go
	// Communication tools: emit post_message/reply_to when at least one supported HANDLE
	// platform is present. gateway.response_mode="tool" suppresses text auto-routing.
	// Requires nullclaw >= v0.3.0.
	tools, err := shared.CommunicationToolsAsConfig(rc, nullclawSupportedPlatforms)
	if err != nil {
		return nil, fmt.Errorf("config generation: communication tools: %w", err)
	}
	if tools != nil {
		if err := shared.SetPath(config, "agents.defaults.tools", tools); err != nil {
			return nil, fmt.Errorf("config generation: agents.defaults.tools: %w", err)
		}
		if err := shared.SetPath(config, "gateway.response_mode", "tool"); err != nil {
			return nil, fmt.Errorf("config generation: gateway.response_mode: %w", err)
		}
	}
```

### Step 4: Run tests to verify they pass

```bash
go test ./internal/driver/nullclaw/... -v
```

Expected: all PASS

### Step 5: Run full suite

```bash
go test ./...
```

### Step 6: Commit

```bash
git add internal/driver/nullclaw/config.go internal/driver/nullclaw/config_test.go
git commit -m "feat(driver/nullclaw): add communication tools and gateway.response_mode:tool config"
```

---

## Task 5: Picoclaw — tool definitions and output.mode

**Files:**
- Modify: `internal/driver/picoclaw/config.go`
- Modify: `internal/driver/picoclaw/config_test.go`

**Supported platforms for this driver:** Use the existing `isSupportedPlatform()` function or the `PlatformTokenVar` map to derive the list. Picoclaw supports 13 platforms — pass them all.

**Runner contract:**
- `agents.defaults.tools`: list of tool schemas
- `output.mode: "tool"`: suppresses text auto-routing. Requires picoclaw >= v0.5.0.

### Step 1: Write the failing tests

Add to `internal/driver/picoclaw/config_test.go`:

```go
func TestGenerateConfigCommunicationTools(t *testing.T) {
	rc := &driver.ResolvedClaw{
		Models: map[string]string{"primary": "anthropic/claude-sonnet-4"},
		Handles: map[string]*driver.HandleInfo{
			"discord": {
				ID:       "bot1",
				Username: "analyst",
				Guilds: []driver.GuildInfo{{
					ID:       "gid1",
					Channels: []driver.ChannelInfo{{ID: "ch1", Name: "floor"}},
				}},
			},
		},
		Environment: map[string]string{
			"DISCORD_BOT_TOKEN": "tok",
			"ANTHROPIC_API_KEY": "key",
		},
	}
	data, err := GenerateConfig(rc)
	if err != nil {
		t.Fatal(err)
	}

	var cfg map[string]interface{}
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}

	toolsRaw := picoclawGet(cfg, "agents", "defaults", "tools")
	tools, ok := toolsRaw.([]interface{})
	if !ok || len(tools) != 2 {
		t.Fatalf("expected 2 tools under agents.defaults.tools, got %#v", toolsRaw)
	}
	first := tools[0].(map[string]interface{})
	if first["name"] != "post_message" {
		t.Errorf("expected post_message, got %v", first["name"])
	}
	schema := first["input_schema"].(map[string]interface{})
	props := schema["properties"].(map[string]interface{})
	if _, ok := props["platform"]; !ok {
		t.Error("expected platform property in tool schema")
	}

	mode := picoclawGet(cfg, "output", "mode")
	if mode != "tool" {
		t.Errorf("expected output.mode=tool, got %v", mode)
	}
}

func TestGenerateConfigNoToolsWithoutSupportedHandle(t *testing.T) {
	rc := &driver.ResolvedClaw{
		Models: map[string]string{"primary": "anthropic/claude-sonnet-4"},
	}
	data, err := GenerateConfig(rc)
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]interface{}
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	if v := picoclawGet(cfg, "agents", "defaults", "tools"); v != nil {
		t.Errorf("expected no tools without handles, got %v", v)
	}
	if v := picoclawGet(cfg, "output", "mode"); v != nil {
		t.Errorf("expected no output.mode without handles, got %v", v)
	}
}

func picoclawGet(m map[string]interface{}, keys ...string) interface{} {
	var cur interface{} = m
	for _, k := range keys {
		cm, ok := cur.(map[string]interface{})
		if !ok {
			return nil
		}
		cur = cm[k]
	}
	return cur
}
```

### Step 2: Run tests to verify they fail

```bash
go test ./internal/driver/picoclaw/... -run TestGenerateConfigCommunicationTools -v
go test ./internal/driver/picoclaw/... -run TestGenerateConfigNoToolsWithoutSupportedHandle -v
```

Expected: FAIL

### Step 3: Add tools to config.go

In `internal/driver/picoclaw/config.go`, derive the supported platform list from the existing platform support function. Add:

```go
// picoclawSupportedPlatforms are the HANDLE platform names this driver can route to.
// Derived from the existing platform support set (13 platforms).
var picoclawSupportedPlatforms = func() []string {
	// Use the same source of truth as the channel config loop.
	// If isSupportedPlatform() exists, enumerate the known set; otherwise list explicitly.
	return []string{
		"discord", "telegram", "slack", "whatsapp", "feishu", "line",
		"qq", "dingtalk", "onebot", "wecom", "wecom_app", "pico", "maixcam",
	}
}()
```

Insert after the channel loop and before the CONFIGURE loop:

```go
	// Communication tools: emit post_message/reply_to when at least one supported HANDLE
	// platform is present. output.mode="tool" suppresses text auto-routing.
	// Requires picoclaw >= v0.5.0.
	tools, err := shared.CommunicationToolsAsConfig(rc, picoclawSupportedPlatforms)
	if err != nil {
		return nil, fmt.Errorf("config generation: communication tools: %w", err)
	}
	if tools != nil {
		if err := shared.SetPath(config, "agents.defaults.tools", tools); err != nil {
			return nil, fmt.Errorf("config generation: agents.defaults.tools: %w", err)
		}
		if err := shared.SetPath(config, "output.mode", "tool"); err != nil {
			return nil, fmt.Errorf("config generation: output.mode: %w", err)
		}
	}
```

### Step 4: Run tests to verify they pass

```bash
go test ./internal/driver/picoclaw/... -v
```

Expected: all PASS

### Step 5: Run final full suite

```bash
go test ./...
go vet ./...
```

Both must pass clean.

### Step 6: Commit

```bash
git add internal/driver/picoclaw/config.go internal/driver/picoclaw/config_test.go
git commit -m "feat(driver/picoclaw): add communication tools and output.mode:tool config"
```

---

## Final verification

### Confirm all commits landed clean

```bash
git log --oneline -6
```

Expected: six commits (one per task).

### Verify generated output for a known example

```bash
# Use quickstart or rollcall — trading-desk's service names depend on which services
# remain after the micro removal. Adjust the service name to one that actually exists.
claw up examples/quickstart/claw-pod.yml -d
```

Then inspect:

```bash
# Find the openclaw service name in the generated file
grep "x-claw\|image:" compose.generated.yml | head -20

# Check openclaw config for tools + responseDelivery
cat .claw-runtime/<openclaw-service>/config/openclaw.json \
  | python3 -m json.tool \
  | grep -A 20 '"tools"'

# Check nullclaw config (path is <service>-nullclaw-home/config.json)
# The runtime dir name matches the service name from compose.generated.yml
ls .claw-runtime/

# Verify CLAWDAPUS.md has Communication Tools section
# Context path: .claw-runtime/context/<agent-id>/CLAWDAPUS.md
find .claw-runtime/context -name "CLAWDAPUS.md" -exec grep -l "Communication Tools" {} \;
```

---

## Runner version requirements

| Driver | Config flag | Required runner version |
|--------|-------------|------------------------|
| OpenClaw | `agents.defaults.responseDelivery: "tool"` | openclaw gateway >= v0.4.0 |
| Nullclaw | `gateway.response_mode: "tool"` | nullclaw >= v0.3.0 |
| Picoclaw | `output.mode: "tool"` | picoclaw >= v0.5.0 |

**Backward safety:** Runners that do not yet support the new flags silently ignore them, falling back to auto-routing behavior identical to today.

## Hermes follow-up

Hermes tool mode requires live investigation of the upstream NousResearch/hermes-agent runtime to identify the exact agent-response-dispatch code in `gateway/platforms/discord.py`. This is tracked separately and not included here. Until Hermes is updated, Hermes agents continue to auto-route text output.
