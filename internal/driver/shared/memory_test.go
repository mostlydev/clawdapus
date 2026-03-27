package shared

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPreparePortableMemoryMigratesLegacySources(t *testing.T) {
	runtimeDir := t.TempDir()

	legacyHermesDir := filepath.Join(runtimeDir, "hermes-home", "memories")
	if err := os.MkdirAll(legacyHermesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacyHermesDir, "MEMORY.md"), []byte("legacy hermes memory"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacyHermesDir, "USER.md"), []byte("legacy user memory"), 0o644); err != nil {
		t.Fatal(err)
	}

	legacyWorkspace := filepath.Join(runtimeDir, "workspace")
	if err := os.MkdirAll(filepath.Join(legacyWorkspace, "memory"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacyWorkspace, "memory", "2026-03-26.md"), []byte("dated note"), 0o644); err != nil {
		t.Fatal(err)
	}

	memoryDir, err := PreparePortableMemory(runtimeDir)
	if err != nil {
		t.Fatalf("PreparePortableMemory returned error: %v", err)
	}

	if memoryDir != filepath.Join(runtimeDir, "memory") {
		t.Fatalf("unexpected memory dir: %q", memoryDir)
	}

	checks := map[string]string{
		filepath.Join(memoryDir, "MEMORY.md"):     "legacy hermes memory",
		filepath.Join(memoryDir, "USER.md"):       "legacy user memory",
		filepath.Join(memoryDir, "2026-03-26.md"): "dated note",
	}
	for path, want := range checks {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if string(data) != want {
			t.Fatalf("unexpected content in %s: %q", path, string(data))
		}
	}
}

func TestPreparePortableMemoryDoesNotOverwriteExistingFiles(t *testing.T) {
	runtimeDir := t.TempDir()

	targetDir := filepath.Join(runtimeDir, "memory")
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(targetDir, "MEMORY.md"), []byte("canonical"), 0o644); err != nil {
		t.Fatal(err)
	}

	legacyDir := filepath.Join(runtimeDir, "hermes-home", "memories")
	if err := os.MkdirAll(legacyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacyDir, "MEMORY.md"), []byte("legacy"), 0o644); err != nil {
		t.Fatal(err)
	}

	memoryDir, err := PreparePortableMemory(runtimeDir)
	if err != nil {
		t.Fatalf("PreparePortableMemory returned error: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(memoryDir, "MEMORY.md"))
	if err != nil {
		t.Fatalf("read canonical memory: %v", err)
	}
	if string(data) != "canonical" {
		t.Fatalf("expected canonical memory to win, got %q", string(data))
	}
}

func TestPreparePortableMemoryImportsFromExtraRuntimeRoot(t *testing.T) {
	stateDir := t.TempDir()
	runtimeDir := t.TempDir()

	legacyDir := filepath.Join(runtimeDir, "hermes-home", "memories")
	if err := os.MkdirAll(legacyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacyDir, "MEMORY.md"), []byte("legacy carried forward"), 0o644); err != nil {
		t.Fatal(err)
	}

	memoryDir, err := PreparePortableMemory(stateDir, runtimeDir)
	if err != nil {
		t.Fatalf("PreparePortableMemory returned error: %v", err)
	}

	if memoryDir != filepath.Join(stateDir, "memory") {
		t.Fatalf("unexpected memory dir: %q", memoryDir)
	}
	data, err := os.ReadFile(filepath.Join(memoryDir, "MEMORY.md"))
	if err != nil {
		t.Fatalf("read migrated memory: %v", err)
	}
	if string(data) != "legacy carried forward" {
		t.Fatalf("unexpected migrated memory: %q", string(data))
	}
}
