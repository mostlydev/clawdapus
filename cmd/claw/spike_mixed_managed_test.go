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
	microFixture := filepath.Join(repoRoot, "testdata", "microclaw-stub")

	openTag := fmt.Sprintf("claw-spike-openclaw:%d", time.Now().UnixNano())
	microTag := fmt.Sprintf("claw-spike-microclaw:%d", time.Now().UnixNano())
	spikeBuildImage(t, openFixture, openTag, "Clawfile")
	spikeBuildImage(t, microFixture, microTag, "Clawfile")

	t.Cleanup(func() {
		_, _ = exec.Command("docker", "image", "rm", "-f", openTag, microTag).CombinedOutput()
	})

	workDir := t.TempDir()
	agentsDir := filepath.Join(workDir, "agents")
	if err := os.MkdirAll(agentsDir, 0o755); err != nil {
		t.Fatalf("create agents dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(agentsDir, "OPEN.md"), []byte("# Open Agent\n\nYou are open."), 0o644); err != nil {
		t.Fatalf("write OPEN.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(agentsDir, "MICRO.md"), []byte("# Micro Agent\n\nYou are micro."), 0o644); err != nil {
		t.Fatalf("write MICRO.md: %v", err)
	}

	podPath := filepath.Join(workDir, "claw-pod.yml")
	podYAML := fmt.Sprintf(`x-claw:
  pod: mixed-managed-spike

services:
  open:
    image: %s
    x-claw:
      agent: ./agents/OPEN.md

  micro:
    image: %s
    x-claw:
      agent: ./agents/MICRO.md
    environment:
      ANTHROPIC_API_KEY: sk-spike-anthropic
`, openTag, microTag)
	if err := os.WriteFile(podPath, []byte(podYAML), 0o644); err != nil {
		t.Fatalf("write pod file: %v", err)
	}

	prevDetach := composeUpDetach
	composeUpDetach = true
	defer func() { composeUpDetach = prevDetach }()

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
	microConfig := filepath.Join(workDir, ".claw-runtime", "micro", "config", "microclaw.config.yaml")
	if _, err := os.Stat(microConfig); err != nil {
		t.Fatalf("microclaw config not generated: %v", err)
	}
	microSeed := filepath.Join(workDir, ".claw-runtime", "micro", "data", "runtime", "groups", "AGENTS.md")
	if _, err := os.Stat(microSeed); err != nil {
		t.Fatalf("microclaw seeded AGENTS.md not generated: %v", err)
	}

	microBytes, err := os.ReadFile(microConfig)
	if err != nil {
		t.Fatalf("read microclaw config: %v", err)
	}
	var micro map[string]interface{}
	if err := yaml.Unmarshal(microBytes, &micro); err != nil {
		t.Fatalf("parse microclaw config yaml: %v", err)
	}
	if got := micro["llm_provider"]; got != "anthropic" {
		t.Fatalf("expected microclaw llm_provider=anthropic, got %v", got)
	}
	if got := micro["model"]; got != "claude-sonnet-4" {
		t.Fatalf("expected microclaw model=claude-sonnet-4, got %v", got)
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
	if openSvc.Environment["OPENCLAW_CONFIG_PATH"] != "/app/config/openclaw.json" {
		t.Fatalf("unexpected open OPENCLAW_CONFIG_PATH: %q", openSvc.Environment["OPENCLAW_CONFIG_PATH"])
	}

	microSvc, ok := compose.Services["micro"]
	if !ok {
		t.Fatalf("compose missing micro service")
	}
	if microSvc.ReadOnly {
		t.Fatalf("expected micro service to be writable")
	}
	if microSvc.Environment["MICROCLAW_CONFIG"] != "/app/config/microclaw.config.yaml" {
		t.Fatalf("unexpected micro MICROCLAW_CONFIG: %q", microSvc.Environment["MICROCLAW_CONFIG"])
	}

	for _, service := range []string{"open", "micro"} {
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
