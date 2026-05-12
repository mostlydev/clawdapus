package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mostlydev/clawdapus/internal/driver/hermes"
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

	for _, name := range []string{
		"agents/assistant/Clawfile",
		"agents/assistant/AGENTS.md",
		"claw-pod.yml",
		".env.example",
		"MIGRATION.md",
	} {
		if _, err := os.Stat(filepath.Join(destDir, name)); err != nil {
			t.Fatalf("expected %s to exist: %v", name, err)
		}
	}

	clawfile, err := os.ReadFile(filepath.Join(destDir, "agents", "assistant", "Clawfile"))
	if err != nil {
		t.Fatalf("read imported Clawfile: %v", err)
	}
	content := string(clawfile)
	if !strings.Contains(content, "HANDLE discord") {
		t.Error("expected Clawfile to contain HANDLE discord")
	}
	if !strings.Contains(content, "HANDLE telegram") {
		t.Error("expected Clawfile to contain HANDLE telegram")
	}
	if _, err := os.Stat(filepath.Join(destDir, "Clawfile")); !os.IsNotExist(err) {
		t.Fatalf("expected no legacy root Clawfile, got err=%v", err)
	}
}

func TestInitFromOpenClawWithHermesTarget(t *testing.T) {
	srcDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(srcDir, "openclaw.json"), []byte(`{
		"channels": {
			"slack": {"enabled": true, "token": "${SLACK_BOT_TOKEN}"}
		},
		"agents": {
			"defaults": {
				"model": {"primary": "openrouter/anthropic/claude-sonnet-4"}
			}
		}
	}`), 0o644); err != nil {
		t.Fatalf("write source config: %v", err)
	}

	destDir := t.TempDir()
	err := runInitWithOptions(destDir, srcDir, initScaffoldOptions{ClawType: "hermes"}, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	clawfile, err := os.ReadFile(filepath.Join(destDir, "agents", "assistant", "Clawfile"))
	if err != nil {
		t.Fatalf("read Clawfile: %v", err)
	}
	for _, expected := range []string{
		"CLAW_TYPE hermes",
		"HANDLE slack",
	} {
		if !strings.Contains(string(clawfile), expected) {
			t.Fatalf("expected imported Hermes Clawfile to contain %q, got:\n%s", expected, clawfile)
		}
	}
	env, err := os.ReadFile(filepath.Join(destDir, ".env.example"))
	if err != nil {
		t.Fatalf("read .env.example: %v", err)
	}
	for _, expected := range []string{"SLACK_BOT_TOKEN=", "SLACK_APP_TOKEN=", "SLACK_BOT_ID="} {
		if !strings.Contains(string(env), expected) {
			t.Fatalf("expected .env.example to contain %q, got:\n%s", expected, env)
		}
	}
}

func TestInitFromHermesWithOpenClawTarget(t *testing.T) {
	srcDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(srcDir, "config.yaml"), []byte(`model:
  provider: openrouter
  default: anthropic/claude-sonnet-4
`), 0o644); err != nil {
		t.Fatalf("write config.yaml: %v", err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, ".env"), []byte(`DISCORD_BOT_TOKEN=token
DISCORD_BOT_ID=111
DISCORD_ALLOWED_USERS=222,333
`), 0o600); err != nil {
		t.Fatalf("write .env: %v", err)
	}

	destDir := t.TempDir()
	err := runInitWithOptions(destDir, srcDir, initScaffoldOptions{ClawType: "openclaw"}, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	pod, err := os.ReadFile(filepath.Join(destDir, "claw-pod.yml"))
	if err != nil {
		t.Fatalf("read pod: %v", err)
	}
	for _, expected := range []string{
		"channel://discord",
		`- "222"`,
		`- "333"`,
	} {
		if !strings.Contains(string(pod), expected) {
			t.Fatalf("expected pod to contain %q, got:\n%s", expected, pod)
		}
	}
}

