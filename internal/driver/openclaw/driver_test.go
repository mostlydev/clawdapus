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
	if result.Environment["OPENCLAW_CONFIG_PATH"] != openclawConfigPath {
		t.Errorf("expected OPENCLAW_CONFIG_PATH=%s, got %q", openclawConfigPath, result.Environment["OPENCLAW_CONFIG_PATH"])
	}
	if result.Environment["OPENCLAW_STATE_DIR"] != openclawHomeDir {
		t.Errorf("expected OPENCLAW_STATE_DIR=%s, got %q", openclawHomeDir, result.Environment["OPENCLAW_STATE_DIR"])
	}
	if _, ok := result.Environment["OPENCLAW_HOME"]; ok {
		t.Fatalf("OPENCLAW_HOME should not be set when using canonical ~/.openclaw state, got %q", result.Environment["OPENCLAW_HOME"])
	}
	if result.Environment["HOME"] != "/root" {
		t.Errorf("expected HOME=/root so ~/.openclaw resolves onto writable state, got %q", result.Environment["HOME"])
	}
	if result.Environment[shared.PortableMemoryEnv] != shared.PortableMemoryDir {
		t.Errorf("expected %s=%s, got %q", shared.PortableMemoryEnv, shared.PortableMemoryDir, result.Environment[shared.PortableMemoryEnv])
	}

	// ~/.openclaw must be a single tmpfs covering all openclaw state subdirs.
	// The mount options are part of the contract because the container home stays writable
	// even though the root filesystem is read-only.
	tmpfsSet := make(map[string]bool, len(result.Tmpfs))
	for _, p := range result.Tmpfs {
		tmpfsSet[p] = true
	}
	if !tmpfsSet[openclawWorkspaceTmpfs] {
		t.Errorf("expected writable /claw tmpfs %q for workspace writes like SOUL.md, got %v", openclawWorkspaceTmpfs, result.Tmpfs)
	}
	if !tmpfsSet[openclawStateTmpfs] {
		t.Errorf("expected writable %s tmpfs %q, got %v", openclawHomeDir, openclawStateTmpfs, result.Tmpfs)
	}
	if tmpfsSet["/app/state"] {
		t.Error("unexpected legacy /app/state tmpfs")
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

func TestMaterializeWritesJobsUnderConfigDir(t *testing.T) {
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

	// jobs.json must exist in the config/cron/ directory on the host.
	// The parent ~/.openclaw/config bind mount covers this path inside the container.
	jobsPath := filepath.Join(dir, "config", "cron", "jobs.json")
	if _, err := os.Stat(jobsPath); err != nil {
		t.Fatalf("jobs.json not written at config/cron/jobs.json: %v", err)
	}
	jobsDirInfo, err := os.Stat(filepath.Join(dir, "config", "cron"))
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

	var configMount *driver.Mount
	for i := range result.Mounts {
		if result.Mounts[i].ContainerPath == openclawConfigDir {
			configMount = &result.Mounts[i]
			break
		}
	}
	if configMount == nil {
		t.Fatalf("expected %s mount to cover config/cron/jobs.json", openclawConfigDir)
	}
	if configMount.ReadOnly {
		t.Errorf("%s must be read-write so openclaw can update cron job state", openclawConfigDir)
	}
	for i := range result.Mounts {
		if result.Mounts[i].ContainerPath == "/app/state/cron" {
			t.Fatal("unexpected legacy /app/state/cron mount")
		}
		if result.Mounts[i].ContainerPath == "/root/.openclaw/cron" {
			t.Fatal("unexpected direct /root/.openclaw/cron mount; jobs should stay under config dir")
		}
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
