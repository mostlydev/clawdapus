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

// TestSpikePolicyEvaluatorGeneratedPod runs a generated pod with real cllama,
// a fake upstream provider, and a fake policy sidecar. It proves the #307
// evaluator seam without Discord or provider credentials.
//
// Run with: go test -tags spike -v -run TestSpikePolicyEvaluatorGeneratedPod -timeout 10m ./cmd/claw/...
func TestSpikePolicyEvaluatorGeneratedPod(t *testing.T) {
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
	projectName := "policy-spike"
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
	agentImage := fmt.Sprintf("policy-spike-agent:%d", time.Now().UnixNano())
	spikeEnsurePulledImage(t, pythonImage)
	rollcallDir := filepath.Join(repoRoot, "examples", "rollcall")
	spikeBuildImage(t, rollcallDir, "nullclaw:latest", "Dockerfile.nullclaw-base")
	spikeEnsureRepoInfraImages(t, repoRoot, infraComponentClawdash)
	spikeEnsureCllamaPassthroughImage(t, repoRoot)
	t.Cleanup(func() {
		_, _ = exec.Command("docker", "image", "rm", "-f", agentImage).CombinedOutput()
	})

	spikeWriteFile(t, filepath.Join(workDir, "AGENTS.md"), "# Policy Spike Agent\n\nUse the configured model.")
	spikeWriteFile(t, filepath.Join(workDir, "Clawfile"), `FROM nullclaw:latest

CLAW_TYPE nullclaw
AGENT AGENTS.md
MODEL primary openai/gpt-4o
`)
	spikeBuildImage(t, workDir, agentImage, "Clawfile")

	capturesDir := filepath.Join(workDir, "captures")
	if err := os.MkdirAll(capturesDir, 0o777); err != nil {
		t.Fatalf("create captures dir: %v", err)
	}
	spikeWriteFile(t, filepath.Join(workDir, "fake_provider.py"), policySpikeFakeProviderScript())
	spikeWriteFile(t, filepath.Join(workDir, "fake_policy.py"), policySpikeFakePolicyScript())
	spikeWriteFile(t, filepath.Join(workDir, "claw-pod.yml"), fmt.Sprintf(`name: policy-spike

x-claw:
  pod: policy-spike

services:
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

  policy-sidecar:
    image: %s
    command: ["python", "/app/fake_policy.py"]
    volumes:
      - ./fake_policy.py:/app/fake_policy.py:ro
    expose:
      - "8080"
    networks:
      - claw-internal

  policy-agent:
    image: %s
    x-claw:
      agent: ./AGENTS.md
      cllama: passthrough
      cllama-env:
        OPENAI_API_KEY: sk-local-fake
        OPENAI_BASE_URL: http://fake-provider:8080/v1
        CLLAMA_POLICY_URL: http://policy-sidecar:8080
        CLLAMA_POLICY_FAIL_MODE: closed
      models:
        primary: openai/gpt-4o
`, pythonImage, pythonImage, agentImage))

	t.Setenv("CLLAMA_UI_PORT", spikeFreePort(t))
	t.Setenv("CLAWDASH_ADDR", ":"+spikeFreePort(t))

	prevDetach := composeUpDetach
	composeUpDetach = true
	defer func() { composeUpDetach = prevDetach }()

	if err := runComposeUp(filepath.Join(workDir, "claw-pod.yml")); err != nil {
		t.Fatalf("runComposeUp: %v", err)
	}

	for _, svc := range []string{"policy-agent", "cllama", "fake-provider", "policy-sidecar"} {
		spikeWaitRunning(t, spikeComposeContainerID(t, generatedPath, svc), 30*time.Second)
	}
	spikeWaitHealthy(t, spikeComposeContainerID(t, generatedPath, "cllama"), 45*time.Second)

	composeText := spikeReadFile(t, generatedPath)
	if !strings.Contains(composeText, "CLLAMA_POLICY_URL: http://policy-sidecar:8080") {
		t.Fatalf("compose did not forward CLLAMA_POLICY_URL:\n%s", composeText)
	}

	token := spikeReadAgentToken(t, filepath.Join(workDir, ".claw-runtime", "context", "policy-agent", "metadata.json"))
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	denied := policySpikePostCllama(t, networkName, pythonImage, token, "please deny-me")
	if denied.Status != 403 || !strings.Contains(denied.Body, "policy spike deny") {
		t.Fatalf("deny probe should get policy 403, got status=%d body=%s", denied.Status, denied.Body)
	}

	decorated := policySpikePostCllama(t, networkName, pythonImage, token, "please decorate-me")
	if decorated.Status != 200 || !strings.Contains(decorated.Body, "chatcmpl-policy-spike") {
		t.Fatalf("decorate probe should pass through, got status=%d body=%s", decorated.Status, decorated.Body)
	}

	capturePath := filepath.Join(capturesDir, "latest.json")
	if err := waitForCondition(ctx, func() bool {
		_, err := os.Stat(capturePath)
		return err == nil
	}); err != nil {
		t.Fatalf("fake provider did not write capture: %v", err)
	}
	providerText := spikeOpenAIMessageText(t, spikeReadFile(t, capturePath))
	if !strings.Contains(providerText, "policy-spike-decoration") {
		t.Fatalf("provider-visible request missing policy decoration:\n%s", providerText)
	}

	auditOut := policySpikeRunAudit(t, filepath.Join(workDir, "claw-pod.yml"))
	for _, want := range []string{"policy_denied", "policy_decorated"} {
		if !strings.Contains(auditOut, want) {
			t.Fatalf("claw audit missing %s intervention:\n%s", want, auditOut)
		}
	}
}

