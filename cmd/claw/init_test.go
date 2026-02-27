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
