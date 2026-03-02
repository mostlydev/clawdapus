package picoclaw

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mostlydev/clawdapus/internal/driver"
)

func TestDriverRegistered(t *testing.T) {
	d, err := driver.Lookup("picoclaw")
	if err != nil {
		t.Fatalf("picoclaw driver not registered: %v", err)
	}
	if d == nil {
		t.Fatal("picoclaw driver is nil")
	}
}

func TestValidateRequiresAgentPath(t *testing.T) {
	d := &Driver{}
	rc := &driver.ResolvedClaw{
		ServiceName: "pc",
		Models:      map[string]string{"primary": "anthropic/claude-sonnet-4"},
		Handles:     map[string]*driver.HandleInfo{"discord": {}},
		Environment: map[string]string{
			"DISCORD_BOT_TOKEN": "discord-token",
			"ANTHROPIC_API_KEY": "anthropic-key",
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

func TestValidateRequiresPrimaryModel(t *testing.T) {
	rc, _ := newTestRC(t)
	rc.Models = map[string]string{}

	d := &Driver{}
	err := d.Validate(rc)
	if err == nil {
		t.Fatal("expected error for missing primary model")
	}
	if !strings.Contains(err.Error(), "MODEL primary") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateRequiresSupportedHandle(t *testing.T) {
	rc, _ := newTestRC(t)
	rc.Handles = map[string]*driver.HandleInfo{
		"matrix": {},
	}

	d := &Driver{}
	err := d.Validate(rc)
	if err == nil {
		t.Fatal("expected error for no supported HANDLE platforms")
	}
	if !strings.Contains(err.Error(), "no channels enabled") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateDiscordHandleRequiresToken(t *testing.T) {
	rc, _ := newTestRC(t)
	rc.Handles = map[string]*driver.HandleInfo{"discord": {}}
	delete(rc.Environment, "DISCORD_BOT_TOKEN")

	d := &Driver{}
	err := d.Validate(rc)
	if err == nil {
		t.Fatal("expected error for missing DISCORD_BOT_TOKEN")
	}
	if !strings.Contains(err.Error(), "DISCORD_BOT_TOKEN") {
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

func TestMaterializeWritesConfigAndSeededAgents(t *testing.T) {
	rc, tmp := newTestRC(t)
	rc.Models = map[string]string{"primary": "openrouter/anthropic/claude-sonnet-4"}
	rc.Environment["OPENROUTER_API_KEY"] = "or-key"

	runtimeDir := filepath.Join(tmp, "runtime")
	if err := os.MkdirAll(runtimeDir, 0o700); err != nil {
		t.Fatal(err)
	}

	d := &Driver{}
	result, err := d.Materialize(rc, driver.MaterializeOpts{RuntimeDir: runtimeDir, PodName: "fleet-bravo"})
	if err != nil {
		t.Fatalf("Materialize failed: %v", err)
	}

	configPath := filepath.Join(runtimeDir, "picoclaw-home", "config.json")
	if _, err := os.Stat(configPath); err != nil {
		t.Fatalf("expected config at %s: %v", configPath, err)
	}

	seedPath := filepath.Join(runtimeDir, "picoclaw-home", "workspace", "AGENTS.md")
	seeded, err := os.ReadFile(seedPath)
	if err != nil {
		t.Fatalf("read seeded AGENTS.md: %v", err)
	}
	seedStr := string(seeded)
	if !strings.Contains(seedStr, "You are picoclaw") {
		t.Fatalf("seeded AGENTS.md missing agent contract content")
	}
	if !strings.Contains(seedStr, "fleet-bravo") {
		t.Fatalf("seeded AGENTS.md missing pod context")
	}

	if result.ReadOnly != true {
		t.Fatal("picoclaw should run with read-only rootfs")
	}
	if result.SkillDir != picoclawWorkspaceDir+"/skills" {
		t.Fatalf("unexpected skill dir: %q", result.SkillDir)
	}
	if result.SkillLayout != "directory" {
		t.Fatalf("unexpected skill layout: %q", result.SkillLayout)
	}
	if result.Environment["CLAW_MANAGED"] != "true" {
		t.Fatalf("expected CLAW_MANAGED=true, got %q", result.Environment["CLAW_MANAGED"])
	}
	if result.Environment["PICOCLAW_HOME"] != picoclawHomeDir {
		t.Fatalf("expected PICOCLAW_HOME=%s, got %q", picoclawHomeDir, result.Environment["PICOCLAW_HOME"])
	}
	if result.Environment["PICOCLAW_CONFIG"] != picoclawHomeDir+"/config.json" {
		t.Fatalf("unexpected PICOCLAW_CONFIG: %q", result.Environment["PICOCLAW_CONFIG"])
	}

	var homeMount *driver.Mount
	for i := range result.Mounts {
		m := &result.Mounts[i]
		if m.ContainerPath == picoclawHomeDir {
			homeMount = m
		}
	}
	if homeMount == nil || homeMount.ReadOnly {
		t.Fatal("expected writable /home/picoclaw/.picoclaw mount")
	}
}

func TestMaterializeWritesCronJobs(t *testing.T) {
	rc, tmp := newTestRC(t)
	rc.Invocations = []driver.Invocation{
		{
			Schedule: "*/5 * * * *",
			Message:  "Ping status channels",
			Name:     "status-ping",
			To:       "channel-1",
		},
	}

	runtimeDir := filepath.Join(tmp, "runtime")
	if err := os.MkdirAll(runtimeDir, 0o700); err != nil {
		t.Fatal(err)
	}

	d := &Driver{}
	if _, err := d.Materialize(rc, driver.MaterializeOpts{RuntimeDir: runtimeDir}); err != nil {
		t.Fatalf("Materialize failed: %v", err)
	}

	cronPath := filepath.Join(runtimeDir, "picoclaw-home", "workspace", "cron", "jobs.json")
	cronBytes, err := os.ReadFile(cronPath)
	if err != nil {
		t.Fatalf("read cron jobs: %v", err)
	}

	var cron map[string]interface{}
	if err := json.Unmarshal(cronBytes, &cron); err != nil {
		t.Fatalf("parse cron json: %v", err)
	}

	if cron["version"] != float64(1) {
		t.Fatalf("expected cron version 1, got %v", cron["version"])
	}
	jobs, ok := cron["jobs"].([]interface{})
	if !ok || len(jobs) != 1 {
		t.Fatalf("expected one cron job, got %#v", cron["jobs"])
	}
	job, _ := jobs[0].(map[string]interface{})
	if job["name"] != "status-ping" {
		t.Fatalf("unexpected cron job name: %v", job["name"])
	}
}

func TestGenerateCronJobsJSONRejectsInvalidSchedule(t *testing.T) {
	_, err := generateCronJobsJSON([]driver.Invocation{
		{Schedule: "@hourly", Message: "hi"},
	})
	if err == nil {
		t.Fatal("expected cron schedule validation error")
	}
}

func TestParseProbeResponse(t *testing.T) {
	status, detail, err := parseProbeResponse(`{"status":"ok","detail":"service ready"}`)
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if status != "ok" {
		t.Fatalf("unexpected status: %q", status)
	}
	if detail != "service ready" {
		t.Fatalf("unexpected detail: %q", detail)
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

func newTestRC(t *testing.T) (*driver.ResolvedClaw, string) {
	t.Helper()
	tmp := t.TempDir()
	agentPath := filepath.Join(tmp, "AGENTS.md")
	if err := os.WriteFile(agentPath, []byte("# Agent\n\nYou are picoclaw."), 0o644); err != nil {
		t.Fatal(err)
	}

	rc := &driver.ResolvedClaw{
		ServiceName:   "picoclaw",
		ClawType:      "picoclaw",
		AgentHostPath: agentPath,
		Models:        map[string]string{"primary": "anthropic/claude-sonnet-4"},
		Handles: map[string]*driver.HandleInfo{
			"discord": {},
		},
		Environment: map[string]string{
			"DISCORD_BOT_TOKEN": "discord-token",
			"ANTHROPIC_API_KEY": "anthropic-key",
		},
	}
	return rc, tmp
}
