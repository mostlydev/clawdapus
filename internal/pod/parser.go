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
}

type rawClawBlock struct {
	Agent    string                 `yaml:"agent"`
	Persona  string                 `yaml:"persona"`
	Cllama   string                 `yaml:"cllama"`
	Count    int                    `yaml:"count"`
	Handles  map[string]interface{} `yaml:"handles"`
	Surfaces []string               `yaml:"surfaces"`
	Skills   []string               `yaml:"skills"`
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
		service := &Service{
			Image:       svc.Image,
			Environment: svc.Environment,
			Expose:      expose,
		}
		if svc.XClaw != nil {
			count := svc.XClaw.Count
			if count < 1 {
				count = 1
			}
			surfaces := svc.XClaw.Surfaces
			if surfaces == nil {
				surfaces = make([]string, 0)
			}
			skills := svc.XClaw.Skills
			if skills == nil {
				skills = make([]string, 0)
			}
			handles, err := parseHandles(svc.XClaw.Handles)
			if err != nil {
				return nil, fmt.Errorf("service %q: parse handles: %w", name, err)
			}
			service.Claw = &ClawBlock{
				Agent:    svc.XClaw.Agent,
				Persona:  svc.XClaw.Persona,
				Cllama:   svc.XClaw.Cllama,
				Count:    count,
				Handles:  handles,
				Surfaces: surfaces,
				Skills:   skills,
			}
		}
		pod.Services[name] = service
	}

	return pod, nil
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
