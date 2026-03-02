package main

import (
	"io"
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
		"DISCORD_BOT_ID: ${WESTIN_DISCORD_BOT_ID}",
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

func TestAgentAddTypeDefaults(t *testing.T) {
	tests := []struct {
		name      string
		agentName string
		clawType  string
		baseImage string
	}{
		{name: "generic", agentName: "genericone", clawType: "generic", baseImage: "alpine:3.20"},
		{name: "nanoclaw", agentName: "nanoclawone", clawType: "nanoclaw", baseImage: "node:22-slim"},
		{name: "microclaw", agentName: "microclawone", clawType: "microclaw", baseImage: "node:22-slim"},
		{name: "nullclaw", agentName: "nullclawone", clawType: "nullclaw", baseImage: "node:22-slim"},
		{name: "nanobot", agentName: "nanobotone", clawType: "nanobot", baseImage: "nanobot:latest"},
		{name: "picoclaw", agentName: "picoclawone", clawType: "picoclaw", baseImage: "docker.io/sipeed/picoclaw:latest"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			podPath := seedCanonicalProject(t, dir)

			opts := agentAddOptions{
				AgentName: tc.agentName,
				ClawType:  tc.clawType,
				Model:     defaultModel,
				Cllama:    "no",
				Platform:  "none",
				AssumeYes: true,
			}
			if err := runAgentAdd(podPath, opts); err != nil {
				t.Fatalf("runAgentAdd failed: %v", err)
			}

			clawfileData, err := os.ReadFile(filepath.Join(dir, "agents", tc.agentName, "Clawfile"))
			if err != nil {
				t.Fatalf("read generated Clawfile: %v", err)
			}
			clawfile := string(clawfileData)
			if !strings.Contains(clawfile, "FROM "+tc.baseImage) {
				t.Fatalf("expected %s agent Clawfile to use %s base image, got:\n%s", tc.clawType, tc.baseImage, clawfile)
			}
			if !strings.Contains(clawfile, "CLAW_TYPE "+tc.clawType) {
				t.Fatalf("expected agent Clawfile to set CLAW_TYPE %s, got:\n%s", tc.clawType, clawfile)
			}
		})
	}
}

func TestAgentAddTypeFlagUsageListsAllScaffoldTypes(t *testing.T) {
	flag := agentAddCmd.Flags().Lookup("type")
	if flag == nil {
		t.Fatal("expected agent add --type flag to exist")
	}

	usage := flag.Usage
	for _, typ := range []string{"openclaw", "nanoclaw", "microclaw", "nullclaw", "nanobot", "picoclaw", "generic"} {
		if !strings.Contains(usage, typ) {
			t.Fatalf("expected agent add --type usage to include %q, got: %s", typ, usage)
		}
	}
}

func TestAgentAddWarnsOnEnvPrefixCollision(t *testing.T) {
	dir := t.TempDir()
	podPath := seedPrefixCollisionProject(t, dir)

	opts := agentAddOptions{
		AgentName: "my_bot",
		ClawType:  "openclaw",
		Model:     defaultModel,
		Cllama:    "inherit",
		Platform:  "discord",
		AssumeYes: true,
	}
	out, err := captureStdout(t, func() error {
		return runAgentAdd(podPath, opts)
	})
	if err != nil {
		t.Fatalf("runAgentAdd failed: %v", err)
	}
	for _, expected := range []string{
		"[claw] warnings:",
		"env prefix MY_BOT for agent \"my_bot\" matches existing service(s): my-bot",
		".env.example already contains MY_BOT_DISCORD_BOT_TOKEN; my_bot will reuse that value",
	} {
		if !strings.Contains(out, expected) {
			t.Fatalf("expected output to contain %q, got:\n%s", expected, out)
		}
	}
}

