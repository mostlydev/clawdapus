package pod

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/mostlydev/clawdapus/internal/driver"
)

// ParseSurface parses a raw surface string like "volume://research-cache read-only"
// into a ResolvedSurface.
func ParseSurface(raw string) (driver.ResolvedSurface, error) {
	parts := strings.Fields(raw)
	if len(parts) == 0 {
		return driver.ResolvedSurface{}, fmt.Errorf("empty surface declaration")
	}

	parsed, err := url.Parse(parts[0])
	if err != nil {
		return driver.ResolvedSurface{}, fmt.Errorf("invalid surface URI %q: %w", parts[0], err)
	}

	scheme := parsed.Scheme
	target := parsed.Host
	switch {
	case parsed.Host != "" && parsed.Path != "":
		target = parsed.Host + parsed.Path
	case parsed.Host == "" && parsed.Path != "":
		target = parsed.Path
	case target == "":
		target = parsed.Opaque
	}

	if scheme == "" || target == "" {
		return driver.ResolvedSurface{}, fmt.Errorf("surface URI %q must have scheme and target", parts[0])
	}

	accessMode := ""
	if len(parts) > 1 {
		accessMode = strings.Join(parts[1:], " ")
	}

	return driver.ResolvedSurface{
		Scheme:     scheme,
		Target:     target,
		AccessMode: accessMode,
	}, nil
}

// parseChannelSurfaceMap parses a map-form channel surface entry.
// The map must have exactly one key: a channel:// URI.
// Only channel:// scheme is supported in map form.
func parseChannelSurfaceMap(m map[string]interface{}) (driver.ResolvedSurface, error) {
	if len(m) != 1 {
		return driver.ResolvedSurface{}, fmt.Errorf("map-form surface must have exactly one key (the URI), got %d", len(m))
	}
	for rawKey, rawVal := range m {
		s, err := ParseSurface(rawKey)
		if err != nil {
			return driver.ResolvedSurface{}, fmt.Errorf("invalid surface URI %q: %w", rawKey, err)
		}
		if s.Scheme != "channel" {
			return driver.ResolvedSurface{}, fmt.Errorf("map-form surface only supported for channel:// scheme, got %q — use string form for %s", s.Scheme, rawKey)
		}
		config, err := parseChannelConfig(rawVal)
		if err != nil {
			return driver.ResolvedSurface{}, fmt.Errorf("surface %q: %w", rawKey, err)
		}
		s.ChannelConfig = config
		return s, nil
	}
	return driver.ResolvedSurface{}, fmt.Errorf("empty surface map")
}

// parseChannelConfig converts a raw YAML map into a ChannelConfig.
// Returns nil (no error) if the raw value is nil — meaning just enable the channel.
func parseChannelConfig(raw interface{}) (*driver.ChannelConfig, error) {
	if raw == nil {
		return nil, nil
	}
	m, ok := raw.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("channel config must be a map, got %T", raw)
	}
	config := &driver.ChannelConfig{}

	if guildsRaw, ok := m["guilds"]; ok {
		guildsMap, ok := guildsRaw.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("channel config guilds must be a map, got %T", guildsRaw)
		}
		config.Guilds = make(map[string]driver.ChannelGuildConfig, len(guildsMap))
		for guildID, guildRaw := range guildsMap {
			gc, err := parseChannelGuildConfig(guildRaw)
			if err != nil {
				return nil, fmt.Errorf("guild %q: %w", guildID, err)
			}
			config.Guilds[guildID] = gc
		}
	}

	if dmRaw, ok := m["dm"]; ok {
		dm, err := parseChannelDMConfig(dmRaw)
		if err != nil {
			return nil, fmt.Errorf("dm config: %w", err)
		}
		config.DM = dm
	}

	return config, nil
}

func parseChannelGuildConfig(raw interface{}) (driver.ChannelGuildConfig, error) {
	m, ok := raw.(map[string]interface{})
	if !ok {
		return driver.ChannelGuildConfig{}, fmt.Errorf("guild config must be a map, got %T", raw)
	}
	gc := driver.ChannelGuildConfig{}
	if v, ok := m["policy"]; ok {
		s, ok := v.(string)
		if !ok {
			return gc, fmt.Errorf("guild policy must be a string")
		}
		gc.Policy = s
	}
	if v, ok := m["require_mention"]; ok {
		b, ok := v.(bool)
		if !ok {
			return gc, fmt.Errorf("guild require_mention must be a bool")
		}
		gc.RequireMention = b
	}
	if v, ok := m["users"]; ok {
		users, err := toStringSlice(v)
		if err != nil {
			return gc, fmt.Errorf("guild users: %w", err)
		}
		gc.Users = users
	}
	return gc, nil
}

func parseChannelDMConfig(raw interface{}) (driver.ChannelDMConfig, error) {
	m, ok := raw.(map[string]interface{})
	if !ok {
		return driver.ChannelDMConfig{}, fmt.Errorf("dm config must be a map, got %T", raw)
	}
	dm := driver.ChannelDMConfig{}
	if v, ok := m["enabled"]; ok {
		b, ok := v.(bool)
		if !ok {
			return dm, fmt.Errorf("dm.enabled must be a bool")
		}
		dm.Enabled = b
	}
	if v, ok := m["policy"]; ok {
		s, ok := v.(string)
		if !ok {
			return dm, fmt.Errorf("dm.policy must be a string")
		}
		dm.Policy = s
	}
	if v, ok := m["allow_from"]; ok {
		ids, err := toStringSlice(v)
		if err != nil {
			return dm, fmt.Errorf("dm.allow_from: %w", err)
		}
		dm.AllowFrom = ids
	}
	return dm, nil
}

// toStringSlice converts []interface{} (from YAML) to []string.
func toStringSlice(raw interface{}) ([]string, error) {
	slice, ok := raw.([]interface{})
	if !ok {
		return nil, fmt.Errorf("expected list, got %T", raw)
	}
	out := make([]string, 0, len(slice))
	for i, v := range slice {
		s, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("entry %d must be a string, got %T", i, v)
		}
		out = append(out, s)
	}
	return out, nil
}
