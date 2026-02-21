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

	// Gateway must run in local mode inside managed containers (not cloud/hosted mode).
	// Required: without this openclaw refuses to start the gateway.
	if err := setPath(config, "gateway.mode", "local"); err != nil {
		return nil, fmt.Errorf("config generation: %w", err)
	}

	// Set workspace to /claw so openclaw finds AGENTS.md (mounted there) and workspace skills
	// (/claw/skills/). Bootstrap-extra-files paths (e.g. "CLAWDAPUS.md") are also relative
	// to workspace, so /claw/CLAWDAPUS.md resolves correctly.
	if err := setPath(config, "agents.defaults.workspace", "/claw"); err != nil {
		return nil, fmt.Errorf("config generation: %w", err)
	}

	// Apply MODEL directives. openclaw uses "fallbacks" ([]string), not "fallback" (string).
	for slot, model := range rc.Models {
		if slot == "fallback" {
			if err := setPath(config, "agents.defaults.model.fallbacks", []string{model}); err != nil {
				return nil, fmt.Errorf("config generation: %w", err)
			}
			continue
		}
		if err := setPath(config, "agents.defaults.model."+slot, model); err != nil {
			return nil, fmt.Errorf("config generation: %w", err)
		}
	}

	// Apply HANDLE directives first: they provide structural defaults per platform.
	// CONFIGURE runs after so operator overrides always take precedence.
	for platform := range rc.Handles {
		switch platform {
		case "discord":
			h := rc.Handles[platform]
			if err := setPath(config, "channels.discord.enabled", true); err != nil {
				return nil, fmt.Errorf("config generation: HANDLE discord: %w", err)
			}
			if err := setPath(config, "channels.discord.token", "${DISCORD_BOT_TOKEN}"); err != nil {
				return nil, fmt.Errorf("config generation: HANDLE discord: %w", err)
			}
			if err := setPath(config, "channels.discord.groupPolicy", "allowlist"); err != nil {
				return nil, fmt.Errorf("config generation: HANDLE discord: %w", err)
			}
			if err := setPath(config, "channels.discord.dmPolicy", "allowlist"); err != nil {
				return nil, fmt.Errorf("config generation: HANDLE discord: %w", err)
			}
			if h != nil && len(h.Guilds) > 0 {
				guilds := make(map[string]interface{})
				for _, g := range h.Guilds {
					guilds[g.ID] = map[string]interface{}{"requireMention": true}
				}
				if err := setPath(config, "channels.discord.guilds", guilds); err != nil {
					return nil, fmt.Errorf("config generation: HANDLE discord: %w", err)
				}
			}
		case "slack", "telegram":
			if err := setPath(config, "channels."+platform+".enabled", true); err != nil {
				return nil, fmt.Errorf("config generation: HANDLE %s: %w", platform, err)
			}
		default:
			// Unknown platform — no native config path known; log and skip.
			// The env var broadcast still fires regardless.
			fmt.Printf("[claw] warning: openclaw driver has no config mapping for HANDLE platform %q; skipping channel enablement\n", platform)
		}
	}

	// Apply CONFIGURE directives: operator overrides that take precedence over HANDLE defaults.
	for _, cmd := range rc.Configures {
		path, value, err := parseConfigSetCommand(cmd)
		if err != nil {
			return nil, fmt.Errorf("config generation: %w", err)
		}
		if err := setPath(config, path, value); err != nil {
			return nil, fmt.Errorf("config generation: %w", err)
		}
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
