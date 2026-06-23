package hermes

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/mostlydev/clawdapus/internal/cllama"
	"github.com/mostlydev/clawdapus/internal/driver"
	"github.com/mostlydev/clawdapus/internal/driver/shared"
	"gopkg.in/yaml.v3"
)

const (
	hermesHomeDir                 = "/root/.hermes"
	hermesWorkspaceDir            = "/workspace"
	hermesPersonaDir              = "/persona"
	hermesDefaultAgentIdentityEnv = "HERMES_DEFAULT_AGENT_IDENTITY"
	hermesGatewayLockDirEnv       = "HERMES_GATEWAY_LOCK_DIR"
	hermesAllowSilentFinalEnv     = "HERMES_ALLOW_SILENT_FINAL"
	hermesChatStatusDeliveryEnv   = "HERMES_CHAT_STATUS_DELIVERY"
	hermesToolProgressModeEnv     = "HERMES_TOOL_PROGRESS_MODE"
	hermesMemoryIndexMaxCharsEnv  = "HERMES_MEMORY_INDEX_MAX_CHARS"
	hermesUserMemoryMaxCharsEnv   = "HERMES_USER_MEMORY_MAX_CHARS"
	hermesDiscordReplyMentionEnv  = "DISCORD_ALLOW_MENTION_REPLIED_USER"
	clawdapusDisabledToolsEnv     = "CLAWDAPUS_DISABLED_TOOLS"
	hermesTextToSpeechTool        = "text_to_speech"
	hermesDefaultGatewayLockDir   = "/tmp/hermes-gateway-locks"
	hermesDefaultBusyInputMode    = "queue"
	hermesDefaultXDGStateHome     = "/tmp/xdg-state"
	hermesDefaultNoProxy          = "localhost,127.0.0.1,cllama"
	managedDefaultAgentIdentity   = "You are a Clawdapus-managed agent. Your identity, authority, communication policy, memory policy, and tool-use rules are defined by the Clawdapus project context loaded below: AGENTS.md, CLAWDAPUS.md, SOUL.md, mounted skills, feeds, and managed-tool policy. Do not identify as Hermes or as a generic assistant. Follow the Clawdapus contract when it is more specific than runner defaults; otherwise retain the Hermes runtime guidance below, including persistent memory behavior."
)

var supportedPlatforms = []string{"discord", "slack", "telegram"}

func GenerateConfig(rc *driver.ResolvedClaw, modelCfg *modelConfig) ([]byte, error) {
	modelBlock := map[string]any{
		"default":  modelCfg.DefaultModel,
		"provider": modelCfg.Provider,
	}
	if modelCfg.BaseURL != "" {
		modelBlock["base_url"] = modelCfg.BaseURL
	}
	if modelCfg.APIKey != "" {
		modelBlock["api_key"] = modelCfg.APIKey
	}
	// The gateway bridges display config into env at boot, so keep these UX
	// defaults in config.yaml and let operators override them with CONFIGURE.
	config := map[string]any{
		"approvals": map[string]any{
			"mode":      "off",
			"cron_mode": "approve",
		},
		"cron": map[string]any{
			"wrap_response": false,
		},
		"display": map[string]any{
			"busy_input_mode":      hermesDefaultBusyInputMode,
			"busy_ack_enabled":     false,
			"memory_notifications": "off",
		},
		"model": modelBlock,
		"onboarding": map[string]any{
			"seen": map[string]any{
				"busy_input_prompt":        true,
				"tool_progress_prompt":     true,
				"openclaw_residue_cleanup": true,
			},
		},
		"terminal": map[string]any{
			"backend": "local",
			"cwd":     hermesWorkspaceDir,
			"timeout": 180,
		},
	}
	if toolsets := platformToolsetsForHandles(rc); len(toolsets) > 0 {
		config["platform_toolsets"] = toolsets
	}
	if platforms := platformConfigsForHandles(rc); len(platforms) > 0 {
		config["platforms"] = platforms
	}

	for _, cmd := range rc.Configures {
		path, value, err := shared.ParseConfigSetCommand(cmd, "hermes")
		if err != nil {
			return nil, fmt.Errorf("config generation: %w", err)
		}
		if err := shared.SetPath(config, path, value); err != nil {
			return nil, fmt.Errorf("config generation: %w", err)
		}
	}

	data, err := yaml.Marshal(config)
	if err != nil {
		return nil, fmt.Errorf("config generation: marshal yaml: %w", err)
	}
	return data, nil
}

