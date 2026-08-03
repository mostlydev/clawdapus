package openclaw

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/mostlydev/clawdapus/internal/cllama"
	"github.com/mostlydev/clawdapus/internal/driver"
	"github.com/mostlydev/clawdapus/internal/driver/shared"
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
	// (/claw/skills/). CLAWDAPUS.md is mounted separately and inlined into the effective
	// AGENTS contract by the driver, so no extra bootstrap hook is required.
	if err := setPath(config, "agents.defaults.workspace", "/claw"); err != nil {
		return nil, fmt.Errorf("config generation: %w", err)
	}

	// Apply MODEL directives. openclaw uses "fallbacks" ([]string), not
	// "fallback" (string); the whole fallback family (fallback, fallback-2,
	// ...) projects into that one array in chain order.
	if chain := cllama.FallbackChain(rc.Models); len(chain) > 0 {
		if err := setPath(config, "agents.defaults.model.fallbacks", chain); err != nil {
			return nil, fmt.Errorf("config generation: %w", err)
		}
	}
	for slot, model := range rc.Models {
		if cllama.FallbackSlotOrdinal(slot) > 0 {
			continue
		}
		if err := setPath(config, "agents.defaults.model."+slot, model); err != nil {
			return nil, fmt.Errorf("config generation: %w", err)
		}
	}

	if len(rc.Cllama) > 0 {
		firstProxy := cllama.ProxyBaseURL(rc.Cllama[0])
		providerModels, err := cllama.CollectProviderModels(rc.Models)
		if err != nil {
			return nil, fmt.Errorf("config generation: %w", err)
		}
		for provider, modelIDs := range providerModels {
			basePath := "models.providers." + provider
			if err := setPath(config, basePath+".baseUrl", firstProxy); err != nil {
				return nil, fmt.Errorf("config generation: cllama provider %q baseUrl: %w", provider, err)
			}
			if rc.CllamaToken != "" {
				if err := setPath(config, basePath+".apiKey", rc.CllamaToken); err != nil {
					return nil, fmt.Errorf("config generation: cllama provider %q apiKey: %w", provider, err)
				}
			}
			api, err := openclawModelAPIForIngress(cllama.IngressSurfaceForProvider(provider))
			if err != nil {
				return nil, fmt.Errorf("config generation: cllama provider %q api: %w", provider, err)
			}
			if err := setPath(config, basePath+".api", api); err != nil {
				return nil, fmt.Errorf("config generation: cllama provider %q api: %w", provider, err)
			}
			modelDefs := make([]interface{}, 0, len(modelIDs))
			for _, modelID := range modelIDs {
				modelDefs = append(modelDefs, map[string]interface{}{
					"id":   modelID,
					"name": modelID,
				})
			}
			if err := setPath(config, basePath+".models", modelDefs); err != nil {
				return nil, fmt.Errorf("config generation: cllama provider %q models: %w", provider, err)
			}
		}
	}

	// Apply HANDLE directives first: they provide structural defaults per platform.
	// CONFIGURE runs after so operator overrides always take precedence.
	var allMentionPatterns []string
	agentName := rc.ServiceName
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
			// Use pairing mode by default so Discord DM behavior is valid without
			// requiring an explicit allowFrom wildcard.
			if err := setPath(config, "channels.discord.dmPolicy", "pairing"); err != nil {
				return nil, fmt.Errorf("config generation: HANDLE discord: %w", err)
			}
			// allowBots: unconditional — peer agents must be able to mention each other.
			if err := setPath(config, "channels.discord.allowBots", true); err != nil {
				return nil, fmt.Errorf("config generation: HANDLE discord: %w", err)
			}

			// Collect all discord bot IDs: own + peers, sorted for determinism.
			allBotIDs := discordBotIDs(rc)

			// Collect mention patterns into the shared slice (agents.list written after loop).
			if h != nil {
				if h.ID != "" {
					allMentionPatterns = append(allMentionPatterns, fmt.Sprintf(`<@!?%s>`, h.ID))
				}
				if h.Username != "" {
					agentName = strings.ToUpper(h.Username[:1]) + h.Username[1:]
				}
			}

			// Guild entries: requireMention + users allowlist + per-channel allow entries.
			if h != nil && len(h.Guilds) > 0 {
				guilds := make(map[string]interface{})
				for _, g := range h.Guilds {
					guildEntry := map[string]interface{}{"requireMention": true}
					if len(allBotIDs) > 0 {
						guildEntry["users"] = stringsToIface(allBotIDs)
					}
					if len(g.Channels) > 0 {
						channels := make(map[string]interface{})
						for _, ch := range g.Channels {
							channels[ch.ID] = map[string]interface{}{
								"enabled":        true,
								"requireMention": true,
							}
						}
						guildEntry["channels"] = channels
					}
					guilds[g.ID] = guildEntry
				}
				if err := setPath(config, "channels.discord.guilds", guilds); err != nil {
					return nil, fmt.Errorf("config generation: HANDLE discord: %w", err)
				}
			}

			// Pre-enable the discord plugin so the gateway's auto-doctor finds nothing to add.
			// Without this, gateway startup overwrites our config (changedPaths=1) to add this entry.
			if err := setPath(config, "plugins.entries.discord.enabled", true); err != nil {
				return nil, fmt.Errorf("config generation: HANDLE discord: %w", err)
			}
		case "telegram":
			h := rc.Handles[platform]
			if err := setPath(config, "channels.telegram.enabled", true); err != nil {
				return nil, fmt.Errorf("config generation: HANDLE telegram: %w", err)
			}
			if err := setPath(config, "channels.telegram.token", "${TELEGRAM_BOT_TOKEN}"); err != nil {
				return nil, fmt.Errorf("config generation: HANDLE telegram: %w", err)
			}

			// Collect mention patterns into the shared slice (agents.list written after loop by Task 2.5)
			if h != nil {
				username := h.Username
				if username == "" {
					username = rc.ServiceName
				}
				if username != "" {
					allMentionPatterns = append(allMentionPatterns, fmt.Sprintf(`\b@?%s\b`, regexp.QuoteMeta(username)))
				}
				if h.Username != "" {
					agentName = strings.ToUpper(h.Username[:1]) + h.Username[1:]
				}
			}

			if err := setPath(config, "plugins.entries.telegram.enabled", true); err != nil {
				return nil, fmt.Errorf("config generation: HANDLE telegram: %w", err)
			}
		case "slack":
			h := rc.Handles[platform]
			if err := setPath(config, "channels.slack.enabled", true); err != nil {
				return nil, fmt.Errorf("config generation: HANDLE slack: %w", err)
			}
			if err := setPath(config, "channels.slack.token", "${SLACK_BOT_TOKEN}"); err != nil {
				return nil, fmt.Errorf("config generation: HANDLE slack: %w", err)
			}

			// Collect mention patterns into the shared slice (agents.list written after loop)
			if h != nil {
				username := h.Username
				if username == "" {
					username = rc.ServiceName
				}
				if username != "" {
					allMentionPatterns = append(allMentionPatterns, fmt.Sprintf(`\b@?%s\b`, regexp.QuoteMeta(username)))
				}
				if h.ID != "" {
					allMentionPatterns = append(allMentionPatterns, fmt.Sprintf(`<@%s>`, h.ID))
				}
				if h.Username != "" {
					agentName = strings.ToUpper(h.Username[:1]) + h.Username[1:]
				}
			}

			if err := setPath(config, "plugins.entries.slack.enabled", true); err != nil {
				return nil, fmt.Errorf("config generation: HANDLE slack: %w", err)
			}
		default:
			// Unknown platform — no native config path known; log and skip.
			// The env var broadcast still fires regardless.
			fmt.Printf("[claw] warning: openclaw driver has no config mapping for HANDLE platform %q; skipping channel enablement\n", platform)
		}
	}

	// Write agents.list and responseDelivery once after the platform loop.
	if len(rc.Handles) > 0 {
		// Deduplicate mention patterns
		seen := make(map[string]struct{})
		deduped := make([]string, 0, len(allMentionPatterns))
		for _, p := range allMentionPatterns {
			if _, ok := seen[p]; !ok {
				seen[p] = struct{}{}
				deduped = append(deduped, p)
			}
		}

		agentEntry := map[string]interface{}{"id": "main", "name": agentName}
		if len(deduped) > 0 {
			agentEntry["groupChat"] = map[string]interface{}{
				"mentionPatterns": stringsToIface(deduped),
			}
		}
		if err := setPath(config, "agents.list", []interface{}{agentEntry}); err != nil {
			return nil, fmt.Errorf("config generation: agents.list: %w", err)
		}
		// OpenClaw enforces tool-only communication natively when Discord handles are
		// configured — no extra config key is needed or accepted.
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

	// Apply SURFACE channel directives — refine routing config set by HANDLE.
	// SURFACE runs after HANDLE so it takes precedence where keys overlap.
	for _, surface := range rc.Surfaces {
		if surface.Scheme != "channel" || surface.ChannelConfig == nil {
			continue
		}
		switch surface.Target {
		case "discord":
			if err := applyDiscordChannelSurface(config, surface.ChannelConfig); err != nil {
				return nil, fmt.Errorf("config generation: SURFACE channel://discord: %w", err)
			}
			// Other platforms: silently skip (unsupported = no config, not an error here)
		}
	}

	return json.MarshalIndent(config, "", "  ")
}