func TestAgentAddPreservesFlatProjectLayout(t *testing.T) {
	dir := t.TempDir()
	podPath := seedFlatProject(t, dir)

	opts := agentAddOptions{
		AgentName: "westin",
		ClawType:  "openclaw",
		Model:     defaultModel,
		Cllama:    "inherit",
		Platform:  "discord",
		AssumeYes: true,
	}
	if err := runAgentAdd(podPath, opts); err != nil {
		t.Fatalf("runAgentAdd failed: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dir, "Clawfile.westin")); err != nil {
		t.Fatalf("expected Clawfile.westin to exist: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "AGENTS-westin.md")); err != nil {
		t.Fatalf("expected AGENTS-westin.md to exist: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "agents", "westin")); !os.IsNotExist(err) {
		t.Fatalf("expected agents/westin not to be created for flat project")
	}

	clawfileData, err := os.ReadFile(filepath.Join(dir, "Clawfile.westin"))
	if err != nil {
		t.Fatalf("read Clawfile.westin: %v", err)
	}
	if !strings.Contains(string(clawfileData), "AGENT AGENTS-westin.md") {
		t.Fatalf("expected flat Clawfile to reference AGENTS-westin.md, got:\n%s", string(clawfileData))
	}

	podData, err := os.ReadFile(podPath)
	if err != nil {
		t.Fatalf("read pod file: %v", err)
	}
	podText := string(podData)
	for _, expected := range []string{
		"westin:",
		"context: .",
		"dockerfile: Clawfile.westin",
		"agent: ./AGENTS-westin.md",
		"DISCORD_BOT_ID: ${WESTIN_DISCORD_BOT_ID}",
	} {
		if !strings.Contains(podText, expected) {
			t.Fatalf("expected pod to contain %q, got:\n%s", expected, podText)
		}
	}
}

func TestAgentAddLayoutOverrideFlatOnCanonicalProject(t *testing.T) {
	dir := t.TempDir()
	podPath := seedCanonicalProject(t, dir)

	opts := agentAddOptions{
		AgentName: "analyst",
		ClawType:  "openclaw",
		Model:     defaultModel,
		Cllama:    "inherit",
		Platform:  "none",
		Layout:    "flat",
		AssumeYes: true,
	}
	if err := runAgentAdd(podPath, opts); err != nil {
		t.Fatalf("runAgentAdd failed: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dir, "Clawfile.analyst")); err != nil {
		t.Fatalf("expected Clawfile.analyst to exist: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "AGENTS-analyst.md")); err != nil {
		t.Fatalf("expected AGENTS-analyst.md to exist: %v", err)
	}
}

func TestAgentAddRejectsInvalidLayoutOverride(t *testing.T) {
	dir := t.TempDir()
	podPath := seedCanonicalProject(t, dir)

	err := runAgentAdd(podPath, agentAddOptions{
		AgentName: "analyst",
		ClawType:  "openclaw",
		Model:     defaultModel,
		Cllama:    "inherit",
		Platform:  "none",
		Layout:    "weird",
		AssumeYes: true,
	})
	if err == nil {
		t.Fatal("expected invalid --layout to fail")
	}
	if !strings.Contains(err.Error(), "invalid --layout") {
		t.Fatalf("expected invalid layout error, got: %v", err)
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

func seedPrefixCollisionProject(t *testing.T, dir string) string {
	t.Helper()

	if err := os.MkdirAll(filepath.Join(dir, "agents", "my-bot"), 0o755); err != nil {
		t.Fatalf("create my-bot dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "agents", "my-bot", "Clawfile"), []byte("FROM openclaw:latest\nCLAW_TYPE openclaw\nAGENT AGENTS.md\nMODEL primary "+defaultModel+"\n"), 0o644); err != nil {
		t.Fatalf("write my-bot Clawfile: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "agents", "my-bot", "AGENTS.md"), []byte("# My Bot"), 0o644); err != nil {
		t.Fatalf("write my-bot AGENTS.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".env.example"), []byte("MY_BOT_DISCORD_BOT_TOKEN=\nMY_BOT_DISCORD_BOT_ID=\n"), 0o644); err != nil {
		t.Fatalf("write .env.example: %v", err)
	}

	podContent := `x-claw:
  pod: desk
services:
  my-bot:
    image: desk-my-bot:latest
    build:
      context: ./agents/my-bot
    x-claw:
      agent: ./agents/my-bot/AGENTS.md
      cllama: passthrough
      cllama-env:
        OPENROUTER_API_KEY: "${OPENROUTER_API_KEY}"
      handles:
        discord:
          id: "${MY_BOT_DISCORD_BOT_ID}"
          username: "my-bot"
    environment:
      DISCORD_BOT_TOKEN: "${MY_BOT_DISCORD_BOT_TOKEN}"
`
	podPath := filepath.Join(dir, "claw-pod.yml")
	if err := os.WriteFile(podPath, []byte(podContent), 0o644); err != nil {
		t.Fatalf("write pod file: %v", err)
	}
	return podPath
}

func seedFlatProject(t *testing.T, dir string) string {
	t.Helper()

	if err := os.WriteFile(filepath.Join(dir, "Clawfile"), []byte("FROM openclaw:latest\nCLAW_TYPE openclaw\nAGENT AGENTS.md\nMODEL primary "+defaultModel+"\n"), 0o644); err != nil {
		t.Fatalf("write Clawfile: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("# Assistant"), 0o644); err != nil {
		t.Fatalf("write AGENTS.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".env.example"), []byte("DISCORD_BOT_TOKEN=\nDISCORD_BOT_ID=\n"), 0o644); err != nil {
		t.Fatalf("write .env.example: %v", err)
	}

	podContent := `x-claw:
  pod: desk
services:
  assistant:
    image: desk-assistant:latest
    build:
      context: .
      dockerfile: Clawfile
    x-claw:
      agent: ./AGENTS.md
      cllama: passthrough
      cllama-env:
        OPENROUTER_API_KEY: "${OPENROUTER_API_KEY}"
      handles:
        discord:
          id: "${DISCORD_BOT_ID}"
          username: "assistant"
    environment:
      DISCORD_BOT_TOKEN: "${DISCORD_BOT_TOKEN}"
`
	podPath := filepath.Join(dir, "claw-pod.yml")
	if err := os.WriteFile(podPath, []byte(podContent), 0o644); err != nil {
		t.Fatalf("write pod file: %v", err)
	}
	return podPath
}

func captureStdout(t *testing.T, fn func() error) (string, error) {
	t.Helper()

	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("create stdout pipe: %v", err)
	}
	os.Stdout = w

	runErr := fn()

	_ = w.Close()
	os.Stdout = old
	data, readErr := io.ReadAll(r)
	_ = r.Close()
	if readErr != nil {
		t.Fatalf("read captured stdout: %v", readErr)
	}
	return string(data), runErr
}
