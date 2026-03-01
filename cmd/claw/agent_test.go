package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAgentAddCreatesFilesAndUpdatesPod(t *testing.T) {
	dir := t.TempDir()
	podPath := seedCanonicalProject(t, dir)

	opts := agentAddOptions{
		AgentName:   "westin",
		ClawType:    "openclaw",
		Model:       defaultModel,
		Cllama:      "inherit",
		Platform:    "discord",
		VolumeSpecs: []string{"shared:read-only"},
		AssumeYes:   true,
	}
	if err := runAgentAdd(podPath, opts); err != nil {
		t.Fatalf("runAgentAdd failed: %v", err)
	}

	for _, rel := range []string{
		"agents/westin/Clawfile",
		"agents/westin/AGENTS.md",
	} {
		if _, err := os.Stat(filepath.Join(dir, rel)); err != nil {
			t.Fatalf("expected %s to exist: %v", rel, err)
		}
	}

	podData, err := os.ReadFile(podPath)
	if err != nil {
		t.Fatalf("read pod file: %v", err)
	}
	podText := string(podData)
	for _, expect := range []string{
		"westin:",
		"image: desk-westin:latest",
		"context: ./agents/westin",
		"agent: ./agents/westin/AGENTS.md",
		"DISCORD_BOT_TOKEN: ${WESTIN_DISCORD_BOT_TOKEN}",
		"volume://shared read-only",
	} {
		if !strings.Contains(podText, expect) {
			t.Fatalf("expected pod to contain %q, got:\n%s", expect, podText)
		}
	}

	envData, err := os.ReadFile(filepath.Join(dir, ".env.example"))
	if err != nil {
		t.Fatalf("read .env.example: %v", err)
	}
	envText := string(envData)
	for _, key := range []string{
		"WESTIN_DISCORD_BOT_TOKEN=",
		"WESTIN_DISCORD_BOT_ID=",
	} {
		if !strings.Contains(envText, key) {
			t.Fatalf("expected .env.example to contain %q, got:\n%s", key, envText)
		}
	}
}

func TestAgentAddDryRunWritesNothing(t *testing.T) {
	dir := t.TempDir()
	podPath := seedCanonicalProject(t, dir)

	originalPod, err := os.ReadFile(podPath)
	if err != nil {
		t.Fatalf("read pod file: %v", err)
	}

	opts := agentAddOptions{
		AgentName: "westin",
		ClawType:  "openclaw",
		Model:     defaultModel,
		Cllama:    "inherit",
		Platform:  "discord",
		DryRun:    true,
		AssumeYes: true,
	}
	if err := runAgentAdd(podPath, opts); err != nil {
		t.Fatalf("runAgentAdd failed: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dir, "agents", "westin")); !os.IsNotExist(err) {
		t.Fatalf("expected agents/westin not to exist after dry-run")
	}
	afterPod, err := os.ReadFile(podPath)
	if err != nil {
		t.Fatalf("read pod file after dry-run: %v", err)
	}
	if string(afterPod) != string(originalPod) {
		t.Fatalf("pod file changed in dry-run mode")
	}
}

func TestAgentAddReuseContractDoesNotCreateLocalAgentFile(t *testing.T) {
	dir := t.TempDir()
	podPath := seedCanonicalProject(t, dir)

	if err := os.MkdirAll(filepath.Join(dir, "shared", "trader"), 0o755); err != nil {
		t.Fatalf("create shared contract dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "shared", "trader", "AGENTS.md"), []byte("# Trader"), 0o644); err != nil {
		t.Fatalf("write shared contract: %v", err)
	}

	opts := agentAddOptions{
		AgentName:    "allen",
		ClawType:     "openclaw",
		Model:        defaultModel,
		Cllama:       "inherit",
		Platform:     "none",
		ContractPath: "./shared/trader/AGENTS.md",
		AssumeYes:    true,
	}
	if err := runAgentAdd(podPath, opts); err != nil {
		t.Fatalf("runAgentAdd failed: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dir, "agents", "allen", "AGENTS.md")); !os.IsNotExist(err) {
		t.Fatalf("expected agents/allen/AGENTS.md not to be created when reusing contract")
	}

	podData, err := os.ReadFile(podPath)
	if err != nil {
		t.Fatalf("read pod file: %v", err)
	}
	if !strings.Contains(string(podData), "agent: ./shared/trader/AGENTS.md") {
		t.Fatalf("expected pod to reference shared contract, got:\n%s", string(podData))
	}
}

func TestAgentAddRejectsAbsoluteContractPath(t *testing.T) {
	dir := t.TempDir()
	podPath := seedCanonicalProject(t, dir)
	absContract := filepath.Join(dir, "agents", "assistant", "AGENTS.md")

	opts := agentAddOptions{
		AgentName:    "newbie",
		ClawType:     "openclaw",
		Model:        defaultModel,
		Cllama:       "inherit",
		Platform:     "none",
		ContractPath: absContract,
		AssumeYes:    true,
	}
	err := runAgentAdd(podPath, opts)
	if err == nil {
		t.Fatal("expected absolute --contract path to be rejected")
	}
	if !strings.Contains(err.Error(), "absolute contract paths are not supported") {
		t.Fatalf("expected absolute-path error, got: %v", err)
	}
}

func seedCanonicalProject(t *testing.T, dir string) string {
	t.Helper()

	if err := os.MkdirAll(filepath.Join(dir, "agents", "assistant"), 0o755); err != nil {
		t.Fatalf("create assistant dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "agents", "assistant", "Clawfile"), []byte("FROM openclaw:latest\nCLAW_TYPE openclaw\nAGENT AGENTS.md\nMODEL primary "+defaultModel+"\n"), 0o644); err != nil {
		t.Fatalf("write assistant Clawfile: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "agents", "assistant", "AGENTS.md"), []byte("# Assistant"), 0o644); err != nil {
		t.Fatalf("write assistant AGENTS.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".env.example"), []byte("OPENROUTER_API_KEY=\nDISCORD_BOT_TOKEN=\nDISCORD_BOT_ID=\n"), 0o644); err != nil {
		t.Fatalf("write .env.example: %v", err)
	}

	podContent := `x-claw:
  pod: desk
services:
  assistant:
    image: desk-assistant:latest
    build:
      context: ./agents/assistant
    x-claw:
      agent: ./agents/assistant/AGENTS.md
      cllama: passthrough
      cllama-env:
        OPENROUTER_API_KEY: "${OPENROUTER_API_KEY}"
      handles:
        discord:
          id: "${DISCORD_BOT_ID}"
          username: "assistant"
      surfaces:
        - "volume://shared read-write"
    environment:
      DISCORD_BOT_TOKEN: "${DISCORD_BOT_TOKEN}"
volumes:
  shared: {}
`
	podPath := filepath.Join(dir, "claw-pod.yml")
	if err := os.WriteFile(podPath, []byte(podContent), 0o644); err != nil {
		t.Fatalf("write pod file: %v", err)
	}
	return podPath
}
