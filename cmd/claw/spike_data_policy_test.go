//go:build spike

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/mostlydev/clawdapus/internal/cllama"
)

// TestSpikeDataPolicyConservative joins the compiler's data_policy metadata to
// cllama enforcement under the conservative default policy: an agent whose
// fake provider endpoint carries a synthetic attestation is admitted with the
// OpenAI no-store control applied and metadata-only local history, while an
// otherwise identical agent without the attestation is refused before any
// provider effect.
func TestSpikeDataPolicyConservative(t *testing.T) {
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
	projectName := "data-policy-spike"
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
	agentImage := fmt.Sprintf("data-policy-spike-agent:%d", time.Now().UnixNano())
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
	// The capture dir is written by the provider container's user.
	if err := os.Chmod(capturesDir, 0o777); err != nil {
		t.Fatalf("chmod captures dir: %v", err)
	}

	const fakeEndpoint = "http://fake-openai:8080/v1"
	spikeWriteFile(t, filepath.Join(workDir, "AGENTS.md"), "# Data Policy Spike Agent\n\nUse the configured model.\n")
	spikeWriteFile(t, filepath.Join(workDir, "fake_provider.py"), dataPolicySpikeFakeProviderScript())
	spikeWriteFile(t, filepath.Join(workDir, "claw-pod.yml"), fmt.Sprintf(`name: data-policy-spike

x-claw:
  pod: data-policy-spike
  # Every conservative default is kept: regions us/eu, no training, no
  # provider retention, no local content retention.
  data-policy-defaults:
    regions: [us, eu]
    training: none
    retention: none
    local-retention: none
    attestations:
      # TEST-ONLY synthetic attestation. It vouches for the in-pod fake
      # provider so the conservative policy can be exercised end to end; it
      # describes no real provider account.
      - provider: openai
        endpoint: %[1]s
        guarantees: [no-training, processing:us, storage:us, zero-data-retention]
  cllama-defaults:
    proxy: [passthrough]
    env:
      OPENAI_API_KEY: sk-local-fake
      OPENAI_BASE_URL: %[1]s
  models-defaults:
    primary: openai/gpt-4o

services:
  attested:
    image: %[2]s
    x-claw:
      agent: ./AGENTS.md

  unattested:
    image: %[2]s
    x-claw:
      agent: ./AGENTS.md
      # Same conservative policy, but no attestation: a service-level policy
      # replaces the pod default entirely.
      data-policy:
        regions: [us, eu]
        training: none
        retention: none
        local-retention: none

  fake-openai:
    image: %[3]s
    command: ["python", "/app/fake_provider.py"]
    volumes:
      - ./fake_provider.py:/app/fake_provider.py:ro
      - ./captures:/captures:rw
    expose:
      - "8080"
    networks:
      - claw-internal
`, fakeEndpoint, agentImage, pythonImage))

	t.Setenv("CLLAMA_UI_PORT", spikeFreePort(t))
	t.Setenv("CLAWDASH_ADDR", ":"+spikeFreePort(t))

	prevDetach := composeUpDetach
	composeUpDetach = true
	defer func() { composeUpDetach = prevDetach }()

	if err := runComposeUp(filepath.Join(workDir, "claw-pod.yml")); err != nil {
		t.Fatalf("runComposeUp: %v", err)
	}
	for _, svc := range []string{"attested", "unattested", "cllama", "fake-openai"} {
		spikeWaitRunning(t, spikeComposeContainerID(t, generatedPath, svc), 30*time.Second)
	}
	spikeWaitHealthy(t, spikeComposeContainerID(t, generatedPath, "cllama"), 45*time.Second)

	// 2. The compiled metadata carries the policy cllama enforces.
	attestedMeta := filepath.Join(workDir, ".claw-runtime", "context", "attested", "metadata.json")
	unattestedMeta := filepath.Join(workDir, ".claw-runtime", "context", "unattested", "metadata.json")
	conservative := func(atts []cllama.DataPolicyAttestation) cllama.DataPolicy {
		return cllama.DataPolicy{
			Version:        1,
			Regions:        []string{"eu", "us"},
			Training:       "none",
			Retention:      "none",
			LocalRetention: "none",
			Attestations:   atts,
		}
	}
	dataPolicySpikeAssertMetadata(t, attestedMeta, conservative([]cllama.DataPolicyAttestation{{
		Provider:   "openai",
		Endpoint:   fakeEndpoint,
		Guarantees: []string{"no-training", "processing:us", "storage:us", "zero-data-retention"},
	}}))
	dataPolicySpikeAssertMetadata(t, unattestedMeta, conservative([]cllama.DataPolicyAttestation{}))

	// 3. The attested agent's request succeeds with the no-store control.
	const attestedMarker = "DP-SPIKE-ATTESTED-7f3a91"
	const unattestedMarker = "DP-SPIKE-UNATTESTED-c2e844"
	attestedToken := spikeReadAgentToken(t, attestedMeta)
	allowed := dataPolicySpikePost(t, networkName, pythonImage, attestedToken, attestedMarker)
	if allowed.Status != 200 || !strings.Contains(allowed.Body, "chatcmpl-data-policy-spike") {
		t.Fatalf("attested request should succeed, got status=%d body=%s", allowed.Status, allowed.Body)
	}

	captures := dataPolicySpikeWaitCaptures(t, filepath.Join(capturesDir, "requests.jsonl"), 1)
	if len(captures) != 1 {
		t.Fatalf("fake provider captured %d requests, want 1:\n%+v", len(captures), captures)
	}
	got := captures[0]
	if got.Path != "/v1/chat/completions" {
		t.Fatalf("upstream path = %q, want /v1/chat/completions", got.Path)
	}
	if store, present := got.Body["store"]; !present || store != false {
		t.Fatalf("upstream body store = %v (present=%v), want false; body=%v", store, present, got.Body)
	}
	keys := make([]string, 0, len(got.Body))
	for k := range got.Body {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if want := []string{"messages", "model", "store"}; !reflect.DeepEqual(keys, want) {
		t.Fatalf("upstream body keys = %v, want %v (request widened); body=%v", keys, want, got.Body)
	}
	if got.Body["model"] != "gpt-4o" {
		t.Fatalf("upstream model = %v, want gpt-4o", got.Body["model"])
	}
	userMessages := dataPolicySpikeNonSystemMessages(t, got.Body["messages"])
	wantUser := []map[string]any{{"role": "user", "content": "Summarize " + attestedMarker}}
	if !reflect.DeepEqual(userMessages, wantUser) {
		t.Fatalf("upstream non-system messages = %v, want %v", userMessages, wantUser)
	}

	// 4. Local history is metadata-only under local-retention: none.
	historyPath := filepath.Join(workDir, ".claw-session-history", "attested", "history.jsonl")
	historyRaw := dataPolicySpikeWaitFile(t, historyPath)
	if strings.Contains(historyRaw, attestedMarker) || strings.Contains(historyRaw, "data policy spike reply") {
		t.Fatalf("history.jsonl leaked prompt or response content:\n%s", historyRaw)
	}
	historyLines := strings.Split(strings.TrimSpace(historyRaw), "\n")
	if len(historyLines) != 1 {
		t.Fatalf("history.jsonl has %d entries, want 1:\n%s", len(historyLines), historyRaw)
	}
	var entry map[string]any
	if err := json.Unmarshal([]byte(historyLines[0]), &entry); err != nil {
		t.Fatalf("parse history entry: %v\n%s", err, historyLines[0])
	}
	for _, key := range []string{"request_original", "request_effective", "tool_trace"} {
		if _, present := entry[key]; present {
			t.Fatalf("history entry carries %q under local-retention none:\n%s", key, historyLines[0])
		}
	}
	if resp, _ := entry["response"].(map[string]any); resp == nil || resp["format"] != "metadata" || len(resp) != 1 {
		t.Fatalf("history response = %v, want {format: metadata}", entry["response"])
	}
	if code, _ := entry["status_code"].(float64); code != 200 {
		t.Fatalf("history status_code = %v, want 200", entry["status_code"])
	}
	usage, _ := entry["usage"].(map[string]any)
	if usage == nil || usage["prompt_tokens"] != float64(11) || usage["completion_tokens"] != float64(7) {
		t.Fatalf("history usage = %v, want prompt_tokens=11 completion_tokens=7", entry["usage"])
	}
	if entry["claw_id"] != "attested" || entry["effective_provider"] != "openai" {
		t.Fatalf("history entry identity = claw_id %v provider %v:\n%s", entry["claw_id"], entry["effective_provider"], historyLines[0])
	}

	// 5. The unattested agent is refused with zero provider effects.
	unattestedToken := spikeReadAgentToken(t, unattestedMeta)
	refused := dataPolicySpikePost(t, networkName, pythonImage, unattestedToken, unattestedMarker)
	if refused.Status < 400 || refused.Status >= 500 || !strings.Contains(refused.Body, "data policy requirement not established: processing_region") {
		t.Fatalf("unattested request should get a 4xx data-policy refusal, got status=%d body=%s", refused.Status, refused.Body)
	}
	// Give any stray dispatch time to land before counting provider effects.
	time.Sleep(2 * time.Second)
	after := dataPolicySpikeReadCaptures(t, filepath.Join(capturesDir, "requests.jsonl"))
	if len(after) != 1 {
		t.Fatalf("fake provider captured %d requests after refusal, want still 1:\n%+v", len(after), after)
	}
	if raw, _ := os.ReadFile(filepath.Join(capturesDir, "requests.jsonl")); strings.Contains(string(raw), unattestedMarker) {
		t.Fatalf("fake provider received the refused request:\n%s", raw)
	}
	if raw, err := os.ReadFile(filepath.Join(workDir, ".claw-session-history", "unattested", "history.jsonl")); err == nil && strings.TrimSpace(string(raw)) != "" {
		t.Fatalf("refused request must not produce a history entry:\n%s", raw)
	}

	logs, err := exec.Command("docker", "compose", "-f", generatedPath, "logs", "--no-color", "cllama").CombinedOutput()
	if err != nil {
		t.Fatalf("read cllama logs: %v\n%s", err, logs)
	}
	if !dataPolicySpikeHasIntervention(string(logs), "unattested", "data_policy_refused:processing_region") {
		t.Fatalf("cllama logs missing data_policy_refused:processing_region intervention for unattested:\n%s", logs)
	}
	for _, want := range []string{
		"data_policy_processing:openai:attested:us",
		"data_policy_training:openai:attested",
		"data_policy_retention:openai:attested",
	} {
		if !dataPolicySpikeHasIntervention(string(logs), "attested", want) {
			t.Fatalf("cllama logs missing %s intervention for attested:\n%s", want, logs)
		}
	}
	if dataPolicySpikeHasIntervention(string(logs), "attested", "data_policy_refused:processing_region") {
		t.Fatalf("attested agent unexpectedly refused:\n%s", logs)
	}
}

func dataPolicySpikeAssertMetadata(t *testing.T, path string, want cllama.DataPolicy) {
	t.Helper()
	var meta struct {
		DataPolicy *cllama.DataPolicy `json:"data_policy"`
	}
	raw := spikeReadFile(t, path)
	if err := json.Unmarshal([]byte(raw), &meta); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	if meta.DataPolicy == nil {
		t.Fatalf("%s has no data_policy:\n%s", path, raw)
	}
	if !reflect.DeepEqual(*meta.DataPolicy, want) {
		t.Fatalf("%s data_policy = %+v, want %+v", path, *meta.DataPolicy, want)
	}
}

func dataPolicySpikeNonSystemMessages(t *testing.T, raw any) []map[string]any {
	t.Helper()
	list, ok := raw.([]any)
	if !ok {
		t.Fatalf("upstream messages is %T, want array", raw)
	}
	var out []map[string]any
	for _, m := range list {
		msg, ok := m.(map[string]any)
		if !ok {
			t.Fatalf("upstream message is %T, want object", m)
		}
		if msg["role"] == "system" || msg["role"] == "developer" {
			continue
		}
		out = append(out, msg)
	}
	return out
}

func dataPolicySpikeHasIntervention(logs, clawID, reason string) bool {
	for _, line := range strings.Split(logs, "\n") {
		start := strings.Index(line, "{")
		if start < 0 {
			continue
		}
		var ev struct {
			Type         string `json:"type"`
			ClawID       string `json:"claw_id"`
			Intervention string `json:"intervention"`
		}
		if json.Unmarshal([]byte(line[start:]), &ev) != nil {
			continue
		}
		if ev.Type == "intervention" && ev.ClawID == clawID && ev.Intervention == reason {
			return true
		}
	}
	return false
}

type dataPolicySpikeCapture struct {
	Path string         `json:"path"`
	Body map[string]any `json:"body"`
}

func dataPolicySpikeReadCaptures(t *testing.T, path string) []dataPolicySpikeCapture {
	t.Helper()
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatalf("read captures: %v", err)
	}
	var out []dataPolicySpikeCapture
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var c dataPolicySpikeCapture
		if err := json.Unmarshal([]byte(line), &c); err != nil {
			t.Fatalf("parse capture: %v\n%s", err, line)
		}
		out = append(out, c)
	}
	return out
}

