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

	modelRef, err := shared.PrimaryModelRef(rc.Models)
	if err != nil {
		return nil, fmt.Errorf("nanobot driver: %w", err)
	}

	if err := shared.SetPath(config, "agents.defaults.model", modelRef); err != nil {
		return nil, fmt.Errorf("config generation: %w", err)
	}
	if err := shared.SetPath(config, "agents.defaults.workspace", "/root/.nanobot/workspace"); err != nil {
		return nil, fmt.Errorf("config generation: %w", err)
	}

	if len(rc.Cllama) > 0 {
		if strings.TrimSpace(rc.CllamaToken) == "" {
			return nil, fmt.Errorf("config generation: CLLAMA is enabled but token is empty")
		}
		firstProxy := cllama.ProxyBaseURL(rc.Cllama[0])
		for _, provider := range shared.CollectProviders(rc.Models) {
			base := "providers." + provider
			if err := shared.SetPath(config, base+".base_url", firstProxy); err != nil {
				return nil, fmt.Errorf("config generation: cllama provider %q base_url: %w", provider, err)
			}
			if err := shared.SetPath(config, base+".api_key", rc.CllamaToken); err != nil {
				return nil, fmt.Errorf("config generation: cllama provider %q api_key: %w", provider, err)
			}
		}
	} else {
		for _, provider := range shared.CollectProviders(rc.Models) {
			if token := shared.ResolveProviderAPIKey(provider, rc.Environment); token != "" {
				if err := shared.SetPath(config, "providers."+provider+".api_key", token); err != nil {
					return nil, fmt.Errorf("config generation: provider %q api_key: %w", provider, err)
				}
			}
		}
	}

	for platform, h := range rc.Handles {
		switch strings.ToLower(platform) {
		case "discord":
			if err := shared.SetPath(config, "channels.discord.enabled", true); err != nil {
				return nil, fmt.Errorf("config generation: HANDLE discord: %w", err)
			}
			if token := shared.ResolveEnvTokenFromMap(rc.Environment, "DISCORD_BOT_TOKEN"); token != "" {
				if err := shared.SetPath(config, "channels.discord.token", token); err != nil {
					return nil, fmt.Errorf("config generation: HANDLE discord: %w", err)
				}
			}
			if h != nil {
				for _, g := range h.Guilds {
					gid := strings.TrimSpace(g.ID)
					if gid == "" {
						continue
					}
					if err := shared.SetPath(config, "channels.discord.guild_id", gid); err != nil {
						return nil, fmt.Errorf("config generation: HANDLE discord: %w", err)
					}
					break
				}
			}
		case "telegram":
			if err := shared.SetPath(config, "channels.telegram.enabled", true); err != nil {
				return nil, fmt.Errorf("config generation: HANDLE telegram: %w", err)
			}
			if token := shared.ResolveEnvTokenFromMap(rc.Environment, "TELEGRAM_BOT_TOKEN"); token != "" {
				if err := shared.SetPath(config, "channels.telegram.bot_token", token); err != nil {
					return nil, fmt.Errorf("config generation: HANDLE telegram: %w", err)
				}
			}
		case "slack":
			if err := shared.SetPath(config, "channels.slack.enabled", true); err != nil {
				return nil, fmt.Errorf("config generation: HANDLE slack: %w", err)
			}
			if token := shared.ResolveEnvTokenFromMap(rc.Environment, "SLACK_BOT_TOKEN"); token != "" {
				if err := shared.SetPath(config, "channels.slack.bot_token", token); err != nil {
					return nil, fmt.Errorf("config generation: HANDLE slack: %w", err)
				}
			}
			if appToken := shared.ResolveEnvTokenFromMap(rc.Environment, "SLACK_APP_TOKEN"); appToken != "" {
				if err := shared.SetPath(config, "channels.slack.app_token", appToken); err != nil {
					return nil, fmt.Errorf("config generation: HANDLE slack: %w", err)
				}
			}
			if signingSecret := shared.ResolveEnvTokenFromMap(rc.Environment, "SLACK_SIGNING_SECRET"); signingSecret != "" {
				if err := shared.SetPath(config, "channels.slack.signing_secret", signingSecret); err != nil {
					return nil, fmt.Errorf("config generation: HANDLE slack: %w", err)
				}
			}
		default:
			fmt.Printf("[claw] warning: nanobot driver has no config mapping for HANDLE platform %q; skipping channel enablement\n", platform)
		}
	}

	// Apply CONFIGURE directives last so operator settings override defaults.
	for _, cmd := range rc.Configures {
		path, value, err := shared.ParseConfigSetCommand(cmd, "nanobot")
		if err != nil {
			return nil, fmt.Errorf("config generation: %w", err)
		}
		if err := shared.SetPath(config, path, value); err != nil {
			return nil, fmt.Errorf("config generation: %w", err)
		}
	}

	return json.MarshalIndent(config, "", "  ")
}
