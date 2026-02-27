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
	if err := os.WriteFile(agentPath, []byte("# Test Agent\n\nYou are Allen."), 0644); err != nil {
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

	// SkillDir targets orchestrator's container/skills path
	if result.SkillDir != "/workspace/container/skills" {
		t.Errorf("expected orchestrator skill dir /workspace/container/skills, got %q", result.SkillDir)
	}
	if result.SkillLayout != "directory" {
		t.Errorf("expected directory skill layout, got %q", result.SkillLayout)
	}

	// Check mounts
	hasDockerSocket := false
	hasCombinedClaude := false
	hasOldAgent := false
	hasOldClawdapus := false
	for _, m := range result.Mounts {
		if m.ContainerPath == "/var/run/docker.sock" {
			hasDockerSocket = true
			if m.ReadOnly {
				t.Error("Docker socket mount should be read-write")
			}
		}
		if m.ContainerPath == "/workspace/groups/main/CLAUDE.md" {
			hasCombinedClaude = true
			if !m.ReadOnly {
				t.Error("combined CLAUDE.md mount should be read-only")
			}
		}
		// Old paths must NOT appear
		if m.ContainerPath == "/workspace/AGENTS.md" {
			hasOldAgent = true
		}
		if m.ContainerPath == "/workspace/CLAWDAPUS.md" {
			hasOldClawdapus = true
		}
	}
	if !hasDockerSocket {
		t.Error("expected Docker socket mount")
	}
	if !hasCombinedClaude {
		t.Error("expected combined CLAUDE.md mount at /workspace/groups/main/CLAUDE.md")
	}
	if hasOldAgent {
		t.Error("should NOT have old /workspace/AGENTS.md mount")
	}
	if hasOldClawdapus {
		t.Error("should NOT have old /workspace/CLAWDAPUS.md mount")
	}

	// Verify combined CLAUDE.md contains both agent content and CLAWDAPUS.md
	combinedPath := filepath.Join(runtimeDir, "CLAUDE.md")
	content, err := os.ReadFile(combinedPath)
	if err != nil {
		t.Fatalf("CLAUDE.md not written: %v", err)
	}
	if !strings.Contains(string(content), "Test Agent") {
		t.Error("combined CLAUDE.md should contain agent contract content")
	}
	if !strings.Contains(string(content), "test-pod") {
		t.Error("combined CLAUDE.md should contain pod name from CLAWDAPUS.md")
	}
	if !strings.Contains(string(content), "nano-bot") {
		t.Error("combined CLAUDE.md should contain service name from CLAWDAPUS.md")
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

	// ANTHROPIC_BASE_URL as env var (orchestrator forwards to agent-runners)
	if result.Environment["ANTHROPIC_BASE_URL"] != "http://cllama-passthrough:8080/v1" {
		t.Errorf("expected ANTHROPIC_BASE_URL rewritten to proxy, got %q", result.Environment["ANTHROPIC_BASE_URL"])
	}

	// ANTHROPIC_API_KEY must NOT be in env — goes to .env file instead
	if _, ok := result.Environment["ANTHROPIC_API_KEY"]; ok {
		t.Error("ANTHROPIC_API_KEY should NOT be in env vars — must go to .env file")
	}

	// CLAW_NETWORK for agent-runner pod connectivity
	if result.Environment["CLAW_NETWORK"] != "test-pod_claw-internal" {
		t.Errorf("expected CLAW_NETWORK=test-pod_claw-internal, got %q", result.Environment["CLAW_NETWORK"])
	}

	// .env file mount with ANTHROPIC_API_KEY
	hasEnvMount := false
	for _, m := range result.Mounts {
		if m.ContainerPath == "/workspace/.env" {
			hasEnvMount = true
			if !m.ReadOnly {
				t.Error(".env mount should be read-only")
			}
		}
	}
	if !hasEnvMount {
		t.Error("expected .env mount at /workspace/.env for cllama token")
	}

	// Verify .env file content
	envPath := filepath.Join(runtimeDir, ".env")
	envContent, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatalf(".env not written: %v", err)
	}
	if !strings.Contains(string(envContent), "ANTHROPIC_API_KEY=nano-bot:abc123") {
		t.Errorf(".env should contain ANTHROPIC_API_KEY token, got %q", string(envContent))
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
	if _, ok := result.Environment["CLAW_NETWORK"]; ok {
		t.Error("expected no CLAW_NETWORK when cllama not enabled")
	}

	// No .env mount
	for _, m := range result.Mounts {
		if m.ContainerPath == "/workspace/.env" {
			t.Error("expected no .env mount when cllama not enabled")
		}
	}
}
