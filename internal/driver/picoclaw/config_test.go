package picoclaw

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

func modelEntryByName(data []byte, modelName string) (map[string]interface{}, bool) {
	v, ok := getPath(data, "model_list")
	if !ok {
		return nil, false
	}
	modelList, ok := v.([]interface{})
	if !ok {
		return nil, false
	}

	for _, item := range modelList {
		entry, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		if entry["model_name"] == modelName {
			return entry, true
		}
	}
	return nil, false
}

func TestGenerateConfigModelListDirect(t *testing.T) {
	rc := &driver.ResolvedClaw{
		Models: map[string]string{
			"primary":  "openrouter/anthropic/claude-sonnet-4",
			"fallback": "anthropic/claude-3-5-haiku",
		},
		Environment: map[string]string{
			"OPENROUTER_API_KEY": "or-key",
			"ANTHROPIC_API_KEY":  "anthropic-key",
		},
	}

	data, err := GenerateConfig(rc)
	if err != nil {
		t.Fatal(err)
	}

	if v, _ := getPath(data, "agents.defaults.model_name"); v != "primary" {
		t.Fatalf("unexpected agents.defaults.model_name: %v", v)
	}
	if v, _ := getPath(data, "agents.defaults.workspace"); v != picoclawWorkspaceDir {
		t.Fatalf("unexpected agents.defaults.workspace: %v", v)
	}

	primary, ok := modelEntryByName(data, "primary")
	if !ok {
		t.Fatal("missing model_list entry for primary")
	}
	if primary["model"] != "openrouter/anthropic/claude-sonnet-4" {
		t.Fatalf("unexpected primary model: %v", primary["model"])
	}
	if primary["api_key"] != "or-key" {
		t.Fatalf("unexpected primary api_key: %v", primary["api_key"])
	}

	fallback, ok := modelEntryByName(data, "fallback")
	if !ok {
		t.Fatal("missing model_list entry for fallback")
	}
	if fallback["model"] != "anthropic/claude-3-5-haiku" {
		t.Fatalf("unexpected fallback model: %v", fallback["model"])
	}
	if fallback["api_key"] != "anthropic-key" {
		t.Fatalf("unexpected fallback api_key: %v", fallback["api_key"])
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

	for slot, expectedProtocol := range map[string]string{
		"primary":  "openai/openrouter/anthropic/claude-sonnet-4",
		"fallback": "openai/anthropic/claude-3-5-haiku",
	} {
		entry, ok := modelEntryByName(data, slot)
		if !ok {
			t.Fatalf("missing model_list entry for %s", slot)
		}
		if entry["model"] != expectedProtocol {
			t.Fatalf("unexpected model for %s: %v", slot, entry["model"])
		}
		if entry["api_base"] != "http://cllama:8080/v1" {
			t.Fatalf("unexpected api_base for %s: %v", slot, entry["api_base"])
		}
		if entry["api_key"] != "agent-token" {
			t.Fatalf("unexpected api_key for %s: %v", slot, entry["api_key"])
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
			"wecom":    {},
		},
		Environment: map[string]string{
			"ANTHROPIC_API_KEY":    "anthropic-key",
			"DISCORD_BOT_TOKEN":    "discord-token",
			"TELEGRAM_BOT_TOKEN":   "telegram-token",
			"SLACK_BOT_TOKEN":      "slack-token",
			"SLACK_APP_TOKEN":      "slack-app-token",
			"SLACK_SIGNING_SECRET": "slack-signing-secret",
			"WECOM_BOT_TOKEN":      "wecom-token",
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
	if v, _ := getPath(data, "channels.slack.app_token"); v != "slack-app-token" {
		t.Fatalf("unexpected channels.slack.app_token: %v", v)
	}
	if v, _ := getPath(data, "channels.slack.signing_secret"); v != "slack-signing-secret" {
		t.Fatalf("unexpected channels.slack.signing_secret: %v", v)
	}
	if v, _ := getPath(data, "channels.wecom.bot_token"); v != "wecom-token" {
		t.Fatalf("unexpected channels.wecom.bot_token: %v", v)
	}
}

func TestGenerateConfigConfigureOverride(t *testing.T) {
	rc := &driver.ResolvedClaw{
		Models: map[string]string{
			"primary":  "anthropic/claude-sonnet-4",
			"fallback": "openai/gpt-4.1",
		},
		Environment: map[string]string{
			"ANTHROPIC_API_KEY": "anthropic-key",
			"OPENAI_API_KEY":    "openai-key",
		},
		Configures: []string{
			"picoclaw config set agents.defaults.model_name \"fallback\"",
			"picoclaw config set channels.discord.enabled true",
		},
	}

	data, err := GenerateConfig(rc)
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := getPath(data, "agents.defaults.model_name"); v != "fallback" {
		t.Fatalf("expected model_name override from CONFIGURE, got %v", v)
	}
	if v, _ := getPath(data, "channels.discord.enabled"); v != true {
		t.Fatalf("expected discord enabled override from CONFIGURE, got %v", v)
	}
}
