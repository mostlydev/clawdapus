package picoclaw

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/mostlydev/clawdapus/internal/cllama"
	"github.com/mostlydev/clawdapus/internal/driver"
	"github.com/mostlydev/clawdapus/internal/driver/shared"
)

const (
	picoclawHomeDir      = "/home/picoclaw/.picoclaw"
	picoclawWorkspaceDir = "/home/picoclaw/.picoclaw/workspace"
	picoclawGatewayHost  = "localhost"
	picoclawGatewayPort  = 18790
)

var supportedPlatforms = []string{
	"discord",
	"telegram",
	"slack",
	"whatsapp",
	"feishu",
	"line",
	"qq",
	"dingtalk",
	"onebot",
	"wecom",
	"wecom_app",
	"pico",
	"maixcam",
}

var supportedPlatformSet = map[string]struct{}{
	"discord":   {},
	"telegram":  {},
	"slack":     {},
	"whatsapp":  {},
	"feishu":    {},
	"line":      {},
	"qq":        {},
	"dingtalk":  {},
	"onebot":    {},
	"wecom":     {},
	"wecom_app": {},
	"pico":      {},
	"maixcam":   {},
}

// GenerateConfig builds a picoclaw JSON config from resolved Claw directives.
func GenerateConfig(rc *driver.ResolvedClaw) ([]byte, error) {
	if _, err := shared.PrimaryModelRef(rc.Models); err != nil {
		return nil, fmt.Errorf("picoclaw driver: %w", err)
	}

	config := make(map[string]interface{})

	if err := shared.SetPath(config, "agents.defaults.model_name", "primary"); err != nil {
		return nil, fmt.Errorf("config generation: %w", err)
	}
	if err := shared.SetPath(config, "agents.defaults.workspace", picoclawWorkspaceDir); err != nil {
		return nil, fmt.Errorf("config generation: %w", err)
	}
	if err := shared.SetPath(config, "gateway.host", picoclawGatewayHost); err != nil {
		return nil, fmt.Errorf("config generation: %w", err)
	}
	if err := shared.SetPath(config, "gateway.port", picoclawGatewayPort); err != nil {
		return nil, fmt.Errorf("config generation: %w", err)
	}

	modelList, err := buildModelList(rc)
	if err != nil {
		return nil, fmt.Errorf("config generation: %w", err)
	}
	if err := shared.SetPath(config, "model_list", modelList); err != nil {
		return nil, fmt.Errorf("config generation: %w", err)
	}

	normalizedHandles := make(map[string]*driver.HandleInfo, len(rc.Handles))
	for platform, h := range rc.Handles {
		p := normalizePlatform(platform)
		if p == "" {
			continue
		}
		if _, exists := normalizedHandles[p]; !exists {
			normalizedHandles[p] = h
		}
	}

	platforms := make([]string, 0, len(normalizedHandles))
	for platform := range normalizedHandles {
		platforms = append(platforms, platform)
	}
	sort.Strings(platforms)

	for _, platform := range platforms {
		if !isSupportedPlatform(platform) {
			fmt.Printf("[claw] warning: picoclaw driver has no config mapping for HANDLE platform %q; skipping channel enablement\n", platform)
			continue
		}

		channel := map[string]interface{}{
			"enabled": true,
			"output":  map[string]interface{}{"mode": "tool"},
		}

		tokenVar := shared.PlatformTokenVar(platform)
		if tokenVar != "" {
			token, err := shared.ResolveEnvTokenFromMapWithRuntimeEnv(rc.Environment, tokenVar, rc.RuntimeEnv)
			if err != nil {
				return nil, fmt.Errorf("config generation: HANDLE %s: %s: %w", platform, tokenVar, err)
			}
			if token != "" {
				channel["token"] = token
				channel["bot_token"] = token
			}
		}

		switch platform {
		case "discord":
			channel["mention_only"] = true
			if h := normalizedHandles[platform]; h != nil {
				for _, g := range h.Guilds {
					gid := strings.TrimSpace(g.ID)
					if gid == "" {
						continue
					}
					channel["guild_id"] = gid
					break
				}
			}
		case "slack":
			appToken, err := shared.ResolveEnvTokenFromMapWithRuntimeEnv(rc.Environment, "SLACK_APP_TOKEN", rc.RuntimeEnv)
			if err != nil {
				return nil, fmt.Errorf("config generation: HANDLE slack: SLACK_APP_TOKEN: %w", err)
			}
			if appToken != "" {
				channel["app_token"] = appToken
			}
			signingSecret, err := shared.ResolveEnvTokenFromMapWithRuntimeEnv(rc.Environment, "SLACK_SIGNING_SECRET", rc.RuntimeEnv)
			if err != nil {
				return nil, fmt.Errorf("config generation: HANDLE slack: SLACK_SIGNING_SECRET: %w", err)
			}
			if signingSecret != "" {
				channel["signing_secret"] = signingSecret
			}
		}

		if err := shared.SetPath(config, "channels."+platform, channel); err != nil {
			return nil, fmt.Errorf("config generation: HANDLE %s: %w", platform, err)
		}
	}

	// Apply CONFIGURE directives last so operator settings override defaults.
	for _, cmd := range rc.Configures {
		path, value, err := shared.ParseConfigSetCommand(cmd, "picoclaw")
		if err != nil {
			return nil, fmt.Errorf("config generation: %w", err)
		}
		if err := shared.SetPath(config, path, value); err != nil {
			return nil, fmt.Errorf("config generation: %w", err)
		}
	}

	return json.MarshalIndent(config, "", "  ")
}