func dataPolicySpikeWaitCaptures(t *testing.T, path string, n int) []dataPolicySpikeCapture {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		captures := dataPolicySpikeReadCaptures(t, path)
		if len(captures) >= n || time.Now().After(deadline) {
			return captures
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func dataPolicySpikeWaitFile(t *testing.T, path string) string {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if raw, err := os.ReadFile(path); err == nil && strings.TrimSpace(string(raw)) != "" {
			return string(raw)
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s was not written before timeout", path)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func dataPolicySpikePost(t *testing.T, networkName, image, token, marker string) budgetSpikeHTTPResult {
	t.Helper()
	out, err := spikeDockerProbe(networkName, image, dataPolicySpikePostScript, token, marker)
	if err != nil {
		t.Fatalf("cllama probe transport failed: %v\n%s", err, out)
	}
	status, body, ok := strings.Cut(out, "\n")
	if !ok {
		t.Fatalf("cllama probe emitted malformed output:\n%s", out)
	}
	var parsed int
	if _, err := fmt.Sscanf(strings.TrimPrefix(strings.TrimSpace(status), "STATUS="), "%d", &parsed); err != nil {
		t.Fatalf("parse cllama probe status %q: %v\n%s", status, err, out)
	}
	return budgetSpikeHTTPResult{Status: parsed, Body: body}
}

const dataPolicySpikePostScript = `
import json, sys, urllib.error, urllib.request
payload = {"model":"openai/gpt-4o","messages":[{"role":"user","content":"Summarize " + sys.argv[2]}]}
req = urllib.request.Request(
    "http://cllama:8080/v1/chat/completions",
    data=json.dumps(payload).encode("utf-8"),
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

func dataPolicySpikeFakeProviderScript() string {
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
        raw = self.rfile.read(length)
        try:
            body = json.loads(raw)
        except Exception:
            body = {"_unparsed": raw.decode("utf-8", "replace")}
        with open("/captures/requests.jsonl", "a") as f:
            f.write(json.dumps({"path": self.path, "body": body}) + "\n")
        response = {
            "id": "chatcmpl-data-policy-spike",
            "object": "chat.completion",
            "model": "gpt-4o",
            "choices": [{"index": 0, "message": {"role": "assistant", "content": "data policy spike reply"}, "finish_reason": "stop"}],
            "usage": {"prompt_tokens": 11, "completion_tokens": 7, "total_tokens": 18},
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
