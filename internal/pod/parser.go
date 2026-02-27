package pod

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/mostlydev/clawdapus/internal/driver"
)

// rawPod is the YAML deserialization target.
type rawPod struct {
	XClaw    rawPodClaw            `yaml:"x-claw"`
	Services map[string]rawService `yaml:"services"`
}

type rawPodClaw struct {
	Pod    string `yaml:"pod"`
	Master string `yaml:"master"`
}

type rawService struct {
	Image       string            `yaml:"image"`
	XClaw       *rawClawBlock     `yaml:"x-claw"`
	Environment map[string]string `yaml:"environment"`
	Expose      []interface{}     `yaml:"expose"`
	Ports       []interface{}     `yaml:"ports"`
}

type rawInvokeEntry struct {
	Schedule string `yaml:"schedule"`
	Message  string `yaml:"message"`
	Name     string `yaml:"name"`
	To       string `yaml:"to"`
}

type rawClawBlock struct {
	Agent     string                 `yaml:"agent"`
	Persona   string                 `yaml:"persona"`
	Cllama    interface{}            `yaml:"cllama"`
	CllamaEnv map[string]string      `yaml:"cllama-env"`
	Count     int                    `yaml:"count"`
	Handles   map[string]interface{} `yaml:"handles"`
	Surfaces  []interface{}          `yaml:"surfaces"`
	Skills    []string               `yaml:"skills"`
	Invoke    []rawInvokeEntry       `yaml:"invoke"`
}

// Parse reads a claw-pod.yml from the given reader.
func Parse(r io.Reader) (*Pod, error) {
	var raw rawPod
	decoder := yaml.NewDecoder(r)
	if err := decoder.Decode(&raw); err != nil {
		return nil, fmt.Errorf("parse claw-pod.yml: %w", err)
	}

	pod := &Pod{
		Name:     raw.XClaw.Pod,
		Services: make(map[string]*Service, len(raw.Services)),
	}

	for name, svc := range raw.Services {
		expose, err := parseExpose(svc.Expose)
		if err != nil {
			return nil, fmt.Errorf("service %q: parse expose: %w", name, err)
		}
		if expose == nil {
			expose = make([]string, 0)
		}
		ports, err := parsePorts(svc.Ports)
		if err != nil {
			return nil, fmt.Errorf("service %q: parse ports: %w", name, err)
		}
		if ports == nil {
			ports = make([]string, 0)
		}
		service := &Service{
			Image:       svc.Image,
			Environment: svc.Environment,
			Expose:      expose,
			Ports:       ports,
		}
		if svc.XClaw != nil {
			count := svc.XClaw.Count
			if count < 1 {
				count = 1
			}
			cllama, err := parseStringOrList(svc.XClaw.Cllama)
			if err != nil {
				return nil, fmt.Errorf("service %q: parse cllama: %w", name, err)
			}
			parsedSurfaces := make([]driver.ResolvedSurface, 0, len(svc.XClaw.Surfaces))
			for _, rawSurf := range svc.XClaw.Surfaces {
				switch v := rawSurf.(type) {
				case string:
					s, err := ParseSurface(v)
					if err != nil {
						return nil, fmt.Errorf("service %q: surface %q: %w", name, v, err)
					}
					parsedSurfaces = append(parsedSurfaces, s)
				case map[string]interface{}:
					s, err := parseChannelSurfaceMap(v)
					if err != nil {
						return nil, fmt.Errorf("service %q: map-form surface: %w", name, err)
					}
					parsedSurfaces = append(parsedSurfaces, s)
				default:
					return nil, fmt.Errorf("service %q: unsupported surface entry type %T", name, rawSurf)
				}
			}
			skills := svc.XClaw.Skills
			if skills == nil {
				skills = make([]string, 0)
			}
			handles, err := parseHandles(svc.XClaw.Handles)
			if err != nil {
				return nil, fmt.Errorf("service %q: parse handles: %w", name, err)
			}
			invoke := make([]InvokeEntry, 0, len(svc.XClaw.Invoke))
			for _, rawInv := range svc.XClaw.Invoke {
				if rawInv.Schedule == "" || rawInv.Message == "" {
					return nil, fmt.Errorf("service %q: invoke entry missing required field (schedule or message)", name)
				}
				invoke = append(invoke, InvokeEntry{
					Schedule: rawInv.Schedule,
					Message:  rawInv.Message,
					Name:     rawInv.Name,
					To:       rawInv.To,
				})
			}
			service.Claw = &ClawBlock{
				Agent:     svc.XClaw.Agent,
				Persona:   svc.XClaw.Persona,
				Cllama:    cllama,
				CllamaEnv: svc.XClaw.CllamaEnv,
				Count:     count,
				Handles:   handles,
				Surfaces:  parsedSurfaces,
				Skills:    skills,
				Invoke:    invoke,
			}
		}
		pod.Services[name] = service
	}

	return pod, nil
}

