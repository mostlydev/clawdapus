//go:build spike

package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestSpikeBudgetEnforcementAndOverride(t *testing.T) {
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
	projectName := "budget-spike"
	generatedPath := filepath.Join(workDir, "compose.generated.yml")
	networkName := projectName + "_claw-internal"
	spikeCleanupProject(projectName, generatedPath)
	t.Cleanup(func() {
		spikeCleanupProject(projectName, generatedPath)
	})
	t.Cleanup(func() {
		if !t.Failed() {
			return
		}
		out, _ := exec.Command("docker", "compose", "-f", generatedPath, "logs", "--tail", "200").CombinedOutput()
		t.Logf("compose logs:\n%s", out)
	})

	pythonImage := "python:3.12-alpine"
	agentImage := fmt.Sprintf("budget-spike-agent:%d", time.Now().UnixNano())
	spikeEnsurePulledImage(t, pythonImage)
	spikeBuildImage(t, filepath.Join(repoRoot, "testdata", "openclaw-stub"), agentImage, "Clawfile")
	spikeEnsureRepoInfraImages(t, repoRoot, infraComponentClawAPI, infraComponentClawdash)
	spikeEnsureCllamaPassthroughImage(t, repoRoot)
	t.Cleanup(func() {
		_, _ = exec.Command("docker", "image", "rm", "-f", agentImage).CombinedOutput()
	})

	spikeWriteFile(t, filepath.Join(workDir, "AGENTS.md"), "# Budget Spike Agent\n\nUse the configured model.")
	spikeWriteFile(t, filepath.Join(workDir, "fake_provider.py"), budgetSpikeFakeProviderScript())
	spikeWriteFile(t, filepath.Join(workDir, "claw-pod.yml"), fmt.Sprintf(`name: budget-spike

x-claw:
  pod: budget-spike
  master: analyst
  budget-defaults:
    limit-usd: 0.00005
    max-requests: 10
    window: 1h
    behavior: hard_stop
  cllama-defaults:
    proxy: [passthrough]
    env:
      OPENAI_API_KEY: sk-local-fake
      OPENAI_BASE_URL: http://fake-provider:8080/v1
  models-defaults:
    primary: openai/gpt-4o

services:
  analyst:
    image: %s
    x-claw:
      agent: ./AGENTS.md
      surfaces:
        - service://claw-api

  fake-provider:
    image: %s
    command: ["python", "/app/fake_provider.py"]
    volumes:
      - ./fake_provider.py:/app/fake_provider.py:ro
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

	for _, svc := range []string{"analyst", "claw-api", "cllama", "fake-provider"} {
		id := spikeComposeContainerID(t, generatedPath, svc)
		spikeWaitRunning(t, id, 30*time.Second)
	}
	spikeWaitHealthy(t, spikeComposeContainerID(t, generatedPath, "cllama"), 45*time.Second)
	spikeWaitHealthy(t, spikeComposeContainerID(t, generatedPath, "claw-api"), 45*time.Second)

	metadataPath := filepath.Join(workDir, ".claw-runtime", "context", "analyst", "metadata.json")
	token := spikeReadAgentToken(t, metadataPath)
	metadata := spikeReadFile(t, metadataPath)
	if !strings.Contains(metadata, `"budget"`) || !strings.Contains(metadata, `"limit_usd": 0.00005`) {
		t.Fatalf("metadata.json missing compiled budget:\n%s", metadata)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	first := budgetSpikePostCllama(t, networkName, pythonImage, token)
	if first.Status != 200 || !strings.Contains(first.Body, "chatcmpl-budget-spike") {
		t.Fatalf("first cllama request should succeed, got status=%d body=%s", first.Status, first.Body)
	}

	var second budgetSpikeHTTPResult
	if err := waitForCondition(ctx, func() bool {
		second = budgetSpikePostCllama(t, networkName, pythonImage, token)
		return second.Status == 429 && strings.Contains(second.Body, "budget_exceeded")
	}); err != nil {
		t.Fatalf("second cllama request never hit budget 429: %v\nlast status=%d body=%s", err, second.Status, second.Body)
	}

	auditOut := budgetSpikeRunAudit(t, filepath.Join(workDir, "claw-pod.yml"))
	if !strings.Contains(auditOut, "budget_exceeded") {
		t.Fatalf("claw audit did not show budget_exceeded intervention:\n%s", auditOut)
	}

	overrideBody := map[string]any{
		"claw_id":   "analyst",
		"limit_usd": 1.0,
		"window":    "1h",
		"behavior":  "hard_stop",
	}
	out, err := callClawAPICompose(generatedPath, "analyst", httpMethodPost, "/fleet/budget/set", overrideBody)
	if err != nil {
		t.Fatalf("fleet.budget.set request failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), `"claw_id":"analyst"`) && !strings.Contains(string(out), `"claw_id": "analyst"`) {
		t.Fatalf("unexpected budget-set response:\n%s", out)
	}

	overridePath := filepath.Join(workDir, ".claw-governance", "analyst", "budget.json")
	overrideRaw := spikeReadFile(t, overridePath)
	if !strings.Contains(overrideRaw, `"limit_usd": 1`) && !strings.Contains(overrideRaw, `"limit_usd":1`) {
		t.Fatalf("budget override file missing raised cap:\n%s", overrideRaw)
	}

	var resumed budgetSpikeHTTPResult
	if err := waitForCondition(ctx, func() bool {
		resumed = budgetSpikePostCllama(t, networkName, pythonImage, token)
		return resumed.Status == 200 && strings.Contains(resumed.Body, "chatcmpl-budget-spike")
	}); err != nil {
		t.Fatalf("cllama traffic did not resume after budget override: %v\nlast status=%d body=%s", err, resumed.Status, resumed.Body)
	}
}

type budgetSpikeHTTPResult struct {
	Status int
	Body   string
}

func budgetSpikePostCllama(t *testing.T, networkName, image, token string) budgetSpikeHTTPResult {
	t.Helper()
	out, err := spikeDockerProbe(networkName, image, budgetSpikePostCllamaScript, token)
	if err != nil {
		t.Fatalf("cllama probe transport failed: %v\n%s", err, out)
	}
	status, body, ok := strings.Cut(out, "\n")
	if !ok {
		t.Fatalf("cllama probe emitted malformed output:\n%s", out)
	}
	status = strings.TrimPrefix(strings.TrimSpace(status), "STATUS=")
	var parsed int
	if _, err := fmt.Sscanf(status, "%d", &parsed); err != nil {
		t.Fatalf("parse cllama probe status %q: %v\n%s", status, err, out)
	}
	return budgetSpikeHTTPResult{Status: parsed, Body: body}
}

func budgetSpikeRunAudit(t *testing.T, podPath string) string {
	t.Helper()
	prevPodFile := composePodFile
	prevSince := auditSince
	prevClaw := auditClaw
	prevType := auditType
	prevJSON := auditJSON
	defer func() {
		composePodFile = prevPodFile
		auditSince = prevSince
		auditClaw = prevClaw
		auditType = prevType
		auditJSON = prevJSON
	}()

	composePodFile = podPath
	auditSince = "2h"
	auditClaw = "analyst"
	auditType = "intervention"
	auditJSON = true
	var buf bytes.Buffer
	auditCmd.SetOut(&buf)
	defer auditCmd.SetOut(os.Stdout)
	if err := auditCmd.RunE(auditCmd, nil); err != nil {
		t.Fatalf("audit command failed: %v", err)
	}
	return buf.String()
}

func spikeComposeContainerID(t *testing.T, composePath, service string) string {
	t.Helper()
	out, err := exec.Command("docker", "compose", "-f", composePath, "ps", "-q", service).Output()
	if err != nil {
		t.Fatalf("docker compose ps %s: %v", service, err)
	}
	id := strings.TrimSpace(string(out))
	if id == "" {
		t.Fatalf("service %s has no container ID", service)
	}
	return id
}

const budgetSpikePostCllamaScript = `
import json, sys, urllib.error, urllib.request
payload = {"model":"openai/gpt-4o","messages":[{"role":"user","content":"Spend a little budget."}]}
body = json.dumps(payload).encode("utf-8")
req = urllib.request.Request(
    "http://cllama:8080/v1/chat/completions",
    data=body,
    headers={"Content-Type":"application/json","Authorization":"Bearer " + sys.argv[1]},
)
try:
    with urllib.request.urlopen(req, timeout=15) as resp:
        status = resp.status
        data = resp.read().decode("utf-8")
except urllib.error.HTTPError as exc:
    status = exc.code
    data = exc.read().decode("utf-8")
sys.stdout.write("STATUS=%d\n%s" % (status, data))
`

func budgetSpikeFakeProviderScript() string {
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
        self.rfile.read(length)
        response = {
            "id": "chatcmpl-budget-spike",
            "object": "chat.completion",
            "choices": [{"index": 0, "message": {"role": "assistant", "content": "ok"}, "finish_reason": "stop"}],
            "usage": {"prompt_tokens": 10, "completion_tokens": 10, "total_tokens": 20},
        }
        encoded = json.dumps(response).encode("utf-8")
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(encoded)))
        self.end_headers()
        self.wfile.write(encoded)

    def log_message(self, fmt, *args):
        return

HTTPServer(("0.0.0.0", 8080), Handler).serve_forever()
`
}
