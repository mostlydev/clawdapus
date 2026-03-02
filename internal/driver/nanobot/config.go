package nanobot

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mostlydev/clawdapus/internal/cllama"
	"github.com/mostlydev/clawdapus/internal/driver"
	"github.com/mostlydev/clawdapus/internal/driver/shared"
)

// GenerateConfig builds a nanobot JSON config from resolved Claw directives.
func GenerateConfig(rc *driver.ResolvedClaw) ([]byte, error) {
	config := make(map[string]interface{})

	modelRef, err := primaryModelRef(rc.Models)
	if err != nil {
		return nil, err
	}

	if err := setPath(config, "agents.defaults.model", modelRef); err != nil {
		return nil, fmt.Errorf("config generation: %w", err)
	}
	if err := setPath(config, "agents.defaults.workspace", "/root/.nanobot/workspace"); err != nil {
		return nil, fmt.Errorf("config generation: %w", err)
	}

	if len(rc.Cllama) > 0 {
		if strings.TrimSpace(rc.CllamaToken) == "" {
			return nil, fmt.Errorf("config generation: CLLAMA is enabled but token is empty")
		}
		firstProxy := cllama.ProxyBaseURL(rc.Cllama[0])
		for _, provider := range shared.CollectProviders(rc.Models) {
			base := "providers." + provider
			if err := setPath(config, base+".base_url", firstProxy); err != nil {
				return nil, fmt.Errorf("config generation: cllama provider %q base_url: %w", provider, err)
			}
			if err := setPath(config, base+".api_key", rc.CllamaToken); err != nil {
				return nil, fmt.Errorf("config generation: cllama provider %q api_key: %w", provider, err)
			}
		}
	} else {
		for _, provider := range shared.CollectProviders(rc.Models) {
			if token := shared.ResolveProviderAPIKey(provider, rc.Environment); token != "" {
				if err := setPath(config, "providers."+provider+".api_key", token); err != nil {
					return nil, fmt.Errorf("config generation: provider %q api_key: %w", provider, err)
				}
			}
		}
	}

	for platform, h := range rc.Handles {
		switch strings.ToLower(platform) {
		case "discord":
			if err := setPath(config, "channels.discord.enabled", true); err != nil {
				return nil, fmt.Errorf("config generation: HANDLE discord: %w", err)
			}
			if token := shared.ResolveEnvTokenFromMap(rc.Environment, "DISCORD_BOT_TOKEN"); token != "" {
				if err := setPath(config, "channels.discord.token", token); err != nil {
					return nil, fmt.Errorf("config generation: HANDLE discord: %w", err)
				}
			}
			if h != nil {
				for _, g := range h.Guilds {
					gid := strings.TrimSpace(g.ID)
					if gid == "" {
						continue
					}
					if err := setPath(config, "channels.discord.guild_id", gid); err != nil {
						return nil, fmt.Errorf("config generation: HANDLE discord: %w", err)
					}
					break
				}
			}
		case "telegram":
			if err := setPath(config, "channels.telegram.enabled", true); err != nil {
				return nil, fmt.Errorf("config generation: HANDLE telegram: %w", err)
			}
			if token := shared.ResolveEnvTokenFromMap(rc.Environment, "TELEGRAM_BOT_TOKEN"); token != "" {
				if err := setPath(config, "channels.telegram.bot_token", token); err != nil {
					return nil, fmt.Errorf("config generation: HANDLE telegram: %w", err)
				}
			}
		case "slack":
			if err := setPath(config, "channels.slack.enabled", true); err != nil {
				return nil, fmt.Errorf("config generation: HANDLE slack: %w", err)
			}
			if token := shared.ResolveEnvTokenFromMap(rc.Environment, "SLACK_BOT_TOKEN"); token != "" {
				if err := setPath(config, "channels.slack.bot_token", token); err != nil {
					return nil, fmt.Errorf("config generation: HANDLE slack: %w", err)
				}
			}
			if appToken := shared.ResolveEnvTokenFromMap(rc.Environment, "SLACK_APP_TOKEN"); appToken != "" {
				if err := setPath(config, "channels.slack.app_token", appToken); err != nil {
					return nil, fmt.Errorf("config generation: HANDLE slack: %w", err)
				}
			}
			if signingSecret := shared.ResolveEnvTokenFromMap(rc.Environment, "SLACK_SIGNING_SECRET"); signingSecret != "" {
				if err := setPath(config, "channels.slack.signing_secret", signingSecret); err != nil {
					return nil, fmt.Errorf("config generation: HANDLE slack: %w", err)
				}
			}
		default:
			fmt.Printf("[claw] warning: nanobot driver has no config mapping for HANDLE platform %q; skipping channel enablement\n", platform)
		}
	}

	// Apply CONFIGURE directives last so operator settings override defaults.
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
	if len(parts) < 5 || parts[0] != "nanobot" || parts[1] != "config" || parts[2] != "set" {
		return "", nil, fmt.Errorf("unrecognized CONFIGURE command: %q (expected 'nanobot config set <path> <value>')", cmd)
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

func primaryModelRef(models map[string]string) (string, error) {
	if models == nil {
		return "", fmt.Errorf("nanobot driver: missing MODEL primary (set `MODEL primary <provider/model>` in Clawfile)")
	}
	if primary := strings.TrimSpace(models["primary"]); primary != "" {
		return primary, nil
	}
	return "", fmt.Errorf("nanobot driver: missing MODEL primary (set `MODEL primary <provider/model>` in Clawfile)")
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
