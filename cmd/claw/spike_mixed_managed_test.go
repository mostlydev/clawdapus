//go:build spike

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func TestSpikeMixedManagedTypesCoexist(t *testing.T) {
	if err := exec.Command("docker", "info").Run(); err != nil {
		t.Skipf("docker not available: %v", err)
	}

	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	repoRoot := filepath.Join(filepath.Dir(thisFile), "..", "..")

	openFixture := filepath.Join(repoRoot, "testdata", "openclaw-stub")
	nanoFixture := filepath.Join(repoRoot, "testdata", "nanobot-stub")

	openTag := fmt.Sprintf("claw-spike-openclaw:%d", time.Now().UnixNano())
	nanoTag := fmt.Sprintf("claw-spike-nanobot:%d", time.Now().UnixNano())
	spikeBuildImage(t, openFixture, openTag, "Clawfile")
	spikeBuildImage(t, nanoFixture, nanoTag, "Clawfile")

	t.Cleanup(func() {
		_, _ = exec.Command("docker", "image", "rm", "-f", openTag, nanoTag).CombinedOutput()
	})

	spikeEnsureRepoInfraImages(t, repoRoot, infraComponentClawdash)

	workDir := t.TempDir()
	agentsDir := filepath.Join(workDir, "agents")
	if err := os.MkdirAll(agentsDir, 0o755); err != nil {
		t.Fatalf("create agents dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(agentsDir, "OPEN.md"), []byte("# Open Agent\n\nYou are open."), 0o644); err != nil {
		t.Fatalf("write OPEN.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(agentsDir, "NANO.md"), []byte("# Nano Agent\n\nYou are nano."), 0o644); err != nil {
		t.Fatalf("write NANO.md: %v", err)
	}

	podPath := filepath.Join(workDir, "claw-pod.yml")
	podYAML := fmt.Sprintf(`x-claw:
  pod: mixed-managed-spike

services:
  open:
    image: %s
    x-claw:
      agent: ./agents/OPEN.md

  nano:
    image: %s
    x-claw:
      agent: ./agents/NANO.md
    environment:
      ANTHROPIC_API_KEY: sk-spike-anthropic
`, openTag, nanoTag)
	if err := os.WriteFile(podPath, []byte(podYAML), 0o644); err != nil {
		t.Fatalf("write pod file: %v", err)
	}

	prevDetach := composeUpDetach
	composeUpDetach = true
	defer func() { composeUpDetach = prevDetach }()
	t.Setenv("CLAWDASH_ADDR", ":"+spikeFreePort(t))

	if err := runComposeUp(podPath); err != nil {
		t.Fatalf("runComposeUp failed: %v", err)
	}

	composePath := filepath.Join(workDir, "compose.generated.yml")
	t.Cleanup(func() {
		cmd := exec.Command("docker", "compose", "-f", composePath, "down", "--volumes", "--remove-orphans")
		_, _ = cmd.CombinedOutput()
	})

	openConfig := filepath.Join(workDir, ".claw-runtime", "open", "config", "openclaw.json")
	if _, err := os.Stat(openConfig); err != nil {
		t.Fatalf("openclaw config not generated: %v", err)
	}
	nanoConfig := filepath.Join(workDir, ".claw-runtime", "nano", "nanobot-home", "config.json")
	if _, err := os.Stat(nanoConfig); err != nil {
		t.Fatalf("nanobot config not generated: %v", err)
	}
	nanoSeed := filepath.Join(workDir, ".claw-runtime", "nano", "nanobot-home", "workspace", "AGENTS.md")
	if _, err := os.Stat(nanoSeed); err != nil {
		t.Fatalf("nanobot seeded AGENTS.md not generated: %v", err)
	}

	nanoBytes, err := os.ReadFile(nanoConfig)
	if err != nil {
		t.Fatalf("read nanobot config: %v", err)
	}
	var nano map[string]interface{}
	if err := yaml.Unmarshal(nanoBytes, &nano); err != nil {
		t.Fatalf("parse nanobot config json: %v", err)
	}
	agents, _ := nano["agents"].(map[string]interface{})
	defaults, _ := agents["defaults"].(map[string]interface{})
	if got := defaults["model"]; got != "anthropic/claude-sonnet-4" {
		t.Fatalf("expected nanobot agents.defaults.model=anthropic/claude-sonnet-4, got %v", got)
	}

	composeBytes, err := os.ReadFile(composePath)
	if err != nil {
		t.Fatalf("read compose output: %v", err)
	}
	var compose struct {
		Services map[string]struct {
			ReadOnly    bool              `yaml:"read_only"`
			Environment map[string]string `yaml:"environment"`
			Volumes     []string          `yaml:"volumes"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal(composeBytes, &compose); err != nil {
		t.Fatalf("parse compose yaml: %v", err)
	}

	openSvc, ok := compose.Services["open"]
	if !ok {
		t.Fatalf("compose missing open service")
	}
	if !openSvc.ReadOnly {
		t.Fatalf("expected open service to remain read_only")
	}
	if openSvc.Environment["OPENCLAW_CONFIG_PATH"] != "/root/.openclaw/config/openclaw.json" {
		t.Fatalf("unexpected open OPENCLAW_CONFIG_PATH: %q", openSvc.Environment["OPENCLAW_CONFIG_PATH"])
	}

	nanoSvc, ok := compose.Services["nano"]
	if !ok {
		t.Fatalf("compose missing nano service")
	}
	if !nanoSvc.ReadOnly {
		t.Fatalf("expected nano service to remain read_only")
	}
	if nanoSvc.Environment["CLAW_MANAGED"] != "true" {
		t.Fatalf("unexpected nano CLAW_MANAGED: %q", nanoSvc.Environment["CLAW_MANAGED"])
	}
	if nanoSvc.Environment["HOME"] != "/root" {
		t.Fatalf("unexpected nano HOME: %q", nanoSvc.Environment["HOME"])
	}

	for _, service := range []string{"open", "nano"} {
		out, err := exec.Command("docker", "compose", "-f", composePath, "ps", "-q", service).Output()
		if err != nil {
			t.Fatalf("docker compose ps %s: %v", service, err)
		}
		id := strings.TrimSpace(string(out))
		if id == "" {
			t.Fatalf("service %s has no container ID", service)
		}
		spikeWaitRunning(t, id, 30*time.Second)
		spikeWaitHealthy(t, id, 45*time.Second)
	}
}
