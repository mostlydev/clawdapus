//go:build spike

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestSpikeCapabilityWaveArtifacts verifies that claw up correctly compiles the
// capability evolution wave (ADR-020 + ADR-021) context artifacts when a service
// declares memory capability subscriptions:
//
//   - memory.json is written into the agent's context dir
//   - The manifest carries the correct base_url (resolved from the memory
//     service's claw.describe descriptor and its expose port)
//   - recall/retain/forget paths match the descriptor
//   - containers start and reach the running state
//
// Does not require Discord tokens, LLM API keys, or a real cllama inference
// call.  It does require Docker and network access to pull alpine:3.20.
//
// Run with: go test -tags spike -v -run TestSpikeCapabilityWaveArtifacts ./cmd/claw/...
func TestSpikeCapabilityWaveArtifacts(t *testing.T) {
	if err := exec.Command("docker", "info").Run(); err != nil {
		t.Skipf("docker not available: %v", err)
	}

	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	repoRoot := filepath.Join(filepath.Dir(thisFile), "..", "..")

	// ── Build images ────────────────────────────────────────────────────────

	agentTag := fmt.Sprintf("claw-spike-cwav-agent:%d", time.Now().UnixNano())
	memTag := fmt.Sprintf("claw-spike-cwav-mem:%d", time.Now().UnixNano())
	spikeBuildImage(t, filepath.Join(repoRoot, "testdata", "openclaw-stub"), agentTag, "Clawfile")
	spikeBuildImage(t, filepath.Join(repoRoot, "examples", "reference-memory"), memTag, "Dockerfile")
	t.Cleanup(func() {
		exec.Command("docker", "image", "rm", "-f", agentTag, memTag).CombinedOutput()
	})

	// Ensure the cllama passthrough image is available (agent uses cllama: passthrough).
	spikeEnsureCllamaPassthroughImage(t, repoRoot)

	// ── Working directory ───────────────────────────────────────────────────

	workDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workDir, "AGENTS.md"), []byte("# Agent\n\nYou are the test agent."), 0o644); err != nil {
		t.Fatalf("write AGENTS.md: %v", err)
	}

	podPath := filepath.Join(workDir, "claw-pod.yml")
	podYAML := fmt.Sprintf(`x-claw:
  pod: capability-wave-spike

services:
  agent:
    image: %s
    x-claw:
      agent: ./AGENTS.md
      cllama: passthrough
      cllama-env:
        XAI_API_KEY: sk-spike-fake-not-real
      memory:
        service: mem-svc
        timeout-ms: 5000

  mem-svc:
    image: %s
    expose:
      - "8080"
`, agentTag, memTag)
	if err := os.WriteFile(podPath, []byte(podYAML), 0o644); err != nil {
		t.Fatalf("write pod file: %v", err)
	}

	prevDetach := composeUpDetach
	composeUpDetach = true
	defer func() { composeUpDetach = prevDetach }()

	if err := runComposeUp(podPath); err != nil {
		t.Fatalf("runComposeUp: %v", err)
	}

	composePath := filepath.Join(workDir, "compose.generated.yml")
	t.Cleanup(func() {
		exec.Command("docker", "compose", "-f", composePath, "down", "--volumes", "--remove-orphans").CombinedOutput()
	})

	// ── Verify memory.json artifact ─────────────────────────────────────────

	memJSONPath := filepath.Join(workDir, ".claw-runtime", "context", "agent", "memory.json")
	data, err := os.ReadFile(memJSONPath)
	if err != nil {
		t.Fatalf("memory.json not generated at %s: %v", memJSONPath, err)
	}

	var manifest struct {
		Version int    `json:"version"`
		Service string `json:"service"`
		BaseURL string `json:"base_url"`
		Recall  *struct {
			Path string `json:"path"`
		} `json:"recall,omitempty"`
		Retain *struct {
			Path string `json:"path"`
		} `json:"retain,omitempty"`
		Forget *struct {
			Path string `json:"path"`
		} `json:"forget,omitempty"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("parse memory.json: %v\n%s", err, data)
	}

	if manifest.Version != 1 {
		t.Errorf("memory.json version = %d, want 1", manifest.Version)
	}
	if manifest.Service != "mem-svc" {
		t.Errorf("memory.json service = %q, want %q", manifest.Service, "mem-svc")
	}
	if !strings.Contains(manifest.BaseURL, "mem-svc") {
		t.Errorf("memory.json base_url = %q, want to contain service name", manifest.BaseURL)
	}
	if manifest.Recall == nil || manifest.Recall.Path != "/recall" {
		t.Errorf("memory.json recall path = %v, want /recall", manifest.Recall)
	}
	if manifest.Retain == nil || manifest.Retain.Path != "/retain" {
		t.Errorf("memory.json retain path = %v, want /retain", manifest.Retain)
	}
	if manifest.Forget == nil || manifest.Forget.Path != "/forget" {
		t.Errorf("memory.json forget path = %v, want /forget", manifest.Forget)
	}
	t.Logf("memory.json: version=%d service=%s base_url=%s recall=%s retain=%s forget=%s",
		manifest.Version, manifest.Service, manifest.BaseURL,
		manifest.Recall.Path, manifest.Retain.Path, manifest.Forget.Path)

	// ── Verify containers reach running state ───────────────────────────────

	for _, svc := range []string{"agent", "mem-svc"} {
		out, err := exec.Command("docker", "compose", "-f", composePath, "ps", "-q", svc).Output()
		if err != nil {
			t.Fatalf("docker compose ps %s: %v", svc, err)
		}
		id := strings.TrimSpace(string(out))
		if id == "" {
			t.Fatalf("service %s: no container found", svc)
		}
		spikeWaitRunning(t, id, 30*time.Second)
		t.Logf("service %s: running (container %s)", svc, id[:min(len(id), 12)])
	}

	// The reference memory adapter has an HTTP healthcheck — confirm it goes healthy.
	out, err := exec.Command("docker", "compose", "-f", composePath, "ps", "-q", "mem-svc").Output()
	if err == nil {
		if id := strings.TrimSpace(string(out)); id != "" {
			spikeWaitHealthy(t, id, 45*time.Second)
			t.Log("mem-svc: healthy")
		}
	}
}
