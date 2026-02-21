package runtime

import (
	"os"
	"path/filepath"
	"strings"
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

	realAgentPath, err := filepath.EvalSymlinks(agentPath)
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	if mount.HostPath != realAgentPath {
		t.Fatalf("expected host path %q, got %q", realAgentPath, mount.HostPath)
	}
	if mount.ContainerPath != "/claw/contracts/AGENTS.md" {
		t.Fatalf("expected nested container path, got %q", mount.ContainerPath)
	}
	if !mount.ReadOnly {
		t.Fatal("expected read-only contract mount")
	}
}

func TestResolveContractRejectsSymlinkOutsideBase(t *testing.T) {
	podDir := t.TempDir()
	outsideDir := t.TempDir()

	outside := filepath.Join(outsideDir, "sensitive.md")
	if err := os.WriteFile(outside, []byte("# sensitive"), 0644); err != nil {
		t.Fatalf("write outside file: %v", err)
	}

	linkPath := filepath.Join(podDir, "AGENTS.md")
	if err := os.Symlink(outside, linkPath); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}

	_, err := ResolveContract(podDir, "AGENTS.md")
	if err == nil {
		t.Fatal("expected error for symlink that escapes base directory")
	}
	if !strings.Contains(err.Error(), "escapes base directory") {
		t.Fatalf("expected escapes error, got: %v", err)
	}
}

func TestResolveContractRejectsDirectory(t *testing.T) {
	podDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(podDir, "AGENTS.md"), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	_, err := ResolveContract(podDir, "AGENTS.md")
	if err == nil {
		t.Fatal("expected error for agent path that is a directory")
	}
	if !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("expected regular file error, got: %v", err)
	}
}
