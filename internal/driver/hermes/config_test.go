package hermes

import (
	"strings"
	"testing"

	"github.com/mostlydev/clawdapus/internal/driver"
	"gopkg.in/yaml.v3"
)

func TestGenerateConfigOpenRouterModel(t *testing.T) {
	rc := &driver.ResolvedClaw{
		Models: map[string]string{"primary": "openrouter/anthropic/claude-sonnet-4"},
		Handles: map[string]*driver.HandleInfo{
			"discord": {},
		},
		Environment: map[string]string{
			"DISCORD_BOT_TOKEN":  "discord-token",
			"OPENROUTER_API_KEY": "or-key",
		},
	}

	mc, err := resolveModelConfig(rc)
	if err != nil {
		t.Fatalf("resolveModelConfig returned error: %v", err)
	}
	data, err := GenerateConfig(rc, mc)
	if err != nil {
		t.Fatalf("GenerateConfig returned error: %v", err)
	}

	var cfg map[string]any
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("parse generated yaml: %v", err)
	}

	model, _ := cfg["model"].(map[string]any)
	if got := model["provider"]; got != "openrouter" {
		t.Fatalf("expected openrouter provider, got %#v", got)
	}
	if got := model["default"]; got != "anthropic/claude-sonnet-4" {
		t.Fatalf("expected bare OpenRouter model id, got %#v", got)
	}
}

func TestGenerateConfigAnthropicModelUsesBareModelName(t *testing.T) {
	rc := &driver.ResolvedClaw{
		Models: map[string]string{"primary": "anthropic/claude-sonnet-4"},
		Handles: map[string]*driver.HandleInfo{
			"telegram": {},
		},
		Environment: map[string]string{
			"TELEGRAM_BOT_TOKEN": "telegram-token",
			"ANTHROPIC_API_KEY":  "anthropic-key",
		},
	}

	mc, err := resolveModelConfig(rc)
	if err != nil {
		t.Fatalf("resolveModelConfig returned error: %v", err)
	}
	data, err := GenerateConfig(rc, mc)
	if err != nil {
		t.Fatalf("GenerateConfig returned error: %v", err)
	}

	var cfg map[string]any
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("parse generated yaml: %v", err)
	}

	model, _ := cfg["model"].(map[string]any)
	if got := model["provider"]; got != "anthropic" {
		t.Fatalf("expected anthropic provider, got %#v", got)
	}
	if got := model["default"]; got != "claude-sonnet-4" {
		t.Fatalf("expected bare anthropic model, got %#v", got)
	}
}

func TestGenerateEnvFileIncludesCllamaRouting(t *testing.T) {
	rc := &driver.ResolvedClaw{
		Models:      map[string]string{"primary": "openrouter/anthropic/claude-sonnet-4"},
		Cllama:      []string{"passthrough"},
		CllamaToken: "agent-token",
		Handles: map[string]*driver.HandleInfo{
			"slack": {},
		},
		Environment: map[string]string{
			"SLACK_BOT_TOKEN": "slack-bot",
			"SLACK_APP_TOKEN": "slack-app",
		},
	}

	mc, err := resolveModelConfig(rc)
	if err != nil {
		t.Fatalf("resolveModelConfig returned error: %v", err)
	}
	data, err := GenerateEnvFile(rc, mc)
	if err != nil {
		t.Fatalf("GenerateEnvFile returned error: %v", err)
	}
	text := string(data)
	if !strings.Contains(text, "OPENAI_BASE_URL=http://cllama:8080/v1") {
		t.Fatalf("expected cllama base URL in env file, got:\n%s", text)
	}
	if !strings.Contains(text, "OPENAI_API_KEY=agent-token") {
		t.Fatalf("expected cllama token in env file, got:\n%s", text)
	}
	if !strings.Contains(text, "HERMES_HOME=/root/.hermes") {
		t.Fatalf("expected HERMES_HOME in env file, got:\n%s", text)
	}
	if !strings.Contains(text, "MESSAGING_CWD=/workspace") {
		t.Fatalf("expected MESSAGING_CWD in env file, got:\n%s", text)
	}
}
