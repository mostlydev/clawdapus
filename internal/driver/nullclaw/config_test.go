package nullclaw

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

func TestGenerateConfigSetsGateway(t *testing.T) {
	rc := &driver.ResolvedClaw{}
	data, err := GenerateConfig(rc)
	if err != nil {
		t.Fatal(err)
	}

	if v, _ := getPath(data, "gateway.port"); v != float64(3000) {
		t.Fatalf("expected gateway.port=3000, got %v", v)
	}
	if v, _ := getPath(data, "gateway.host"); v != "127.0.0.1" {
		t.Fatalf("expected gateway.host=127.0.0.1, got %v", v)
	}
	if v, _ := getPath(data, "gateway.require_pairing"); v != true {
		t.Fatalf("expected gateway.require_pairing=true, got %v", v)
	}
}

func TestGenerateConfigSetsModelPrimary(t *testing.T) {
	rc := &driver.ResolvedClaw{
		Models: map[string]string{
			"primary": "openrouter/anthropic/claude-sonnet-4",
		},
	}
	data, err := GenerateConfig(rc)
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := getPath(data, "agents.defaults.model.primary"); v != "openrouter/anthropic/claude-sonnet-4" {
		t.Fatalf("unexpected model.primary: %v", v)
	}
}

func TestGenerateConfigModelFallback(t *testing.T) {
	rc := &driver.ResolvedClaw{
		Models: map[string]string{
			"primary":  "anthropic/claude-sonnet-4",
			"fallback": "openrouter/meta-llama/llama-3.3-70b-instruct",
		},
	}
	data, err := GenerateConfig(rc)
	if err != nil {
		t.Fatal(err)
	}
	v, ok := getPath(data, "reliability.fallback_providers")
	if !ok {
		t.Fatal("expected reliability.fallback_providers")
	}
	arr, ok := v.([]interface{})
	if !ok || len(arr) != 1 || arr[0] != "openrouter/meta-llama/llama-3.3-70b-instruct" {
		t.Fatalf("unexpected fallback providers: %#v", v)
	}
}

func TestGenerateConfigDiscordHandle(t *testing.T) {
	rc := &driver.ResolvedClaw{
		Handles: map[string]*driver.HandleInfo{
			"discord": {
				Guilds: []driver.GuildInfo{{ID: "123456"}},
			},
		},
		Environment: map[string]string{
			"DISCORD_BOT_TOKEN": "discord-token",
		},
	}
	data, err := GenerateConfig(rc)
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := getPath(data, "channels.discord.accounts.main.token"); v != "discord-token" {
		t.Fatalf("unexpected discord token: %v", v)
	}
	if v, _ := getPath(data, "channels.discord.accounts.main.guild_id"); v != "123456" {
		t.Fatalf("unexpected discord guild_id: %v", v)
	}
}

func TestGenerateConfigTelegramHandle(t *testing.T) {
	rc := &driver.ResolvedClaw{
		Handles: map[string]*driver.HandleInfo{
			"telegram": {},
		},
		Environment: map[string]string{
			"TELEGRAM_BOT_TOKEN": "tg-token",
		},
	}
	data, err := GenerateConfig(rc)
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := getPath(data, "channels.telegram.accounts.main.bot_token"); v != "tg-token" {
		t.Fatalf("unexpected telegram bot token: %v", v)
	}
}

func TestGenerateConfigSlackHandle(t *testing.T) {
	rc := &driver.ResolvedClaw{
		Handles: map[string]*driver.HandleInfo{
			"slack": {},
		},
		Environment: map[string]string{
			"SLACK_BOT_TOKEN":      "xoxb-123",
			"SLACK_APP_TOKEN":      "xapp-456",
			"SLACK_SIGNING_SECRET": "secret-1",
		},
	}
	data, err := GenerateConfig(rc)
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := getPath(data, "channels.slack.accounts.main.bot_token"); v != "xoxb-123" {
		t.Fatalf("unexpected slack bot token: %v", v)
	}
	if v, _ := getPath(data, "channels.slack.accounts.main.app_token"); v != "xapp-456" {
		t.Fatalf("unexpected slack app token: %v", v)
	}
	if v, _ := getPath(data, "channels.slack.accounts.main.signing_secret"); v != "secret-1" {
		t.Fatalf("unexpected slack signing secret: %v", v)
	}
	if v, _ := getPath(data, "channels.slack.accounts.main.mode"); v != "socket" {
		t.Fatalf("unexpected slack mode: %v", v)
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
		basePath := "models.providers." + provider
		if v, _ := getPath(data, basePath+".base_url"); v != "http://cllama-passthrough:8080/v1" {
			t.Fatalf("unexpected %s.base_url: %v", basePath, v)
		}
		if v, _ := getPath(data, basePath+".api_key"); v != "agent-token" {
			t.Fatalf("unexpected %s.api_key: %v", basePath, v)
		}
	}
}

func TestGenerateConfigConfigure(t *testing.T) {
	rc := &driver.ResolvedClaw{
		Configures: []string{
			"nullclaw config set gateway.port 8081",
			"nullclaw config set gateway.host \"0.0.0.0\"",
		},
	}
	data, err := GenerateConfig(rc)
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := getPath(data, "gateway.port"); v != float64(8081) {
		t.Fatalf("expected gateway.port override, got %v", v)
	}
	if v, _ := getPath(data, "gateway.host"); v != "0.0.0.0" {
		t.Fatalf("expected gateway.host override, got %v", v)
	}
}

func TestGenerateConfigConfigureOverridesHandle(t *testing.T) {
	rc := &driver.ResolvedClaw{
		Handles: map[string]*driver.HandleInfo{
			"discord": {},
		},
		Environment: map[string]string{
			"DISCORD_BOT_TOKEN": "default-token",
		},
		Configures: []string{
			"nullclaw config set channels.discord.accounts.main.token \"override-token\"",
		},
	}
	data, err := GenerateConfig(rc)
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := getPath(data, "channels.discord.accounts.main.token"); v != "override-token" {
		t.Fatalf("expected CONFIGURE override token, got %v", v)
	}
}

func TestGenerateConfigDeterministic(t *testing.T) {
	rc := &driver.ResolvedClaw{
		Models: map[string]string{
			"primary":  "openrouter/anthropic/claude-sonnet-4",
			"fallback": "anthropic/claude-3-5-haiku",
		},
		Handles: map[string]*driver.HandleInfo{
			"discord": {},
			"slack":   {},
		},
		Environment: map[string]string{
			"DISCORD_BOT_TOKEN": "discord-token",
			"SLACK_BOT_TOKEN":   "xoxb-123",
		},
		Configures: []string{
			"nullclaw config set gateway.port 3001",
		},
	}

	first, err := GenerateConfig(rc)
	if err != nil {
		t.Fatal(err)
	}
	second, err := GenerateConfig(rc)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Fatal("expected deterministic output")
	}
}
