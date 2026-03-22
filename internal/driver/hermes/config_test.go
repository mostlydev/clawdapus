package hermes

import (
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

func TestResolvedEnvValueExpandsCompoundVars(t *testing.T) {
	// Set env vars for the test
	t.Setenv("TEST_ID_A", "111")
	t.Setenv("TEST_ID_B", "222")
	t.Setenv("TEST_ID_C", "333")

	env := map[string]string{
		"SINGLE":   "${TEST_ID_A}",
		"COMPOUND": "${TEST_ID_A},${TEST_ID_B},${TEST_ID_C}",
		"LITERAL":  "plain-value",
	}

	if got := resolvedEnvValue(env, "SINGLE"); got != "111" {
		t.Fatalf("single var: expected 111, got %q", got)
	}
	if got := resolvedEnvValue(env, "COMPOUND"); got != "111,222,333" {
		t.Fatalf("compound var: expected 111,222,333, got %q", got)
	}
	if got := resolvedEnvValue(env, "LITERAL"); got != "plain-value" {
		t.Fatalf("literal: expected plain-value, got %q", got)
	}
}

func TestGenerateConfigNoBaseURLWithoutCllama(t *testing.T) {
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
	if _, exists := model["base_url"]; exists {
		t.Fatal("expected no base_url when cllama is not enabled")
	}
	if _, exists := model["api_key"]; exists {
		t.Fatal("expected no api_key when cllama is not enabled")
	}
}

func TestGenerateConfigIncludesCllamaRouting(t *testing.T) {
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
	data, err := GenerateConfig(rc, mc)
	if err != nil {
		t.Fatalf("GenerateConfig returned error: %v", err)
	}
	var cfg map[string]any
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("parse generated yaml: %v", err)
	}
	model, _ := cfg["model"].(map[string]any)
	if got := model["base_url"]; got != "http://cllama:8080/v1" {
		t.Fatalf("expected cllama base_url in config, got %#v", got)
	}
	if got := model["api_key"]; got != "agent-token" {
		t.Fatalf("expected cllama api_key in config, got %#v", got)
	}
	if got := model["provider"]; got != "custom" {
		t.Fatalf("expected custom provider, got %#v", got)
	}
}
