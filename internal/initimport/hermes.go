package initimport

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

func readHermes(configPath string) (Descriptor, error) {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return Descriptor{}, fmt.Errorf("read Hermes config: %w", err)
	}
	var raw map[string]any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return Descriptor{}, fmt.Errorf("parse Hermes config.yaml: %w", err)
	}

	root := filepath.Dir(configPath)
	env, err := readDotEnv(filepath.Join(root, ".env"))
	if err != nil {
		return Descriptor{}, fmt.Errorf("read source .env: %w", err)
	}

	desc := Descriptor{
		Kind:      SourceHermes,
		Config:    configPath,
		Root:      root,
		AgentName: "assistant",
		EnvVars:   env,
	}

	if soulPath := filepath.Join(root, "SOUL.md"); fileExists(soulPath) {
		soul, err := os.ReadFile(soulPath)
		if err != nil {
			return Descriptor{}, fmt.Errorf("read SOUL.md: %w", err)
		}
		desc.Identity = strings.TrimSpace(string(soul))
		if heading := firstMarkdownHeading(desc.Identity); heading != "" {
			desc.AgentName = normalizeName(heading, "assistant")
		}
	}
	if value := strings.TrimSpace(env["HERMES_DEFAULT_AGENT_IDENTITY"]); value != "" {
		if desc.Identity != "" {
			desc.Identity = strings.TrimRight(desc.Identity, "\n") + "\n\n# Imported Hermes Default Identity\n\n" + value
		} else {
			desc.Identity = value
		}
	}

	model, _ := raw["model"].(map[string]any)
	provider, _ := model["provider"].(string)
	defaultModel, _ := model["default"].(string)
	baseURL, _ := model["base_url"].(string)
	apiKey, _ := model["api_key"].(string)
	desc.Models.Primary = hermesModelRef(provider, defaultModel, baseURL, apiKey)
	if desc.Models.Primary.Provider == "" {
		desc.Models.Primary = ModelRef{Provider: "openrouter", Model: "anthropic/claude-sonnet-5"}
	}
	if strings.TrimSpace(baseURL) != "" {
		desc.Cllama = true
	}

	if env["DISCORD_BOT_TOKEN"] != "" || env["DISCORD_BOT_ID"] != "" || env["DISCORD_ALLOWED_USERS"] != "" {
		desc.Channels.Discord = &DiscordChannel{
			Token:          env["DISCORD_BOT_TOKEN"],
			BotID:          env["DISCORD_BOT_ID"],
			RequireMention: strings.EqualFold(env["DISCORD_REQUIRE_MENTION"], "true"),
			AllowFrom:      stringSlice(env["DISCORD_ALLOWED_USERS"]),
		}
	}
	if env["SLACK_BOT_TOKEN"] != "" || env["SLACK_APP_TOKEN"] != "" || env["SLACK_BOT_ID"] != "" || env["SLACK_ALLOWED_USERS"] != "" {
		desc.Channels.Slack = &SlackChannel{
			BotToken:     env["SLACK_BOT_TOKEN"],
			AppToken:     env["SLACK_APP_TOKEN"],
			BotID:        env["SLACK_BOT_ID"],
			AllowedUsers: stringSlice(env["SLACK_ALLOWED_USERS"]),
		}
	}
	if env["TELEGRAM_BOT_TOKEN"] != "" || env["TELEGRAM_BOT_ID"] != "" {
		desc.Channels.Telegram = &TelegramChannel{Token: env["TELEGRAM_BOT_TOKEN"], BotID: env["TELEGRAM_BOT_ID"]}
	}

	if dir := filepath.Join(root, "skills"); dirExists(dir) {
		desc.SkillsDir = dir
	}
	if dir := filepath.Join(root, "cron"); dirExists(dir) {
		desc.CronDir = dir
	}
	for key := range raw {
		switch key {
		case "model", "terminal":
		default:
			desc.RawNotes = append(desc.RawNotes, fmt.Sprintf("unrecognized Hermes config key %q was not imported", key))
		}
	}
	return desc, nil
}

func hermesModelRef(provider, defaultModel, baseURL, apiKey string) ModelRef {
	provider = strings.TrimSpace(provider)
	defaultModel = strings.TrimSpace(defaultModel)
	ref := ModelRef{Provider: provider, Model: defaultModel, BaseURL: strings.TrimSpace(baseURL), APIKey: strings.TrimSpace(apiKey)}
	if provider == "custom" && strings.Contains(defaultModel, "/") {
		if parsed, ok := SplitModelRef(defaultModel); ok {
			ref.Provider = parsed.Provider
			ref.Model = parsed.Model
		}
	}
	if provider != "" && !strings.Contains(defaultModel, "/") {
		ref.Model = defaultModel
	}
	if provider == "" && strings.Contains(defaultModel, "/") {
		if parsed, ok := SplitModelRef(defaultModel); ok {
			ref.Provider = parsed.Provider
			ref.Model = parsed.Model
		}
	}
	return ref
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
