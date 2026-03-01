package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInitScaffoldCreatesFiles(t *testing.T) {
	dir := t.TempDir()
	err := runInit(dir, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, name := range []string{
		"agents/assistant/Clawfile",
		"agents/assistant/AGENTS.md",
		"claw-pod.yml",
		".env.example",
		".gitignore",
	} {
		path := filepath.Join(dir, name)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			t.Errorf("expected %s to exist", name)
		}
	}

	podData, err := os.ReadFile(filepath.Join(dir, "claw-pod.yml"))
	if err != nil {
		t.Fatalf("read pod file: %v", err)
	}
	pod := string(podData)
	if !strings.Contains(pod, "build:") || !strings.Contains(pod, "context: ./agents/assistant") {
		t.Fatalf("expected pod scaffold to include build context; got:\n%s", pod)
	}
	if !strings.Contains(pod, "agent: ./agents/assistant/AGENTS.md") {
		t.Fatalf("expected pod scaffold to include pod-root-relative agent path; got:\n%s", pod)
	}
	for _, envLine := range []string{
		"DISCORD_BOT_TOKEN: \"${DISCORD_BOT_TOKEN}\"",
		"DISCORD_BOT_ID: \"${DISCORD_BOT_ID}\"",
	} {
		if !strings.Contains(pod, envLine) {
			t.Fatalf("expected pod scaffold to include %q; got:\n%s", envLine, pod)
		}
	}
}

func TestInitScaffoldRefusesToOverwrite(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "agents", "assistant"), 0o755); err != nil {
		t.Fatalf("create agent dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "agents", "assistant", "Clawfile"), []byte("existing"), 0o644); err != nil {
		t.Fatalf("seed existing Clawfile: %v", err)
	}

	err := runInit(dir, "")
	if err == nil {
		t.Fatal("expected error when Clawfile already exists")
	}
}

func TestInitFromOpenClawConfig(t *testing.T) {
	// Create a mock OpenClaw config directory
	srcDir := t.TempDir()
	configDir := filepath.Join(srcDir, "config")
	os.MkdirAll(configDir, 0755)
	os.WriteFile(filepath.Join(configDir, "openclaw.json"), []byte(`{
		"channels": {
			"discord": {"enabled": true, "token": "${DISCORD_BOT_TOKEN}"},
			"telegram": {"enabled": true, "token": "${TELEGRAM_BOT_TOKEN}"}
		},
		"agents": {
			"defaults": {
				"model": {"primary": "openrouter/anthropic/claude-sonnet-4"}
			}
		}
	}`), 0644)

	destDir := t.TempDir()
	err := runInit(destDir, srcDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify generated Clawfile has detected handles
	clawfile, _ := os.ReadFile(filepath.Join(destDir, "Clawfile"))
	content := string(clawfile)
	if !strings.Contains(content, "HANDLE discord") {
		t.Error("expected Clawfile to contain HANDLE discord")
	}
	if !strings.Contains(content, "HANDLE telegram") {
		t.Error("expected Clawfile to contain HANDLE telegram")
	}
}

func TestInitScaffoldAppendsGitignoreEntries(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("node_modules/\n"), 0o644); err != nil {
		t.Fatalf("seed .gitignore: %v", err)
	}

	if err := runInit(dir, ""); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	gitignoreData, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil {
		t.Fatalf("read .gitignore: %v", err)
	}
	gitignore := string(gitignoreData)
	for _, expected := range []string{"node_modules/", ".env", "*.generated.*"} {
		if !strings.Contains(gitignore, expected) {
			t.Errorf("expected .gitignore to contain %q, got:\n%s", expected, gitignore)
		}
	}
}
