package initimport

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDetectAmbiguousSourceRequiresOverride(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "openclaw.json"), `{"channels":{}}`)
	mustWrite(t, filepath.Join(dir, "config.yaml"), "model:\n  provider: openrouter\n  default: anthropic/claude-sonnet-4\n")

	if _, err := Detect(dir, ""); err == nil {
		t.Fatal("expected ambiguous source to fail")
	}
	desc, err := Detect(dir, SourceHermes)
	if err != nil {
		t.Fatalf("override should select Hermes: %v", err)
	}
	if desc.Kind != SourceHermes {
		t.Fatalf("expected Hermes descriptor, got %q", desc.Kind)
	}
}

func TestTranslateOpenClawSlackRoutingWritesActionNote(t *testing.T) {
	src := Descriptor{
		Kind:      SourceOpenClaw,
		AgentName: "assistant",
		Models:    ModelSlots{Primary: ModelRef{Provider: "openrouter", Model: "anthropic/claude-sonnet-4"}},
		Channels: Channels{Slack: &SlackChannel{
			BotToken:     "${SLACK_BOT_TOKEN}",
			AppToken:     "${SLACK_APP_TOKEN}",
			BotID:        "${SLACK_BOT_ID}",
			AllowedUsers: []string{"U111"},
		}},
	}
	plan, err := Translate(src, Options{ProjectName: "demo"})
	if err != nil {
		t.Fatalf("unexpected translate error: %v", err)
	}
	migration := renderMigration(plan)
	if !strings.Contains(migration, "Slack allowed-user routing has no channel://slack surface") {
		t.Fatalf("expected Slack routing action note, got:\n%s", migration)
	}
}

func TestTranslateProxyModelEmitsCllama(t *testing.T) {
	src := Descriptor{
		Kind:      SourceOpenClaw,
		AgentName: "assistant",
		Models: ModelSlots{Primary: ModelRef{
			Provider: "openrouter",
			Model:    "anthropic/claude-sonnet-4",
			BaseURL:  "http://cllama:8080/v1",
		}},
	}
	plan, err := Translate(src, Options{ProjectName: "demo"})
	if err != nil {
		t.Fatalf("unexpected translate error: %v", err)
	}
	if !plan.Cllama {
		t.Fatal("expected proxy source model to emit cllama")
	}
}

func TestTranslateRejectsCllamaNoWithProxySource(t *testing.T) {
	src := Descriptor{
		Kind: SourceOpenClaw,
		Models: ModelSlots{Primary: ModelRef{
			Provider: "openrouter",
			Model:    "anthropic/claude-sonnet-4",
			BaseURL:  "http://proxy.example/v1",
		}},
	}

	_, err := Translate(src, Options{ProjectName: "demo", CllamaOverride: "0"})
	if err == nil {
		t.Fatal("expected --cllama=no/0 to fail with proxy source")
	}
	if !strings.Contains(err.Error(), "--cllama=no") {
		t.Fatalf("expected clear cllama error, got: %v", err)
	}
}

func TestTranslateCronIsMigrationAction(t *testing.T) {
	src := Descriptor{
		Kind:    SourceHermes,
		CronDir: "/tmp/source-cron",
		Models:  ModelSlots{Primary: ModelRef{Provider: "openrouter", Model: "anthropic/claude-sonnet-4"}},
		Channels: Channels{Discord: &DiscordChannel{
			Token: "${DISCORD_BOT_TOKEN}",
			BotID: "${DISCORD_BOT_ID}",
		}},
	}

	plan, err := Translate(src, Options{ProjectName: "demo"})
	if err != nil {
		t.Fatalf("unexpected translate error: %v", err)
	}
	migration := renderMigration(plan)
	if !strings.Contains(migration, "cron files copied to imported/cron/") {
		t.Fatalf("expected cron action note, got:\n%s", migration)
	}
}

