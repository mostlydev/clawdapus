package persona

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMaterializeLocalPersonaCopiesDirectory(t *testing.T) {
	baseDir := t.TempDir()
	srcDir := filepath.Join(baseDir, "personas", "allen")
	if err := os.MkdirAll(filepath.Join(srcDir, "history"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "style.md"), []byte("dry humor\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "history", "memory.txt"), []byte("first trade\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	runtimeDir := filepath.Join(baseDir, ".claw-runtime", "bot")
	if err := os.MkdirAll(runtimeDir, 0o755); err != nil {
		t.Fatal(err)
	}

	resolved, err := Materialize(baseDir, runtimeDir, "./personas/allen")
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	if resolved == nil {
		t.Fatal("expected resolved persona")
	}

	stylePath := filepath.Join(resolved.HostPath, "style.md")
	got, err := os.ReadFile(stylePath)
	if err != nil {
		t.Fatalf("read materialized style file: %v", err)
	}
	if string(got) != "dry humor\n" {
		t.Fatalf("unexpected style content: %q", string(got))
	}

	memoryPath := filepath.Join(resolved.HostPath, "history", "memory.txt")
	if _, err := os.Stat(memoryPath); err != nil {
		t.Fatalf("expected nested persona file to be copied: %v", err)
	}
}

func TestMaterializeRejectsEscapingLocalPersonaPath(t *testing.T) {
	baseDir := t.TempDir()
	runtimeDir := filepath.Join(baseDir, ".claw-runtime", "bot")
	if err := os.MkdirAll(runtimeDir, 0o755); err != nil {
		t.Fatal(err)
	}

	_, err := Materialize(baseDir, runtimeDir, "../elsewhere")
	if err == nil {
		t.Fatal("expected escape error")
	}
	if !strings.Contains(err.Error(), "escapes base directory") {
		t.Fatalf("unexpected error: %v", err)
	}
}
