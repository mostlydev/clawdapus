package initimport

import (
	"fmt"
	"strings"

	"github.com/mostlydev/clawdapus/internal/driver/hermes"
)

func Translate(src Descriptor, opts Options) (Plan, error) {
	model := src.Models.Primary
	fallbacks := append([]ModelRef(nil), src.Models.Fallback...)
	notes := Notes{}
	target, err := targetForSource(src.Kind)
	if err != nil {
		return Plan{}, err
	}
	if override := strings.TrimSpace(opts.ModelOverride); override != "" {
		parsed, ok := SplitModelRef(override)
		if !ok {
			return Plan{}, fmt.Errorf("--model %q is invalid (expected provider/model)", override)
		}
		model = parsed
		if len(fallbacks) > 0 {
			notes.Action = append(notes.Action, "source fallback models were not preserved because --model override was used")
			fallbacks = nil
		}
	}
	if model.Provider == "" {
		model = ModelRef{Provider: "openrouter", Model: "anthropic/claude-sonnet-4"}
	}
	if len(fallbacks) > 1 {
		notes.Action = append(notes.Action, fmt.Sprintf("source fallback chain preserved at pod level via x-claw.models.fallback: %s", strings.Join(modelRefStrings(fallbacks), ", ")))
	}
	if isCllamaDisabled(opts.CllamaOverride) && model.BaseURL != "" {
		return Plan{}, fmt.Errorf("--cllama=no cannot import source model base_url %q; pass --model <provider/model> to use a native route or omit --cllama=no", model.BaseURL)
	}

	env := newEnvBuilder()
	for _, note := range src.RawNotes {
		notes.Action = append(notes.Action, note)
	}

	projectName := normalizeName(opts.ProjectName, "clawdapus-import")
	agentName := normalizeName(opts.AgentName, normalizeName(src.AgentName, "assistant"))
	if strings.TrimSpace(opts.AgentName) == "" && strings.TrimSpace(src.AgentName) != "" {
		agentName = normalizeName(src.AgentName, "assistant")
	}

	cllama := src.Cllama || model.BaseURL != "" || isCllamaForced(opts.CllamaOverride)
	if isCllamaDisabled(opts.CllamaOverride) {
		cllama = false
	}
	baseImage := strings.TrimSpace(opts.BaseImage)
	if baseImage == "" {
		baseImage = baseImageForTarget(target)
	}

	plan := Plan{
		Source:        src,
		Target:        target,
		ProjectName:   projectName,
		AgentName:     agentName,
		BaseImage:     baseImage,
		Model:         model,
		Fallback:      fallbacks,
		Cllama:        cllama,
		Environment:   map[string]string{},
		CllamaEnv:     map[string]string{},
		AgentContract: defaultImportedContract(src, target),
		SoulContent:   sourceSoul(src),
		SkillsDir:     src.SkillsDir,
		CronDir:       src.CronDir,
	}

	if err := translateModel(&plan, &notes, env); err != nil {
		return Plan{}, err
	}
	translateChannels(&plan, &notes, env)
	if plan.Target == TargetHermes && len(plan.Handles) == 0 {
		return Plan{}, fmt.Errorf("Hermes import requires at least one supported handle in the source .env (DISCORD_BOT_TOKEN, SLACK_BOT_TOKEN/SLACK_APP_TOKEN, or TELEGRAM_BOT_TOKEN)")
	}
	if src.CronDir != "" {
		notes.Action = append(notes.Action, "cron files copied to imported/cron/ as references; translate them to INVOKE directives")
	}
	if src.Identity != "" && src.EnvVars["HERMES_DEFAULT_AGENT_IDENTITY"] != "" {
		notes.Action = append(notes.Action, "HERMES_DEFAULT_AGENT_IDENTITY was folded into AGENTS.md/SOUL.md, not preserved as raw env")
	}

	for key, value := range env.values {
		plan.Environment[key] = value
	}
	if plan.Cllama {
		for key, value := range plan.Environment {
			if strings.HasSuffix(key, "_API_KEY") || strings.HasSuffix(key, "_TOKEN") {
				switch key {
				case "DISCORD_BOT_TOKEN", "SLACK_BOT_TOKEN", "SLACK_APP_TOKEN", "TELEGRAM_BOT_TOKEN":
					continue
				default:
					plan.CllamaEnv[key] = value
				}
			}
		}
	}
	notes.SecretNotes = append(notes.SecretNotes, env.secretNotes...)
	plan.Notes = notes
	return plan, nil
}

