package nullclaw

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/mostlydev/clawdapus/internal/driver"
)

// GenerateConfig builds a nullclaw JSON config from resolved Claw directives.
// Output is deterministic because map keys are sorted by encoding/json.
func GenerateConfig(rc *driver.ResolvedClaw) ([]byte, error) {
	config := make(map[string]interface{})

	// Conservative gateway defaults: keep local bind + pairing requirement.
	if err := setPath(config, "gateway.port", 3000); err != nil {
		return nil, fmt.Errorf("config generation: %w", err)
	}
	if err := setPath(config, "gateway.host", "127.0.0.1"); err != nil {
		return nil, fmt.Errorf("config generation: %w", err)
	}
	if err := setPath(config, "gateway.require_pairing", true); err != nil {
		return nil, fmt.Errorf("config generation: %w", err)
	}

	// Safety defaults.
	if err := setPath(config, "autonomy.level", "supervised"); err != nil {
		return nil, fmt.Errorf("config generation: %w", err)
	}
	if err := setPath(config, "autonomy.workspace_only", true); err != nil {
		return nil, fmt.Errorf("config generation: %w", err)
	}

	for slot, model := range rc.Models {
		if slot == "fallback" {
			if err := setPath(config, "reliability.fallback_providers", []string{model}); err != nil {
				return nil, fmt.Errorf("config generation: %w", err)
			}
			continue
		}
		if err := setPath(config, "agents.defaults.model."+slot, model); err != nil {
			return nil, fmt.Errorf("config generation: %w", err)
		}
	}

	if len(rc.Cllama) > 0 {
		if strings.TrimSpace(rc.CllamaToken) == "" {
			return nil, fmt.Errorf("config generation: CLLAMA is enabled but token is empty")
		}
		firstProxy := fmt.Sprintf("http://cllama-%s:8080/v1", rc.Cllama[0])
		for _, provider := range collectProviders(rc.Models) {
			base := "models.providers." + provider
			if err := setPath(config, base+".base_url", firstProxy); err != nil {
				return nil, fmt.Errorf("config generation: cllama provider %q base_url: %w", provider, err)
			}
			if err := setPath(config, base+".api_key", rc.CllamaToken); err != nil {
				return nil, fmt.Errorf("config generation: cllama provider %q api_key: %w", provider, err)
			}
		}
	}

	// HANDLE defaults first. CONFIGURE runs last and overrides these values.
	for platform, h := range rc.Handles {
		switch strings.ToLower(platform) {
		case "discord":
			if token := resolveEnvTokenFromMap(rc.Environment, "DISCORD_BOT_TOKEN"); token != "" {
				if err := setPath(config, "channels.discord.accounts.main.token", token); err != nil {
					return nil, fmt.Errorf("config generation: HANDLE discord: %w", err)
				}
			}
			if h != nil {
				for _, g := range h.Guilds {
					gid := strings.TrimSpace(g.ID)
					if gid == "" {
						continue
					}
					if err := setPath(config, "channels.discord.accounts.main.guild_id", gid); err != nil {
						return nil, fmt.Errorf("config generation: HANDLE discord: %w", err)
					}
					break
				}
			}
		case "telegram":
			if token := resolveEnvTokenFromMap(rc.Environment, "TELEGRAM_BOT_TOKEN"); token != "" {
				if err := setPath(config, "channels.telegram.accounts.main.bot_token", token); err != nil {
					return nil, fmt.Errorf("config generation: HANDLE telegram: %w", err)
				}
			}
		case "slack":
			if token := resolveEnvTokenFromMap(rc.Environment, "SLACK_BOT_TOKEN"); token != "" {
				if err := setPath(config, "channels.slack.accounts.main.bot_token", token); err != nil {
					return nil, fmt.Errorf("config generation: HANDLE slack: %w", err)
				}
			}
			appToken := resolveEnvTokenFromMap(rc.Environment, "SLACK_APP_TOKEN")
			if appToken != "" {
				if err := setPath(config, "channels.slack.accounts.main.app_token", appToken); err != nil {
					return nil, fmt.Errorf("config generation: HANDLE slack: %w", err)
				}
				if err := setPath(config, "channels.slack.accounts.main.mode", "socket"); err != nil {
					return nil, fmt.Errorf("config generation: HANDLE slack: %w", err)
				}
			}
			signingSecret := resolveEnvTokenFromMap(rc.Environment, "SLACK_SIGNING_SECRET")
			if signingSecret != "" {
				if err := setPath(config, "channels.slack.accounts.main.signing_secret", signingSecret); err != nil {
					return nil, fmt.Errorf("config generation: HANDLE slack: %w", err)
				}
				if appToken == "" {
					if err := setPath(config, "channels.slack.accounts.main.mode", "http"); err != nil {
						return nil, fmt.Errorf("config generation: HANDLE slack: %w", err)
					}
				}
			}
		default:
			fmt.Printf("[claw] warning: nullclaw driver has no config mapping for HANDLE platform %q; skipping channel enablement\n", platform)
		}
	}

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

func parseConfigSetCommand(cmd string) (string, interface{}, error) {
	parts := strings.Fields(cmd)
	if len(parts) < 5 || parts[0] != "nullclaw" || parts[1] != "config" || parts[2] != "set" {
		return "", nil, fmt.Errorf("unrecognized CONFIGURE command: %q (expected 'nullclaw config set <path> <value>')", cmd)
	}
	path := strings.TrimSpace(parts[3])
	if path == "" {
		return "", nil, fmt.Errorf("unrecognized CONFIGURE command: %q (expected non-empty path)", cmd)
	}

	valueText := strings.TrimSpace(strings.Join(parts[4:], " "))
	if valueText == "" {
		return "", nil, fmt.Errorf("unrecognized CONFIGURE command: %q (expected non-empty value)", cmd)
	}

	var typed interface{}
	if err := json.Unmarshal([]byte(valueText), &typed); err == nil {
		return path, typed, nil
	}
	return path, valueText, nil
}

func splitModelRef(ref string) (string, string, bool) {
	trimmed := strings.TrimSpace(ref)
	if trimmed == "" {
		return "", "", false
	}
	parts := strings.SplitN(trimmed, "/", 2)
	if len(parts) == 1 {
		return "anthropic", parts[0], true
	}
	provider := strings.ToLower(strings.TrimSpace(parts[0]))
	model := strings.TrimSpace(parts[1])
	if provider == "" || model == "" {
		return "", "", false
	}
	return provider, model, true
}

func collectProviders(models map[string]string) []string {
	seen := make(map[string]struct{})
	for _, ref := range models {
		provider, _, ok := splitModelRef(ref)
		if !ok {
			continue
		}
		seen[provider] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for provider := range seen {
		out = append(out, provider)
	}
	sort.Strings(out)
	return out
}

func resolveEnvTokenFromMap(env map[string]string, key string) string {
	if env == nil {
		return ""
	}
	return resolveEnvToken(env[key])
}

func resolveEnvToken(raw string) string {
	v := strings.TrimSpace(raw)
	if v == "" {
		return ""
	}
	if strings.HasPrefix(v, "${") && strings.HasSuffix(v, "}") {
		name := strings.TrimSpace(v[2 : len(v)-1])
		if name == "" {
			return ""
		}
		return strings.TrimSpace(os.Getenv(name))
	}
	if strings.HasPrefix(v, "$") {
		name := strings.TrimSpace(strings.TrimPrefix(v, "$"))
		if isEnvVarName(name) {
			return strings.TrimSpace(os.Getenv(name))
		}
	}
	return v
}

func isEnvVarName(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		if i == 0 && !(r == '_' || (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z')) {
			return false
		}
		if !(r == '_' || (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')) {
			return false
		}
	}
	return true
}

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
