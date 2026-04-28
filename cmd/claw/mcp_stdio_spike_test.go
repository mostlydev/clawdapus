//go:build spike

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestSpikeMCPStdio(t *testing.T) {
	if err := exec.Command("docker", "info").Run(); err != nil {
		t.Skipf("docker not available: %v", err)
	}

	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	repoRoot := filepath.Join(filepath.Dir(thisFile), "..", "..")

	wrapperTag := fmt.Sprintf("claw-spike-mcp-stdio:%d", time.Now().UnixNano())
	agentTag := fmt.Sprintf("claw-spike-mcp-agent:%d", time.Now().UnixNano())
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
	copyFile(t, filepath.Join(repoRoot, "examples", "mcp-stdio", "echo.claw-describe.json"), filepath.Join(workDir, "echo.claw-describe.json"))
	if err := os.WriteFile(filepath.Join(workDir, "AGENTS.md"), []byte("# Agent\n\nUse the echo tool.\n"), 0o644); err != nil {
		t.Fatalf("write AGENTS.md: %v", err)
	}

	wrapperPort := spikeFreePort(t)
	podPath := filepath.Join(workDir, "claw-pod.yml")
	podYAML := fmt.Sprintf(`x-claw:
  pod: mcp-stdio-spike

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
    ports:
      - "127.0.0.1:%s:8080"
    x-claw:
      describe-file: ./echo.claw-describe.json
      mcp-stdio:
        command: node
        args: ["/srv/echo-server/server.js"]
`, agentTag, wrapperTag, wrapperPort)
	if err := os.WriteFile(podPath, []byte(podYAML), 0o644); err != nil {
		t.Fatalf("write pod: %v", err)
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

	endpoint := "http://127.0.0.1:" + wrapperPort + "/mcp"
	sessionID := mcpInitialize(t, endpoint)
	mcpNotifyInitialized(t, endpoint, sessionID)
	got := mcpCallEcho(t, endpoint, sessionID, "wrapped")
	if got != "wrapped" {
		t.Fatalf("echo result = %q, want wrapped", got)
	}
}

func copyFile(t *testing.T, src, dst string) {
	t.Helper()
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("read %s: %v", src, err)
	}
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		t.Fatalf("write %s: %v", dst, err)
	}
}

func composeContainerID(t *testing.T, composePath, service string) string {
	t.Helper()
	out, err := exec.Command("docker", "compose", "-f", composePath, "ps", "-q", service).Output()
	if err != nil {
		t.Fatalf("docker compose ps %s: %v", service, err)
	}
	id := strings.TrimSpace(string(out))
	if id == "" {
		t.Fatalf("no container id for %s", service)
	}
	return id
}

func mcpInitialize(t *testing.T, endpoint string) string {
	t.Helper()
	req := []byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"spike","version":"test"}}}`)
	resp := mcpPost(t, endpoint, "", req, http.StatusOK)
	defer resp.Body.Close()
	sessionID := resp.Header.Get("MCP-Session-Id")
	if sessionID == "" {
		t.Fatal("initialize response missing MCP-Session-Id")
	}
	return sessionID
}

func mcpNotifyInitialized(t *testing.T, endpoint, sessionID string) {
	t.Helper()
	mcpPost(t, endpoint, sessionID, []byte(`{"jsonrpc":"2.0","method":"notifications/initialized"}`), http.StatusAccepted)
}

func mcpCallEcho(t *testing.T, endpoint, sessionID, message string) string {
	t.Helper()
	body := fmt.Sprintf(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"echo","arguments":{"message":%q}}}`, message)
	resp := mcpPost(t, endpoint, sessionID, []byte(body), http.StatusOK)
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	var parsed struct {
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("parse MCP response: %v\n%s", err, data)
	}
	if len(parsed.Result.Content) == 0 {
		t.Fatalf("MCP response missing content: %s", data)
	}
	return parsed.Result.Content[0].Text
}

func mcpPost(t *testing.T, endpoint, sessionID string, body []byte, wantStatus int) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if sessionID != "" {
		req.Header.Set("MCP-Session-Id", sessionID)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != wantStatus {
		data, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Fatalf("MCP status = %d, want %d\n%s", resp.StatusCode, wantStatus, data)
	}
	if wantStatus == http.StatusAccepted {
		resp.Body.Close()
	}
	return resp
}