func parseStringOrList(raw interface{}) ([]string, error) {
	if raw == nil {
		return nil, nil
	}

	switch v := raw.(type) {
	case string:
		if strings.TrimSpace(v) == "" {
			return nil, fmt.Errorf("string value must not be empty")
		}
		return []string{v}, nil
	case []string:
		out := make([]string, 0, len(v))
		for i, item := range v {
			if strings.TrimSpace(item) == "" {
				return nil, fmt.Errorf("list item %d must not be empty", i)
			}
			out = append(out, item)
		}
		return out, nil
	case []interface{}:
		out := make([]string, 0, len(v))
		for i, item := range v {
			s, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("list item %d must be a string, got %T", i, item)
			}
			if strings.TrimSpace(s) == "" {
				return nil, fmt.Errorf("list item %d must not be empty", i)
			}
			out = append(out, s)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("expected string or list, got %T", raw)
	}
}

// parseHandles converts a raw x-claw handles map into typed HandleInfo structs.
// Supports two forms per platform:
//   - String shorthand: discord: "123456789"  →  HandleInfo{ID: "123456789"}
//   - Map form:         discord: {id: "...", username: "...", guilds: [...]}
func parseHandles(raw map[string]interface{}) (map[string]*driver.HandleInfo, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	out := make(map[string]*driver.HandleInfo, len(raw))
	for platform, val := range raw {
		normalized := strings.ToLower(strings.TrimSpace(platform))
		if normalized == "" {
			return nil, fmt.Errorf("platform name must not be empty")
		}
		info, err := parseHandleEntry(normalized, val)
		if err != nil {
			return nil, fmt.Errorf("platform %q: %w", normalized, err)
		}
		out[normalized] = info
	}
	return out, nil
}

func parseHandleEntry(platform string, val interface{}) (*driver.HandleInfo, error) {
	switch v := val.(type) {
	case string:
		if v == "" {
			return nil, fmt.Errorf("handle ID must not be empty")
		}
		return &driver.HandleInfo{ID: v}, nil
	case int:
		return &driver.HandleInfo{ID: strconv.Itoa(v)}, nil
	case int64:
		return &driver.HandleInfo{ID: strconv.FormatInt(v, 10)}, nil
	case uint64:
		return &driver.HandleInfo{ID: strconv.FormatUint(v, 10)}, nil
	case map[string]interface{}:
		return parseHandleMap(v)
	default:
		return nil, fmt.Errorf("unsupported handle value type %T", val)
	}
}

func parseHandleMap(m map[string]interface{}) (*driver.HandleInfo, error) {
	info := &driver.HandleInfo{}

	if id, ok := m["id"]; ok {
		switch v := id.(type) {
		case string:
			info.ID = v
		case int:
			info.ID = strconv.Itoa(v)
		case int64:
			info.ID = strconv.FormatInt(v, 10)
		case uint64:
			info.ID = strconv.FormatUint(v, 10)
		default:
			return nil, fmt.Errorf("handle id must be a string")
		}
	}
	if info.ID == "" {
		return nil, fmt.Errorf("handle map must include a non-empty id")
	}

	if username, ok := m["username"]; ok {
		s, ok := username.(string)
		if !ok {
			return nil, fmt.Errorf("handle username must be a string")
		}
		info.Username = s
	}

	if guildsRaw, ok := m["guilds"]; ok {
		guildSlice, ok := guildsRaw.([]interface{})
		if !ok {
			return nil, fmt.Errorf("handle guilds must be a list")
		}
		guilds := make([]driver.GuildInfo, 0, len(guildSlice))
		for i, g := range guildSlice {
			guild, err := parseGuildEntry(g)
			if err != nil {
				return nil, fmt.Errorf("guild[%d]: %w", i, err)
			}
			guilds = append(guilds, guild)
		}
		info.Guilds = guilds
	}

	return info, nil
}

func parseGuildEntry(val interface{}) (driver.GuildInfo, error) {
	m, ok := val.(map[string]interface{})
	if !ok {
		return driver.GuildInfo{}, fmt.Errorf("guild entry must be a map with id:")
	}

	guild := driver.GuildInfo{}

	if id, ok := m["id"]; ok {
		switch v := id.(type) {
		case string:
			guild.ID = v
		case int:
			guild.ID = strconv.Itoa(v)
		case int64:
			guild.ID = strconv.FormatInt(v, 10)
		case uint64:
			guild.ID = strconv.FormatUint(v, 10)
		default:
			return driver.GuildInfo{}, fmt.Errorf("guild id must be a string")
		}
	}
	if guild.ID == "" {
		return driver.GuildInfo{}, fmt.Errorf("guild must have a non-empty id")
	}

	if name, ok := m["name"]; ok {
		s, ok := name.(string)
		if !ok {
			return driver.GuildInfo{}, fmt.Errorf("guild name must be a string")
		}
		guild.Name = s
	}

	if channelsRaw, ok := m["channels"]; ok {
		chanSlice, ok := channelsRaw.([]interface{})
		if !ok {
			return driver.GuildInfo{}, fmt.Errorf("guild channels must be a list")
		}
		channels := make([]driver.ChannelInfo, 0, len(chanSlice))
		for i, c := range chanSlice {
			ch, err := parseChannelEntry(c)
			if err != nil {
				return driver.GuildInfo{}, fmt.Errorf("channel[%d]: %w", i, err)
			}
			channels = append(channels, ch)
		}
		guild.Channels = channels
	}

	return guild, nil
}

