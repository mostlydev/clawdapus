package openclaw

import (
	"encoding/json"
	"testing"

	"github.com/mostlydev/clawdapus/internal/driver"
)

func TestGenerateConfigParsesMultiWordConfigureValue(t *testing.T) {
	rc := &driver.ResolvedClaw{
		Models: make(map[string]string),
		Configures: []string{
			"openclaw config set agents.defaults.system_prompt You are a terse assistant",
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
	if defaults["system_prompt"] != "You are a terse assistant" {
		t.Fatalf("expected multi-word configure value, got %#v", defaults["system_prompt"])
	}
}

func TestGenerateConfigRejectsConfigureWithoutValue(t *testing.T) {
	rc := &driver.ResolvedClaw{
		Models: make(map[string]string),
		Configures: []string{
			"openclaw config set agents.defaults.heartbeat.every",
		},
	}

	_, err := GenerateConfig(rc)
	if err == nil {
		t.Fatal("expected error for CONFIGURE command without value")
	}
}

func TestGenerateConfigParsesNumericAndBooleanValues(t *testing.T) {
	rc := &driver.ResolvedClaw{
		Models: make(map[string]string),
		Configures: []string{
			"openclaw config set agents.defaults.max_tokens 4096",
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
	if _, ok := defaults["max_tokens"].(float64); !ok {
		t.Fatalf("expected numeric max_tokens, got %#v", defaults["max_tokens"])
	}
}

func TestGenerateConfigNoHooksKeyByDefault(t *testing.T) {
	// openclaw rejects unknown keys in the hooks section. The driver must not
	// emit any hooks config unless the operator explicitly sets one via CONFIGURE.
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

	if _, exists := config["hooks"]; exists {
		t.Error("expected no 'hooks' key in default config (openclaw rejects unknown hook keys)")
	}
}

func TestGenerateConfigRejectsPathTypeConflicts(t *testing.T) {
	rc := &driver.ResolvedClaw{
		Models: make(map[string]string),
		Configures: []string{
			"openclaw config set agents.defaults.model primary",
			"openclaw config set agents.defaults.model.primary test/model",
		},
	}

	_, err := GenerateConfig(rc)
	if err == nil {
		t.Fatal("expected path conflict error")
	}
}
