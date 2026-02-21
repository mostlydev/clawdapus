package driver

// Driver translates Clawfile intent into runner-specific enforcement.
// Fail-closed: Validate runs before compose up, PostApply runs after.
type Driver interface {
	Validate(rc *ResolvedClaw) error
	Materialize(rc *ResolvedClaw, opts MaterializeOpts) (*MaterializeResult, error)
	PostApply(rc *ResolvedClaw, opts PostApplyOpts) error
	HealthProbe(ref ContainerRef) (*Health, error)
}

// ResolvedClaw combines image-level claw labels with pod-level x-claw overrides.
type ResolvedClaw struct {
	ServiceName   string
	ImageRef      string
	ClawType      string
	Agent         string            // filename from image labels (e.g., "AGENTS.md")
	AgentHostPath string            // resolved host path for bind mount
	Models        map[string]string // slot -> provider/model
	Handles       map[string]*HandleInfo // platform -> contact card (from x-claw handles block)
	Surfaces      []ResolvedSurface
	Skills        []ResolvedSkill
	Privileges    map[string]string
	Configures    []string          // openclaw config set commands from labels
	Count         int               // from pod x-claw (default 1)
	Environment   map[string]string // from pod environment block
}

// HandleInfo is the full contact card for an agent on a platform.
// Enables sibling services to mention, message, and route to this agent.
type HandleInfo struct {
	ID       string      `json:"id"`
	Username string      `json:"username,omitempty"`
	Guilds   []GuildInfo `json:"guilds,omitempty"`
}

// GuildInfo describes one guild/server/workspace membership.
type GuildInfo struct {
	ID       string        `json:"id"`
	Name     string        `json:"name,omitempty"`
	Channels []ChannelInfo `json:"channels,omitempty"`
}

// ChannelInfo describes a single channel within a guild.
type ChannelInfo struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
}

type ResolvedSkill struct {
	Name     string // basename (e.g., "custom-workflow.md")
	HostPath string // resolved absolute host path
}

type ResolvedSurface struct {
	Scheme     string   // channel, service, volume, host, egress
	Target     string   // discord, fleet-master, shared-cache, etc.
	AccessMode string   // read-only, read-write (for volume/host surfaces)
	Ports      []string // exposed ports from service definition (service surfaces only)
}

type GeneratedSkill struct {
	Name    string // filename (e.g., "surface-fleet-master.md")
	Content []byte // skill file content
}

type MaterializeOpts struct {
	RuntimeDir string // host directory for generated artifacts
	PodName    string // pod name for context injection (CLAWDAPUS.md)
}

// MaterializeResult describes what the compose generator must add to the service.
type MaterializeResult struct {
	Mounts      []Mount
	Tmpfs       []string          // paths needing tmpfs (for read_only: true)
	Environment map[string]string // additional env vars
	Healthcheck *Healthcheck
	ReadOnly    bool              // default: true
	Restart     string            // default: "on-failure"
	SkillDir    string            // container path for skills (e.g., "/claw/skills")
}

type Mount struct {
	HostPath      string
	ContainerPath string
	ReadOnly      bool
}

type Healthcheck struct {
	Test     []string
	Interval string
	Timeout  string
	Retries  int
}

type PostApplyOpts struct {
	ContainerID string
}

type ContainerRef struct {
	ContainerID string
	ServiceName string
}

type Health struct {
	OK     bool
	Detail string
}
