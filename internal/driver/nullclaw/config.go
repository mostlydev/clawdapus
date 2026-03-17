package nullclaw

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mostlydev/clawdapus/internal/driver"
	"github.com/mostlydev/clawdapus/internal/driver/shared"
)

// GenerateConfig builds a nullclaw JSON config from resolved Claw directives.
// Output is deterministic because map keys are sorted by encoding/json.
func GenerateConfig(rc *driver.ResolvedClaw) ([]byte, error) {
	config := make(map[string]interface{})

	// Conservative gateway defaults: keep local bind + pairing requirement.
	if err := shared.SetPath(config, "gateway.port", 3000); err != nil {
		return nil, fmt.Errorf("config generation: %w", err)
	}
	if err := shared.SetPath(config, "gateway.host", "127.0.0.1"); err != nil {
		return nil, fmt.Errorf("config generation: %w", err)
	}
	if err := shared.SetPath(config, "gateway.require_pairing", true); err != nil {
		return nil, fmt.Errorf("config generation: %w", err)
	}

	// Safety defaults.
	if err := shared.SetPath(config, "autonomy.level", "supervised"); err != nil {
		return nil, fmt.Errorf("config generation: %w", err)
	}
	if err := shared.SetPath(config, "autonomy.workspace_only", true); err != nil {
		return nil, fmt.Errorf("config generation: %w", err)
	}

	for slot, model := range rc.Models {
		if slot == "fallback" {
			if err := shared.SetPath(config, "reliability.fallback_providers", []string{model}); err != nil {
				return nil, fmt.Errorf("config generation: %w", err)
			}
			continue
		}
		if err := shared.SetPath(config, "agents.defaults.model."+slot, model); err != nil {
			return nil, fmt.Errorf("config generation: %w", err)
		}
	}

	if len(rc.Cllama) > 0 {
		if strings.TrimSpace(rc.CllamaToken) == "" {
			return nil, fmt.Errorf("config generation: CLLAMA is enabled but token is empty")
		}
		firstProxy := fmt.Sprintf("http://cllama-%s:8080/v1", rc.Cllama[0])
		for _, provider := range shared.CollectProviders(rc.Models) {
			base := "models.providers." + provider
			if err := shared.SetPath(config, base+".base_url", firstProxy); err != nil {
				return nil, fmt.Errorf("config generation: cllama provider %q base_url: %w", provider, err)
			}
			if err := shared.SetPath(config, base+".api_key", rc.CllamaToken); err != nil {
				return nil, fmt.Errorf("config generation: cllama provider %q api_key: %w", provider, err)
			}
		}
	}

	// HANDLE defaults first. CONFIGURE runs last and overrides these values.
	for platform, h := range rc.Handles {
		switch strings.ToLower(platform) {
		case "discord":
			if token := shared.ResolveEnvTokenFromMap(rc.Environment, "DISCORD_BOT_TOKEN"); token != "" {
				if err := shared.SetPath(config, "channels.discord.accounts.main.token", token); err != nil {
					return nil, fmt.Errorf("config generation: HANDLE discord: %w", err)
				}
			}
			if h != nil {
				for _, g := range h.Guilds {
					gid := strings.TrimSpace(g.ID)
					if gid == "" {
						continue
					}
					if err := shared.SetPath(config, "channels.discord.accounts.main.guild_id", gid); err != nil {
						return nil, fmt.Errorf("config generation: HANDLE discord: %w", err)
					}
					break
				}
			}
		case "telegram":
			if token := shared.ResolveEnvTokenFromMap(rc.Environment, "TELEGRAM_BOT_TOKEN"); token != "" {
				if err := shared.SetPath(config, "channels.telegram.accounts.main.bot_token", token); err != nil {
					return nil, fmt.Errorf("config generation: HANDLE telegram: %w", err)
				}
			}
		case "slack":
			if token := shared.ResolveEnvTokenFromMap(rc.Environment, "SLACK_BOT_TOKEN"); token != "" {
				if err := shared.SetPath(config, "channels.slack.accounts.main.bot_token", token); err != nil {
					return nil, fmt.Errorf("config generation: HANDLE slack: %w", err)
				}
			}
			appToken := shared.ResolveEnvTokenFromMap(rc.Environment, "SLACK_APP_TOKEN")
			if appToken != "" {
				if err := shared.SetPath(config, "channels.slack.accounts.main.app_token", appToken); err != nil {
					return nil, fmt.Errorf("config generation: HANDLE slack: %w", err)
				}
				if err := shared.SetPath(config, "channels.slack.accounts.main.mode", "socket"); err != nil {
					return nil, fmt.Errorf("config generation: HANDLE slack: %w", err)
				}
			}
			signingSecret := shared.ResolveEnvTokenFromMap(rc.Environment, "SLACK_SIGNING_SECRET")
			if signingSecret != "" {
				if err := shared.SetPath(config, "channels.slack.accounts.main.signing_secret", signingSecret); err != nil {
					return nil, fmt.Errorf("config generation: HANDLE slack: %w", err)
				}
				if appToken == "" {
					if err := shared.SetPath(config, "channels.slack.accounts.main.mode", "http"); err != nil {
						return nil, fmt.Errorf("config generation: HANDLE slack: %w", err)
					}
				}
			}
		default:
			fmt.Printf("[claw] warning: nullclaw driver has no config mapping for HANDLE platform %q; skipping channel enablement\n", platform)
		}
	}

	for _, cmd := range rc.Configures {
		path, value, err := shared.ParseConfigSetCommand(cmd, "nullclaw")
		if err != nil {
			return nil, fmt.Errorf("config generation: %w", err)
		}
		if err := shared.SetPath(config, path, value); err != nil {
			return nil, fmt.Errorf("config generation: %w", err)
		}
	}

	return json.MarshalIndent(config, "", "  ")
}
