package nullclaw

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mostlydev/clawdapus/internal/driver"
)

func TestDriverRegistered(t *testing.T) {
	d, err := driver.Lookup("nullclaw")
	if err != nil {
		t.Fatalf("nullclaw driver not registered: %v", err)
	}
	if d == nil {
		t.Fatal("nullclaw driver is nil")
	}
}

func TestValidateRequiresAgentPath(t *testing.T) {
	d := &Driver{}
	rc := &driver.ResolvedClaw{ServiceName: "null"}
	err := d.Validate(rc)
	if err == nil {
		t.Fatal("expected error for missing agent host path")
	}
	if !strings.Contains(err.Error(), "no agent host path") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateRequiresAgentFileExists(t *testing.T) {
	d := &Driver{}
	rc := &driver.ResolvedClaw{
		ServiceName:   "null",
		AgentHostPath: "/path/that/does/not/exist/AGENTS.md",
	}
	if err := d.Validate(rc); err == nil {
		t.Fatal("expected error for missing agent file")
	}
}

func TestValidateAcceptsBasicConfig(t *testing.T) {
	rc, _ := newTestRC(t)
	d := &Driver{}
	if err := d.Validate(rc); err != nil {
		t.Fatalf("unexpected validation error: %v", err)
	}
}

func TestValidateDiscordHandleRequiresToken(t *testing.T) {
	rc, _ := newTestRC(t)
	rc.Handles = map[string]*driver.HandleInfo{
		"discord": {ID: "1"},
	}
	rc.Environment = map[string]string{}

	d := &Driver{}
	err := d.Validate(rc)
	if err == nil {
		t.Fatal("expected error for missing DISCORD_BOT_TOKEN")
	}
	if !strings.Contains(err.Error(), "DISCORD_BOT_TOKEN") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateTelegramHandleRequiresToken(t *testing.T) {
	rc, _ := newTestRC(t)
	rc.Handles = map[string]*driver.HandleInfo{
		"telegram": {ID: "1"},
	}
	rc.Environment = map[string]string{}

	d := &Driver{}
	err := d.Validate(rc)
	if err == nil {
		t.Fatal("expected error for missing TELEGRAM_BOT_TOKEN")
	}
	if !strings.Contains(err.Error(), "TELEGRAM_BOT_TOKEN") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateSlackHandleRequiresToken(t *testing.T) {
	rc, _ := newTestRC(t)
	rc.Handles = map[string]*driver.HandleInfo{
		"slack": {ID: "1"},
	}
	rc.Environment = map[string]string{}

	d := &Driver{}
	err := d.Validate(rc)
	if err == nil {
		t.Fatal("expected error for missing SLACK_BOT_TOKEN")
	}
	if !strings.Contains(err.Error(), "SLACK_BOT_TOKEN") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateRejectsInvalidConfigureCommand(t *testing.T) {
	rc, _ := newTestRC(t)
	rc.Configures = []string{"some random command"}

	d := &Driver{}
	err := d.Validate(rc)
	if err == nil {
		t.Fatal("expected error for invalid CONFIGURE command")
	}
	if !strings.Contains(err.Error(), "CONFIGURE") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestMaterializeWritesConfig(t *testing.T) {
	rc, tmp := newTestRC(t)

	d := &Driver{}
	_, err := d.Materialize(rc, driver.MaterializeOpts{RuntimeDir: tmp, PodName: "pod-a"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	configPath := filepath.Join(tmp, "nullclaw-home", "config.json")
	if _, err := os.Stat(configPath); err != nil {
		t.Fatalf("expected config at %s: %v", configPath, err)
	}
}

func TestMaterializeMounts(t *testing.T) {
	rc, tmp := newTestRC(t)

	d := &Driver{}
	result, err := d.Materialize(rc, driver.MaterializeOpts{RuntimeDir: tmp, PodName: "pod-a"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var homeMount *driver.Mount
	var imageHomeMount *driver.Mount
	var agentMount *driver.Mount
	var clawdapusMount *driver.Mount
	for i := range result.Mounts {
		m := &result.Mounts[i]
		switch m.ContainerPath {
		case "/root/.nullclaw":
			homeMount = m
		case "/nullclaw-data/.nullclaw":
			imageHomeMount = m
		case "/claw/AGENTS.md":
			agentMount = m
		case "/claw/CLAWDAPUS.md":
			clawdapusMount = m
		}
	}

	if homeMount == nil || homeMount.ReadOnly {
		t.Fatal("expected writable /root/.nullclaw mount")
	}
	if imageHomeMount == nil || imageHomeMount.ReadOnly {
		t.Fatal("expected writable /nullclaw-data/.nullclaw mount")
	}
	if agentMount == nil || !agentMount.ReadOnly {
		t.Fatal("expected readonly /claw/AGENTS.md mount")
	}
	if clawdapusMount == nil || !clawdapusMount.ReadOnly {
		t.Fatal("expected readonly /claw/CLAWDAPUS.md mount")
	}
}

func TestMaterializeEnvironment(t *testing.T) {
	rc, tmp := newTestRC(t)

	d := &Driver{}
	result, err := d.Materialize(rc, driver.MaterializeOpts{RuntimeDir: tmp})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Environment["CLAW_MANAGED"] != "true" {
		t.Fatalf("expected CLAW_MANAGED=true, got %q", result.Environment["CLAW_MANAGED"])
	}
}

func TestMaterializeHealthcheck(t *testing.T) {
	rc, tmp := newTestRC(t)

	d := &Driver{}
	result, err := d.Materialize(rc, driver.MaterializeOpts{RuntimeDir: tmp})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Healthcheck == nil || len(result.Healthcheck.Test) == 0 {
		t.Fatal("expected healthcheck config")
	}
	joined := strings.Join(result.Healthcheck.Test, " ")
	if !strings.Contains(joined, "/health") {
		t.Fatalf("expected /health in healthcheck command, got %q", joined)
	}
}

func TestMaterializeSkillDir(t *testing.T) {
	rc, tmp := newTestRC(t)

	d := &Driver{}
	result, err := d.Materialize(rc, driver.MaterializeOpts{RuntimeDir: tmp})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.SkillDir != "/claw/skills" {
		t.Fatalf("unexpected skill dir: %q", result.SkillDir)
	}
	if result.SkillLayout != "" {
		t.Fatalf("unexpected skill layout: %q", result.SkillLayout)
	}
}

func TestMaterializeCllamaConfig(t *testing.T) {
	rc, tmp := newTestRC(t)
	rc.Models = map[string]string{
		"primary": "anthropic/claude-sonnet-4",
	}
	rc.Cllama = []string{"passthrough"}
	rc.CllamaToken = "token-a"

	d := &Driver{}
	if _, err := d.Materialize(rc, driver.MaterializeOpts{RuntimeDir: tmp}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	configPath := filepath.Join(tmp, "nullclaw-home", "config.json")
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if !strings.Contains(string(data), "http://cllama-passthrough:8080/v1") {
		t.Fatalf("expected cllama base_url in config: %s", string(data))
	}
	if !strings.Contains(string(data), "\"api_key\": \"token-a\"") {
		t.Fatalf("expected cllama api_key in config: %s", string(data))
	}
}

func TestPostApplyRequiresContainerID(t *testing.T) {
	rc, _ := newTestRC(t)
	d := &Driver{}
	err := d.PostApply(rc, driver.PostApplyOpts{})
	if err == nil {
		t.Fatal("expected error for missing container ID")
	}
}

func TestBuildInvocationCommand(t *testing.T) {
	cmd, err := buildInvocationCommand("hello 'world'")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := "nullclaw agent -m 'hello '\"'\"'world'\"'\"''"
	if cmd != expected {
		t.Fatalf("unexpected command:\nwant: %s\ngot:  %s", expected, cmd)
	}
}

func TestBuildInvocationCommandEmptyMessage(t *testing.T) {
	_, err := buildInvocationCommand("   ")
	if err == nil {
		t.Fatal("expected error for empty message")
	}
}

func TestBuildCronAddArgs(t *testing.T) {
	args := buildCronAddArgs("*/5 * * * *", "nullclaw agent -m 'hello'")
	want := []string{"nullclaw", "cron", "add", "*/5 * * * *", "nullclaw agent -m 'hello'"}
	if len(args) != len(want) {
		t.Fatalf("unexpected args len: %#v", args)
	}
	for i := range want {
		if args[i] != want[i] {
			t.Fatalf("unexpected arg[%d]: want %q got %q", i, want[i], args[i])
		}
	}
}

func TestParseCronListOutput(t *testing.T) {
	text := `
info(cron): Scheduled jobs (2):
info(cron): - job-1 | */5 * * * * | next=1740700000 | status=n/a cmd: nullclaw agent -m 'hello'
info(cron): - job-2 | 0 9 * * 1-5 | next=1740701000 | status=n/a cmd: nullclaw agent -m 'market open'
`
	parsed := parseCronListOutput(text)
	if len(parsed) != 2 {
		t.Fatalf("expected 2 parsed cron jobs, got %d", len(parsed))
	}
	if _, ok := parsed[cronEntryKey("*/5 * * * *", "nullclaw agent -m 'hello'")]; !ok {
		t.Fatalf("missing expected key for first cron job: %#v", parsed)
	}
	if _, ok := parsed[cronEntryKey("0 9 * * 1-5", "nullclaw agent -m 'market open'")]; !ok {
		t.Fatalf("missing expected key for second cron job: %#v", parsed)
	}
}

func newTestRC(t *testing.T) (*driver.ResolvedClaw, string) {
	t.Helper()
	tmp := t.TempDir()
	agentPath := filepath.Join(tmp, "AGENTS.md")
	if err := os.WriteFile(agentPath, []byte("# Agent\n\nYou are nullclaw."), 0o644); err != nil {
		t.Fatal(err)
	}

	rc := &driver.ResolvedClaw{
		ServiceName:   "null",
		ClawType:      "nullclaw",
		AgentHostPath: agentPath,
		Models: map[string]string{
			"primary": "anthropic/claude-sonnet-4",
		},
		Environment: map[string]string{},
	}
	return rc, tmp
}