func platformToolsetsForHandles(rc *driver.ResolvedClaw) map[string]any {
	presets := map[string]string{
		"discord":  "hermes-discord",
		"slack":    "hermes-slack",
		"telegram": "hermes-telegram",
	}
	toolsets := make(map[string]any)
	if rc == nil {
		return toolsets
	}
	for rawPlatform := range rc.Handles {
		platform := strings.ToLower(strings.TrimSpace(rawPlatform))
		preset, ok := presets[platform]
		if !ok {
			continue
		}
		toolsets[platform] = []string{preset}
	}
	return toolsets
}

func platformConfigsForHandles(rc *driver.ResolvedClaw) map[string]any {
	configs := make(map[string]any)
	if rc == nil {
		return configs
	}
	for rawPlatform := range rc.Handles {
		platform := strings.ToLower(strings.TrimSpace(rawPlatform))
		switch platform {
		case "discord", "slack", "telegram":
			configs[platform] = map[string]any{
				"gateway_restart_notification": false,
			}
		}
	}
	return configs
}

func GenerateEnvFile(rc *driver.ResolvedClaw, modelCfg *modelConfig) ([]byte, error) {

	env := make(map[string]string)
	for key, value := range modelCfg.Env {
		env[key] = value
	}
	env["HERMES_HOME"] = hermesHomeDir
	env[hermesGatewayLockDirEnv] = hermesDefaultGatewayLockDir
	env["MESSAGING_CWD"] = hermesWorkspaceDir
	env["TERMINAL_CWD"] = hermesWorkspaceDir
	env["XDG_STATE_HOME"] = hermesDefaultXDGStateHome
	env["NO_PROXY"] = hermesDefaultNoProxy
	env["no_proxy"] = hermesDefaultNoProxy
	env[hermesDefaultAgentIdentityEnv] = managedDefaultAgentIdentity
	if hasDiscordHandle(rc) || hasSlackHandle(rc) {
		env[hermesAllowSilentFinalEnv] = "1"
		env[hermesToolProgressModeEnv] = "off"
	}
	if hasManagedChatHandle(rc) {
		env[hermesChatStatusDeliveryEnv] = "off"
	}
	if hasDiscordHandle(rc) {
		// Hermes upstream defaults reply-mention pings to True
		// (DISCORD_ALLOW_MENTION_REPLIED_USER), which produces mention loops in
		// multi-agent pods even with DISCORD_REQUIRE_MENTION=true. Force false
		// by default; operators can re-enable per service via x-claw env.
		if _, set := env[hermesDiscordReplyMentionEnv]; !set {
			env[hermesDiscordReplyMentionEnv] = "false"
		}
	}
	if disabled := resolveDisabledHermesTools(rc); len(disabled) > 0 {
		env[clawdapusDisabledToolsEnv] = strings.Join(disabled, ",")
	}

	for _, key := range allowedEnvPassthroughKeys() {
		value, err := resolvedEnvValue(rc, key)
		if err != nil {
			return nil, err
		}
		if value != "" {
			env[key] = value
		}
	}
	if rc != nil && rc.Hermes != nil && rc.Hermes.AllowSilent {
		env[hermesAllowSilentFinalEnv] = "1"
	}

	keys := make([]string, 0, len(env))
	for key := range env {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	var out strings.Builder
	for _, key := range keys {
		out.WriteString(key)
		out.WriteByte('=')
		out.WriteString(dotenvValue(env[key]))
		out.WriteByte('\n')
	}
	return []byte(out.String()), nil
}

func WriteEffectiveAgents(workspaceDir, agentHostPath, clawdapusMD string) (string, error) {
	agentData, err := os.ReadFile(agentHostPath)
	if err != nil {
		return "", fmt.Errorf("read agent contract: %w", err)
	}

	agentsPath := filepath.Join(workspaceDir, "AGENTS.md")
	if err := os.WriteFile(agentsPath, []byte(shared.ComposeEffectiveAgentsMD(string(agentData), clawdapusMD)), 0o644); err != nil {
		return "", fmt.Errorf("write effective AGENTS.md: %w", err)
	}
	return agentsPath, nil
}

// WriteDefaultSoul writes a Clawdapus-generated SOUL.md to the Hermes home
// directory. This pre-empts the Hermes runner's default identity seeding
// ("You are Hermes, an AI assistant made by Nous Research") and establishes
// the agent's own identity from its service name.
//
// The generated soul keeps the voice/craft guidance that makes Hermes agents
// effective (concise, no sycophancy, varied structure) while replacing the
// runner-branded identity with the agent's Clawdapus identity.
func WriteDefaultSoul(homeDir, agentName, podName string) error {
	displayName := strings.ToUpper(agentName[:1]) + agentName[1:]
	soul := fmt.Sprintf(`# %s

You are %s, an agent in the %s pod. Your identity, role, and operating rules
are defined in your contract (AGENTS.md in your workspace). Follow your contract
as your primary authority.

Do not identify as Hermes, a generic assistant, or any identity other than %s.
When asked who you are, answer from your contract identity.

## Voice

Be direct. Lead with the answer, not the reasoning. Match the energy of whoever
you are talking to. Technical depth for technical people. Terse for terse.

Do not use emojis. Use unicode symbols for visual structure when helpful.

No sycophancy ("Great question!", "I'd be happy to help"). No filler
("Here's the thing", "It's worth noting"). No hype words ("revolutionary",
"game-changing", "seamless").

Vary sentence length and structure. Write like a person, not a template.
Most responses are short. Cut anything that does not earn its place.
`, displayName, displayName, podName, displayName)

	soulPath := filepath.Join(homeDir, "SOUL.md")
	if err := os.WriteFile(soulPath, []byte(soul), 0o644); err != nil {
		return fmt.Errorf("write default SOUL.md: %w", err)
	}
	return nil
}

func CopyPersonaSoul(personaHostPath, homeDir string) error {
	soulPath := filepath.Join(personaHostPath, "SOUL.md")
	info, err := os.Stat(soulPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("stat persona SOUL.md: %w", err)
	}
	if info.IsDir() {
		return nil
	}

	data, err := os.ReadFile(soulPath)
	if err != nil {
		return fmt.Errorf("read persona SOUL.md: %w", err)
	}
	if err := os.WriteFile(filepath.Join(homeDir, "SOUL.md"), data, 0o644); err != nil {
		return fmt.Errorf("write Hermes SOUL.md: %w", err)
	}
	return nil
}

type modelConfig struct {
	DefaultModel string
	Provider     string
	BaseURL      string // written to config.yaml model.base_url for custom providers
	APIKey       string // written to config.yaml model.api_key for custom providers
	Env          map[string]string
}

func resolveModelConfig(rc *driver.ResolvedClaw) (*modelConfig, error) {
	modelRef, err := shared.PrimaryModelRef(rc.Models)
	if err != nil {
		return nil, fmt.Errorf("hermes driver: %w", err)
	}

	provider, modelID, ok := shared.SplitModelRef(modelRef)
	if !ok {
		return nil, fmt.Errorf("hermes driver: invalid MODEL primary %q (expected provider/model)", modelRef)
	}

	if len(rc.Cllama) > 0 {
		if strings.TrimSpace(rc.CllamaToken) == "" {
			return nil, fmt.Errorf("hermes driver: CLLAMA is enabled but token is empty")
		}
		return &modelConfig{
			DefaultModel: modelRef,
			Provider:     "custom",
			BaseURL:      cllama.ProxyBaseURL(rc.Cllama[0]),
			APIKey:       rc.CllamaToken,
			Env:          map[string]string{},
		}, nil
	}

	baseURL, err := resolvedEnvValue(rc, "OPENAI_BASE_URL")
	if err != nil {
		return nil, err
	}
	if baseURL != "" {
		defaultModel := modelRef
		if provider == "openai" {
			defaultModel = modelID
		}
		apiKey, err := resolvedEnvValue(rc, "OPENAI_API_KEY")
		if err != nil {
			return nil, err
		}
		return &modelConfig{
			DefaultModel: defaultModel,
			Provider:     "custom",
			BaseURL:      baseURL,
			APIKey:       apiKey,
			Env:          map[string]string{},
		}, nil
	}

	switch shared.NormalizeProvider(provider) {
	case "openrouter":
		return &modelConfig{
			DefaultModel: modelID,
			Provider:     "openrouter",
			Env:          map[string]string{},
		}, nil
	case "anthropic":
		return &modelConfig{
			DefaultModel: modelID,
			Provider:     "anthropic",
			Env:          map[string]string{},
		}, nil
	case "openai":
		return &modelConfig{
			DefaultModel: modelID,
			Provider:     "custom",
			Env: map[string]string{
				"OPENAI_BASE_URL": "https://api.openai.com/v1",
			},
		}, nil
	default:
		return nil, fmt.Errorf(
			"hermes driver: MODEL primary provider %q requires OPENAI_BASE_URL or CLLAMA for Hermes v1",
			provider,
		)
	}
}

func allowedEnvPassthroughKeys() []string {
	return []string{
		"ANTHROPIC_API_KEY",
		"ANTHROPIC_TOKEN",
		"CLAW_API_TOKEN",
		"CLAW_API_URL",
		"DISCORD_ALLOW_BOTS",
		"DISCORD_ALLOWED_USERS",
		hermesDiscordReplyMentionEnv,
		"DISCORD_AUTO_THREAD",
		"DISCORD_BOT_TOKEN",
		"DISCORD_FREE_RESPONSE_CHANNELS",
		"DISCORD_HOME_CHANNEL",
		"DISCORD_HOME_CHANNEL_NAME",
		"DISCORD_REQUIRE_MENTION",
		"FIRECRAWL_API_KEY",
		"FIRECRAWL_API_URL",
		"GATEWAY_ALLOWED_USERS",
		"GATEWAY_ALLOW_ALL_USERS",
		hermesGatewayLockDirEnv,
		hermesAllowSilentFinalEnv,
		hermesChatStatusDeliveryEnv,
		hermesToolProgressModeEnv,
		hermesMemoryIndexMaxCharsEnv,
		hermesUserMemoryMaxCharsEnv,
		"NO_PROXY",
		"no_proxy",
		"OPENAI_API_KEY",
		"OPENROUTER_API_KEY",
		"SLACK_ALLOWED_USERS",
		"SLACK_ALLOW_ALL_USERS",
		"SLACK_ALLOW_BOTS",
		"SLACK_APP_TOKEN",
		"SLACK_BOT_TOKEN",
		"SLACK_FREE_RESPONSE_CHANNELS",
		"SLACK_HOME_CHANNEL",
		"SLACK_HOME_CHANNEL_NAME",
		"SLACK_REACTIONS",
		"SLACK_REQUIRE_MENTION",
		"TELEGRAM_ALLOWED_USERS",
		"TELEGRAM_BOT_TOKEN",
		"TELEGRAM_HOME_CHANNEL",
		"TELEGRAM_HOME_CHANNEL_NAME",
		"XDG_STATE_HOME",
	}
}

func hasDiscordHandle(rc *driver.ResolvedClaw) bool {
	return hasHandle(rc, "discord")
}

func hasSlackHandle(rc *driver.ResolvedClaw) bool {
	return hasHandle(rc, "slack")
}

func hasManagedChatHandle(rc *driver.ResolvedClaw) bool {
	return hasDiscordHandle(rc) || hasSlackHandle(rc) || hasHandle(rc, "telegram")
}

func hasHandle(rc *driver.ResolvedClaw, platform string) bool {
	if rc == nil {
		return false
	}
	for rawPlatform := range rc.Handles {
		if strings.ToLower(strings.TrimSpace(rawPlatform)) == platform {
			return true
		}
	}
	return false
}

func resolveDisabledHermesTools(rc *driver.ResolvedClaw) []string {
	if !hasDiscordHandle(rc) && !hasSlackHandle(rc) {
		return nil
	}

	disabled := []string{hermesTextToSpeechTool}
	if rc == nil || rc.Hermes == nil || len(rc.Hermes.AllowTools) == 0 {
		return disabled
	}

	allowSet := make(map[string]struct{}, len(rc.Hermes.AllowTools))
	for _, tool := range rc.Hermes.AllowTools {
		tool = strings.TrimSpace(tool)
		if tool != "" {
			allowSet[tool] = struct{}{}
		}
	}

	out := make([]string, 0, len(disabled))
	for _, tool := range disabled {
		if _, allowed := allowSet[tool]; allowed {
			continue
		}
		out = append(out, tool)
	}
	return out
}

func resolvedEnvValue(rc *driver.ResolvedClaw, key string) (string, error) {
	value, err := shared.ResolveEnvTokenFromMapWithRuntimeEnv(rc.Environment, key, rc.RuntimeEnv)
	if err != nil {
		return "", fmt.Errorf("%s: %w", key, err)
	}
	return value, nil
}

func resolvedEnvOrDefault(rc *driver.ResolvedClaw, key, fallback string) (string, error) {
	value, err := resolvedEnvValue(rc, key)
	if err != nil {
		return "", err
	}
	if value == "" {
		return fallback, nil
	}
	return value, nil
}

func dotenvValue(value string) string {
	if value == "" {
		return `""`
	}
	if strings.ContainsAny(value, " \t\n\r#\"'\\") {
		return strconv.Quote(value)
	}
	return value
}
