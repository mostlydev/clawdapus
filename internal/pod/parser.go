package pod

import (
	"fmt"
	"io"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"gopkg.in/yaml.v3"

	"github.com/mostlydev/clawdapus/internal/clawapi"
	"github.com/mostlydev/clawdapus/internal/driver"
	"github.com/mostlydev/clawdapus/internal/schedule"
)

// rawPod is the YAML deserialization target.
type rawPod struct {
	XClaw    rawPodClaw            `yaml:"x-claw"`
	Services map[string]rawService `yaml:"services"`
}

type rawPodClaw struct {
	Pod                   string                 `yaml:"pod"`
	Master                string                 `yaml:"master"`
	SequentialConformance bool                   `yaml:"sequential-conformance"`
	HandlesDefaults       map[string]interface{} `yaml:"handles-defaults"`
	Context               *rawContextConfig      `yaml:"context"`
	ChannelMemory         *rawChannelMemoryEntry `yaml:"channel-memory"`
	Principals            []rawPrincipalEntry    `yaml:"principals"`
	AlertWebhooks         []string               `yaml:"alert-webhooks"`
	AlertMentions         []string               `yaml:"alert-mentions"`
}

type rawPrincipalEntry struct {
	Name            string   `yaml:"name"`
	Verbs           []string `yaml:"verbs"`
	Scope           string   `yaml:"scope"`
	Services        []string `yaml:"services"`
	ClawIDs         []string `yaml:"claw_ids"`
	ComposeServices []string `yaml:"compose_services"`
	InjectInto      string   `yaml:"inject-into"`
}

type rawService struct {
	Image       string        `yaml:"image"`
	XClaw       *rawClawBlock `yaml:"x-claw"`
	Environment interface{}   `yaml:"environment"`
	Expose      interface{}   `yaml:"expose"`
	Ports       interface{}   `yaml:"ports"`
}

type rawInvokeEntry struct {
	Schedule string                 `yaml:"schedule"`
	Message  string                 `yaml:"message"`
	Name     string                 `yaml:"name"`
	To       string                 `yaml:"to"`
	When     map[string]interface{} `yaml:"when"`
}

type rawClawBlock struct {
	Agent        string                 `yaml:"agent"`
	Persona      string                 `yaml:"persona"`
	DescribeFile string                 `yaml:"describe-file"`
	Cllama       interface{}            `yaml:"cllama"`
	Models       ModelSlots             `yaml:"models"`
	CllamaEnv    map[string]string      `yaml:"cllama-env"`
	Count        int                    `yaml:"count"`
	Handles      map[string]interface{} `yaml:"handles"`
	Feeds        []rawFeedEntry         `yaml:"feeds"`
	Tools        []rawToolPolicyEntry   `yaml:"tools"`
	ToolPolicy   *rawToolPolicyConfig   `yaml:"tool-policy"`
	Budget       *rawBudgetConfig       `yaml:"budget"`
	Memory       *rawMemoryEntry        `yaml:"memory"`
	Include      []rawIncludeEntry      `yaml:"include"`
	Surfaces     []interface{}          `yaml:"surfaces"`
	Skills       []string               `yaml:"skills"`
	Invoke       []rawInvokeEntry       `yaml:"invoke"`
	Context      *rawContextConfig      `yaml:"context"`
	ClawAPI      interface{}            `yaml:"claw-api"`
	MCPStdio     *rawMCPStdioBlock      `yaml:"mcp-stdio"`
	Hermes       *rawHermesConfig       `yaml:"hermes"`
}

type rawContextConfig struct {
	Channel *rawChannelContextConfig `yaml:"channel"`
	Blocks  []rawContextBlockConfig  `yaml:"blocks"`
}

type rawChannelContextConfig struct {
	Since              string `yaml:"since"`
	Limit              int    `yaml:"limit"`
	MaxCharsHyphen     int    `yaml:"max-chars"`
	MaxCharsUnderscore int    `yaml:"max_chars"`
	Buffer             int    `yaml:"buffer"`
}

type rawContextBlockConfig struct {
	ID                 string `yaml:"id"`
	Kind               string `yaml:"kind"`
	Text               string `yaml:"text"`
	Enabled            *bool  `yaml:"enabled"`
	Placement          string `yaml:"placement"`
	MaxCharsHyphen     int    `yaml:"max-chars"`
	MaxCharsUnderscore int    `yaml:"max_chars"`
	Cadence            string `yaml:"cadence"`
}

type rawMCPStdioBlock struct {
	Command string   `yaml:"command"`
	Args    []string `yaml:"args"`
}

type rawHermesConfig struct {
	AllowTools  []string `yaml:"allow-tools"`
	AllowSilent bool     `yaml:"allow-silent"`
}

type rawFeedEntry struct {
	Name        string `yaml:"name"`
	Source      string `yaml:"source"`
	Path        string `yaml:"path"`
	TTL         int    `yaml:"ttl"`
	Description string `yaml:"description"`
	Unresolved  bool   `yaml:"-"`
}

type rawToolPolicyEntry struct {
	Service string      `yaml:"service"`
	Allow   interface{} `yaml:"allow"`
}

type rawToolPolicyConfig struct {
	MaxRounds        *int `yaml:"max-rounds"`
	TimeoutPerToolMS *int `yaml:"timeout-per-tool-ms"`
	TotalTimeoutMS   *int `yaml:"total-timeout-ms"`
}

type rawBudgetConfig struct {
	LimitUSD              *float64 `yaml:"limit-usd"`
	LimitUSDUnderscore    *float64 `yaml:"limit_usd"`
	MaxRequests           *int     `yaml:"max-requests"`
	MaxRequestsUnderscore *int     `yaml:"max_requests"`
	Requests              *int     `yaml:"requests"`
	Window                string   `yaml:"window"`
	Behavior              string   `yaml:"behavior"`
}

type rawMemoryEntry struct {
	Service   string `yaml:"service"`
	TimeoutMS *int   `yaml:"timeout-ms"`
}