func buildModelList(rc *driver.ResolvedClaw) ([]map[string]interface{}, error) {
	slots := sortedModelSlots(rc.Models)
	entries := make([]map[string]interface{}, 0, len(slots))

	if len(rc.Cllama) > 0 && strings.TrimSpace(rc.CllamaToken) == "" {
		return nil, fmt.Errorf("CLLAMA is enabled but token is empty")
	}

	firstProxy := ""
	if len(rc.Cllama) > 0 {
		firstProxy = cllama.ProxyBaseURL(rc.Cllama[0])
	}

	for _, slot := range slots {
		ref := strings.TrimSpace(rc.Models[slot])
		if ref == "" {
			continue
		}

		provider, modelID, ok := shared.SplitModelRef(ref)
		if !ok {
			return nil, fmt.Errorf("invalid MODEL %s %q (expected provider/model)", slot, ref)
		}

		entry := map[string]interface{}{
			"model_name": slot,
			"model":      provider + "/" + modelID,
		}

		if len(rc.Cllama) > 0 {
			entry["model"] = "openai/" + ref
			entry["api_base"] = firstProxy
			entry["api_key"] = rc.CllamaToken
		} else {
			llmProvider := shared.NormalizeProvider(provider)
			apiKey, err := shared.ResolveProviderAPIKeyWithRuntimeEnv(llmProvider, rc.Environment, rc.RuntimeEnv)
			if err != nil {
				return nil, fmt.Errorf("MODEL %s provider %q api_key: %w", slot, llmProvider, err)
			}
			if apiKey != "" {
				entry["api_key"] = apiKey
			}
		}

		entries = append(entries, entry)
	}

	return entries, nil
}

func sortedModelSlots(models map[string]string) []string {
	out := make([]string, 0, len(models))

	if strings.TrimSpace(models["primary"]) != "" {
		out = append(out, "primary")
	}

	// The fallback family orders numerically (fallback, fallback-2, ...,
	// fallback-10) so model_list entries preserve declared chain order.
	family := make([]string, 0, len(models))
	others := make([]string, 0, len(models))
	for slot, ref := range models {
		if slot == "primary" || strings.TrimSpace(ref) == "" {
			continue
		}
		if cllama.FallbackSlotOrdinal(slot) > 0 {
			family = append(family, slot)
			continue
		}
		others = append(others, slot)
	}
	sort.Slice(family, func(i, j int) bool {
		return cllama.FallbackSlotOrdinal(family[i]) < cllama.FallbackSlotOrdinal(family[j])
	})
	sort.Strings(others)

	out = append(out, family...)
	return append(out, others...)
}

func normalizePlatform(platform string) string {
	return strings.ToLower(strings.TrimSpace(platform))
}

func isSupportedPlatform(platform string) bool {
	_, ok := supportedPlatformSet[normalizePlatform(platform)]
	return ok
}
