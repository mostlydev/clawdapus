package hermes

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mostlydev/clawdapus/internal/driver"
)

func TestDriverRegistered(t *testing.T) {
	d, err := driver.Lookup("hermes")
	if err != nil {
		t.Fatalf("hermes driver not registered: %v", err)
	}
	if d == nil {
		t.Fatal("hermes driver is nil")
	}
}

func TestValidateRequiresAgentPath(t *testing.T) {
	d := &Driver{}
	rc := &driver.ResolvedClaw{
		ServiceName: "hermes",
		Models:      map[string]string{"primary": "openrouter/anthropic/claude-sonnet-4"},
		Handles:     map[string]*driver.HandleInfo{"discord": {}},
		Environment: map[string]string{
			"DISCORD_BOT_TOKEN":  "discord-token",
			"OPENROUTER_API_KEY": "or-key",
		},
	}

	err := d.Validate(rc)
	if err == nil {
		t.Fatal("expected error for missing agent host path")
	}
	if !strings.Contains(err.Error(), "no agent host path") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateRejectsUnsupportedHandle(t *testing.T) {
	rc, _ := newTestRC(t)
	rc.Handles = map[string]*driver.HandleInfo{
		"discord": {},
		"signal":  {},
	}

	err := (&Driver{}).Validate(rc)
	if err == nil {
		t.Fatal("expected unsupported HANDLE validation error")
	}
	if !strings.Contains(err.Error(), "not supported") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateRequiresSupportedHandle(t *testing.T) {
	rc, _ := newTestRC(t)
	rc.Handles = map[string]*driver.HandleInfo{}

	err := (&Driver{}).Validate(rc)
	if err == nil {
		t.Fatal("expected supported HANDLE validation error")
	}
	if !strings.Contains(err.Error(), "no supported HANDLE platforms") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateSlackRequiresAppToken(t *testing.T) {
	rc, _ := newTestRC(t)
	rc.Handles = map[string]*driver.HandleInfo{"slack": {}}
	delete(rc.Environment, "SLACK_APP_TOKEN")

	err := (&Driver{}).Validate(rc)
	if err == nil {
		t.Fatal("expected missing SLACK_APP_TOKEN validation error")
	}
	if !strings.Contains(err.Error(), "SLACK_BOT_TOKEN and SLACK_APP_TOKEN") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateRejectsInvalidCron(t *testing.T) {
	rc, _ := newTestRC(t)
	rc.Invocations = []driver.Invocation{{Schedule: "@hourly", Message: "Ping"}}

	err := (&Driver{}).Validate(rc)
	if err == nil {
		t.Fatal("expected invalid cron validation error")
	}
	if !strings.Contains(err.Error(), "invalid cron expression") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestMaterializeRejectsEmptyCllamaToken(t *testing.T) {
	rc, tmp := newTestRC(t)
	rc.Cllama = []string{"passthrough"}
	rc.CllamaToken = ""

	// Validate should pass — cllama token is not yet available at validation time
	// (the two-pass compose-up generates it between Validate and Materialize).
	if err := (&Driver{}).Validate(rc); err != nil {
		t.Fatalf("Validate should pass with empty cllama token: %v", err)
	}

	runtimeDir := filepath.Join(tmp, "runtime")
	if err := os.MkdirAll(runtimeDir, 0o700); err != nil {
		t.Fatal(err)
	}
	_, err := (&Driver{}).Materialize(rc, driver.MaterializeOpts{RuntimeDir: runtimeDir, PodName: "test"})
	if err == nil {
		t.Fatal("expected Materialize to reject empty CLLAMA token")
	}
	if !strings.Contains(err.Error(), "CLLAMA is enabled but token is empty") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestMaterializeWritesRuntimeLayout(t *testing.T) {
	rc, tmp := newTestRC(t)
	rc.PersonaHostPath = ""
	rc.Invocations = []driver.Invocation{
		{Schedule: "*/5 * * * *", Message: "Ping status", Name: "status"},
	}

	runtimeDir := filepath.Join(tmp, "runtime")
	if err := os.MkdirAll(runtimeDir, 0o700); err != nil {
		t.Fatal(err)
	}

	result, err := (&Driver{}).Materialize(rc, driver.MaterializeOpts{RuntimeDir: runtimeDir, PodName: "fleet-echo"})
	if err != nil {
		t.Fatalf("Materialize returned error: %v", err)
	}

	if _, err := os.Stat(filepath.Join(runtimeDir, "hermes-home", "config.yaml")); err != nil {
		t.Fatalf("expected config.yaml: %v", err)
	}
	if _, err := os.Stat(filepath.Join(runtimeDir, "hermes-home", ".env")); err != nil {
		t.Fatalf("expected .env: %v", err)
	}
	if _, err := os.Stat(filepath.Join(runtimeDir, "hermes-home", "cron", "jobs.json")); err != nil {
		t.Fatalf("expected jobs.json: %v", err)
	}
	agentsData, err := os.ReadFile(filepath.Join(runtimeDir, "workspace", "AGENTS.md"))
	if err != nil {
		t.Fatalf("read AGENTS.md: %v", err)
	}
	if !strings.Contains(string(agentsData), "You are Hermes.") {
		t.Fatalf("expected original contract content in AGENTS.md")
	}
	if !strings.Contains(string(agentsData), "fleet-echo") {
		t.Fatalf("expected pod context in AGENTS.md")
	}

	if result.ReadOnly != true {
		t.Fatal("expected Hermes to run with read-only rootfs")
	}
	if result.SkillDir != hermesHomeDir+"/skills" {
		t.Fatalf("unexpected SkillDir: %q", result.SkillDir)
	}
	if result.SkillLayout != "directory" {
		t.Fatalf("unexpected SkillLayout: %q", result.SkillLayout)
	}
	if result.Environment["HERMES_HOME"] != hermesHomeDir {
		t.Fatalf("unexpected HERMES_HOME: %q", result.Environment["HERMES_HOME"])
	}
	if result.Environment["MESSAGING_CWD"] != hermesWorkspaceDir {
		t.Fatalf("unexpected MESSAGING_CWD: %q", result.Environment["MESSAGING_CWD"])
	}
	if result.Environment["TERMINAL_CWD"] != hermesWorkspaceDir {
		t.Fatalf("unexpected TERMINAL_CWD: %q", result.Environment["TERMINAL_CWD"])
	}

	// Default SOUL.md should be written when no persona is configured
	soulData, err := os.ReadFile(filepath.Join(runtimeDir, "hermes-home", "SOUL.md"))
	if err != nil {
		t.Fatalf("expected default SOUL.md: %v", err)
	}
	soulStr := string(soulData)
	if !strings.Contains(soulStr, "# Hermes") {
		t.Fatalf("expected titlecase agent name in SOUL.md header, got: %s", soulStr[:100])
	}
	if !strings.Contains(soulStr, "fleet-echo") {
		t.Fatalf("expected pod name in SOUL.md")
	}
	if strings.Contains(soulStr, "Nous Research") {
		t.Fatal("default SOUL.md should not contain Hermes runner identity")
	}
}

func TestMaterializePersonaSoulOverridesDefault(t *testing.T) {
	rc, tmp := newTestRC(t)
	personaDir := filepath.Join(tmp, "persona")
	if err := os.MkdirAll(personaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(personaDir, "SOUL.md"), []byte("Custom persona soul"), 0o644); err != nil {
		t.Fatal(err)
	}
	rc.PersonaHostPath = personaDir

	runtimeDir := filepath.Join(tmp, "runtime")
	if err := os.MkdirAll(runtimeDir, 0o700); err != nil {
		t.Fatal(err)
	}

	_, err := (&Driver{}).Materialize(rc, driver.MaterializeOpts{RuntimeDir: runtimeDir, PodName: "test"})
	if err != nil {
		t.Fatalf("Materialize returned error: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(runtimeDir, "hermes-home", "SOUL.md"))
	if err != nil {
		t.Fatalf("expected SOUL.md: %v", err)
	}
	if string(data) != "Custom persona soul" {
		t.Fatalf("expected persona SOUL.md to override default, got: %q", string(data))
	}
}

func TestMaterializeCopiesPersonaSoul(t *testing.T) {
	rc, tmp := newTestRC(t)
	personaDir := filepath.Join(tmp, "persona")
	if err := os.MkdirAll(personaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(personaDir, "SOUL.md"), []byte("Persona soul"), 0o644); err != nil {
		t.Fatal(err)
	}
	rc.PersonaHostPath = personaDir

	runtimeDir := filepath.Join(tmp, "runtime")
	if err := os.MkdirAll(runtimeDir, 0o700); err != nil {
		t.Fatal(err)
	}

	result, err := (&Driver{}).Materialize(rc, driver.MaterializeOpts{RuntimeDir: runtimeDir})
	if err != nil {
		t.Fatalf("Materialize returned error: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(runtimeDir, "hermes-home", "SOUL.md"))
	if err != nil {
		t.Fatalf("expected copied SOUL.md: %v", err)
	}
	if string(data) != "Persona soul" {
		t.Fatalf("unexpected SOUL.md content: %q", string(data))
	}
	if result.Environment["CLAW_PERSONA_DIR"] != hermesPersonaDir {
		t.Fatalf("unexpected CLAW_PERSONA_DIR: %q", result.Environment["CLAW_PERSONA_DIR"])
	}
}

func TestCopyPersonaSoulNoopWhenMissing(t *testing.T) {
	personaDir := t.TempDir() // no SOUL.md inside
	homeDir := t.TempDir()

	if err := CopyPersonaSoul(personaDir, homeDir); err != nil {
		t.Fatalf("CopyPersonaSoul should succeed when SOUL.md is absent: %v", err)
	}
	if _, err := os.Stat(filepath.Join(homeDir, "SOUL.md")); !os.IsNotExist(err) {
		t.Fatal("expected no SOUL.md in homeDir when persona has none")
	}
}

func TestMaterializeCllamaSetsContainerEnv(t *testing.T) {
	rc, tmp := newTestRC(t)
	rc.Cllama = []string{"passthrough"}
	rc.CllamaToken = "weston:abc123"

	runtimeDir := filepath.Join(tmp, "runtime")
	if err := os.MkdirAll(runtimeDir, 0o700); err != nil {
		t.Fatal(err)
	}

	result, err := (&Driver{}).Materialize(rc, driver.MaterializeOpts{RuntimeDir: runtimeDir, PodName: "test"})
	if err != nil {
		t.Fatalf("Materialize returned error: %v", err)
	}

	if got := result.Environment["OPENAI_BASE_URL"]; got != "http://cllama:8080/v1" {
		t.Fatalf("expected OPENAI_BASE_URL=http://cllama:8080/v1, got %q", got)
	}
	if got := result.Environment["OPENAI_API_KEY"]; got != "weston:abc123" {
		t.Fatalf("expected OPENAI_API_KEY=weston:abc123, got %q", got)
	}

	// Verify config.yaml also has the routing
	configData, err := os.ReadFile(filepath.Join(runtimeDir, "hermes-home", "config.yaml"))
	if err != nil {
		t.Fatalf("read config.yaml: %v", err)
	}
	cfg := string(configData)
	if !strings.Contains(cfg, "base_url: http://cllama:8080/v1") {
		t.Fatalf("expected base_url in config.yaml, got:\n%s", cfg)
	}
	if !strings.Contains(cfg, "api_key: weston:abc123") {
		t.Fatalf("expected api_key in config.yaml, got:\n%s", cfg)
	}
	if !strings.Contains(cfg, "provider: custom") {
		t.Fatalf("expected custom provider in config.yaml, got:\n%s", cfg)
	}
}

func TestMaterializeDefaultsAutoThreadOff(t *testing.T) {
	rc, tmp := newTestRC(t)
	runtimeDir := filepath.Join(tmp, "runtime")
	if err := os.MkdirAll(runtimeDir, 0o700); err != nil {
		t.Fatal(err)
	}

	result, err := (&Driver{}).Materialize(rc, driver.MaterializeOpts{RuntimeDir: runtimeDir, PodName: "test"})
	if err != nil {
		t.Fatalf("Materialize returned error: %v", err)
	}

	if got := result.Environment["DISCORD_AUTO_THREAD"]; got != "false" {
		t.Fatalf("expected DISCORD_AUTO_THREAD=false, got %q", got)
	}
}

func newTestRC(t *testing.T) (*driver.ResolvedClaw, string) {
	t.Helper()
	tmp := t.TempDir()
	agentPath := filepath.Join(tmp, "AGENTS.md")
	if err := os.WriteFile(agentPath, []byte("# Agent\n\nYou are Hermes."), 0o644); err != nil {
		t.Fatal(err)
	}

	return &driver.ResolvedClaw{
		ServiceName:   "hermes",
		ClawType:      "hermes",
		AgentHostPath: agentPath,
		Models:        map[string]string{"primary": "openrouter/anthropic/claude-sonnet-4"},
		Handles: map[string]*driver.HandleInfo{
			"discord": {},
		},
		Environment: map[string]string{
			"DISCORD_BOT_TOKEN":  "discord-token",
			"OPENROUTER_API_KEY": "or-key",
			"SLACK_BOT_TOKEN":    "slack-bot",
			"SLACK_APP_TOKEN":    "slack-app",
		},
	}, tmp
}