// parseConfigSetCommand extracts dotted path and value from
// "openclaw config set <dotted.path> <value>".
func parseConfigSetCommand(cmd string) (string, interface{}, error) {
	return shared.ParseConfigSetCommand(cmd, "openclaw")
}

// platformBotIDs collects all bot IDs for a given platform from own handle and
// peer handles, sorted for deterministic output.
func platformBotIDs(rc *driver.ResolvedClaw, platform string) []string {
	seen := make(map[string]struct{})
	if h := rc.Handles[platform]; h != nil {
		if id := shared.ResolveEnvToken(h.ID); id != "" {
			seen[id] = struct{}{}
		}
	}
	for _, peerHandles := range rc.PeerHandles {
		if ph, ok := peerHandles[platform]; ok && ph != nil {
			if id := shared.ResolveEnvToken(ph.ID); id != "" {
				seen[id] = struct{}{}
			}
		}
	}
	ids := make([]string, 0, len(seen))
	for id := range seen {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// discordBotIDs collects all Discord bot IDs from own handle and peer handles,
// sorted for deterministic output.
func discordBotIDs(rc *driver.ResolvedClaw) []string {
	return platformBotIDs(rc, "discord")
}

// stringsToIface converts []string to []interface{} for JSON marshaling.
func stringsToIface(ss []string) []interface{} {
	out := make([]interface{}, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}

// applyDiscordChannelSurface applies ChannelConfig to the openclaw config map
// for the discord channel. Runs after HANDLE so it can refine/override routing.
func applyDiscordChannelSurface(config map[string]interface{}, cc *driver.ChannelConfig) error {
	dmPolicy := ""
	if cc.DM.Policy != "" {
		dmPolicy = normalizeDiscordDMPolicy(cc.DM.Policy)
		if err := setPath(config, "channels.discord.dmPolicy", dmPolicy); err != nil {
			return err
		}
	}

	allowFrom := append([]string(nil), cc.DM.AllowFrom...)
	if dmPolicy == "open" && !containsString(allowFrom, "*") {
		allowFrom = append(allowFrom, "*")
	}
	if len(allowFrom) > 0 {
		if err := setPath(config, "channels.discord.allowFrom", stringsToIface(allowFrom)); err != nil {
			return err
		}
	}
	for guildID, guildCfg := range cc.Guilds {
		base := fmt.Sprintf("channels.discord.guilds.%s", guildID)
		if guildCfg.Policy != "" {
			return fmt.Errorf("guild policy is not supported by the current OpenClaw runtime for guild %q; remove channel://discord guild policy until runtime support lands", guildID)
		}
		if guildCfg.RequireMention {
			if err := setPath(config, base+".requireMention", true); err != nil {
				return err
			}
		}
		if len(guildCfg.Users) > 0 {
			if err := setPath(config, base+".users", stringsToIface(guildCfg.Users)); err != nil {
				return err
			}
		}
	}
	return nil
}

func normalizeDiscordDMPolicy(policy string) string {
	value := strings.ToLower(strings.TrimSpace(policy))
	switch value {
	case "denylist":
		// Backward-compatible alias for the current openclaw "open" mode.
		return "open"
	case "pairing", "allowlist", "open", "disabled":
		return value
	default:
		return strings.TrimSpace(policy)
	}
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func openclawModelAPIForIngress(surface cllama.IngressSurface) (string, error) {
	switch surface {
	case cllama.IngressSurfaceAnthropicMessages:
		return "anthropic-messages", nil
	case cllama.IngressSurfaceOpenAIChatCompletions:
		return "openai-completions", nil
	default:
		return "", fmt.Errorf("unsupported cllama ingress surface %q", surface)
	}
}

// setPath sets a nested value in a map using a dotted path.
func setPath(obj map[string]interface{}, path string, value interface{}) error {
	return shared.SetPath(obj, path, value)
}

func getNestedPath(root map[string]interface{}, path ...string) (interface{}, bool) {
	var current interface{} = root
	for _, key := range path {
		next, ok := current.(map[string]interface{})
		if !ok {
			return nil, false
		}
		current, ok = next[key]
		if !ok {
			return nil, false
		}
	}
	return current, true
}

func firstRawEnvRef(env map[string]string, keys ...string) string {
	if env == nil {
		return ""
	}
	for _, key := range keys {
		if raw := strings.TrimSpace(env[key]); raw != "" {
			return raw
		}
	}
	return ""
}
