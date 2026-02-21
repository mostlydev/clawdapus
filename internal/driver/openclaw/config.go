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
		if err := setPath(config, "agents.defaults.model."+slot, model); err != nil {
			return nil, fmt.Errorf("config generation: %w", err)
		}
	}

	// Apply CONFIGURE directives (parse "openclaw config set <path> <value>")
	for _, cmd := range rc.Configures {
		path, value, err := parseConfigSetCommand(cmd)
		if err != nil {
			return nil, fmt.Errorf("config generation: %w", err)
		}
		if err := setPath(config, path, value); err != nil {
			return nil, fmt.Errorf("config generation: %w", err)
		}
	}

	// Apply HANDLE directives: enable each platform in channels config.
	for platform := range rc.Handles {
		switch platform {
		case "discord", "slack", "telegram":
			if err := setPath(config, "channels."+platform+".enabled", true); err != nil {
				return nil, fmt.Errorf("config generation: HANDLE %s: %w", platform, err)
			}
		default:
			// Unknown platform — no native config path known; log and skip.
			// The env var broadcast still fires regardless.
			fmt.Printf("[claw] warning: openclaw driver has no config mapping for HANDLE platform %q; skipping channel enablement\n", platform)
		}
	}

	// Always enable bootstrap-extra-files hook and ensure CLAWDAPUS.md is in paths.
	// Force enabled=true (CLAWDAPUS.md injection is required for clawdapus to function).
	// Merge paths: preserve any user-added paths from CONFIGURE directives.
	if err := ensureBootstrapHook(config); err != nil {
		return nil, fmt.Errorf("config generation: %w", err)
	}

	return json.MarshalIndent(config, "", "  ")
}

// parseConfigSetCommand extracts dotted path and value from
// "openclaw config set <dotted.path> <value>".
func parseConfigSetCommand(cmd string) (string, interface{}, error) {
	parts := strings.Fields(cmd)
	// Expected: "openclaw" "config" "set" "<path>" "<value>"
	if len(parts) < 5 || parts[0] != "openclaw" || parts[1] != "config" || parts[2] != "set" {
		return "", nil, fmt.Errorf("unrecognized CONFIGURE command: %q (expected 'openclaw config set <path> <value>')", cmd)
	}
	path := parts[3]
	value := strings.TrimSpace(strings.Join(parts[4:], " "))
	if value == "" {
		return "", nil, fmt.Errorf("unrecognized CONFIGURE command: %q (expected non-empty value)", cmd)
	}

	// Preserve native JSON scalar/object/array types when possible.
	var typed interface{}
	if err := json.Unmarshal([]byte(value), &typed); err == nil {
		return path, typed, nil
	}

	return path, value, nil
}

// ensureBootstrapHook forces the bootstrap-extra-files hook to be enabled and
// ensures "CLAWDAPUS.md" is in its paths list, merging with any user-configured paths.
func ensureBootstrapHook(config map[string]interface{}) error {
	// Navigate to hooks.bootstrap-extra-files, creating intermediate maps as needed.
	hookPath := "hooks.bootstrap-extra-files"
	hookObj, err := getOrCreatePath(config, hookPath)
	if err != nil {
		return err
	}

	// Force enabled=true — CLAWDAPUS.md injection is non-negotiable.
	hookObj["enabled"] = true

	// Merge "CLAWDAPUS.md" into existing paths (dedupe).
	const required = "CLAWDAPUS.md"
	existing := extractStringSlice(hookObj["paths"])
	found := false
	for _, p := range existing {
		if p == required {
			found = true
			break
		}
	}
	if !found {
		existing = append(existing, required)
	}
	hookObj["paths"] = existing

	return nil
}

// getOrCreatePath navigates a dotted path in config, creating intermediate maps,
// and returns the final map node.
func getOrCreatePath(obj map[string]interface{}, path string) (map[string]interface{}, error) {
	parts := strings.Split(path, ".")
	current := obj
	for _, part := range parts {
		nextRaw, exists := current[part]
		if !exists {
			next := make(map[string]interface{})
			current[part] = next
			current = next
			continue
		}
		next, ok := nextRaw.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("path conflict at %q: expected object, found %T", part, nextRaw)
		}
		current = next
	}
	return current, nil
}

// extractStringSlice converts an interface{} (expected to be []interface{} of strings
// from prior JSON/setPath operations) into a []string. Returns nil for non-slice values.
func extractStringSlice(v interface{}) []string {
	if v == nil {
		return nil
	}
	// Direct []string (from our own setPath calls)
	if ss, ok := v.([]string); ok {
		return ss
	}
	// []interface{} (from JSON unmarshal or mixed operations)
	if arr, ok := v.([]interface{}); ok {
		out := make([]string, 0, len(arr))
		for _, item := range arr {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// setPath sets a nested value in a map using a dotted path.
func setPath(obj map[string]interface{}, path string, value interface{}) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return fmt.Errorf("invalid empty config path")
	}

	parts := strings.Split(path, ".")
	current := obj
	for i, part := range parts {
		if part == "" {
			return fmt.Errorf("invalid config path %q", path)
		}

		if i == len(parts)-1 {
			if existing, exists := current[part]; exists {
				if _, isMap := existing.(map[string]interface{}); isMap {
					return fmt.Errorf("path conflict at %q: cannot overwrite object with value", strings.Join(parts[:i+1], "."))
				}
			}
			current[part] = value
			return nil
		}

		nextRaw, exists := current[part]
		if !exists {
			next := make(map[string]interface{})
			current[part] = next
			current = next
			continue
		}

		next, ok := nextRaw.(map[string]interface{})
		if !ok {
			return fmt.Errorf("path conflict at %q: expected object, found %T", strings.Join(parts[:i+1], "."), nextRaw)
		}
		current = next
	}

	return nil
}