type rawChannelMemoryEntry struct {
	Service string `yaml:"service"`
}

type rawIncludeEntry struct {
	ID          string `yaml:"id"`
	File        string `yaml:"file"`
	Mode        string `yaml:"mode"`
	Description string `yaml:"description"`
}

// Parse reads a claw-pod.yml from the given reader.
func Parse(r io.Reader) (*Pod, error) {
	src, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("read claw-pod.yml: %w", err)
	}

	var root map[string]interface{}
	if err := yaml.Unmarshal(src, &root); err != nil {
		return nil, fmt.Errorf("parse claw-pod.yml: %w", err)
	}
	if err := expandPodDefaults(root); err != nil {
		return nil, fmt.Errorf("parse claw-pod.yml: %w", err)
	}

	expandedSrc, err := yaml.Marshal(root)
	if err != nil {
		return nil, fmt.Errorf("parse claw-pod.yml: marshal expanded config: %w", err)
	}

	var raw rawPod
	if err := yaml.Unmarshal(expandedSrc, &raw); err != nil {
		return nil, fmt.Errorf("parse claw-pod.yml: %w", err)
	}

	preservedRoot := deepCopyMap(root)
	delete(preservedRoot, "x-claw")
	delete(preservedRoot, "services")

	pod := &Pod{
		Name:                  raw.XClaw.Pod,
		Master:                strings.TrimSpace(raw.XClaw.Master),
		SequentialConformance: raw.XClaw.SequentialConformance,
		Services:              make(map[string]*Service, len(raw.Services)),
		Compose:               preservedRoot,
		AlertWebhooks:         raw.XClaw.AlertWebhooks,
		AlertMentions:         raw.XClaw.AlertMentions,
	}
	contextConfig, err := parseContextConfig(raw.XClaw.Context)
	if err != nil {
		return nil, fmt.Errorf("x-claw.context: %w", err)
	}
	pod.Context = contextConfig

	rawServices, err := mapStringAny(root["services"])
	if err != nil {
		return nil, fmt.Errorf("parse claw-pod.yml: services: %w", err)
	}

	for name, svc := range raw.Services {
		serviceCompose := make(map[string]interface{})
		if rawServices != nil {
			rawServiceMap, err := mapStringAny(rawServices[name])
			if err != nil {
				return nil, fmt.Errorf("service %q: %w", name, err)
			}
			serviceCompose = deepCopyMap(rawServiceMap)
			delete(serviceCompose, "x-claw")
		}

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
		environment, err := parseEnvironment(svc.Environment)
		if err != nil {
			return nil, fmt.Errorf("service %q: parse environment: %w", name, err)
		}

		service := &Service{
			Image:       svc.Image,
			Compose:     serviceCompose,
			Environment: environment,
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
			rawHandles := svc.XClaw.Handles
			if svc.XClaw.MCPStdio == nil {
				rawHandles = mergeHandleDefaults(raw.XClaw.HandlesDefaults, svc.XClaw.Handles)
			}
			handles, err := parseHandles(rawHandles)
			if err != nil {
				return nil, fmt.Errorf("service %q: parse handles: %w", name, err)
			}
			include, err := parseIncludes(svc.XClaw.Include)
			if err != nil {
				return nil, fmt.Errorf("service %q: parse include: %w", name, err)
			}
			feeds, err := parseFeeds(svc.XClaw.Feeds)
			if err != nil {
				return nil, fmt.Errorf("service %q: parse feeds: %w", name, err)
			}
			tools, err := parseTools(svc.XClaw.Tools)
			if err != nil {
				return nil, fmt.Errorf("service %q: parse tools: %w", name, err)
			}
			toolPolicy, err := parseToolPolicy(svc.XClaw.ToolPolicy)
			if err != nil {
				return nil, fmt.Errorf("service %q: parse tool-policy: %w", name, err)
			}
			budget, err := parseBudget(svc.XClaw.Budget)
			if err != nil {
				return nil, fmt.Errorf("service %q: parse budget: %w", name, err)
			}
			memory, err := parseMemory(svc.XClaw.Memory)
			if err != nil {
				return nil, fmt.Errorf("service %q: parse memory: %w", name, err)
			}
			contextConfig, err := parseContextConfig(svc.XClaw.Context)
			if err != nil {
				return nil, fmt.Errorf("service %q: context: %w", name, err)
			}
			mcpStdio, err := parseMCPStdio(name, svc.XClaw.MCPStdio, svc.XClaw.Agent, cllama, count)
			if err != nil {
				return nil, err
			}
			hermesConfig, err := parseHermesConfig(svc.XClaw.Hermes)
			if err != nil {
				return nil, fmt.Errorf("service %q: hermes: %w", name, err)
			}
			invoke := make([]InvokeEntry, 0, len(svc.XClaw.Invoke))
			for _, rawInv := range svc.XClaw.Invoke {
				if rawInv.Schedule == "" || rawInv.Message == "" {
					return nil, fmt.Errorf("service %q: invoke entry missing required field (schedule or message)", name)
				}
				when, err := parseInvokeWhen(rawInv.When)
				if err != nil {
					return nil, fmt.Errorf("service %q: invoke when: %w", name, err)
				}
				invoke = append(invoke, InvokeEntry{
					Schedule: rawInv.Schedule,
					Message:  rawInv.Message,
					Name:     rawInv.Name,
					To:       rawInv.To,
					When:     when,
				})
			}
			clawAPIMode, err := parseClawAPIMode(svc.XClaw.ClawAPI)
			if err != nil {
				return nil, fmt.Errorf("service %q: claw-api: %w", name, err)
			}
			service.Claw = &ClawBlock{
				Agent:        svc.XClaw.Agent,
				Persona:      svc.XClaw.Persona,
				DescribeFile: strings.TrimSpace(svc.XClaw.DescribeFile),
				Cllama:       cllama,
				Models:       svc.XClaw.Models,
				CllamaEnv:    svc.XClaw.CllamaEnv,
				Count:        count,
				Handles:      handles,
				Feeds:        feeds,
				Tools:        tools,
				ToolPolicy:   toolPolicy,
				Budget:       budget,
				Memory:       memory,
				Include:      include,
				Surfaces:     parsedSurfaces,
				Skills:       skills,
				Invoke:       invoke,
				Context:      contextConfig,
				ClawAPIMode:  clawAPIMode,
				MCPStdio:     mcpStdio,
				Hermes:       hermesConfig,
			}
		}
		pod.Services[name] = service
	}

	channelMemory, err := parseChannelMemory(raw.XClaw.ChannelMemory, pod.Services)
	if err != nil {
		return nil, fmt.Errorf("x-claw.channel-memory: %w", err)
	}
	pod.ChannelMemory = channelMemory

	if pod.Master != "" {
		svc, ok := pod.Services[pod.Master]
		if !ok {
			return nil, fmt.Errorf("x-claw.master %q targets unknown service", pod.Master)
		}
		if svc == nil || svc.Claw == nil {
			return nil, fmt.Errorf("x-claw.master %q must target a claw-managed service", pod.Master)
		}
	}

	principals, err := parsePrincipals(raw.XClaw.Principals, pod.Services)
	if err != nil {
		return nil, fmt.Errorf("parse claw-pod.yml: principals: %w", err)
	}
	pod.Principals = principals
	for name, svc := range pod.Services {
		if svc == nil || svc.Claw == nil {
			continue
		}
		for _, feed := range svc.Claw.Feeds {
			if feed.Unresolved {
				continue
			}
			if _, ok := pod.Services[feed.Source]; ok {
				continue
			}
			if feed.Source == "claw-api" && pod.Master != "" {
				continue
			}
			return nil, fmt.Errorf("service %q: feed %q targets unknown source %q", name, feed.Name, feed.Source)
		}
	}
	if err := validateHandleIdentityUniqueness(pod); err != nil {
		return nil, err
	}

	return pod, nil
}

