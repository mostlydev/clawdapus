package nanobot

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mostlydev/clawdapus/internal/driver"
)

func getPath(data []byte, path string) (interface{}, bool) {
	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, false
	}
	keys := strings.Split(path, ".")
	var current interface{} = m
	for _, key := range keys {
		cm, ok := current.(map[string]interface{})
		if !ok {
			return nil, false
		}
		current, ok = cm[key]
		if !ok {
			return nil, false
		}
	}
	return current, true
}

func TestGenerateConfigSetsModelAndWorkspace(t *testing.T) {
	rc := &driver.ResolvedClaw{
		Models: map[string]string{"primary": "openrouter/anthropic/claude-sonnet-4"},
		Environment: map[string]string{
			"OPENROUTER_API_KEY": "or-key",
		},
	}
	data, err := GenerateConfig(rc)
	if err != nil {
		t.Fatal(err)
	}

	if v, _ := getPath(data, "agents.defaults.model"); v != "openrouter/anthropic/claude-sonnet-4" {
		t.Fatalf("unexpected agents.defaults.model: %v", v)
	}
	if v, _ := getPath(data, "agents.defaults.workspace"); v != "/root/.nanobot/workspace" {
		t.Fatalf("unexpected agents.defaults.workspace: %v", v)
	}
	if v, _ := getPath(data, "providers.openrouter.api_key"); v != "or-key" {
		t.Fatalf("unexpected providers.openrouter.api_key: %v", v)
	}
}

func TestGenerateConfigCllamaRewrite(t *testing.T) {
	rc := &driver.ResolvedClaw{
		Models: map[string]string{
			"primary":  "openrouter/anthropic/claude-sonnet-4",
			"fallback": "anthropic/claude-3-5-haiku",
		},
		Cllama:      []string{"passthrough"},
		CllamaToken: "agent-token",
	}

	data, err := GenerateConfig(rc)
	if err != nil {
		t.Fatal(err)
	}

	for _, provider := range []string{"openrouter", "anthropic"} {
		basePath := "providers." + provider
		if v, _ := getPath(data, basePath+".base_url"); v != "http://cllama:8080/v1" {
			t.Fatalf("unexpected %s.base_url: %v", basePath, v)
		}
		if v, _ := getPath(data, basePath+".api_key"); v != "agent-token" {
			t.Fatalf("unexpected %s.api_key: %v", basePath, v)
		}
	}
}

func TestGenerateConfigHandleMappings(t *testing.T) {
	rc := &driver.ResolvedClaw{
		Models: map[string]string{"primary": "anthropic/claude-sonnet-4"},
		Handles: map[string]*driver.HandleInfo{
			"discord": {
				Guilds: []driver.GuildInfo{{ID: "123"}},
			},
			"telegram": {},
			"slack":    {},
		},
		Environment: map[string]string{
			"ANTHROPIC_API_KEY":    "anthropic-key",
			"DISCORD_BOT_TOKEN":    "discord-token",
			"TELEGRAM_BOT_TOKEN":   "telegram-token",
			"SLACK_BOT_TOKEN":      "slack-token",
			"SLACK_APP_TOKEN":      "app-token",
			"SLACK_SIGNING_SECRET": "secret-token",
		},
	}

	data, err := GenerateConfig(rc)
	if err != nil {
		t.Fatal(err)
	}

	if v, _ := getPath(data, "channels.discord.enabled"); v != true {
		t.Fatalf("expected channels.discord.enabled=true, got %v", v)
	}
	if v, _ := getPath(data, "channels.discord.token"); v != "discord-token" {
		t.Fatalf("unexpected channels.discord.token: %v", v)
	}
	if v, _ := getPath(data, "channels.discord.guild_id"); v != "123" {
		t.Fatalf("unexpected channels.discord.guild_id: %v", v)
	}
	if v, _ := getPath(data, "channels.telegram.bot_token"); v != "telegram-token" {
		t.Fatalf("unexpected channels.telegram.bot_token: %v", v)
	}
	if v, _ := getPath(data, "channels.slack.bot_token"); v != "slack-token" {
		t.Fatalf("unexpected channels.slack.bot_token: %v", v)
	}
	if v, _ := getPath(data, "channels.slack.app_token"); v != "app-token" {
		t.Fatalf("unexpected channels.slack.app_token: %v", v)
	}
}

func TestGenerateConfigConfigureOverride(t *testing.T) {
	rc := &driver.ResolvedClaw{
		Models: map[string]string{"primary": "anthropic/claude-sonnet-4"},
		Environment: map[string]string{
			"ANTHROPIC_API_KEY": "anthropic-key",
		},
		Configures: []string{
			"nanobot config set agents.defaults.model \"openai/gpt-4.1\"",
			"nanobot config set channels.discord.enabled true",
		},
	}

	data, err := GenerateConfig(rc)
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := getPath(data, "agents.defaults.model"); v != "openai/gpt-4.1" {
		t.Fatalf("expected model override from CONFIGURE, got %v", v)
	}
	if v, _ := getPath(data, "channels.discord.enabled"); v != true {
		t.Fatalf("expected discord enabled override from CONFIGURE, got %v", v)
	}
}