func parseChannelEntry(val interface{}) (driver.ChannelInfo, error) {
	switch v := val.(type) {
	case string:
		return driver.ChannelInfo{ID: v}, nil
	case int:
		return driver.ChannelInfo{ID: strconv.Itoa(v)}, nil
	case int64:
		return driver.ChannelInfo{ID: strconv.FormatInt(v, 10)}, nil
	case uint64:
		return driver.ChannelInfo{ID: strconv.FormatUint(v, 10)}, nil
	case map[string]interface{}:
		ch := driver.ChannelInfo{}
		if id, ok := v["id"]; ok {
			switch sv := id.(type) {
			case string:
				ch.ID = sv
			case int:
				ch.ID = strconv.Itoa(sv)
			case int64:
				ch.ID = strconv.FormatInt(sv, 10)
			case uint64:
				ch.ID = strconv.FormatUint(sv, 10)
			default:
				return driver.ChannelInfo{}, fmt.Errorf("channel id must be a string")
			}
		}
		if ch.ID == "" {
			return driver.ChannelInfo{}, fmt.Errorf("channel must have a non-empty id")
		}
		if name, ok := v["name"]; ok {
			s, ok := name.(string)
			if !ok {
				return driver.ChannelInfo{}, fmt.Errorf("channel name must be a string")
			}
			ch.Name = s
		}
		return ch, nil
	default:
		return driver.ChannelInfo{}, fmt.Errorf("unsupported channel entry type %T", val)
	}
}

// parsePorts extracts the container-side port from compose ports: entries.
// Supports string form ("8080:80", "80", "127.0.0.1:8080:80/tcp"),
// integer form, and map form ({target: 80, published: 8080}).
// Only the container (target) port is returned — what other containers reach.
func parsePorts(raw []interface{}) ([]string, error) {
	if raw == nil {
		return nil, nil
	}
	out := make([]string, 0, len(raw))
	for i, entry := range raw {
		switch v := entry.(type) {
		case string:
			port := containerPortFromString(v)
			if port != "" {
				out = append(out, port)
			}
		case int:
			out = append(out, strconv.Itoa(v))
		case int64:
			out = append(out, strconv.FormatInt(v, 10))
		case uint64:
			out = append(out, strconv.FormatUint(v, 10))
		case map[string]interface{}:
			// Map form: {target: <container-port>, published: <host-port>, ...}
			if target, ok := v["target"]; ok {
				switch tv := target.(type) {
				case int:
					out = append(out, strconv.Itoa(tv))
				case int64:
					out = append(out, strconv.FormatInt(tv, 10))
				case uint64:
					out = append(out, strconv.FormatUint(tv, 10))
				case string:
					if tv != "" {
						out = append(out, tv)
					}
				}
			}
		default:
			return nil, fmt.Errorf("entry %d: unsupported ports value type %T", i, entry)
		}
	}
	return out, nil
}

// containerPortFromString extracts the container (target) port from a compose
// ports string such as "8080:80", "80", "127.0.0.1:8080:80/tcp", or "80/tcp".
// Returns the port number without protocol suffix.
func containerPortFromString(s string) string {
	// Strip trailing /tcp, /udp, etc.
	if idx := strings.LastIndex(s, "/"); idx >= 0 {
		s = s[:idx]
	}
	// Take the last colon-separated segment (container port)
	if idx := strings.LastIndex(s, ":"); idx >= 0 {
		s = s[idx+1:]
	}
	return strings.TrimSpace(s)
}

func parseExpose(raw []interface{}) ([]string, error) {
	if raw == nil {
		return nil, nil
	}

	out := make([]string, 0, len(raw))
	for i, port := range raw {
		switch v := port.(type) {
		case string:
			out = append(out, v)
		case int:
			out = append(out, strconv.Itoa(v))
		case int64:
			out = append(out, strconv.FormatInt(v, 10))
		case uint:
			out = append(out, strconv.FormatUint(uint64(v), 10))
		case uint64:
			out = append(out, strconv.FormatUint(v, 10))
		default:
			return nil, fmt.Errorf("entry %d: unsupported expose value type %T", i, port)
		}
	}
	return out, nil
}