func parseInvokeWhen(raw map[string]interface{}) (*schedule.When, error) {
	if len(raw) == 0 {
		return nil, nil
	}

	when := &schedule.When{}
	for key, value := range raw {
		switch strings.TrimSpace(key) {
		case "calendar":
			text, ok := value.(string)
			if !ok {
				return nil, fmt.Errorf("calendar must be a string")
			}
			when.Calendar = strings.TrimSpace(text)
		case "session":
			text, ok := value.(string)
			if !ok {
				return nil, fmt.Errorf("session must be a string")
			}
			sessionValue, err := schedule.ParseSession(text)
			if err != nil {
				return nil, err
			}
			when.Session = sessionValue
		default:
			return nil, fmt.Errorf("unknown field %q", key)
		}
	}

	if strings.TrimSpace(when.Calendar) == "" {
		return nil, fmt.Errorf("missing calendar discriminator")
	}
	if err := when.Validate(); err != nil {
		return nil, err
	}
	return when, nil
}

func validateHandleIdentityUniqueness(p *Pod) error {
	serviceNames := make([]string, 0, len(p.Services))
	for name := range p.Services {
		serviceNames = append(serviceNames, name)
	}
	sort.Strings(serviceNames)

	type handleOwner struct {
		platform string
		id       string
		service  string
	}

	owners := make(map[string]handleOwner)
	for _, name := range serviceNames {
		svc := p.Services[name]
		if svc == nil || svc.Claw == nil {
			continue
		}
		for platform, info := range svc.Claw.Handles {
			if info == nil {
				continue
			}
			id := strings.TrimSpace(info.ID)
			if id == "" {
				continue
			}
			if svc.Claw.Count > 1 {
				return fmt.Errorf("service %q: handle %s id %q cannot be used with count=%d; concurrent replicas need unique identities", name, platform, id, svc.Claw.Count)
			}
			// Sequential conformance pods are allowed to reuse handle IDs across
			// services — they are exercised one runtime at a time, not concurrently.
			if p.SequentialConformance {
				continue
			}
			key := platform + "\x00" + id
			if prev, ok := owners[key]; ok {
				return fmt.Errorf("services %q and %q declare the same %s handle id %q; concurrently active services need unique identities", prev.service, name, prev.platform, prev.id)
			}
			owners[key] = handleOwner{
				platform: platform,
				id:       id,
				service:  name,
			}
		}
	}

	return nil
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

func parseFeeds(rawFeeds []rawFeedEntry) ([]FeedEntry, error) {
	if len(rawFeeds) == 0 {
		return nil, nil
	}

	out := make([]FeedEntry, 0, len(rawFeeds))
	for i, raw := range rawFeeds {
		name := strings.TrimSpace(raw.Name)
		if raw.Unresolved {
			if name == "" {
				return nil, fmt.Errorf("feed %d: unresolved feed name must not be empty", i)
			}
			out = append(out, FeedEntry{
				Name:       name,
				Unresolved: true,
			})
			continue
		}

		source := strings.TrimSpace(raw.Source)
		if source == "" {
			return nil, fmt.Errorf("feed %d: source is required", i)
		}
		feedPath := strings.TrimSpace(raw.Path)
		if feedPath == "" {
			return nil, fmt.Errorf("feed %d: path is required", i)
		}
		if !strings.HasPrefix(feedPath, "/") {
			return nil, fmt.Errorf("feed %d: path %q must start with '/'", i, feedPath)
		}
		if raw.TTL <= 0 {
			return nil, fmt.Errorf("feed %d: ttl must be > 0", i)
		}
		if name == "" {
			name = deriveFeedName(source, feedPath, i)
		}
		out = append(out, FeedEntry{
			Name:        name,
			Source:      source,
			Path:        feedPath,
			TTL:         raw.TTL,
			Description: strings.TrimSpace(raw.Description),
		})
	}
	return out, nil
}

func parseTools(rawTools []rawToolPolicyEntry) ([]ToolPolicyEntry, error) {
	if len(rawTools) == 0 {
		return nil, nil
	}

	out := make([]ToolPolicyEntry, 0, len(rawTools))
	for i, raw := range rawTools {
		service := strings.TrimSpace(raw.Service)
		if service == "" {
			return nil, fmt.Errorf("tool policy %d: service is required", i)
		}
		allow, err := parseToolAllow(raw.Allow)
		if err != nil {
			return nil, fmt.Errorf("tool policy %d: %w", i, err)
		}
		out = append(out, ToolPolicyEntry{
			Service: service,
			Allow:   allow,
		})
	}
	return out, nil
}

func parseToolAllow(raw interface{}) ([]string, error) {
	if raw == nil {
		return []string{"all"}, nil
	}

	var allow []string
	switch v := raw.(type) {
	case string:
		allow = []string{v}
	case []string:
		allow = append([]string(nil), v...)
	case []interface{}:
		allow = make([]string, 0, len(v))
		for i, item := range v {
			s, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("allow[%d] must be a string, got %T", i, item)
			}
			allow = append(allow, s)
		}
	default:
		return nil, fmt.Errorf("allow must be a string or list, got %T", raw)
	}

	if len(allow) == 0 {
		return nil, fmt.Errorf("allow must not be empty")
	}

	normalized := make([]string, 0, len(allow))
	hasAll := false
	for i, item := range allow {
		item = strings.TrimSpace(item)
		if item == "" {
			return nil, fmt.Errorf("allow[%d] must not be empty", i)
		}
		if item == "all" {
			hasAll = true
		}
		normalized = append(normalized, item)
	}
	if hasAll && len(normalized) > 1 {
		return nil, fmt.Errorf(`allow "all" must not be combined with named tools`)
	}
	return normalized, nil
}