func translateModel(plan *Plan, notes *Notes, env *envBuilder) error {
	model := plan.Model
	if model.BaseURL != "" {
		notes.Action = append(notes.Action, fmt.Sprintf("model provider %q had a custom base_url; emitted CLLAMA passthrough and left upstream verification to the operator", model.Provider))
	}
	providerKey := providerEnvKey(model.Provider)
	if providerKey == "" && model.BaseURL == "" {
		return fmt.Errorf("source model provider %q is not supported for same-runtime import; pass --model <provider/model> to choose openrouter, anthropic, or openai", model.Provider)
	} else if providerKey != "" {
		apiKey := model.APIKey
		if model.BaseURL != "" {
			apiKey = ""
		}
		env.addSecret(providerKey, apiKey, "model API key")
	}
	notes.Applied = append(notes.Applied, fmt.Sprintf("MODEL primary %s", model.String()))
	keptFallbacks := plan.Fallback[:0]
	for _, fallback := range plan.Fallback {
		if key := providerEnvKey(fallback.Provider); key != "" && fallback.BaseURL == "" {
			env.addSecret(key, fallback.APIKey, "fallback model API key")
		} else if fallback.Provider != "" && fallback.BaseURL == "" {
			notes.Action = append(notes.Action, fmt.Sprintf("fallback provider %q is not supported for same-runtime import; the fallback was not emitted", fallback.Provider))
			continue
		}
		notes.Applied = append(notes.Applied, fmt.Sprintf("MODEL fallback %s", fallback.String()))
		keptFallbacks = append(keptFallbacks, fallback)
	}
	plan.Fallback = keptFallbacks
	if plan.Cllama {
		notes.Applied = append(notes.Applied, "CLLAMA passthrough")
	}
	return nil
}

