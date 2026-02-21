package runtime

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveContractMissingFileErrors(t *testing.T) {
	_, err := ResolveContract("/nonexistent/path", "AGENTS.md")
	if err == nil {
		t.Fatal("expected error for missing agent file")
	}
}

func TestResolveContractExistingFileReturns(t *testing.T) {
	dir := t.TempDir()
	agentFile := filepath.Join(dir, "AGENTS.md")
	if err := os.WriteFile(agentFile, []byte("# Contract"), 0644); err != nil {
		t.Fatal(err)
	}

	mount, err := ResolveContract(dir, "AGENTS.md")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	realAgentFile, err := filepath.EvalSymlinks(agentFile)
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	if mount.HostPath != realAgentFile {
		t.Errorf("expected host path %q, got %q", realAgentFile, mount.HostPath)
	}
	if mount.ContainerPath != "/claw/AGENTS.md" {
		t.Errorf("expected container path /claw/AGENTS.md, got %q", mount.ContainerPath)
	}
	if !mount.ReadOnly {
		t.Error("expected read-only mount")
	}
}

func TestResolveContractPathTraversalErrors(t *testing.T) {
	dir := t.TempDir()
	_, err := ResolveContract(dir, "../../../etc/passwd")
	if err == nil {
		t.Fatal("expected error for path traversal attempt")
	}
}

func TestResolveContractEmptyFilenameErrors(t *testing.T) {
	_, err := ResolveContract("/some/dir", "")
	if err == nil {
		t.Fatal("expected error for empty agent filename")
	}
}