func parseToolPolicy(raw *rawToolPolicyConfig) (*ToolPolicyConfig, error) {
	if raw == nil {
		return nil, nil
	}

	if raw.MaxRounds != nil && *raw.MaxRounds <= 0 {
		return nil, fmt.Errorf("max-rounds must be > 0")
	}
	if raw.TimeoutPerToolMS != nil && *raw.TimeoutPerToolMS <= 0 {
		return nil, fmt.Errorf("timeout-per-tool-ms must be > 0")
	}
	if raw.TotalTimeoutMS != nil && *raw.TotalTimeoutMS <= 0 {
		return nil, fmt.Errorf("total-timeout-ms must be > 0")
	}
	if raw.TimeoutPerToolMS != nil && raw.TotalTimeoutMS != nil && *raw.TotalTimeoutMS < *raw.TimeoutPerToolMS {
		return nil, fmt.Errorf("total-timeout-ms must be >= timeout-per-tool-ms")
	}

	return &ToolPolicyConfig{
		MaxRounds:        raw.MaxRounds,
		TimeoutPerToolMS: raw.TimeoutPerToolMS,
		TotalTimeoutMS:   raw.TotalTimeoutMS,
	}, nil
}

func parseBudget(raw *rawBudgetConfig) (*BudgetConfig, error) {
	if raw == nil {
		return nil, nil
	}

	limitUSD, err := selectBudgetLimitUSD(raw.LimitUSD, raw.LimitUSDUnderscore)
	if err != nil {
		return nil, err
	}
	if limitUSD != nil && *limitUSD <= 0 {
		return nil, fmt.Errorf("limit-usd must be > 0")
	}

	maxRequests, err := selectBudgetMaxRequests(raw.MaxRequests, raw.MaxRequestsUnderscore, raw.Requests)
	if err != nil {
		return nil, err
	}
	if maxRequests != nil && *maxRequests <= 0 {
		return nil, fmt.Errorf("max-requests must be > 0")
	}
	if limitUSD == nil && maxRequests == nil {
		return nil, fmt.Errorf("at least one of limit-usd or max-requests is required")
	}

	window := strings.TrimSpace(raw.Window)
	if window == "" {
		return nil, fmt.Errorf("window is required")
	}
	if d, err := time.ParseDuration(window); err != nil || d <= 0 {
		return nil, fmt.Errorf("window must be a positive duration")
	}

	behavior := strings.TrimSpace(raw.Behavior)
	if behavior == "" {
		behavior = "hard_stop"
	}
	if !knownBudgetBehavior(behavior) {
		return nil, fmt.Errorf("unknown behavior %q: must be one of rate_limit, hard_stop, soft_alert", behavior)
	}

	return &BudgetConfig{
		LimitUSD:    limitUSD,
		MaxRequests: maxRequests,
		Window:      window,
		Behavior:    behavior,
	}, nil
}

func selectBudgetLimitUSD(hyphen, underscore *float64) (*float64, error) {
	if hyphen != nil && underscore != nil && *hyphen != *underscore {
		return nil, fmt.Errorf("limit-usd and limit_usd cannot both be set to different values")
	}
	if hyphen != nil {
		return hyphen, nil
	}
	return underscore, nil
}

func selectBudgetMaxRequests(hyphen, underscore, requests *int) (*int, error) {
	selected := hyphen
	name := "max-requests"
	for _, candidate := range []struct {
		name  string
		value *int
	}{
		{name: "max_requests", value: underscore},
		{name: "requests", value: requests},
	} {
		if candidate.value == nil {
			continue
		}
		if selected != nil && *selected != *candidate.value {
			return nil, fmt.Errorf("%s and %s cannot both be set to different values", name, candidate.name)
		}
		selected = candidate.value
		name = candidate.name
	}
	return selected, nil
}

func knownBudgetBehavior(behavior string) bool {
	switch behavior {
	case "rate_limit", "hard_stop", "soft_alert":
		return true
	default:
		return false
	}
}

func parseMemory(raw *rawMemoryEntry) (*MemoryEntry, error) {
	if raw == nil {
		return nil, nil
	}

	service := strings.TrimSpace(raw.Service)
	if service == "" {
		return nil, fmt.Errorf("service is required")
	}

	timeoutMS := 300
	if raw.TimeoutMS != nil {
		if *raw.TimeoutMS <= 0 {
			return nil, fmt.Errorf("timeout-ms must be > 0")
		}
		timeoutMS = *raw.TimeoutMS
	}

	return &MemoryEntry{
		Service:   service,
		TimeoutMS: timeoutMS,
	}, nil
}

