package openclaw

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mostlydev/clawdapus/internal/driver"
	"github.com/mostlydev/clawdapus/internal/driver/shared"
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
	configDirInfo, err := os.Stat(filepath.Join(dir, "config"))
	if err != nil {
		t.Fatalf("stat config dir: %v", err)
	}
	if got := configDirInfo.Mode().Perm(); got != 0o777 {
		t.Fatalf("config dir mode = %o, want 777", got)
	}
	configInfo, err := os.Stat(configPath)
	if err != nil {
		t.Fatalf("stat config file: %v", err)
	}
	if got := configInfo.Mode().Perm(); got != 0o666 {
		t.Fatalf("config file mode = %o, want 666", got)
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
	if result.Environment[shared.PortableMemoryEnv] != shared.PortableMemoryDir {
		t.Errorf("expected %s=%s, got %q", shared.PortableMemoryEnv, shared.PortableMemoryDir, result.Environment[shared.PortableMemoryEnv])
	}

	// /app/state must be a single tmpfs covering all openclaw state subdirs.
	// The options are part of the contract because OpenClaw now runs as uid/gid 1000.
	tmpfsSet := make(map[string]bool, len(result.Tmpfs))
	for _, p := range result.Tmpfs {
		tmpfsSet[p] = true
	}
	if !tmpfsSet["/claw"] {
		t.Error("expected writable /claw tmpfs for workspace writes like SOUL.md")
	}
	if !tmpfsSet[openclawStateTmpfs] {
		t.Errorf("expected writable /app/state tmpfs %q, got %v", openclawStateTmpfs, result.Tmpfs)
	}
	if tmpfsSet["/root/.openclaw"] {
		t.Error("unexpected tmpfs /root/.openclaw — should use /app/state now")
	}

	if result.Restart != "on-failure" {
		t.Errorf("expected restart=on-failure, got %q", result.Restart)
	}

	foundMemoryMount := false
	for _, mount := range result.Mounts {
		if mount.ContainerPath == shared.PortableMemoryDir {
			foundMemoryMount = true
			if mount.ReadOnly {
				t.Fatal("portable memory mount should be writable")
			}
		}
	}
	if !foundMemoryMount {
		t.Fatal("expected portable memory mount")
	}
}

func TestMaterializeUsesStateDirForPortableMemory(t *testing.T) {
	runtimeDir := t.TempDir()
	stateDir := t.TempDir()
	agentFile := filepath.Join(runtimeDir, "AGENTS.md")
	if err := os.WriteFile(agentFile, []byte("# Contract"), 0o644); err != nil {
		t.Fatal(err)
	}

	d := &Driver{}
	rc := &driver.ResolvedClaw{
		ClawType:      "openclaw",
		Agent:         "AGENTS.md",
		AgentHostPath: agentFile,
		Models:        map[string]string{"primary": "anthropic/claude-sonnet-4"},
	}
	result, err := d.Materialize(rc, driver.MaterializeOpts{RuntimeDir: runtimeDir, StateDir: stateDir})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := filepath.Join(stateDir, "memory")
	found := false
	for _, mount := range result.Mounts {
		if mount.ContainerPath == shared.PortableMemoryDir {
			found = true
			if mount.HostPath != want {
				t.Fatalf("portable memory host path = %q, want %q", mount.HostPath, want)
			}
		}
	}
	if !found {
		t.Fatal("expected portable memory mount")
	}
	if _, err := os.Stat(filepath.Join(stateDir, "memory", "MEMORY.md")); err != nil {
		t.Fatalf("expected persistent state memory file: %v", err)
	}
	if _, err := os.Stat(filepath.Join(runtimeDir, "memory")); !os.IsNotExist(err) {
		t.Fatalf("runtime memory dir should not be materialized when StateDir is set, got err=%v", err)
	}
}

