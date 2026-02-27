package nanoclaw

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mostlydev/clawdapus/internal/driver"
)

func TestDriverRegistered(t *testing.T) {
	d, err := driver.Lookup("nanoclaw")
	if err != nil {
		t.Fatalf("nanoclaw driver not registered: %v", err)
	}
	if d == nil {
		t.Fatal("nanoclaw driver is nil")
	}
}

func TestValidateRequiresAgentPath(t *testing.T) {
	d := &Driver{}
	rc := &driver.ResolvedClaw{
		ServiceName: "test",
		Privileges:  map[string]string{"docker-socket": "true"},
	}
	err := d.Validate(rc)
	if err == nil {
		t.Fatal("expected error for missing agent host path")
	}
	if !strings.Contains(err.Error(), "no agent host path") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateRequiresDockerSocket(t *testing.T) {
	tmp := t.TempDir()
	agentPath := filepath.Join(tmp, "AGENTS.md")
	if err := os.WriteFile(agentPath, []byte("test"), 0644); err != nil {
		t.Fatal(err)
	}

	d := &Driver{}
	rc := &driver.ResolvedClaw{
		ServiceName:   "test",
		AgentHostPath: agentPath,
		Privileges:    map[string]string{},
	}
	err := d.Validate(rc)
	if err == nil {
		t.Fatal("expected error for missing docker-socket privilege")
	}
	if !strings.Contains(err.Error(), "PRIVILEGE docker-socket") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateAcceptsValidConfig(t *testing.T) {
	tmp := t.TempDir()
	agentPath := filepath.Join(tmp, "AGENTS.md")
	if err := os.WriteFile(agentPath, []byte("test"), 0644); err != nil {
		t.Fatal(err)
	}

	d := &Driver{}
	rc := &driver.ResolvedClaw{
		ServiceName:   "test",
		AgentHostPath: agentPath,
		Privileges:    map[string]string{"docker-socket": "true"},
	}
	if err := d.Validate(rc); err != nil {
		t.Fatalf("unexpected validation error: %v", err)
	}
}

func TestValidateWarnsOnInvocations(t *testing.T) {
	tmp := t.TempDir()
	agentPath := filepath.Join(tmp, "AGENTS.md")
	if err := os.WriteFile(agentPath, []byte("test"), 0644); err != nil {
		t.Fatal(err)
	}

	d := &Driver{}
	rc := &driver.ResolvedClaw{
		ServiceName:   "test",
		AgentHostPath: agentPath,
		Privileges:    map[string]string{"docker-socket": "true"},
		Invocations:   []driver.Invocation{{Schedule: "0 * * * *", Message: "test"}},
	}
	// Should not error, just warn
	if err := d.Validate(rc); err != nil {
		t.Fatalf("invocations should produce warning, not error: %v", err)
	}
}

// --- Materialize tests ---

func newTestRC(t *testing.T) (*driver.ResolvedClaw, string) {
	t.Helper()
	tmp := t.TempDir()
	agentPath := filepath.Join(tmp, "AGENTS.md")
	if err := os.WriteFile(agentPath, []byte("# Test Agent"), 0644); err != nil {
		t.Fatal(err)
	}
	rc := &driver.ResolvedClaw{
		ServiceName:   "nano-bot",
		ClawType:      "nanoclaw",
		AgentHostPath: agentPath,
		Privileges:    map[string]string{"docker-socket": "true"},
	}
	return rc, tmp
}

func TestMaterializeBasic(t *testing.T) {
	rc, tmp := newTestRC(t)
	runtimeDir := filepath.Join(tmp, "runtime")
	if err := os.MkdirAll(runtimeDir, 0700); err != nil {
		t.Fatal(err)
	}

	d := &Driver{}
	result, err := d.Materialize(rc, driver.MaterializeOpts{RuntimeDir: runtimeDir, PodName: "test-pod"})
	if err != nil {
		t.Fatalf("Materialize failed: %v", err)
	}

	// ReadOnly must be false — NanoClaw needs writable workspace
	if result.ReadOnly {
		t.Error("expected ReadOnly=false for nanoclaw")
	}

	// SkillDir and SkillLayout
	if result.SkillDir != "/home/node/.claude/skills" {
		t.Errorf("expected Claude Code skill dir, got %q", result.SkillDir)
	}
	if result.SkillLayout != "directory" {
		t.Errorf("expected directory skill layout, got %q", result.SkillLayout)
	}

	// Check for Docker socket mount
	hasDockerSocket := false
	hasAgent := false
	hasClawdapus := false
	for _, m := range result.Mounts {
		if m.ContainerPath == "/var/run/docker.sock" {
			hasDockerSocket = true
			if m.ReadOnly {
				t.Error("Docker socket mount should be read-write")
			}
		}
		if m.ContainerPath == "/workspace/AGENTS.md" {
			hasAgent = true
			if !m.ReadOnly {
				t.Error("agent mount should be read-only")
			}
		}
		if m.ContainerPath == "/workspace/CLAWDAPUS.md" {
			hasClawdapus = true
			if !m.ReadOnly {
				t.Error("CLAWDAPUS.md mount should be read-only")
			}
		}
	}
	if !hasDockerSocket {
		t.Error("expected Docker socket mount")
	}
	if !hasAgent {
		t.Error("expected agent contract mount at /workspace/AGENTS.md")
	}
	if !hasClawdapus {
		t.Error("expected CLAWDAPUS.md mount at /workspace/CLAWDAPUS.md")
	}

	// Verify CLAWDAPUS.md was written
	clawdapusPath := filepath.Join(runtimeDir, "CLAWDAPUS.md")
	content, err := os.ReadFile(clawdapusPath)
	if err != nil {
		t.Fatalf("CLAWDAPUS.md not written: %v", err)
	}
	if !strings.Contains(string(content), "nano-bot") {
		t.Error("CLAWDAPUS.md should contain service name")
	}
	if !strings.Contains(string(content), "test-pod") {
		t.Error("CLAWDAPUS.md should contain pod name")
	}

	// Environment
	if result.Environment["CLAW_MANAGED"] != "true" {
		t.Error("expected CLAW_MANAGED=true in environment")
	}
}

func TestMaterializeWithCllama(t *testing.T) {
	rc, tmp := newTestRC(t)
	rc.Cllama = []string{"passthrough"}
	rc.CllamaToken = "nano-bot:abc123"
	runtimeDir := filepath.Join(tmp, "runtime")
	if err := os.MkdirAll(runtimeDir, 0700); err != nil {
		t.Fatal(err)
	}

	d := &Driver{}
	result, err := d.Materialize(rc, driver.MaterializeOpts{RuntimeDir: runtimeDir, PodName: "test-pod"})
	if err != nil {
		t.Fatalf("Materialize failed: %v", err)
	}

	if result.Environment["ANTHROPIC_BASE_URL"] != "http://cllama-passthrough:8080/v1" {
		t.Errorf("expected ANTHROPIC_BASE_URL rewritten to proxy, got %q", result.Environment["ANTHROPIC_BASE_URL"])
	}
	if result.Environment["ANTHROPIC_API_KEY"] != "nano-bot:abc123" {
		t.Errorf("expected ANTHROPIC_API_KEY set to bearer token, got %q", result.Environment["ANTHROPIC_API_KEY"])
	}
}

func TestMaterializeWithoutCllama(t *testing.T) {
	rc, tmp := newTestRC(t)
	runtimeDir := filepath.Join(tmp, "runtime")
	if err := os.MkdirAll(runtimeDir, 0700); err != nil {
		t.Fatal(err)
	}

	d := &Driver{}
	result, err := d.Materialize(rc, driver.MaterializeOpts{RuntimeDir: runtimeDir, PodName: "test-pod"})
	if err != nil {
		t.Fatalf("Materialize failed: %v", err)
	}

	if _, ok := result.Environment["ANTHROPIC_BASE_URL"]; ok {
		t.Error("expected no ANTHROPIC_BASE_URL when cllama not enabled")
	}
	if _, ok := result.Environment["ANTHROPIC_API_KEY"]; ok {
		t.Error("expected no ANTHROPIC_API_KEY when cllama not enabled")
	}
}