func parseChannelMemory(raw *rawChannelMemoryEntry, services map[string]*Service) (*ChannelMemoryConfig, error) {
	if raw == nil {
		return nil, nil
	}
	service := strings.TrimSpace(raw.Service)
	if service == "" {
		return nil, fmt.Errorf("service is required")
	}
	if service == "claw-wall" {
		return nil, fmt.Errorf("service %q is reserved for the conversation wall sidecar", service)
	}
	if _, ok := services[service]; !ok {
		return nil, fmt.Errorf("service %q does not exist", service)
	}
	return &ChannelMemoryConfig{Service: service}, nil
}

func parseContextConfig(raw *rawContextConfig) (*ContextConfig, error) {
	if raw == nil {
		return nil, nil
	}

	channel, err := parseChannelContextConfig(raw.Channel)
	if err != nil {
		return nil, fmt.Errorf("channel: %w", err)
	}
	blocks, err := parseContextBlockConfigs(raw.Blocks)
	if err != nil {
		return nil, fmt.Errorf("blocks: %w", err)
	}
	if channel == nil && blocks == nil {
		return &ContextConfig{}, nil
	}
	return &ContextConfig{Channel: channel, Blocks: blocks}, nil
}

func parseChannelContextConfig(raw *rawChannelContextConfig) (*ChannelContextConfig, error) {
	if raw == nil {
		return nil, nil
	}

	since := strings.TrimSpace(raw.Since)
	if since != "" {
		d, err := time.ParseDuration(since)
		if err != nil || d < 0 {
			return nil, fmt.Errorf("since must be a non-negative duration")
		}
	}
	if raw.Limit < 0 {
		return nil, fmt.Errorf("limit must be >= 0")
	}
	if raw.Buffer < 0 {
		return nil, fmt.Errorf("buffer must be >= 0")
	}
	maxChars, err := selectChannelContextMaxChars(raw.MaxCharsHyphen, raw.MaxCharsUnderscore)
	if err != nil {
		return nil, err
	}
	if maxChars < 0 {
		return nil, fmt.Errorf("max-chars must be >= 0")
	}

	return &ChannelContextConfig{
		Since:    since,
		Limit:    raw.Limit,
		MaxChars: maxChars,
		Buffer:   raw.Buffer,
	}, nil
}

func selectChannelContextMaxChars(hyphen, underscore int) (int, error) {
	if hyphen != 0 && underscore != 0 && hyphen != underscore {
		return 0, fmt.Errorf("max-chars and max_chars cannot both be set to different values")
	}
	if hyphen != 0 {
		return hyphen, nil
	}
	return underscore, nil
}

func parseContextBlockConfigs(raw []rawContextBlockConfig) ([]ContextBlockConfig, error) {
	if raw == nil {
		return nil, nil
	}
	out := make([]ContextBlockConfig, 0, len(raw))
	seen := make(map[string]struct{}, len(raw))
	for i, entry := range raw {
		block, err := parseContextBlockConfig(entry)
		if err != nil {
			return nil, fmt.Errorf("entry %d: %w", i, err)
		}
		if _, ok := seen[block.ID]; ok {
			return nil, fmt.Errorf("entry %d: duplicate id %q", i, block.ID)
		}
		seen[block.ID] = struct{}{}
		out = append(out, block)
	}
	return out, nil
}

func parseContextBlockConfig(raw rawContextBlockConfig) (ContextBlockConfig, error) {
	id := strings.TrimSpace(raw.ID)
	if id == "" {
		return ContextBlockConfig{}, fmt.Errorf("id must not be empty")
	}
	kind := strings.TrimSpace(raw.Kind)
	if kind == "" {
		kind = "context_block"
	}
	text := strings.TrimSpace(raw.Text)
	if text == "" {
		return ContextBlockConfig{}, fmt.Errorf("text must not be empty")
	}
	placement := strings.TrimSpace(raw.Placement)
	if placement == "" {
		placement = "after_feeds"
	}
	if placement != "before_feeds" && placement != "after_feeds" {
		return ContextBlockConfig{}, fmt.Errorf("placement must be before_feeds or after_feeds")
	}
	cadence := strings.TrimSpace(raw.Cadence)
	if cadence == "" {
		cadence = "every_turn"
	}
	if cadence != "every_turn" {
		return ContextBlockConfig{}, fmt.Errorf("cadence must be every_turn")
	}
	maxChars, err := selectContextBlockMaxChars(raw.MaxCharsHyphen, raw.MaxCharsUnderscore)
	if err != nil {
		return ContextBlockConfig{}, err
	}
	if maxChars == 0 {
		maxChars = 800
	}
	if maxChars < 0 {
		return ContextBlockConfig{}, fmt.Errorf("max-chars must be >= 0")
	}
	if utf8.RuneCountInString(text) > maxChars {
		return ContextBlockConfig{}, fmt.Errorf("text length must be <= max-chars")
	}
	enabled := true
	if raw.Enabled != nil {
		enabled = *raw.Enabled
	}
	return ContextBlockConfig{
		ID:        id,
		Kind:      kind,
		Text:      text,
		Enabled:   enabled,
		Placement: placement,
		MaxChars:  maxChars,
		Cadence:   cadence,
	}, nil
}

func selectContextBlockMaxChars(hyphen, underscore int) (int, error) {
	if hyphen != 0 && underscore != 0 && hyphen != underscore {
		return 0, fmt.Errorf("max-chars and max_chars cannot both be set to different values")
	}
	if hyphen != 0 {
		return hyphen, nil
	}
	return underscore, nil
}

