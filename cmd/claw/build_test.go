package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveClawfilePathRejectsMissingInput(t *testing.T) {
	_, err := resolveClawfilePath(filepath.Join(t.TempDir(), "missing"))
	if err == nil {
		t.Fatal("expected error for missing input path")
	}
}

func TestResolveClawfilePathRejectsDirWithoutClawfile(t *testing.T) {
	dir := t.TempDir()
	_, err := resolveClawfilePath(dir)
	if err == nil {
		t.Fatal("expected error for directory without Clawfile")
	}
}

func TestResolveClawfilePathAcceptsDirWithClawfile(t *testing.T) {
	dir := t.TempDir()
	clawfile := filepath.Join(dir, "Clawfile")
	if err := os.WriteFile(clawfile, []byte("FROM alpine\nCLAW_TYPE openclaw\n"), 0o644); err != nil {
		t.Fatalf("write Clawfile: %v", err)
	}

	got, err := resolveClawfilePath(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != clawfile {
		t.Fatalf("expected %q, got %q", clawfile, got)
	}
}