func TestTranslateCustomProviderRequiresModelOverride(t *testing.T) {
	src := Descriptor{
		Kind:   SourceHermes,
		Models: ModelSlots{Primary: ModelRef{Provider: "mistral-ai", Model: "large"}},
	}

	_, err := Translate(src, Options{ProjectName: "demo"})
	if err == nil {
		t.Fatal("expected custom provider to fail")
	}
	if !strings.Contains(err.Error(), "pass --model") {
		t.Fatalf("expected --model recovery hint, got: %v", err)
	}
}

func TestTranslateFallbackModelsEmitClawfileLines(t *testing.T) {
	src := Descriptor{
		Kind: SourceOpenClaw,
		Models: ModelSlots{
			Primary: ModelRef{Provider: "openrouter", Model: "anthropic/claude-sonnet-4"},
			Fallback: []ModelRef{
				{Provider: "anthropic", Model: "claude-haiku-3-5"},
				{Provider: "openai", Model: "gpt-4.1-mini"},
			},
		},
	}

	plan, err := Translate(src, Options{ProjectName: "demo"})
	if err != nil {
		t.Fatalf("unexpected translate error: %v", err)
	}
	clawfile := renderClawfile(plan)
	if !strings.Contains(clawfile, "MODEL fallback anthropic/claude-haiku-3-5") {
		t.Fatalf("expected fallback model in Clawfile, got:\n%s", clawfile)
	}
	if strings.Contains(clawfile, "fallback_2") {
		t.Fatalf("expected additional fallbacks to stay out of Clawfile, got:\n%s", clawfile)
	}
	if got := plan.Environment["ANTHROPIC_API_KEY"]; got != "${ANTHROPIC_API_KEY}" {
		t.Fatalf("expected fallback provider key placeholder, got %q", got)
	}
	if _, ok := plan.Environment["OPENAI_API_KEY"]; ok {
		t.Fatal("did not expect placeholder for additional fallback that current runtimes ignore")
	}
	migration := renderMigration(plan)
	if !strings.Contains(migration, "additional source fallback models") || !strings.Contains(migration, "openai/gpt-4.1-mini") {
		t.Fatalf("expected additional fallback migration note, got:\n%s", migration)
	}
}

func TestTranslateUnsupportedFallbackProviderIsNotEmitted(t *testing.T) {
	src := Descriptor{
		Kind: SourceOpenClaw,
		Models: ModelSlots{
			Primary:  ModelRef{Provider: "openrouter", Model: "anthropic/claude-sonnet-4"},
			Fallback: []ModelRef{{Provider: "mistral-ai", Model: "large"}},
		},
	}

	plan, err := Translate(src, Options{ProjectName: "demo"})
	if err != nil {
		t.Fatalf("unexpected translate error: %v", err)
	}
	clawfile := renderClawfile(plan)
	if strings.Contains(clawfile, "MODEL fallback mistral-ai/large") {
		t.Fatalf("unsupported fallback should not be emitted, got:\n%s", clawfile)
	}
	migration := renderMigration(plan)
	if !strings.Contains(migration, `fallback provider "mistral-ai" is not supported`) {
		t.Fatalf("expected unsupported fallback migration note, got:\n%s", migration)
	}
}

func TestReadOpenClawDiscordRequireMentionAndSortedGuilds(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "openclaw.json"), `{
		"channels": {
			"discord": {
				"enabled": true,
				"requireMention": true,
				"guilds": {
					"z-guild": {"users": ["9"]},
					"a-guild": {"requireMention": true, "users": ["1"]}
				}
			}
		}
	}`)

	desc, err := readOpenClaw(filepath.Join(dir, "openclaw.json"))
	if err != nil {
		t.Fatalf("read openclaw: %v", err)
	}
	discord := desc.Channels.Discord
	if discord == nil || !discord.RequireMention {
		t.Fatalf("expected root requireMention to be read, got %#v", discord)
	}
	if len(discord.Guilds) != 2 || discord.Guilds[0].ID != "a-guild" || discord.Guilds[1].ID != "z-guild" {
		t.Fatalf("expected sorted guilds, got %#v", discord.Guilds)
	}
}