func parseMCPStdio(serviceName string, raw *rawMCPStdioBlock, agent string, cllama []string, count int) (*MCPStdioBlock, error) {
	if raw == nil {
		return nil, nil
	}
	command := strings.TrimSpace(raw.Command)
	if command == "" {
		return nil, fmt.Errorf("service %q: mcp-stdio command is required", serviceName)
	}
	if strings.TrimSpace(agent) != "" {
		return nil, fmt.Errorf("service %q: mcp-stdio cannot be combined with agent", serviceName)
	}
	if len(cllama) > 0 {
		return nil, fmt.Errorf("service %q: mcp-stdio cannot be combined with cllama", serviceName)
	}
	if count > 1 {
		return nil, fmt.Errorf("service %q: mcp-stdio does not support count > 1", serviceName)
	}
	args := raw.Args
	if args == nil {
		args = []string{}
	}
	return &MCPStdioBlock{
		Command: command,
		Args:    append([]string(nil), args...),
	}, nil
}

func parseHermesConfig(raw *rawHermesConfig) (*driver.HermesConfig, error) {
	if raw == nil || (len(raw.AllowTools) == 0 && !raw.AllowSilent) {
		return nil, nil
	}

	allowTools := make([]string, 0, len(raw.AllowTools))
	for i, tool := range raw.AllowTools {
		tool = strings.TrimSpace(tool)
		if tool == "" {
			return nil, fmt.Errorf("allow-tools[%d] must not be empty", i)
		}
		allowTools = append(allowTools, tool)
	}
	if len(allowTools) == 0 && !raw.AllowSilent {
		return nil, nil
	}
	return &driver.HermesConfig{
		AllowTools:  allowTools,
		AllowSilent: raw.AllowSilent,
	}, nil
}

func (r *rawFeedEntry) UnmarshalYAML(node *yaml.Node) error {
	switch node.Kind {
	case yaml.ScalarNode:
		if strings.TrimSpace(node.Value) == "" {
			return fmt.Errorf("feed name must not be empty")
		}
		r.Name = strings.TrimSpace(node.Value)
		r.Unresolved = true
		return nil
	case yaml.MappingNode:
		type alias rawFeedEntry
		var parsed alias
		if err := node.Decode(&parsed); err != nil {
			return err
		}
		*r = rawFeedEntry(parsed)
		r.Unresolved = false
		return nil
	default:
		return fmt.Errorf("feed entry must be a string or mapping, got %s", yamlKindString(node.Kind))
	}
}

func (r *rawToolPolicyEntry) UnmarshalYAML(node *yaml.Node) error {
	switch node.Kind {
	case yaml.ScalarNode:
		if strings.TrimSpace(node.Value) == "" {
			return fmt.Errorf("tool service must not be empty")
		}
		r.Service = strings.TrimSpace(node.Value)
		r.Allow = nil
		return nil
	case yaml.MappingNode:
		type alias rawToolPolicyEntry
		var parsed alias
		if err := node.Decode(&parsed); err != nil {
			return err
		}
		*r = rawToolPolicyEntry(parsed)
		return nil
	default:
		return fmt.Errorf("tool entry must be a string or mapping, got %s", yamlKindString(node.Kind))
	}
}

func expandPodDefaults(root map[string]interface{}) error {
	if len(root) == 0 {
		return nil
	}

	rawXClaw, err := mapStringAny(root["x-claw"])
	if err != nil {
		return fmt.Errorf("x-claw: %w", err)
	}
	if rawXClaw == nil {
		return nil
	}

	defaults := podDefaults{
		CllamaDefaults:   deepCopyMapOrNil(rawXClaw["cllama-defaults"]),
		ModelsDefaults:   deepCopyMapOrNil(rawXClaw["models-defaults"]),
		SurfacesDefaults: deepCopySliceOrNil(rawXClaw["surfaces-defaults"]),
		FeedsDefaults:    deepCopySliceOrNil(rawXClaw["feeds-defaults"]),
		ToolsDefaults:    deepCopySliceOrNil(rawXClaw["tools-defaults"]),
		ToolPolicy:       deepCopyMapOrNil(rawXClaw["tool-policy-defaults"]),
		BudgetDefaults:   deepCopyMapOrNil(rawXClaw["budget-defaults"]),
		MemoryDefaults:   deepCopyMapOrNil(rawXClaw["memory-defaults"]),
		SkillsDefaults:   deepCopySliceOrNil(rawXClaw["skills-defaults"]),
	}

	rawServices, err := mapStringAny(root["services"])
	if err != nil {
		return fmt.Errorf("services: %w", err)
	}
	for name, rawSvc := range rawServices {
		svcMap, err := mapStringAny(rawSvc)
		if err != nil {
			return fmt.Errorf("service %q: %w", name, err)
		}
		rawBlock, err := mapStringAny(svcMap["x-claw"])
		if err != nil {
			return fmt.Errorf("service %q: x-claw: %w", name, err)
		}
		if rawBlock == nil {
			continue
		}

		// alert-webhooks and alert-mentions are pod-scoped only.
		// A service x-claw block that declares either key is a hard error.
		for _, forbidden := range []string{"alert-webhooks", "alert-mentions"} {
			if _, found := rawBlock[forbidden]; found {
				return fmt.Errorf("service %q: %q is a pod-level field and cannot be declared on a service", name, forbidden)
			}
		}

		if !rawBlockHasMCPStdio(rawBlock) {
			if err := applyRawPodDefaults(rawBlock, defaults); err != nil {
				return fmt.Errorf("service %q: %w", name, err)
			}
		}
	}

	return nil
}

func rawBlockHasMCPStdio(raw map[string]interface{}) bool {
	_, ok := raw["mcp-stdio"]
	return ok
}

type podDefaults struct {
	CllamaDefaults   map[string]interface{}
	ModelsDefaults   map[string]interface{}
	SurfacesDefaults []interface{}
	FeedsDefaults    []interface{}
	ToolsDefaults    []interface{}
	ToolPolicy       map[string]interface{}
	BudgetDefaults   map[string]interface{}
	MemoryDefaults   map[string]interface{}
	SkillsDefaults   []interface{}
}

