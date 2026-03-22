package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mostlydev/clawdapus/internal/driver"
)

func TestResolveComposeGeneratedPathDefaultMissing(t *testing.T) {
	// Run from a temp dir with no compose.generated.yml
	orig, _ := os.Getwd()
	dir := t.TempDir()
	os.Chdir(dir)
	defer os.Chdir(orig)

	composePodFile = "" // reset global
	_, err := resolveComposeGeneratedPath()
	if err == nil {
		t.Fatal("expected error when compose.generated.yml does not exist")
	}
	got := err.Error()
	if !strings.Contains(got, "no compose.generated.yml found in ") || !strings.Contains(got, "(rerun from pod directory or pass --file <path-to-claw-pod.yml>)") {
		t.Errorf("unexpected error message: %s", got)
	}
}

func TestResolveComposeGeneratedPathDefaultExists(t *testing.T) {
	orig, _ := os.Getwd()
	dir := t.TempDir()
	os.Chdir(dir)
	defer os.Chdir(orig)

	os.WriteFile(filepath.Join(dir, "compose.generated.yml"), []byte("services: {}"), 0644)

	composePodFile = "" // reset global
	path, err := resolveComposeGeneratedPath()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasSuffix(path, string(filepath.Separator)+"compose.generated.yml") {
		t.Errorf("expected absolute compose.generated.yml path, got %q", path)
	}
}

func TestResolveComposeGeneratedPathWithPodFile(t *testing.T) {
	dir := t.TempDir()
	podDir := filepath.Join(dir, "examples", "openclaw")
	os.MkdirAll(podDir, 0755)
	os.WriteFile(filepath.Join(podDir, "claw-pod.yml"), []byte("services: {}"), 0644)
	os.WriteFile(filepath.Join(podDir, "compose.generated.yml"), []byte("services: {}"), 0644)

	composePodFile = filepath.Join(podDir, "claw-pod.yml")
	defer func() { composePodFile = "" }()

	path, err := resolveComposeGeneratedPath()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := filepath.Join(podDir, "compose.generated.yml")
	if path != expected {
		t.Errorf("expected %q, got %q", expected, path)
	}
}

func TestResolveComposeGeneratedPathWithPodFileMissingGenerated(t *testing.T) {
	dir := t.TempDir()
	podDir := filepath.Join(dir, "examples", "openclaw")
	os.MkdirAll(podDir, 0755)
	os.WriteFile(filepath.Join(podDir, "claw-pod.yml"), []byte("services: {}"), 0644)
	// No compose.generated.yml

	composePodFile = filepath.Join(podDir, "claw-pod.yml")
	defer func() { composePodFile = "" }()

	_, err := resolveComposeGeneratedPath()
	if err == nil {
		t.Fatal("expected error when compose.generated.yml missing next to pod file")
	}
}

func TestResolveComposeGeneratedPathStaleWithPodFile(t *testing.T) {
	dir := t.TempDir()
	podDir := filepath.Join(dir, "examples", "openclaw")
	os.MkdirAll(podDir, 0755)

	// Write compose.generated.yml first (older)
	os.WriteFile(filepath.Join(podDir, "compose.generated.yml"), []byte("services: {}"), 0644)
	time.Sleep(50 * time.Millisecond)
	// Write pod file second (newer) — generated file is stale
	os.WriteFile(filepath.Join(podDir, "claw-pod.yml"), []byte("services: {}"), 0644)

	composePodFile = filepath.Join(podDir, "claw-pod.yml")
	defer func() { composePodFile = "" }()

	_, err := resolveComposeGeneratedPath()
	if err == nil {
		t.Fatal("expected error when compose.generated.yml is stale")
	}
	if !strings.Contains(err.Error(), "claw up") {
		t.Errorf("expected error to mention 'claw up', got: %s", err.Error())
	}
}

func TestResolveComposeGeneratedPathStaleDefaultDir(t *testing.T) {
	orig, _ := os.Getwd()
	dir := t.TempDir()
	os.Chdir(dir)
	defer os.Chdir(orig)

	// Write compose.generated.yml first (older)
	os.WriteFile(filepath.Join(dir, "compose.generated.yml"), []byte("services: {}"), 0644)
	time.Sleep(50 * time.Millisecond)
	// Write pod file second (newer) — generated file is stale
	os.WriteFile(filepath.Join(dir, "claw-pod.yml"), []byte("services: {}"), 0644)

	composePodFile = "" // reset global
	_, err := resolveComposeGeneratedPath()
	if err == nil {
		t.Fatal("expected error when compose.generated.yml is stale")
	}
	if !strings.Contains(err.Error(), "claw up") {
		t.Errorf("expected error to mention 'claw up', got: %s", err.Error())
	}
}

func TestResolveComposeGeneratedPathFreshNotStale(t *testing.T) {
	dir := t.TempDir()
	podDir := filepath.Join(dir, "examples", "openclaw")
	os.MkdirAll(podDir, 0755)

	// Write pod file first (older)
	os.WriteFile(filepath.Join(podDir, "claw-pod.yml"), []byte("services: {}"), 0644)
	time.Sleep(50 * time.Millisecond)
	// Write compose.generated.yml second (newer) — generated file is fresh
	os.WriteFile(filepath.Join(podDir, "compose.generated.yml"), []byte("services: {}"), 0644)

	composePodFile = filepath.Join(podDir, "claw-pod.yml")
	defer func() { composePodFile = "" }()

	path, err := resolveComposeGeneratedPath()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := filepath.Join(podDir, "compose.generated.yml")
	if path != expected {
		t.Errorf("expected %q, got %q", expected, path)
	}
}

func TestBuiltinDriversRegistered(t *testing.T) {
	for _, clawType := range []string{"openclaw", "hermes", "nanoclaw", "microclaw", "nullclaw", "nanobot", "picoclaw"} {
		if _, err := driver.Lookup(clawType); err != nil {
			t.Fatalf("expected driver %q to be registered in CLI package: %v", clawType, err)
		}
	}
}
