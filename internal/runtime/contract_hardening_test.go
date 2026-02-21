package runtime

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveContractRejectsAbsoluteAgentPath(t *testing.T) {
	dir := t.TempDir()
	_, err := ResolveContract(dir, "/etc/passwd")
	if err == nil {
		t.Fatal("expected error for absolute path outside baseDir")
	}
}

func TestResolveContractAllowsNestedAgentPath(t *testing.T) {
	dir := t.TempDir()
	nestedDir := filepath.Join(dir, "contracts")
	if err := os.MkdirAll(nestedDir, 0755); err != nil {
		t.Fatalf("mkdir nested dir: %v", err)
	}

	agentPath := filepath.Join(nestedDir, "AGENTS.md")
	if err := os.WriteFile(agentPath, []byte("# Contract"), 0644); err != nil {
		t.Fatalf("write nested agent file: %v", err)
	}

	mount, err := ResolveContract(dir, "contracts/AGENTS.md")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if mount.HostPath != agentPath {
		t.Fatalf("expected host path %q, got %q", agentPath, mount.HostPath)
	}
	if mount.ContainerPath != "/claw/contracts/AGENTS.md" {
		t.Fatalf("expected nested container path, got %q", mount.ContainerPath)
	}
	if !mount.ReadOnly {
		t.Fatal("expected read-only contract mount")
	}
}
