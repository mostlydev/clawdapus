package pod

import (
	"github.com/mostlydev/clawdapus/internal/driver"
	"github.com/mostlydev/clawdapus/internal/schedule"
)

// Pod represents a parsed claw-pod.yml.
type Pod struct {
	Name                  string
	Master                string
	SequentialConformance bool // when true, shared handle IDs across services are allowed (sequential conformance spikes only)
	Services              map[string]*Service
	Compose               map[string]interface{} // preserved top-level compose keys except x-claw and services
	ClawAPI               *ClawAPIConfig
	Clawdash              *ClawdashConfig // runtime-only dashboard sidecar config, injected by claw up
	Context               *ContextConfig
	ChannelMemory         *ChannelMemoryConfig
	Principals            []PodPrincipal
	AlertWebhooks         []string // pod-scoped Discord webhook URLs for pool-transition alerts
	AlertMentions         []string // pod-scoped @-mention targets for alerts (e.g. "@wojtek", "@infra")
}

// Service represents a service in a claw-pod.yml.
type Service struct {
	Image       string
	Compose     map[string]interface{} // preserved compose service keys except x-claw
	Claw        *ClawBlock
	Environment map[string]string
	Expose      []string // ports exposed to other containers (from compose expose:)
	Ports       []string // container-side ports from compose ports: (host:container or plain container)
}

func (s *Service) IsMCPStdioSidecar() bool {
	return s != nil && s.Claw != nil && s.Claw.MCPStdio != nil
}

func (s *Service) IsAgentManaged() bool {
	return s != nil && s.Claw != nil && s.Claw.MCPStdio == nil
}

// InvokeEntry is a scheduled agent task declared in the pod x-claw.invoke block.
type InvokeEntry struct {
	Schedule string // 5-field cron expression
	Message  string // agent task payload
	Name     string // optional human-readable job name
	To       string // delivery target (name or ID; optional platform prefix "platform:target")
	When     *schedule.When
}

// ClawBlock represents the x-claw extension on a service.
type ClawBlock struct {
	Agent        string
	Persona      string
	DescribeFile string
	Cllama       []string
	Models       map[string]string
	CllamaEnv    map[string]string
	CllamaTokens map[string]string // runtime-only: expanded service name -> token
	Count        int
	Handles      map[string]*driver.HandleInfo // platform → contact card
	Feeds        []FeedEntry
	Tools        []ToolPolicyEntry
	ToolPolicy   *ToolPolicyConfig
	Budget       *BudgetConfig
	Memory       *MemoryEntry
	Include      []IncludeEntry
	Surfaces     []driver.ResolvedSurface
	Skills       []string
	Invoke       []InvokeEntry
	Context      *ContextConfig
	ClawAPIMode  string // "self" when claw-api: self is declared; empty otherwise
	MCPStdio     *MCPStdioBlock
	Hermes       *driver.HermesConfig
}

type MCPStdioBlock struct {
	Command string
	Args    []string
}

// PodPrincipal is an explicit principal declared in the pod-level x-claw.principals list.
type PodPrincipal struct {
	Name            string   `yaml:"name"`
	Verbs           []string `yaml:"verbs"`
	Scope           string   `yaml:"scope,omitempty"`
	Services        []string `yaml:"services,omitempty"`
	ClawIDs         []string `yaml:"claw_ids,omitempty"`
	ComposeServices []string `yaml:"compose_services,omitempty"`
	InjectInto      string   `yaml:"inject-into,omitempty"`
}

type FeedEntry struct {
	Name        string
	Source      string
	Path        string
	TTL         int
	Description string
	Unresolved  bool
}

type ToolPolicyEntry struct {
	Service string
	Allow   []string
}

// ToolPolicyConfig holds per-service overrides for the cllama managed-tool
// mediation policy. Nil fields inherit the compiled-in defaults.
type ToolPolicyConfig struct {
	MaxRounds        *int
	TimeoutPerToolMS *int
	TotalTimeoutMS   *int
}

// BudgetConfig holds per-service cllama budget and request-rate caps. Nil caps
// are disabled; Window must be set when any cap is configured.
type BudgetConfig struct {
	LimitUSD    *float64
	MaxRequests *int
	Window      string
	Behavior    string
}

type MemoryEntry struct {
	Service   string
	TimeoutMS int
}

type ChannelMemoryConfig struct {
	Service string
}

type ContextConfig struct {
	Channel *ChannelContextConfig
}

type ChannelContextConfig struct {
	Since    string
	Limit    int
	MaxChars int
	Buffer   int
}

type IncludeEntry struct {
	ID          string
	File        string
	Mode        string
	Description string
}