func TestInitFromRejectsUnsupportedTargetType(t *testing.T) {
	srcDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(srcDir, "openclaw.json"), []byte(`{"channels":{}}`), 0o644); err != nil {
		t.Fatalf("write source config: %v", err)
	}

	err := runInitWithOptions(t.TempDir(), srcDir, initScaffoldOptions{ClawType: "nanobot"}, false)
	if err == nil {
		t.Fatal("expected unsupported import target to fail")
	}
	if !strings.Contains(err.Error(), "import target") {
		t.Fatalf("expected import target error, got: %v", err)
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

func TestInitScaffoldTypeDefaults(t *testing.T) {
	tests := []struct {
		name      string
		clawType  string
		baseImage string
	}{
		{name: "generic", clawType: "generic", baseImage: "alpine:3.20"},
		{name: "hermes", clawType: "hermes", baseImage: hermes.BaseImageTag},
		{name: "nanoclaw", clawType: "nanoclaw", baseImage: "nanoclaw-orchestrator:latest"},
		{name: "microclaw", clawType: "microclaw", baseImage: "microclaw:latest"},
		{name: "nullclaw", clawType: "nullclaw", baseImage: "nullclaw:latest"},
		{name: "nanobot", clawType: "nanobot", baseImage: "nanobot:latest"},
		{name: "picoclaw", clawType: "picoclaw", baseImage: "picoclaw:latest"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := runInitWithOptions(dir, "", initScaffoldOptions{ClawType: tc.clawType}, false); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			clawfileData, err := os.ReadFile(filepath.Join(dir, "agents", "assistant", "Clawfile"))
			if err != nil {
				t.Fatalf("read Clawfile: %v", err)
			}
			clawfile := string(clawfileData)
			if !strings.Contains(clawfile, "FROM "+tc.baseImage) {
				t.Fatalf("expected %s scaffold to use %s base image, got:\n%s", tc.clawType, tc.baseImage, clawfile)
			}
			if !strings.Contains(clawfile, "CLAW_TYPE "+tc.clawType) {
				t.Fatalf("expected scaffold to set CLAW_TYPE %s, got:\n%s", tc.clawType, clawfile)
			}
		})
	}
}

func TestInitScaffoldHermesSlackIncludesSocketModeTokens(t *testing.T) {
	dir := t.TempDir()
	err := runInitWithOptions(dir, "", initScaffoldOptions{
		ClawType: "hermes",
		Platform: "slack",
	}, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	podData, err := os.ReadFile(filepath.Join(dir, "claw-pod.yml"))
	if err != nil {
		t.Fatalf("read pod file: %v", err)
	}
	pod := string(podData)
	for _, expected := range []string{
		"SLACK_BOT_TOKEN: \"${SLACK_BOT_TOKEN}\"",
		"SLACK_APP_TOKEN: \"${SLACK_APP_TOKEN}\"",
		"SLACK_BOT_ID: \"${SLACK_BOT_ID}\"",
	} {
		if !strings.Contains(pod, expected) {
			t.Fatalf("expected pod scaffold to include %q; got:\n%s", expected, pod)
		}
	}

	envData, err := os.ReadFile(filepath.Join(dir, ".env.example"))
	if err != nil {
		t.Fatalf("read .env.example: %v", err)
	}
	env := string(envData)
	for _, expected := range []string{
		"SLACK_BOT_TOKEN=",
		"SLACK_APP_TOKEN=",
		"SLACK_BOT_ID=",
	} {
		if !strings.Contains(env, expected) {
			t.Fatalf("expected .env.example to contain %q; got:\n%s", expected, env)
		}
	}
}

func TestInitTypeFlagUsageListsAllScaffoldTypes(t *testing.T) {
	flag := initCmd.Flags().Lookup("type")
	if flag == nil {
		t.Fatal("expected init --type flag to exist")
	}

	usage := flag.Usage
	for _, typ := range []string{"openclaw", "hermes", "nanoclaw", "microclaw", "nullclaw", "nanobot", "picoclaw", "generic"} {
		if !strings.Contains(usage, typ) {
			t.Fatalf("expected init --type usage to include %q, got: %s", typ, usage)
		}
	}
}