type policySpikeHTTPResult struct {
	Status int
	Body   string
}

func policySpikePostCllama(t *testing.T, networkName, image, token, message string) policySpikeHTTPResult {
	t.Helper()
	out, err := spikeDockerProbe(networkName, image, policySpikePostCllamaScript, token, message)
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
	return policySpikeHTTPResult{Status: parsed, Body: body}
}

func policySpikeRunAudit(t *testing.T, podPath string) string {
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
	auditClaw = "policy-agent"
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

const policySpikePostCllamaScript = `
import json, sys, urllib.error, urllib.request
payload = {"model":"openai/gpt-4o","messages":[{"role":"user","content":sys.argv[2]}]}
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

func policySpikeFakeProviderScript() string {
	return `import json, os
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
        body = self.rfile.read(length).decode("utf-8")
        with open("/captures/latest.json", "w") as f:
            f.write(body)
        response = {
            "id": "chatcmpl-policy-spike",
            "object": "chat.completion",
            "choices": [{"index": 0, "message": {"role": "assistant", "content": "ok"}, "finish_reason": "stop"}],
            "usage": {"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15},
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

func policySpikeFakePolicyScript() string {
	return `import json
from http.server import BaseHTTPRequestHandler, HTTPServer

def request_text(payload):
    request = payload.get("request") or {}
    return json.dumps(request.get("messages") or request.get("system") or "")

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
        payload = json.loads(self.rfile.read(length).decode("utf-8") or "{}")
        text = request_text(payload)
        status = 200
        response = {}
        if self.path == "/policy/gate-request":
            response = {"verdict": "allow"}
            if "deny-me" in text:
                response = {"verdict": "deny", "reason": "policy spike deny"}
        elif self.path == "/policy/decorate":
            if "decorate-me" in text:
                response = {"messages_patch": [{"role": "system", "content": "policy-spike-decoration"}]}
        elif self.path == "/policy/gate-response":
            response = {"verdict": "allow"}
        elif self.path == "/policy/score":
            status = 202
        else:
            status = 404
            response = {"error": "unknown path"}
        encoded = json.dumps(response).encode("utf-8")
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(encoded)))
        self.end_headers()
        self.wfile.write(encoded)

    def log_message(self, fmt, *args):
        return

HTTPServer(("0.0.0.0", 8080), Handler).serve_forever()
`
}