func TestReadHermesFoldsEnvIdentityWithSoulAndNotesToolsets(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "config.yaml"), `model:
  provider: openrouter
  default: anthropic/claude-sonnet-4
platform_toolsets:
  slack: true
`)
	mustWrite(t, filepath.Join(dir, "SOUL.md"), "# Soul Identity\n\nSoul body.\n")
	mustWrite(t, filepath.Join(dir, ".env"), "HERMES_DEFAULT_AGENT_IDENTITY=Env identity.\n")

	desc, err := readHermes(filepath.Join(dir, "config.yaml"))
	if err != nil {
		t.Fatalf("read hermes: %v", err)
	}
	for _, expected := range []string{"Soul body.", "Imported Hermes Default Identity", "Env identity."} {
		if !strings.Contains(desc.Identity, expected) {
			t.Fatalf("expected identity to contain %q, got:\n%s", expected, desc.Identity)
		}
	}
	if len(desc.RawNotes) == 0 || !strings.Contains(desc.RawNotes[0], "platform_toolsets") {
		t.Fatalf("expected platform_toolsets RawNotes entry, got %#v", desc.RawNotes)
	}
}

func TestEmitCanonicalLayoutAndCronReferences(t *testing.T) {
	srcDir := t.TempDir()
	cronDir := filepath.Join(srcDir, "cron")
	if err := os.MkdirAll(cronDir, 0o755); err != nil {
		t.Fatalf("create cron dir: %v", err)
	}
	mustWrite(t, filepath.Join(cronDir, "daily.yaml"), "schedule: '* * * * *'\n")
	plan := Plan{
		Source:        Descriptor{Kind: SourceHermes, Config: filepath.Join(srcDir, "config.yaml")},
		Target:        TargetHermes,
		ProjectName:   "demo",
		AgentName:     "assistant",
		BaseImage:     "hermes-base:test",
		Model:         ModelRef{Provider: "openrouter", Model: "anthropic/claude-sonnet-4"},
		Handles:       []HandlePlan{{Platform: "slack", IDEnv: "SLACK_BOT_ID", Username: "assistant"}},
		Environment:   map[string]string{"SLACK_BOT_TOKEN": "${SLACK_BOT_TOKEN}", "SLACK_APP_TOKEN": "${SLACK_APP_TOKEN}", "SLACK_BOT_ID": "${SLACK_BOT_ID}"},
		AgentContract: "# Agent Contract\n",
		CronDir:       cronDir,
	}
	dest := t.TempDir()
	if err := Emit(plan, dest); err != nil {
		t.Fatalf("emit: %v", err)
	}
	for _, path := range []string{
		"agents/assistant/Clawfile",
		"agents/assistant/AGENTS.md",
		"claw-pod.yml",
		"MIGRATION.md",
		"imported/cron/daily.yaml",
	} {
		if _, err := os.Stat(filepath.Join(dest, path)); err != nil {
			t.Fatalf("expected %s: %v", path, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dest, "Clawfile")); !os.IsNotExist(err) {
		t.Fatalf("expected no legacy root Clawfile, got %v", err)
	}
}

func TestEnvBuilderRedactsLiteralSecret(t *testing.T) {
	env := newEnvBuilder()
	key := env.addSecret("SLACK_BOT_TOKEN", "xoxb-secret", "slack")
	if key != "SLACK_BOT_TOKEN" {
		t.Fatalf("unexpected key %q", key)
	}
	if got := env.values["SLACK_BOT_TOKEN"]; got != "${SLACK_BOT_TOKEN}" {
		t.Fatalf("expected placeholder, got %q", got)
	}
	if len(env.secretNotes) == 0 || !strings.Contains(env.secretNotes[0], "literal secret") {
		t.Fatalf("expected literal secret note, got %#v", env.secretNotes)
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