func applyRawPodDefaults(raw map[string]interface{}, defaults podDefaults) error {
	if err := applyRawCllamaDefaults(raw, defaults.CllamaDefaults); err != nil {
		return err
	}
	if err := applyRawModelsDefaults(raw, defaults.ModelsDefaults); err != nil {
		return err
	}
	if err := applyRawListDefaults(raw, "surfaces", defaults.SurfacesDefaults); err != nil {
		return err
	}
	if err := applyRawListDefaults(raw, "feeds", defaults.FeedsDefaults); err != nil {
		return err
	}
	if err := applyRawListDefaults(raw, "tools", defaults.ToolsDefaults); err != nil {
		return err
	}
	if err := applyRawObjectDefault(raw, "tool-policy", defaults.ToolPolicy); err != nil {
		return err
	}
	if err := applyRawObjectDefault(raw, "budget", defaults.BudgetDefaults); err != nil {
		return err
	}
	if err := applyRawObjectDefault(raw, "memory", defaults.MemoryDefaults); err != nil {
		return err
	}
	if err := applyRawListDefaults(raw, "skills", defaults.SkillsDefaults); err != nil {
		return err
	}
	return nil
}

func applyRawCllamaDefaults(raw map[string]interface{}, defaults map[string]interface{}) error {
	if len(defaults) == 0 {
		return nil
	}

	if _, present := raw["cllama"]; !present {
		if proxy, ok := defaults["proxy"]; ok {
			raw["cllama"] = deepCopyValue(proxy)
		}
	}

	defaultEnv, err := mapStringAny(defaults["env"])
	if err != nil {
		return fmt.Errorf("cllama-defaults.env: %w", err)
	}
	if len(defaultEnv) == 0 {
		return nil
	}

	serviceEnv, err := mapStringAny(raw["cllama-env"])
	if err != nil {
		return fmt.Errorf("cllama-env: %w", err)
	}
	if len(serviceEnv) == 0 {
		raw["cllama-env"] = deepCopyMap(defaultEnv)
		return nil
	}
	raw["cllama-env"] = mergeStringMap(defaultEnv, serviceEnv)
	return nil
}

func applyRawModelsDefaults(raw map[string]interface{}, defaults map[string]interface{}) error {
	serviceVal, present := raw["models"]
	if !present {
		if len(defaults) > 0 {
			raw["models"] = deepCopyMap(defaults)
		}
		return nil
	}

	serviceMap, err := mapStringAny(serviceVal)
	if err != nil {
		return fmt.Errorf("models: %w", err)
	}
	if serviceMap == nil {
		return nil
	}
	if len(serviceMap) == 0 {
		raw["models"] = map[string]interface{}{}
		return nil
	}
	if len(defaults) == 0 {
		return nil
	}
	raw["models"] = mergeStringMap(defaults, serviceMap)
	return nil
}

func applyRawListDefaults(raw map[string]interface{}, key string, defaults []interface{}) error {
	_, present := raw[key]
	if !present {
		if defaults != nil {
			raw[key] = deepCopyValue(defaults)
		}
		return nil
	}

	serviceList, err := interfaceSlice(raw[key])
	if err != nil {
		return fmt.Errorf("%s: %w", key, err)
	}

	spreadIdx := -1
	for i, item := range serviceList {
		value, ok := item.(string)
		if !ok || strings.TrimSpace(value) != "..." {
			continue
		}
		if spreadIdx >= 0 {
			return fmt.Errorf("%s: spread token \"...\" may appear at most once", key)
		}
		spreadIdx = i
	}
	if spreadIdx < 0 {
		return nil
	}
	if len(defaults) == 0 {
		return fmt.Errorf("%s: spread token \"...\" used but no pod-level %s-defaults declared", key, key)
	}

	expanded := make([]interface{}, 0, len(serviceList)+len(defaults)-1)
	expanded = append(expanded, deepCopyInterfaces(serviceList[:spreadIdx])...)
	expanded = append(expanded, deepCopyInterfaces(defaults)...)
	expanded = append(expanded, deepCopyInterfaces(serviceList[spreadIdx+1:])...)
	raw[key] = expanded
	return nil
}

func applyRawObjectDefault(raw map[string]interface{}, key string, defaults map[string]interface{}) error {
	if len(defaults) == 0 {
		return nil
	}
	if _, present := raw[key]; present {
		return nil
	}
	raw[key] = deepCopyMap(defaults)
	return nil
}

func deepCopyMapOrNil(raw interface{}) map[string]interface{} {
	m, err := mapStringAny(raw)
	if err != nil || m == nil {
		return nil
	}
	return deepCopyMap(m)
}

func deepCopySliceOrNil(raw interface{}) []interface{} {
	items, err := interfaceSlice(raw)
	if err != nil || items == nil {
		return nil
	}
	return deepCopyInterfaces(items)
}

func deepCopyInterfaces(items []interface{}) []interface{} {
	if items == nil {
		return nil
	}
	out := make([]interface{}, 0, len(items))
	for _, item := range items {
		out = append(out, deepCopyValue(item))
	}
	return out
}

func yamlKindString(kind yaml.Kind) string {
	switch kind {
	case yaml.DocumentNode:
		return "document"
	case yaml.SequenceNode:
		return "sequence"
	case yaml.MappingNode:
		return "mapping"
	case yaml.ScalarNode:
		return "scalar"
	case yaml.AliasNode:
		return "alias"
	default:
		return fmt.Sprintf("kind(%d)", kind)
	}
}

func deriveFeedName(source, feedPath string, index int) string {
	base := strings.TrimSpace(path.Base(strings.TrimSpace(feedPath)))
	base = strings.Trim(base, "/.")
	if base == "" || base == "." {
		return fmt.Sprintf("%s-feed-%d", source, index+1)
	}
	return base
}

