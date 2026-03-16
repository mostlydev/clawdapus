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
	return shared.ParseConfigSetCommand(cmd, "nanobot")
}

func primaryModelRef(models map[string]string) (string, error) {
	ref, err := shared.PrimaryModelRef(models)
	if err != nil {
		return "", fmt.Errorf("nanobot driver: %w", err)
	}
	return ref, nil
}

func setPath(obj map[string]interface{}, path string, value interface{}) error {
	return shared.SetPath(obj, path, value)
}
