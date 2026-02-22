package openclaw

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
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
			// allowBots: unconditional — peer agents must be able to mention each other.
			if err := setPath(config, "channels.discord.allowBots", true); err != nil {
				return nil, fmt.Errorf("config generation: HANDLE discord: %w", err)
			}

			// Collect all discord bot IDs: own + peers, sorted for determinism.
			allBotIDs := discordBotIDs(rc)

			// Derive mention patterns from own handle: text name + Discord native mention.
			var mentionPatterns []string
			if h != nil {
				username := h.Username
				if username == "" {
					username = rc.ServiceName
				}
				if username != "" {
					mentionPatterns = append(mentionPatterns, fmt.Sprintf(`(?i)\b@?%s\b`, regexp.QuoteMeta(username)))
				}
				if h.ID != "" {
					mentionPatterns = append(mentionPatterns, fmt.Sprintf(`<@!?%s>`, h.ID))
				}
			}

			// agents.list: single entry for this container's agent.
			agentName := rc.ServiceName
			if h != nil && h.Username != "" {
				agentName = strings.ToUpper(h.Username[:1]) + h.Username[1:]
			}
			agentEntry := map[string]interface{}{"id": "main", "name": agentName}
			if len(mentionPatterns) > 0 {
				agentEntry["groupChat"] = map[string]interface{}{
					"mentionPatterns": stringsToIface(mentionPatterns),
				}
			}
			if err := setPath(config, "agents.list", []interface{}{agentEntry}); err != nil {
				return nil, fmt.Errorf("config generation: HANDLE discord: agents.list: %w", err)
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
								"allow":          true,
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

// discordBotIDs collects all Discord bot IDs from own handle and peer handles,
// sorted for deterministic output.
func discordBotIDs(rc *driver.ResolvedClaw) []string {
	seen := make(map[string]struct{})
	if h := rc.Handles["discord"]; h != nil && h.ID != "" {
		seen[h.ID] = struct{}{}
	}
	for _, peerHandles := range rc.PeerHandles {
		if ph, ok := peerHandles["discord"]; ok && ph != nil && ph.ID != "" {
			seen[ph.ID] = struct{}{}
		}
	}
	ids := make([]string, 0, len(seen))
	for id := range seen {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
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
	if cc.DM.Policy != "" {
		if err := setPath(config, "channels.discord.dmPolicy", cc.DM.Policy); err != nil {
			return err
		}
	}
	if len(cc.DM.AllowFrom) > 0 {
		if err := setPath(config, "channels.discord.allowFrom", stringsToIface(cc.DM.AllowFrom)); err != nil {
			return err
		}
	}
	for guildID, guildCfg := range cc.Guilds {
		base := fmt.Sprintf("channels.discord.guilds.%s", guildID)
		if guildCfg.Policy != "" {
			if err := setPath(config, base+".policy", guildCfg.Policy); err != nil {
				return err
			}
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
