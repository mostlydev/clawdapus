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

func TestGenerateConfigBootstrapHookMergesPaths(t *testing.T) {
	rc := &driver.ResolvedClaw{
		Models: make(map[string]string),
		Configures: []string{
			`openclaw config set hooks.bootstrap-extra-files.paths ["custom-hook.md"]`,
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

	hooks := config["hooks"].(map[string]interface{})
	bef := hooks["bootstrap-extra-files"].(map[string]interface{})

	// enabled must be forced true even if not set by CONFIGURE
	if bef["enabled"] != true {
		t.Errorf("expected enabled=true, got %v", bef["enabled"])
	}

	// paths must contain both user-configured and CLAWDAPUS.md
	paths, ok := bef["paths"].([]interface{})
	if !ok {
		t.Fatalf("expected paths to be array, got %T", bef["paths"])
	}
	hasCustom := false
	hasClawdapus := false
	for _, p := range paths {
		switch p {
		case "custom-hook.md":
			hasCustom = true
		case "CLAWDAPUS.md":
			hasClawdapus = true
		}
	}
	if !hasCustom {
		t.Error("expected user-configured custom-hook.md in paths")
	}
	if !hasClawdapus {
		t.Error("expected CLAWDAPUS.md in paths")
	}
}

func TestGenerateConfigBootstrapHookOverridesEnabledFalse(t *testing.T) {
	rc := &driver.ResolvedClaw{
		Models: make(map[string]string),
		Configures: []string{
			"openclaw config set hooks.bootstrap-extra-files.enabled false",
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

	hooks := config["hooks"].(map[string]interface{})
	bef := hooks["bootstrap-extra-files"].(map[string]interface{})

	// enabled=false from CONFIGURE must be overridden to true
	if bef["enabled"] != true {
		t.Errorf("expected enabled=true (forced), got %v", bef["enabled"])
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
