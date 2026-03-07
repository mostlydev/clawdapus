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
	if _, err := primaryModelRef(rc.Models); err != nil {
		return nil, err
	}

	config := make(map[string]interface{})

	if err := setPath(config, "agents.defaults.model_name", "primary"); err != nil {
		return nil, fmt.Errorf("config generation: %w", err)
	}
	if err := setPath(config, "agents.defaults.workspace", picoclawWorkspaceDir); err != nil {
		return nil, fmt.Errorf("config generation: %w", err)
	}

	modelList, err := buildModelList(rc)
	if err != nil {
		return nil, fmt.Errorf("config generation: %w", err)
	}
	if err := setPath(config, "model_list", modelList); err != nil {
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
		}

		tokenVar := shared.PlatformTokenVar(platform)
		if tokenVar != "" {
			if token := shared.ResolveEnvTokenFromMap(rc.Environment, tokenVar); token != "" {
				channel["token"] = token
				channel["bot_token"] = token
			}
		}

		switch platform {
		case "discord":
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
			if appToken := shared.ResolveEnvTokenFromMap(rc.Environment, "SLACK_APP_TOKEN"); appToken != "" {
				channel["app_token"] = appToken
			}
			if signingSecret := shared.ResolveEnvTokenFromMap(rc.Environment, "SLACK_SIGNING_SECRET"); signingSecret != "" {
				channel["signing_secret"] = signingSecret
			}
		}

		if err := setPath(config, "channels."+platform, channel); err != nil {
			return nil, fmt.Errorf("config generation: HANDLE %s: %w", platform, err)
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
			"model":   provider + "/" + modelID,
		}

		if len(rc.Cllama) > 0 {
			entry["model"] = "openai/" + ref
			entry["api_base"] = firstProxy
			entry["api_key"] = rc.CllamaToken
		} else {
			llmProvider := shared.NormalizeProvider(provider)
			if apiKey := shared.ResolveProviderAPIKey(llmProvider, rc.Environment); apiKey != "" {
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

	others := make([]string, 0, len(models))
	for slot, ref := range models {
		if slot == "primary" || strings.TrimSpace(ref) == "" {
			continue
		}
		others = append(others, slot)
	}
	sort.Strings(others)

	return append(out, others...)
}

func parseConfigSetCommand(cmd string) (string, interface{}, error) {
	parts := strings.Fields(cmd)
	if len(parts) < 5 || parts[0] != "picoclaw" || parts[1] != "config" || parts[2] != "set" {
		return "", nil, fmt.Errorf("unrecognized CONFIGURE command: %q (expected 'picoclaw config set <path> <value>')", cmd)
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
		return "", fmt.Errorf("picoclaw driver: missing MODEL primary (set `MODEL primary <provider/model>` in Clawfile)")
	}
	if primary := strings.TrimSpace(models["primary"]); primary != "" {
		return primary, nil
	}
	return "", fmt.Errorf("picoclaw driver: missing MODEL primary (set `MODEL primary <provider/model>` in Clawfile)")
}

func normalizePlatform(platform string) string {
	return strings.ToLower(strings.TrimSpace(platform))
}

func isSupportedPlatform(platform string) bool {
	_, ok := supportedPlatformSet[normalizePlatform(platform)]
	return ok
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
