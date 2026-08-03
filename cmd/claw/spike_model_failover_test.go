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

// TestSpikeOrderedModelFailover runs the compiled Clawdapus policy through a
// real cllama image and a fake OpenAI-compatible provider. The primary is a
// Responses-only model, the first fallback also fails, and only the second
// fallback succeeds. This locks both the provider-boundary adapter and the
// ordered multi-fallback contract to the metadata Clawdapus emits.
//
// Run with:
//
//	go test -tags spike -v -run TestSpikeOrderedModelFailover -timeout 10m ./cmd/claw/...
func TestSpikeOrderedModelFailover(t *testing.T) {
	if err := exec.Command("docker", "info").Run(); err != nil {
		t.Skipf("docker not available: %v", err)
	}

	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	repoRoot, err := filepath.Abs(filepath.Join(filepath.Dir(thisFile), "..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}

	workDir := t.TempDir()
	projectName := "model-failover-spike"
	generatedPath := filepath.Join(workDir, "compose.generated.yml")
	networkName := projectName + "_claw-internal"
	spikeCleanupProject(projectName, generatedPath)
	t.Cleanup(func() { spikeCleanupProject(projectName, generatedPath) })
	t.Cleanup(func() {
		if !t.Failed() {
			return
		}
		out, _ := exec.Command("docker", "compose", "-f", generatedPath, "logs", "--tail", "200").CombinedOutput()
		t.Logf("compose logs:\n%s", out)
	})

	pythonImage := "python:3.12-alpine"
	agentImage := fmt.Sprintf("model-failover-spike-agent:%d", time.Now().UnixNano())
	spikeEnsurePulledImage(t, pythonImage)
	spikeBuildImage(t, filepath.Join(repoRoot, "testdata", "openclaw-stub"), agentImage, "Clawfile")
	spikeEnsureRepoInfraImages(t, repoRoot, infraComponentClawdash)
	spikeEnsureCllamaPassthroughImage(t, repoRoot)
	t.Cleanup(func() {
		_, _ = exec.Command("docker", "image", "rm", "-f", agentImage).CombinedOutput()
	})

	capturesDir := filepath.Join(workDir, "captures")
	if err := os.MkdirAll(capturesDir, 0o777); err != nil {
		t.Fatalf("create captures dir: %v", err)
	}
	spikeWriteFile(t, filepath.Join(workDir, "AGENTS.md"), "# Model Failover Spike Agent\n\nUse the configured model.\n")
	spikeWriteFile(t, filepath.Join(workDir, "fake_provider.py"), orderedFailoverFakeProviderScript())
	spikeWriteFile(t, filepath.Join(workDir, "claw-pod.yml"), fmt.Sprintf(`name: model-failover-spike

x-claw:
  pod: model-failover-spike
  cllama-defaults:
    proxy: [passthrough]
    env:
      OPENAI_API_KEY: sk-local-fake
      OPENAI_BASE_URL: http://fake-provider:8080/v1
  models-defaults:
    primary: openai/gpt-5.6
    fallback:
      - openai/gpt-4.1
      - openai/gpt-4.1-mini

services:
  agent:
    image: %s
    x-claw:
      agent: ./AGENTS.md

  fake-provider:
    image: %s
    command: ["python", "/app/fake_provider.py"]
    volumes:
      - ./fake_provider.py:/app/fake_provider.py:ro
      - ./captures:/captures:rw
    expose:
      - "8080"
    networks:
      - claw-internal
`, agentImage, pythonImage))

	t.Setenv("CLLAMA_UI_PORT", spikeFreePort(t))
	t.Setenv("CLAWDASH_ADDR", ":"+spikeFreePort(t))

	prevDetach := composeUpDetach
	composeUpDetach = true
	defer func() { composeUpDetach = prevDetach }()

	if err := runComposeUp(filepath.Join(workDir, "claw-pod.yml")); err != nil {
		t.Fatalf("runComposeUp: %v", err)
	}
	for _, svc := range []string{"agent", "cllama", "fake-provider"} {
		spikeWaitRunning(t, spikeComposeContainerID(t, generatedPath, svc), 30*time.Second)
	}
	spikeWaitHealthy(t, spikeComposeContainerID(t, generatedPath, "cllama"), 45*time.Second)

	metadataPath := filepath.Join(workDir, ".claw-runtime", "context", "agent", "metadata.json")
	assertOrderedFallbackMetadata(t, metadataPath)
	token := spikeReadAgentToken(t, metadataPath)

	out, err := spikeDockerProbe(networkName, pythonImage, orderedFailoverProbeScript, token)
	if err != nil {
		t.Fatalf("cllama failover probe failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "chatcmpl-second-fallback") || !strings.Contains(out, "second fallback reached") {
		t.Fatalf("expected second fallback response, got:\n%s", out)
	}

	capturePath := filepath.Join(capturesDir, "requests.jsonl")
	deadline := time.Now().Add(10 * time.Second)
	for {
		if data, readErr := os.ReadFile(capturePath); readErr == nil && strings.Count(strings.TrimSpace(string(data)), "\n")+1 >= 3 {
			assertOrderedFallbackCaptures(t, data)
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("provider did not capture all three candidates before timeout")
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func assertOrderedFallbackMetadata(t *testing.T, path string) {
	t.Helper()
	var metadata struct {
		ModelPolicy struct {
			Allowed []struct {
				Slot string `json:"slot"`
				Ref  string `json:"ref"`
			} `json:"allowed"`
		} `json:"model_policy"`
	}
	if err := json.Unmarshal([]byte(spikeReadFile(t, path)), &metadata); err != nil {
		t.Fatalf("parse metadata.json: %v", err)
	}
	wantSlots := []string{"primary", "fallback", "fallback"}
	wantRefs := []string{"openai/gpt-5.6", "openai/gpt-4.1", "openai/gpt-4.1-mini"}
	if len(metadata.ModelPolicy.Allowed) != len(wantRefs) {
		t.Fatalf("model policy allowed = %+v, want %v", metadata.ModelPolicy.Allowed, wantRefs)
	}
	for i, entry := range metadata.ModelPolicy.Allowed {
		if entry.Slot != wantSlots[i] || entry.Ref != wantRefs[i] {
			t.Fatalf("model policy allowed[%d] = %+v, want slot=%q ref=%q", i, entry, wantSlots[i], wantRefs[i])
		}
	}
}

func assertOrderedFallbackCaptures(t *testing.T, data []byte) {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 3 {
		t.Fatalf("captured requests = %d, want 3:\n%s", len(lines), data)
	}
	wantModels := []string{"gpt-5.6", "gpt-4.1", "gpt-4.1-mini"}
	wantPaths := []string{"/v1/responses", "/v1/chat/completions", "/v1/chat/completions"}
	for i, line := range lines {
		var got struct {
			Path  string `json:"path"`
			Model string `json:"model"`
		}
		if err := json.Unmarshal([]byte(line), &got); err != nil {
			t.Fatalf("parse capture[%d]: %v", i, err)
		}
		if got.Model != wantModels[i] || got.Path != wantPaths[i] {
			t.Fatalf("capture[%d] = %+v, want path=%q model=%q", i, got, wantPaths[i], wantModels[i])
		}
	}
}

const orderedFailoverProbeScript = `
import json, sys, urllib.request
payload = {"model":"openai/gpt-5.6","messages":[{"role":"user","content":"Prove the complete fallback chain."}]}
body = json.dumps(payload).encode("utf-8")
req = urllib.request.Request(
    "http://cllama:8080/v1/chat/completions",
    data=body,
    headers={"Content-Type":"application/json","Authorization":"Bearer " + sys.argv[1]},
)
with urllib.request.urlopen(req, timeout=30) as resp:
    sys.stdout.write(resp.read().decode("utf-8"))
`

func orderedFailoverFakeProviderScript() string {
	return `import json
from http.server import BaseHTTPRequestHandler, HTTPServer

class Handler(BaseHTTPRequestHandler):
    def do_GET(self):
        if self.path == "/health":
            body = b"ok"
            self.send_response(200)
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)
            return
        self.send_response(404)
        self.end_headers()

    def do_POST(self):
        length = int(self.headers.get("Content-Length", "0"))
        payload = json.loads(self.rfile.read(length) or b"{}")
        model = payload.get("model", "")
        with open("/captures/requests.jsonl", "a", encoding="utf-8") as capture:
            capture.write(json.dumps({"path": self.path, "model": model}) + "\n")

        if model != "gpt-4.1-mini":
            body = json.dumps({"error": {"message": "force next candidate"}}).encode("utf-8")
            self.send_response(503)
        else:
            body = json.dumps({
                "id": "chatcmpl-second-fallback",
                "object": "chat.completion",
                "choices": [{
                    "index": 0,
                    "message": {"role": "assistant", "content": "second fallback reached"},
                    "finish_reason": "stop"
                }],
                "usage": {"prompt_tokens": 10, "completion_tokens": 4, "total_tokens": 14}
            }).encode("utf-8")
            self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, fmt, *args):
        return

HTTPServer(("0.0.0.0", 8080), Handler).serve_forever()
`
}
