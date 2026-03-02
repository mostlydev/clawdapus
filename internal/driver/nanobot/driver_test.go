package nanobot

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mostlydev/clawdapus/internal/driver"
)

func TestDriverRegistered(t *testing.T) {
	d, err := driver.Lookup("nanobot")
	if err != nil {
		t.Fatalf("nanobot driver not registered: %v", err)
	}
	if d == nil {
		t.Fatal("nanobot driver is nil")
	}
}

func TestValidateRequiresAgentPath(t *testing.T) {
	d := &Driver{}
	rc := &driver.ResolvedClaw{ServiceName: "nb", Models: map[string]string{"primary": "anthropic/claude-sonnet-4"}}
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

func TestValidateDiscordHandleRequiresToken(t *testing.T) {
	rc, _ := newTestRC(t)
	rc.Handles = map[string]*driver.HandleInfo{"discord": {ID: "123"}}
	rc.Environment = map[string]string{
		"ANTHROPIC_API_KEY": "anthropic-key",
	}

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
	rc.Environment["ANTHROPIC_API_KEY"] = "anthropic-key"
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
	result, err := d.Materialize(rc, driver.MaterializeOpts{RuntimeDir: runtimeDir, PodName: "fleet-alpha"})
	if err != nil {
		t.Fatalf("Materialize failed: %v", err)
	}

	configPath := filepath.Join(runtimeDir, "nanobot-home", "config.json")
	if _, err := os.Stat(configPath); err != nil {
		t.Fatalf("expected config at %s: %v", configPath, err)
	}

	seedPath := filepath.Join(runtimeDir, "nanobot-home", "workspace", "AGENTS.md")
	seeded, err := os.ReadFile(seedPath)
	if err != nil {
		t.Fatalf("read seeded AGENTS.md: %v", err)
	}
	seedStr := string(seeded)
	if !strings.Contains(seedStr, "You are nanobot") {
		t.Fatalf("seeded AGENTS.md missing agent contract content")
	}
	if !strings.Contains(seedStr, "fleet-alpha") {
		t.Fatalf("seeded AGENTS.md missing pod context")
	}

	if result.ReadOnly != true {
		t.Fatal("nanobot should run with read-only rootfs")
	}
	if result.SkillDir != "/root/.nanobot/workspace/skills" {
		t.Fatalf("unexpected skill dir: %q", result.SkillDir)
	}
	if result.SkillLayout != "directory" {
		t.Fatalf("unexpected skill layout: %q", result.SkillLayout)
	}
	if result.Environment["CLAW_MANAGED"] != "true" {
		t.Fatalf("expected CLAW_MANAGED=true, got %q", result.Environment["CLAW_MANAGED"])
	}

	var homeMount *driver.Mount
	for i := range result.Mounts {
		m := &result.Mounts[i]
		if m.ContainerPath == "/root/.nanobot" {
			homeMount = m
		}
	}
	if homeMount == nil || homeMount.ReadOnly {
		t.Fatal("expected writable /root/.nanobot mount")
	}
}

func TestMaterializeWritesCronJobs(t *testing.T) {
	rc, tmp := newTestRC(t)
	rc.Models = map[string]string{"primary": "anthropic/claude-sonnet-4"}
	rc.Environment["ANTHROPIC_API_KEY"] = "anthropic-key"
	rc.Invocations = []driver.Invocation{
		{
			Schedule: "*/5 * * * *",
			Message:  "Check market depth",
			Name:     "market-depth",
			To:       "1234567890",
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

	cronPath := filepath.Join(runtimeDir, "nanobot-home", "cron", "jobs.json")
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
	if job["name"] != "market-depth" {
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

func newTestRC(t *testing.T) (*driver.ResolvedClaw, string) {
	t.Helper()
	tmp := t.TempDir()
	agentPath := filepath.Join(tmp, "AGENTS.md")
	if err := os.WriteFile(agentPath, []byte("# Agent\n\nYou are nanobot."), 0o644); err != nil {
		t.Fatal(err)
	}

	rc := &driver.ResolvedClaw{
		ServiceName:   "nanobot",
		ClawType:      "nanobot",
		AgentHostPath: agentPath,
		Models:        map[string]string{"primary": "anthropic/claude-sonnet-4"},
		Environment:   map[string]string{},
	}
	return rc, tmp
}
