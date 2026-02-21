package openclaw

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mostlydev/clawdapus/internal/driver"
)

func TestValidateMissingAgentErrors(t *testing.T) {
	d := &Driver{}
	rc := &driver.ResolvedClaw{
		ClawType:      "openclaw",
		Agent:         "AGENTS.md",
		AgentHostPath: "/nonexistent/AGENTS.md",
		Models:        make(map[string]string),
		Configures:    make([]string, 0),
	}
	if err := d.Validate(rc); err == nil {
		t.Fatal("expected error for missing agent file")
	}
}

func TestValidatePassesWithAgent(t *testing.T) {
	dir := t.TempDir()
	agentFile := filepath.Join(dir, "AGENTS.md")
	os.WriteFile(agentFile, []byte("# Contract"), 0644)

	d := &Driver{}
	rc := &driver.ResolvedClaw{
		ClawType:      "openclaw",
		Agent:         "AGENTS.md",
		AgentHostPath: agentFile,
		Models:        make(map[string]string),
		Configures:    make([]string, 0),
	}
	if err := d.Validate(rc); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestMaterializeWritesConfigAndReturnsResult(t *testing.T) {
	dir := t.TempDir()
	agentFile := filepath.Join(dir, "AGENTS.md")
	os.WriteFile(agentFile, []byte("# Contract"), 0644)

	d := &Driver{}
	rc := &driver.ResolvedClaw{
		ClawType:      "openclaw",
		Agent:         "AGENTS.md",
		AgentHostPath: agentFile,
		Models:        map[string]string{"primary": "anthropic/claude-sonnet-4"},
		Configures:    []string{"openclaw config set agents.defaults.heartbeat.every 30m"},
	}
	result, err := d.Materialize(rc, driver.MaterializeOpts{RuntimeDir: dir})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Config file should exist inside the config/ subdirectory.
	// The whole directory is bind-mounted so openclaw can write temp files alongside it.
	configPath := filepath.Join(dir, "config", "openclaw.json")
	if _, err := os.Stat(configPath); err != nil {
		t.Fatalf("config file not written at config/openclaw.json: %v", err)
	}

	// Result should include config mount + agent mount
	if len(result.Mounts) < 2 {
		t.Fatalf("expected at least 2 mounts, got %d", len(result.Mounts))
	}

	if !result.ReadOnly {
		t.Error("expected ReadOnly=true")
	}

	if len(result.Tmpfs) == 0 {
		t.Error("expected at least one tmpfs mount")
	}

	// Verify env vars are set correctly
	if result.Environment["OPENCLAW_CONFIG_PATH"] != "/app/config/openclaw.json" {
		t.Errorf("expected OPENCLAW_CONFIG_PATH=/app/config/openclaw.json, got %q", result.Environment["OPENCLAW_CONFIG_PATH"])
	}
	if result.Environment["OPENCLAW_STATE_DIR"] != "/app/state" {
		t.Errorf("expected OPENCLAW_STATE_DIR=/app/state, got %q", result.Environment["OPENCLAW_STATE_DIR"])
	}

	// /app/state must be a single tmpfs covering all openclaw state subdirs.
	tmpfsSet := make(map[string]bool, len(result.Tmpfs))
	for _, p := range result.Tmpfs {
		tmpfsSet[p] = true
	}
	if !tmpfsSet["/app/state"] {
		t.Error("expected single /app/state tmpfs (covers identity, logs, memory, agents, etc.)")
	}
	if tmpfsSet["/root/.openclaw"] {
		t.Error("unexpected tmpfs /root/.openclaw — should use /app/state now")
	}

	if result.Restart != "on-failure" {
		t.Errorf("expected restart=on-failure, got %q", result.Restart)
	}
}

func TestMaterializeJobsDirMountedNotFile(t *testing.T) {
	dir := t.TempDir()
	agentFile := filepath.Join(dir, "AGENTS.md")
	os.WriteFile(agentFile, []byte("# Contract"), 0644)

	d := &Driver{}
	rc := &driver.ResolvedClaw{
		ClawType:      "openclaw",
		Agent:         "AGENTS.md",
		AgentHostPath: agentFile,
		Models:        make(map[string]string),
		Configures:    make([]string, 0),
		ServiceName:   "testsvc",
		Invocations: []driver.Invocation{
			{Schedule: "15 8 * * 1-5", Message: "Morning synthesis"},
		},
	}
	result, err := d.Materialize(rc, driver.MaterializeOpts{RuntimeDir: dir})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// jobs.json must exist in the state/cron/ directory on the host
	jobsPath := filepath.Join(dir, "state", "cron", "jobs.json")
	if _, err := os.Stat(jobsPath); err != nil {
		t.Fatalf("jobs.json not written at state/cron/jobs.json: %v", err)
	}

	// The mount target must be the cron/ DIRECTORY, not the jobs.json file.
	// Mounting the file causes EBUSY when openclaw does atomic rename next to it.
	var jobsMount *driver.Mount
	for i := range result.Mounts {
		if result.Mounts[i].ContainerPath == "/app/state/cron" {
			jobsMount = &result.Mounts[i]
			break
		}
	}
	if jobsMount == nil {
		t.Fatal("expected a mount at /app/state/cron (directory), not /app/state/cron/jobs.json")
	}
	if jobsMount.ReadOnly {
		t.Error("jobs cron dir must be read-write so openclaw can update job state")
	}
}
