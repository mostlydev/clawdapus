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
	// SURFACE channel should override HANDLE's default dmPolicy. Legacy
	// "denylist" is normalized to openclaw's current "open" mode.
	if discord["dmPolicy"] != "open" {
		t.Errorf("expected dmPolicy=open from channel surface, got %v", discord["dmPolicy"])
	}
	allowFrom, ok := discord["allowFrom"].([]interface{})
	if !ok {
		t.Fatalf("expected allowFrom to be an array, got %T", discord["allowFrom"])
	}
	if len(allowFrom) != 1 || allowFrom[0] != "*" {
		t.Errorf(`expected allowFrom=["*"] for dmPolicy=open, got %v`, allowFrom)
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
				ID:     "AAA",
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
