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

func TestSpikeMCPStdioDiscover(t *testing.T) {
	if err := exec.Command("docker", "info").Run(); err != nil {
		t.Skipf("docker not available: %v", err)
	}

	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	repoRoot := filepath.Join(filepath.Dir(thisFile), "..", "..")

	wrapperTag := fmt.Sprintf("claw-spike-mcp-stdio-discover:%d", time.Now().UnixNano())
	agentTag := fmt.Sprintf("claw-spike-mcp-agent-discover:%d", time.Now().UnixNano())
	spikeBuildImage(t, repoRoot, wrapperTag, "dockerfiles/claw-mcp-stdio/Dockerfile")
	spikeBuildImage(t, filepath.Join(repoRoot, "testdata", "openclaw-stub"), agentTag, "Clawfile")
	t.Cleanup(func() {
		exec.Command("docker", "image", "rm", "-f", wrapperTag, agentTag).CombinedOutput()
	})

	spikeEnsureRepoInfraImages(t, repoRoot, infraComponentClawdash)
	spikeEnsureCllamaPassthroughImage(t, repoRoot)

	workDir := t.TempDir()
	echoDir := filepath.Join(workDir, "echo-server")
	if err := os.MkdirAll(echoDir, 0o755); err != nil {
		t.Fatalf("mkdir echo-server: %v", err)
	}
	copyFile(t, filepath.Join(repoRoot, "examples", "mcp-stdio", "echo-server", "server.js"), filepath.Join(echoDir, "server.js"))
	if err := os.WriteFile(filepath.Join(workDir, "AGENTS.md"), []byte("# Agent\n\nUse the echo tool.\n"), 0o644); err != nil {
		t.Fatalf("write AGENTS.md: %v", err)
	}

	podPath := filepath.Join(workDir, "claw-pod.yml")
	podYAML := fmt.Sprintf(`x-claw:
  pod: mcp-stdio-discover-spike

services:
  agent:
    image: %s
    x-claw:
      agent: ./AGENTS.md
      cllama: passthrough
      cllama-env:
        XAI_API_KEY: sk-spike-fake-not-real
      tools:
        - service: echo
          allow: [echo]

  echo:
    image: %s
    volumes:
      - ./echo-server:/srv/echo-server:ro
    expose:
      - "8080"
    x-claw:
      mcp-stdio:
        command: node
        args: ["/srv/echo-server/server.js"]
`, agentTag, wrapperTag)
	if err := os.WriteFile(podPath, []byte(podYAML), 0o644); err != nil {
		t.Fatalf("write pod: %v", err)
	}

	prevPodFile := composePodFile
	composePodFile = podPath
	defer func() { composePodFile = prevPodFile }()
	if err := discoverCmd.RunE(discoverCmd, []string{"echo"}); err != nil {
		t.Fatalf("claw discover echo: %v", err)
	}

	snapshotPath := filepath.Join(workDir, ".claw-discovered", "echo.claw-describe.json")
	snapshotRaw, err := os.ReadFile(snapshotPath)
	if err != nil {
		t.Fatalf("read discovered snapshot: %v", err)
	}
	if !strings.Contains(string(snapshotRaw), `"x-claw-discovery"`) || !strings.Contains(string(snapshotRaw), `"name": "echo"`) {
		t.Fatalf("snapshot missing discovery metadata or echo tool:\n%s", snapshotRaw)
	}

	prevDetach := composeUpDetach
	composeUpDetach = true
	defer func() { composeUpDetach = prevDetach }()
	t.Setenv("CLLAMA_UI_PORT", spikeFreePort(t))
	t.Setenv("CLAWDASH_ADDR", ":"+spikeFreePort(t))

	if err := runComposeUp(podPath); err != nil {
		t.Fatalf("runComposeUp: %v", err)
	}

	composePath := filepath.Join(workDir, "compose.generated.yml")
	t.Cleanup(func() {
		exec.Command("docker", "compose", "-f", composePath, "down", "--volumes", "--remove-orphans").CombinedOutput()
	})

	echoID := composeContainerID(t, composePath, "echo")
	spikeWaitRunning(t, echoID, 30*time.Second)
	spikeWaitHealthy(t, echoID, 60*time.Second)

	toolsPath := filepath.Join(workDir, ".claw-runtime", "context", "agent", "tools.json")
	toolsRaw, err := os.ReadFile(toolsPath)
	if err != nil {
		t.Fatalf("read tools.json: %v", err)
	}
	var manifest struct {
		Tools []struct {
			Name      string `json:"name"`
			Execution struct {
				Transport string `json:"transport"`
				Path      string `json:"path"`
				ToolName  string `json:"tool_name"`
			} `json:"execution"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(toolsRaw, &manifest); err != nil {
		t.Fatalf("parse tools.json: %v\n%s", err, toolsRaw)
	}
	if len(manifest.Tools) != 1 {
		t.Fatalf("tools count = %d, want 1\n%s", len(manifest.Tools), toolsRaw)
	}
	tool := manifest.Tools[0]
	if tool.Name != "echo.echo" || tool.Execution.Transport != "mcp" || tool.Execution.Path != "/mcp" || tool.Execution.ToolName != "echo" {
		t.Fatalf("unexpected tool manifest: %+v\n%s", tool, toolsRaw)
	}
}