func translateChannels(plan *Plan, notes *Notes, env *envBuilder) {
	src := plan.Source
	if src.Channels.Discord != nil {
		d := src.Channels.Discord
		env.addSecret("DISCORD_BOT_TOKEN", firstNonEmpty(d.Token, src.EnvVars["DISCORD_BOT_TOKEN"]), "Discord bot token")
		env.addSecret("DISCORD_BOT_ID", firstNonEmpty(d.BotID, src.EnvVars["DISCORD_BOT_ID"]), "Discord bot id")
		plan.Handles = append(plan.Handles, HandlePlan{Platform: "discord", IDEnv: "DISCORD_BOT_ID", Username: plan.AgentName, Guilds: d.Guilds})
		notes.Applied = append(notes.Applied, "HANDLE discord")
		switch plan.Target {
		case TargetOpenClaw:
			if len(d.AllowFrom) > 0 || d.DMPolicy != "" || len(d.Guilds) > 0 {
				plan.Surfaces = append(plan.Surfaces, SurfacePlan{
					Platform: "discord",
					Discord:  &DiscordSurface{DMAllowFrom: d.AllowFrom, DMPolicy: d.DMPolicy, Guilds: d.Guilds},
				})
				notes.Applied = append(notes.Applied, "Discord routing mapped to channel://discord")
			}
		case TargetHermes:
			if len(d.AllowFrom) > 0 {
				env.addLiteral("DISCORD_ALLOWED_USERS", strings.Join(d.AllowFrom, ","))
				notes.Applied = append(notes.Applied, "DISCORD_ALLOWED_USERS preserved")
			}
			if d.RequireMention {
				env.addLiteral("DISCORD_REQUIRE_MENTION", "true")
			}
			if d.DMPolicy != "" && d.DMPolicy != "pairing" {
				notes.Action = append(notes.Action, fmt.Sprintf("Discord dmPolicy %q has no Hermes equivalent and was not preserved", d.DMPolicy))
			}
		}
	}
	if src.Channels.Slack != nil {
		s := src.Channels.Slack
		env.addSecret("SLACK_BOT_TOKEN", firstNonEmpty(s.BotToken, src.EnvVars["SLACK_BOT_TOKEN"]), "Slack bot token")
		if plan.Target == TargetHermes {
			env.addSecret("SLACK_APP_TOKEN", firstNonEmpty(s.AppToken, src.EnvVars["SLACK_APP_TOKEN"]), "Slack app token")
		}
		env.addSecret("SLACK_BOT_ID", firstNonEmpty(s.BotID, src.EnvVars["SLACK_BOT_ID"]), "Slack bot id")
		plan.Handles = append(plan.Handles, HandlePlan{Platform: "slack", IDEnv: "SLACK_BOT_ID", Username: plan.AgentName})
		notes.Applied = append(notes.Applied, "HANDLE slack")
		if len(s.AllowedUsers) > 0 {
			if plan.Target == TargetHermes {
				env.addLiteral("SLACK_ALLOWED_USERS", strings.Join(s.AllowedUsers, ","))
				notes.Applied = append(notes.Applied, "SLACK_ALLOWED_USERS preserved")
			} else {
				notes.Action = append(notes.Action, "Slack allowed-user routing has no channel://slack surface in v1 and was not preserved")
			}
		}
	}
	if src.Channels.Telegram != nil {
		t := src.Channels.Telegram
		env.addSecret("TELEGRAM_BOT_TOKEN", firstNonEmpty(t.Token, src.EnvVars["TELEGRAM_BOT_TOKEN"]), "Telegram bot token")
		env.addSecret("TELEGRAM_BOT_ID", firstNonEmpty(t.BotID, src.EnvVars["TELEGRAM_BOT_ID"]), "Telegram bot id")
		plan.Handles = append(plan.Handles, HandlePlan{Platform: "telegram", IDEnv: "TELEGRAM_BOT_ID", Username: plan.AgentName})
		notes.Applied = append(notes.Applied, "HANDLE telegram")
	}
}

func baseImageForTarget(target TargetRuntime) string {
	switch target {
	case TargetHermes:
		return hermes.BaseImageTag
	default:
		return "openclaw:latest"
	}
}

func targetForSource(source SourceKind) (TargetRuntime, error) {
	switch source {
	case SourceOpenClaw:
		return TargetOpenClaw, nil
	case SourceHermes:
		return TargetHermes, nil
	default:
		return "", fmt.Errorf("unsupported import source %q", source)
	}
}

func isCllamaForced(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "yes", "true", "1", "passthrough":
		return true
	default:
		return false
	}
}

func isCllamaDisabled(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "no", "false", "0":
		return true
	default:
		return false
	}
}

func defaultImportedContract(src Descriptor, target TargetRuntime) string {
	var b strings.Builder
	b.WriteString("# Agent Contract\n\n")
	if strings.TrimSpace(src.Identity) != "" {
		b.WriteString(strings.TrimSpace(src.Identity))
		b.WriteString("\n\n")
	}
	b.WriteString("You are an imported ")
	b.WriteString(string(target))
	b.WriteString(" agent managed by Clawdapus. Follow the source identity and the generated migration notes until the operator refines this contract.\n")
	return b.String()
}

func sourceSoul(src Descriptor) string {
	if strings.TrimSpace(src.Identity) == "" {
		return ""
	}
	if strings.Contains(src.Identity, "\n") || strings.HasPrefix(strings.TrimSpace(src.Identity), "#") {
		return strings.TrimRight(src.Identity, "\n") + "\n"
	}
	return "# Imported Identity\n\n" + strings.TrimSpace(src.Identity) + "\n"
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func modelRefStrings(models []ModelRef) []string {
	out := make([]string, 0, len(models))
	for _, model := range models {
		if ref := strings.TrimSpace(model.String()); ref != "" {
			out = append(out, ref)
		}
	}
	return out
}
