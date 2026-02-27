package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInitScaffoldCreatesFiles(t *testing.T) {
	dir := t.TempDir()
	err := runInit(dir, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, name := range []string{"Clawfile", "claw-pod.yml", "AGENTS.md", ".env.example"} {
		path := filepath.Join(dir, name)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			t.Errorf("expected %s to exist", name)
		}
	}
}

func TestInitScaffoldRefusesToOverwrite(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "Clawfile"), []byte("existing"), 0644)

	err := runInit(dir, "")
	if err == nil {
		t.Fatal("expected error when Clawfile already exists")
	}
}
