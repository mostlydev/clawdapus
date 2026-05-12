package initimport

import "strings"

type SourceKind string

const (
	SourceOpenClaw SourceKind = "openclaw"
	SourceHermes   SourceKind = "hermes"
)

type TargetRuntime string

const (
	TargetOpenClaw TargetRuntime = "openclaw"
	TargetHermes   TargetRuntime = "hermes"
)

type Descriptor struct {
	Kind      SourceKind
	Config    string
	Root      string
	AgentName string
	Identity  string
	Models    ModelSlots
	Cllama    bool
	Channels  Channels
	EnvVars   map[string]string
	SkillsDir string
	CronDir   string
	RawNotes  []string
}

type Channels struct {
	Discord  *DiscordChannel
	Slack    *SlackChannel
	Telegram *TelegramChannel
}

type DiscordChannel struct {
	Token          string
	BotID          string
	RequireMention bool
	AllowFrom      []string
	DMPolicy       string
	Guilds         []DiscordGuild
}

type DiscordGuild struct {
	ID             string
	RequireMention bool
	Users          []string
}

type SlackChannel struct {
	BotToken     string
	AppToken     string
	BotID        string
	AllowedUsers []string
}

type TelegramChannel struct {
	Token string
	BotID string
}

type ModelSlots struct {
	Primary  ModelRef
	Fallback []ModelRef
}

type ModelRef struct {
	Provider string
	Model    string
	BaseURL  string
	APIKey   string
}

func (m ModelRef) String() string {
	provider := strings.TrimSpace(m.Provider)
	model := strings.TrimSpace(m.Model)
	if provider == "" {
		return model
	}
	if model == "" {
		return provider
	}
	if strings.HasPrefix(model, provider+"/") {
		return model
	}
	return provider + "/" + model
}

func SplitModelRef(ref string) (ModelRef, bool) {
	provider, model, ok := strings.Cut(strings.TrimSpace(ref), "/")
	if !ok || strings.TrimSpace(provider) == "" || strings.TrimSpace(model) == "" {
		return ModelRef{}, false
	}
	return ModelRef{Provider: strings.TrimSpace(provider), Model: strings.TrimSpace(model)}, true
}

type Options struct {
	ProjectName    string
	AgentName      string
	ModelOverride  string
	CllamaOverride string
	BaseImage      string
}

type Plan struct {
	Source        Descriptor
	Target        TargetRuntime
	ProjectName   string
	AgentName     string
	BaseImage     string
	Model         ModelRef
	Fallback      []ModelRef
	Cllama        bool
	Handles       []HandlePlan
	Environment   map[string]string
	CllamaEnv     map[string]string
	Surfaces      []SurfacePlan
	AgentContract string
	SoulContent   string
	SkillsDir     string
	CronDir       string
	Notes         Notes
}

type HandlePlan struct {
	Platform string
	IDEnv    string
	Username string
	Guilds   []DiscordGuild
}

type SurfacePlan struct {
	Platform string
	Discord  *DiscordSurface
}

type DiscordSurface struct {
	DMAllowFrom []string
	DMPolicy    string
	Guilds      []DiscordGuild
}

type Notes struct {
	Applied     []string
	Action      []string
	SecretNotes []string
}
