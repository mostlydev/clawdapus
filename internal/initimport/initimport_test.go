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

func TestTranslateSlackRoutingFatalUnlessAccepted(t *testing.T) {
	src := Descriptor{
		Kind:      SourceHermes,
		AgentName: "assistant",
		Models:    ModelSlots{Primary: ModelRef{Provider: "openrouter", Model: "anthropic/claude-sonnet-4"}},
		Channels: Channels{Slack: &SlackChannel{
			BotToken:     "${SLACK_BOT_TOKEN}",
			AppToken:     "${SLACK_APP_TOKEN}",
			BotID:        "${SLACK_BOT_ID}",
			AllowedUsers: []string{"U111"},
		}},
	}
	plan, err := Translate(src, TargetOpenClaw, Options{ProjectName: "demo"})
	if err != nil {
		t.Fatalf("unexpected translate error: %v", err)
	}
	if !plan.Notes.HasFatal() {
		t.Fatal("expected Slack routing fatal loss")
	}
	if !AcceptLossAllows([]string{"slack-routing"}, plan.Notes.FatalFeatures()) {
		t.Fatal("expected slack-routing accept token to satisfy fatal loss")
	}
}

func TestTranslateProxyModelEmitsCllama(t *testing.T) {
	src := Descriptor{
		Kind:      SourceHermes,
		AgentName: "assistant",
		Models: ModelSlots{Primary: ModelRef{
			Provider: "openrouter",
			Model:    "anthropic/claude-sonnet-4",
			BaseURL:  "http://cllama:8080/v1",
		}},
	}
	plan, err := Translate(src, TargetHermes, Options{ProjectName: "demo"})
	if err != nil {
		t.Fatalf("unexpected translate error: %v", err)
	}
	if !plan.Cllama {
		t.Fatal("expected proxy source model to emit cllama")
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
