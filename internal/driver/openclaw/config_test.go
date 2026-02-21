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