func TestMaterializeInlinesClawdapusContextIntoMountedContract(t *testing.T) {
	dir := t.TempDir()
	agentFile := filepath.Join(dir, "AGENTS.md")
	if err := os.WriteFile(agentFile, []byte("# Contract\n\nFollow the workflow.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	d := &Driver{}
	rc := &driver.ResolvedClaw{
		ServiceName:   "weston",
		ClawType:      "openclaw",
		Agent:         "AGENTS.md",
		AgentHostPath: agentFile,
		Models:        map[string]string{"primary": "anthropic/claude-sonnet-4"},
		Handles: map[string]*driver.HandleInfo{
			"discord": {
				ID:       "1234",
				Username: "weston",
			},
		},
	}

	result, err := d.Materialize(rc, driver.MaterializeOpts{RuntimeDir: dir, PodName: "trading-desk"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var agentMount *driver.Mount
	var clawdapusMount *driver.Mount
	for i := range result.Mounts {
		switch result.Mounts[i].ContainerPath {
		case "/claw/AGENTS.md":
			agentMount = &result.Mounts[i]
		case "/claw/CLAWDAPUS.md":
			clawdapusMount = &result.Mounts[i]
		}
	}
	if agentMount == nil {
		t.Fatal("expected /claw/AGENTS.md mount")
	}
	if clawdapusMount == nil {
		t.Fatal("expected /claw/CLAWDAPUS.md mount")
	}
	if agentMount.HostPath == agentFile {
		t.Fatal("expected /claw/AGENTS.md to mount the generated effective contract")
	}

	effective, err := os.ReadFile(agentMount.HostPath)
	if err != nil {
		t.Fatalf("read effective contract: %v", err)
	}
	text := string(effective)
	if !strings.Contains(text, "# Contract") {
		t.Fatal("expected original agent contract content in effective contract")
	}
	if !strings.Contains(text, "--- BEGIN: infrastructure_context (guide) ---") {
		t.Fatal("expected infrastructure guide block in effective contract")
	}
	if !strings.Contains(text, "## Identity") {
		t.Fatal("expected inlined CLAWDAPUS identity section in effective contract")
	}
	if !strings.Contains(text, "- **Pod:** trading-desk") {
		t.Fatal("expected pod name in inlined CLAWDAPUS context")
	}
	if !strings.Contains(text, "- **Service:** weston") {
		t.Fatal("expected service name in inlined CLAWDAPUS context")
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
	jobsDirInfo, err := os.Stat(filepath.Join(dir, "state", "cron"))
	if err != nil {
		t.Fatalf("stat jobs dir: %v", err)
	}
	if got := jobsDirInfo.Mode().Perm(); got != 0o777 {
		t.Fatalf("jobs dir mode = %o, want 777", got)
	}
	jobsInfo, err := os.Stat(jobsPath)
	if err != nil {
		t.Fatalf("stat jobs.json: %v", err)
	}
	if got := jobsInfo.Mode().Perm(); got != 0o666 {
		t.Fatalf("jobs.json mode = %o, want 666", got)
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

func TestMaterializeNoPersonaOmitsEnvVar(t *testing.T) {
	dir := t.TempDir()
	agentFile := filepath.Join(dir, "AGENTS.md")
	os.WriteFile(agentFile, []byte("# Contract"), 0o644)

	d := &Driver{}
	rc := &driver.ResolvedClaw{
		ClawType:      "openclaw",
		Agent:         "AGENTS.md",
		AgentHostPath: agentFile,
		Models:        make(map[string]string),
	}
	result, err := d.Materialize(rc, driver.MaterializeOpts{RuntimeDir: dir})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v, ok := result.Environment["CLAW_PERSONA_DIR"]; ok {
		t.Fatalf("CLAW_PERSONA_DIR should not be set without persona, got %q", v)
	}
}

func TestMaterializeMountsPersonaWorkspace(t *testing.T) {
	dir := t.TempDir()
	agentFile := filepath.Join(dir, "AGENTS.md")
	personaDir := filepath.Join(dir, "persona-src")
	if err := os.WriteFile(agentFile, []byte("# Contract"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(personaDir, 0o755); err != nil {
		t.Fatal(err)
	}

	d := &Driver{}
	rc := &driver.ResolvedClaw{
		ClawType:        "openclaw",
		Agent:           "AGENTS.md",
		AgentHostPath:   agentFile,
		Persona:         "ghcr.io/mostlydev/personas/allen:latest",
		PersonaHostPath: personaDir,
		Models:          make(map[string]string),
	}
	result, err := d.Materialize(rc, driver.MaterializeOpts{RuntimeDir: dir})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	found := false
	for _, mount := range result.Mounts {
		if mount.ContainerPath == "/claw/persona" {
			found = true
			if mount.ReadOnly {
				t.Fatal("persona mount should be writable")
			}
		}
	}
	if !found {
		t.Fatal("expected /claw/persona mount")
	}
	if result.Environment["CLAW_PERSONA_DIR"] != "/claw/persona" {
		t.Fatalf("expected CLAW_PERSONA_DIR to be set, got %q", result.Environment["CLAW_PERSONA_DIR"])
	}
}
