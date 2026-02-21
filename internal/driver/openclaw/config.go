package openclaw

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mostlydev/clawdapus/internal/driver"
)

// GenerateConfig builds an OpenClaw JSON config from resolved Claw directives.
// Emits standard JSON (valid JSON5). Deterministic output (encoding/json sorts map keys).
func GenerateConfig(rc *driver.ResolvedClaw) ([]byte, error) {
	config := make(map[string]interface{})

	// Apply MODEL directives
	for slot, model := range rc.Models {
		setPath(config, "agents.defaults.model."+slot, model)
	}

	// Apply CONFIGURE directives (parse "openclaw config set <path> <value>")
	for _, cmd := range rc.Configures {
		path, value, err := parseConfigSetCommand(cmd)
		if err != nil {
			return nil, fmt.Errorf("config generation: %w", err)
		}
		setPath(config, path, value)
	}

	return json.MarshalIndent(config, "", "  ")
}

// parseConfigSetCommand extracts dotted path and value from
// "openclaw config set <dotted.path> <value>".
func parseConfigSetCommand(cmd string) (string, string, error) {
	parts := strings.Fields(cmd)
	// Expected: "openclaw" "config" "set" "<path>" "<value>"
	if len(parts) < 5 || parts[0] != "openclaw" || parts[1] != "config" || parts[2] != "set" {
		return "", "", fmt.Errorf("unrecognized CONFIGURE command: %q (expected 'openclaw config set <path> <value>')", cmd)
	}
	path := parts[3]
	value := strings.Join(parts[4:], " ")
	return path, value, nil
}

// setPath sets a nested value in a map using a dotted path.
func setPath(obj map[string]interface{}, path string, value interface{}) {
	parts := strings.Split(path, ".")
	current := obj
	for i, part := range parts {
		if i == len(parts)-1 {
			current[part] = value
			return
		}
		next, ok := current[part].(map[string]interface{})
		if !ok {
			next = make(map[string]interface{})
			current[part] = next
		}
		current = next
	}
}
