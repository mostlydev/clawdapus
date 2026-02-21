package openclaw

import (
	"encoding/json"
	"testing"

	"github.com/mostlydev/clawdapus/internal/driver"
)

func TestGenerateConfigSetsModelPrimary(t *testing.T) {
	rc := &driver.ResolvedClaw{
		Models: map[string]string{
			"primary": "openrouter/anthropic/claude-sonnet-4",
		},
		Configures: make([]string, 0),
	}
	data, err := GenerateConfig(rc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var config map[string]interface{}
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	agents := config["agents"].(map[string]interface{})
	defaults := agents["defaults"].(map[string]interface{})
	model := defaults["model"].(map[string]interface{})
	if model["primary"] != "openrouter/anthropic/claude-sonnet-4" {
		t.Errorf("expected model primary, got %v", model["primary"])
	}
}

func TestGenerateConfigAppliesConfigureDirectives(t *testing.T) {
	rc := &driver.ResolvedClaw{
		Models: make(map[string]string),
		Configures: []string{
			"openclaw config set agents.defaults.heartbeat.every 30m",
			"openclaw config set agents.defaults.heartbeat.target none",
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

	agents := config["agents"].(map[string]interface{})
	defaults := agents["defaults"].(map[string]interface{})
	heartbeat := defaults["heartbeat"].(map[string]interface{})
	if heartbeat["every"] != "30m" {
		t.Errorf("expected heartbeat.every=30m, got %v", heartbeat["every"])
	}
	if heartbeat["target"] != "none" {
		t.Errorf("expected heartbeat.target=none, got %v", heartbeat["target"])
	}
}

func TestGenerateConfigIsDeterministic(t *testing.T) {
	rc := &driver.ResolvedClaw{
		Models: map[string]string{
			"primary":  "anthropic/claude-sonnet-4",
			"fallback": "openai/gpt-4o",
		},
		Configures: []string{
			"openclaw config set agents.defaults.heartbeat.every 30m",
		},
	}
	first, _ := GenerateConfig(rc)
	second, _ := GenerateConfig(rc)
	if string(first) != string(second) {
		t.Error("config generation is not deterministic")
	}
}

func TestGenerateConfigAlwaysAddsBootstrapHook(t *testing.T) {
	rc := &driver.ResolvedClaw{
		Models:     map[string]string{"primary": "test/model"},
		Configures: []string{},
	}

	data, err := GenerateConfig(rc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var config map[string]interface{}
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	hooks, ok := config["hooks"].(map[string]interface{})
	if !ok {
		t.Fatal("expected hooks key in config")
	}
	bef, ok := hooks["bootstrap-extra-files"].(map[string]interface{})
	if !ok {
		t.Fatal("expected hooks.bootstrap-extra-files in config")
	}
	if bef["enabled"] != true {
		t.Error("expected enabled=true")
	}
	paths, ok := bef["paths"].([]interface{})
	if !ok || len(paths) == 0 {
		t.Fatal("expected paths array with CLAWDAPUS.md")
	}
	if paths[0] != "CLAWDAPUS.md" {
		t.Errorf("expected paths[0]=CLAWDAPUS.md, got %v", paths[0])
	}
}

func TestGenerateConfigRejectsUnknownCommand(t *testing.T) {
	rc := &driver.ResolvedClaw{
		Models:     make(map[string]string),
		Configures: []string{
			"some random command",
		},
	}
	_, err := GenerateConfig(rc)
	if err == nil {
		t.Fatal("expected error for unrecognized CONFIGURE command")
	}
}

func TestGenerateConfigHandleEnablesDiscord(t *testing.T) {
	rc := &driver.ResolvedClaw{
		Models:     make(map[string]string),
		Configures: []string{},
		Handles: map[string]*driver.HandleInfo{
			"discord": {ID: "123456789"},
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

	channels, ok := config["channels"].(map[string]interface{})
	if !ok {
		t.Fatal("expected channels key in config")
	}
	discord, ok := channels["discord"].(map[string]interface{})
	if !ok {
		t.Fatal("expected channels.discord in config")
	}
	if discord["enabled"] != true {
		t.Errorf("expected channels.discord.enabled=true, got %v", discord["enabled"])
	}
}

func TestGenerateConfigHandleEnablesSlack(t *testing.T) {
	rc := &driver.ResolvedClaw{
		Models:     make(map[string]string),
		Configures: []string{},
		Handles: map[string]*driver.HandleInfo{
			"slack": {ID: "U123456"},
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

	channels := config["channels"].(map[string]interface{})
	slack := channels["slack"].(map[string]interface{})
	if slack["enabled"] != true {
		t.Errorf("expected channels.slack.enabled=true, got %v", slack["enabled"])
	}
}

func TestGenerateConfigHandleEnablesTelegram(t *testing.T) {
	rc := &driver.ResolvedClaw{
		Models:     make(map[string]string),
		Configures: []string{},
		Handles: map[string]*driver.HandleInfo{
			"telegram": {ID: "987654321"},
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

	channels := config["channels"].(map[string]interface{})
	telegram := channels["telegram"].(map[string]interface{})
	if telegram["enabled"] != true {
		t.Errorf("expected channels.telegram.enabled=true, got %v", telegram["enabled"])
	}
}

func TestGenerateConfigHandleMultiplePlatforms(t *testing.T) {
	rc := &driver.ResolvedClaw{
		Models:     make(map[string]string),
		Configures: []string{},
		Handles: map[string]*driver.HandleInfo{
			"discord": {ID: "111"},
			"slack":   {ID: "U222"},
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

	channels := config["channels"].(map[string]interface{})
	if channels["discord"].(map[string]interface{})["enabled"] != true {
		t.Error("expected channels.discord.enabled=true")
	}
	if channels["slack"].(map[string]interface{})["enabled"] != true {
		t.Error("expected channels.slack.enabled=true")
	}
}

func TestGenerateConfigHandleUnknownPlatformNoError(t *testing.T) {
	rc := &driver.ResolvedClaw{
		Models:     make(map[string]string),
		Configures: []string{},
		Handles: map[string]*driver.HandleInfo{
			"mastodon": {ID: "@bot@example.social"},
		},
	}
	data, err := GenerateConfig(rc)
	if err != nil {
		t.Fatalf("unexpected error for unknown platform: %v", err)
	}

	var config map[string]interface{}
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	// Unknown platform should not create a channels entry
	if channels, ok := config["channels"]; ok {
		if channelMap, ok := channels.(map[string]interface{}); ok {
			if _, hasMastodon := channelMap["mastodon"]; hasMastodon {
				t.Error("expected no channels.mastodon entry for unknown platform")
			}
		}
	}
}

func TestGenerateConfigDiscordFullConfig(t *testing.T) {
	rc := &driver.ResolvedClaw{
		Models:     make(map[string]string),
		Configures: []string{},
		Handles: map[string]*driver.HandleInfo{
			"discord": {
				ID:       "123456789",
				Username: "tiverton",
				Guilds: []driver.GuildInfo{
					{
						ID:   "999888777",
						Name: "Trading Floor",
						Channels: []driver.ChannelInfo{
							{ID: "111222333", Name: "trading-floor"},
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

	channels := config["channels"].(map[string]interface{})
	discord := channels["discord"].(map[string]interface{})

	if discord["enabled"] != true {
		t.Errorf("expected channels.discord.enabled=true, got %v", discord["enabled"])
	}
	if discord["token"] != "${DISCORD_BOT_TOKEN}" {
		t.Errorf("expected channels.discord.token=${DISCORD_BOT_TOKEN}, got %v", discord["token"])
	}
	if discord["groupPolicy"] != "allowlist" {
		t.Errorf("expected channels.discord.groupPolicy=allowlist, got %v", discord["groupPolicy"])
	}
	if discord["dmPolicy"] != "allowlist" {
		t.Errorf("expected channels.discord.dmPolicy=allowlist, got %v", discord["dmPolicy"])
	}

	guilds, ok := discord["guilds"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected channels.discord.guilds to be a map, got %T", discord["guilds"])
	}
	guild, ok := guilds["999888777"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected guilds[999888777] to be a map, got %T", guilds["999888777"])
	}
	if guild["requireMention"] != true {
		t.Errorf("expected guilds[999888777].requireMention=true, got %v", guild["requireMention"])
	}
}

func TestGenerateConfigDiscordNoGuilds(t *testing.T) {
	rc := &driver.ResolvedClaw{
		Models:     make(map[string]string),
		Configures: []string{},
		Handles: map[string]*driver.HandleInfo{
			"discord": {
				ID:       "123456789",
				Username: "tiverton",
				Guilds:   nil,
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

	channels := config["channels"].(map[string]interface{})
	discord := channels["discord"].(map[string]interface{})

	if _, hasGuilds := discord["guilds"]; hasGuilds {
		t.Error("expected no guilds key when Guilds slice is empty")
	}
	// Other discord fields should still be set
	if discord["token"] != "${DISCORD_BOT_TOKEN}" {
		t.Errorf("expected token to be set even with no guilds, got %v", discord["token"])
	}
}

func TestGenerateConfigHandleNilMeansNoChannels(t *testing.T) {
	rc := &driver.ResolvedClaw{
		Models:     make(map[string]string),
		Configures: []string{},
		Handles:    nil,
	}
	data, err := GenerateConfig(rc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var config map[string]interface{}
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	if _, ok := config["channels"]; ok {
		t.Error("expected no channels key when Handles is nil")
	}
}
