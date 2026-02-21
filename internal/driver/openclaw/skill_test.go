package openclaw

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mostlydev/clawdapus/internal/driver"
)

func TestMaterializeReturnsSkillDir(t *testing.T) {
	tmpDir := t.TempDir()
	agentPath := filepath.Join(tmpDir, "AGENTS.md")
	if err := os.WriteFile(agentPath, []byte("# Test"), 0644); err != nil {
		t.Fatal(err)
	}

	d := &Driver{}
	rc := &driver.ResolvedClaw{
		ServiceName:   "test-svc",
		ClawType:      "openclaw",
		Agent:         "AGENTS.md",
		AgentHostPath: agentPath,
		Models:        map[string]string{},
		Configures:    []string{},
	}

	runtimeDir := filepath.Join(tmpDir, "runtime")
	if err := os.MkdirAll(runtimeDir, 0700); err != nil {
		t.Fatal(err)
	}

	result, err := d.Materialize(rc, driver.MaterializeOpts{RuntimeDir: runtimeDir, PodName: "test-pod"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.SkillDir != "/claw/skills" {
		t.Errorf("expected SkillDir=/claw/skills, got %q", result.SkillDir)
	}
}