func mergeHandleDefaults(defaults, service map[string]interface{}) map[string]interface{} {
	if len(defaults) == 0 {
		return service
	}
	if service == nil {
		return deepCopyMap(defaults)
	}
	if len(service) == 0 {
		return map[string]interface{}{}
	}

	merged := deepCopyMap(defaults)
	for platform, serviceVal := range service {
		if defaultVal, ok := merged[platform]; ok {
			merged[platform] = mergeHandleValue(defaultVal, serviceVal)
			continue
		}
		merged[platform] = deepCopyValue(serviceVal)
	}
	return merged
}

func mergeHandleValue(defaultVal, serviceVal interface{}) interface{} {
	defaultMap, defaultOK := canonicalHandleValue(defaultVal)
	serviceMap, serviceOK := canonicalHandleValue(serviceVal)
	if defaultOK && serviceOK {
		return mergeStringMap(defaultMap, serviceMap)
	}
	return deepCopyValue(serviceVal)
}

func canonicalHandleValue(raw interface{}) (map[string]interface{}, bool) {
	switch v := raw.(type) {
	case string:
		return map[string]interface{}{"id": v}, true
	case int:
		return map[string]interface{}{"id": strconv.Itoa(v)}, true
	case int64:
		return map[string]interface{}{"id": strconv.FormatInt(v, 10)}, true
	case uint64:
		return map[string]interface{}{"id": strconv.FormatUint(v, 10)}, true
	case map[string]interface{}:
		return deepCopyMap(v), true
	default:
		return nil, false
	}
}

func mergeStringMap(base, override map[string]interface{}) map[string]interface{} {
	if len(base) == 0 {
		return deepCopyMap(override)
	}
	out := deepCopyMap(base)
	for k, v := range override {
		if baseMap, ok := out[k].(map[string]interface{}); ok {
			if overrideMap, ok := v.(map[string]interface{}); ok {
				out[k] = mergeStringMap(baseMap, overrideMap)
				continue
			}
		}
		out[k] = deepCopyValue(v)
	}
	return out
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

func parseClawAPIMode(raw interface{}) (string, error) {
	if raw == nil {
		return "", nil
	}
	s, ok := raw.(string)
	if !ok {
		return "", fmt.Errorf("unsupported value %v, only \"self\" is valid", raw)
	}
	s = strings.TrimSpace(s)
	if s == "" {
		return "", nil
	}
	if s != "self" {
		return "", fmt.Errorf("unsupported value %q, only \"self\" is valid", s)
	}
	return "self", nil
}

var knownVerbs = map[string]bool{
	clawapi.VerbFleetStatus:       true,
	clawapi.VerbFleetLogs:         true,
	clawapi.VerbFleetQueryMetrics: true,
	clawapi.VerbFleetAlerts:       true,
	clawapi.VerbScheduleRead:      true,
	"fleet.restart":               true,
	"fleet.quarantine":            true,
	"fleet.budget.set":            true,
	"fleet.model.restrict":        true,
	clawapi.VerbScheduleControl:   true,
}

func parsePrincipals(raw []rawPrincipalEntry, services map[string]*Service) ([]PodPrincipal, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	out := make([]PodPrincipal, 0, len(raw))
	for i, entry := range raw {
		name := strings.TrimSpace(entry.Name)
		if name == "" {
			return nil, fmt.Errorf("principal %d: name must not be empty", i)
		}
		if len(entry.Verbs) == 0 {
			return nil, fmt.Errorf("principal %q: verbs must not be empty", name)
		}
		for _, verb := range entry.Verbs {
			if !knownVerbs[verb] {
				return nil, fmt.Errorf("principal %q: unknown verb %q", name, verb)
			}
		}
		scope := strings.TrimSpace(entry.Scope)
		if scope == "pod" && (len(entry.Services) > 0 || len(entry.ClawIDs) > 0 || len(entry.ComposeServices) > 0) {
			return nil, fmt.Errorf("principal %q: scope \"pod\" is mutually exclusive with services, claw_ids, and compose_services", name)
		}
		if entry.InjectInto != "" {
			if _, ok := services[entry.InjectInto]; !ok {
				return nil, fmt.Errorf("principal %q: inject-into %q does not reference a known service", name, entry.InjectInto)
			}
		}
		out = append(out, PodPrincipal{
			Name:            name,
			Verbs:           entry.Verbs,
			Scope:           scope,
			Services:        entry.Services,
			ClawIDs:         entry.ClawIDs,
			ComposeServices: entry.ComposeServices,
			InjectInto:      entry.InjectInto,
		})
	}
	return out, nil
}

func parseIncludes(raw []rawIncludeEntry) ([]IncludeEntry, error) {
	if len(raw) == 0 {
		return nil, nil
	}

	seen := make(map[string]struct{}, len(raw))
	out := make([]IncludeEntry, 0, len(raw))
	for i, item := range raw {
		id := strings.TrimSpace(item.ID)
		if id == "" {
			return nil, fmt.Errorf("entry %d: include id must not be empty", i)
		}
		if _, exists := seen[id]; exists {
			return nil, fmt.Errorf("entry %d: duplicate include id %q", i, id)
		}
		seen[id] = struct{}{}

		file := strings.TrimSpace(item.File)
		if file == "" {
			return nil, fmt.Errorf("entry %d (%s): include file must not be empty", i, id)
		}
		mode := strings.ToLower(strings.TrimSpace(item.Mode))
		switch mode {
		case "enforce", "guide", "reference":
		default:
			return nil, fmt.Errorf("entry %d (%s): unsupported include mode %q", i, id, item.Mode)
		}

		out = append(out, IncludeEntry{
			ID:          id,
			File:        file,
			Mode:        mode,
			Description: strings.TrimSpace(item.Description),
		})
	}

	return out, nil
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
func parsePorts(raw interface{}) ([]string, error) {
	if raw == nil {
		return nil, nil
	}
	entries, err := interfaceSlice(raw)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(entries))
	for i, entry := range entries {
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

func parseExpose(raw interface{}) ([]string, error) {
	if raw == nil {
		return nil, nil
	}

	entries, err := interfaceSlice(raw)
	if err != nil {
		return nil, err
	}

	out := make([]string, 0, len(entries))
	for i, port := range entries {
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
