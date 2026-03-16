package shared

import (
	"strings"
	"testing"
)

func TestSetPathCreatesNestedObjects(t *testing.T) {
	obj := map[string]any{}
	if err := SetPath(obj, "channels.discord.enabled", true); err != nil {
		t.Fatalf("SetPath returned error: %v", err)
	}

	channels, ok := obj["channels"].(map[string]any)
	if !ok {
		t.Fatalf("expected nested channels map, got %#v", obj["channels"])
	}
	discord, ok := channels["discord"].(map[string]any)
	if !ok {
		t.Fatalf("expected nested discord map, got %#v", channels["discord"])
	}
	if enabled, _ := discord["enabled"].(bool); !enabled {
		t.Fatalf("expected enabled=true, got %#v", discord["enabled"])
	}
}

func TestSetPathRejectsObjectOverwrite(t *testing.T) {
	obj := map[string]any{"channels": map[string]any{"discord": map[string]any{}}}
	err := SetPath(obj, "channels.discord", true)
	if err == nil {
		t.Fatal("expected SetPath to reject object overwrite")
	}
	if !strings.Contains(err.Error(), "cannot overwrite object") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseConfigSetCommandParsesJSONValue(t *testing.T) {
	path, value, err := ParseConfigSetCommand(`hermes config set channels.discord.enabled true`, "hermes")
	if err != nil {
		t.Fatalf("ParseConfigSetCommand returned error: %v", err)
	}
	if path != "channels.discord.enabled" {
		t.Fatalf("unexpected path: %q", path)
	}
	if enabled, ok := value.(bool); !ok || !enabled {
		t.Fatalf("expected bool true, got %#v", value)
	}
}

func TestParseConfigSetCommandPreservesStringValue(t *testing.T) {
	path, value, err := ParseConfigSetCommand(`nanobot config set model.default claude-sonnet-4`, "nanobot")
	if err != nil {
		t.Fatalf("ParseConfigSetCommand returned error: %v", err)
	}
	if path != "model.default" {
		t.Fatalf("unexpected path: %q", path)
	}
	if value != "claude-sonnet-4" {
		t.Fatalf("unexpected value: %#v", value)
	}
}

func TestParseConfigSetCommandRejectsUnexpectedPrefix(t *testing.T) {
	_, _, err := ParseConfigSetCommand(`picoclaw config set agents.defaults.model_name "fallback"`, "hermes")
	if err == nil {
		t.Fatal("expected ParseConfigSetCommand to reject wrong driver prefix")
	}
	if !strings.Contains(err.Error(), "hermes config set") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestPrimaryModelRef(t *testing.T) {
	ref, err := PrimaryModelRef(map[string]string{"primary": "anthropic/claude-sonnet-4"})
	if err != nil {
		t.Fatalf("PrimaryModelRef returned error: %v", err)
	}
	if ref != "anthropic/claude-sonnet-4" {
		t.Fatalf("unexpected primary model ref: %q", ref)
	}
}

func TestPrimaryModelRefRequiresPrimary(t *testing.T) {
	_, err := PrimaryModelRef(map[string]string{})
	if err == nil {
		t.Fatal("expected missing MODEL primary error")
	}
	if !strings.Contains(err.Error(), "MODEL primary") {
		t.Fatalf("unexpected error: %v", err)
	}
}
