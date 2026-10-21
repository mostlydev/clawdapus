//go:build spike

package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/mostlydev/clawdapus/internal/runs"
)

// TestSpikePerRunProviderLive complements the deterministic lifecycle spike:
// a real pinned PicoClaw binary must reach a real provider through the candidate
// proxy and complete under supervisor authority. Native tools stay enabled.
// Credentials come from the environment or the existing rollcall fixture;
// only synthetic task/result content is used. No released image is retagged.
func TestSpikePerRunProviderLive(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "../.."))
	key := os.Getenv("ANTHROPIC_API_KEY")
	if key == "" {
		fixture := filepath.Join(root, "examples/rollcall/.env")
		if _, err := os.Stat(fixture); err == nil {
			key = spikeLoadDotEnv(t, fixture)["ANTHROPIC_API_KEY"]
		}
	}
	if strings.TrimSpace(key) == "" {
		t.Skip("ANTHROPIC_API_KEY unavailable; real-provider acceptance not run")
	}
	if err := exec.Command("docker", "info").Run(); err != nil {
		t.Skip("Docker unavailable")
	}
	t.Setenv("ANTHROPIC_API_KEY", key)
	t.Setenv("CLLAMA_UI_PORT", spikeFreePort(t))
	t.Setenv("CLAWDASH_ADDR", ":"+spikeFreePort(t))
	work := t.TempDir()
	generated := filepath.Join(work, "compose.generated.yml")
	var sup *runsSpikeSupervisor
	t.Cleanup(func() {
		if sup != nil {
			sup.stop()
		}
		spikeRunsCleanup(t, work, generated)
	})
	unique := fmt.Sprintf("runs-spike-provider-live-%d", time.Now().UnixNano())
	previousTag := infraCllamaTag
	infraCllamaTag = unique
	t.Cleanup(func() { infraCllamaTag = previousTag })
	proxyImage := "ghcr.io/mostlydev/cllama:" + unique
	spikeBuildImage(t, filepath.Join(root, "cllama"), proxyImage, "Dockerfile")
	t.Cleanup(func() { _ = exec.Command("docker", "image", "rm", "-f", proxyImage).Run() })
	for _, image := range []string{infraImageSpecFor(infraComponentClawdash).ExpectedRef, "docker.io/sipeed/picoclaw:v0.2.7", "python:3.12-alpine"} {
		spikeEnsurePulledImage(t, image)
	}
	workerImage := "claw-runs-worker:" + unique
	workerBuild := filepath.Join(work, "worker-image")
	if err := os.MkdirAll(workerBuild, 0o755); err != nil {
		t.Fatal(err)
	}
	spikeWriteFile(t, filepath.Join(workerBuild, "Dockerfile"), "FROM docker.io/sipeed/picoclaw:v0.2.7\nLABEL claw.type=picoclaw\n")
	spikeBuildImage(t, workerBuild, workerImage, "Dockerfile")
	t.Cleanup(func() { _ = exec.Command("docker", "image", "rm", "-f", workerImage).Run() })
	spikeWriteFile(t, filepath.Join(work, "AGENTS.md"), "# Worker\nAnswer the synthetic task exactly. No tools are necessary.\n")
	podFile := filepath.Join(work, "claw-pod.yml")
	spikeWriteFile(t, podFile, fmt.Sprintf(`name: %s
x-claw:
  pod: %s
  cllama-defaults:
    proxy: [passthrough]
    env:
      ANTHROPIC_API_KEY: ${ANTHROPIC_API_KEY}
  models-defaults:
    primary: anthropic/claude-sonnet-5-5
services:
  worker:
    image: %s
    x-claw:
      agent: ./AGENTS.md
      lifecycle: per-run
      run:
        max-runtime: 3m
        max-authority: 10m
  revoked-probe:
    image: python:3.12-alpine
    profiles: [probe]
    networks: [runs-probe]
    entrypoint: [python, -c]
    command:
      - |
        import json, sys, urllib.request, urllib.error
        token = json.load(sys.stdin)["token"]
        req = urllib.request.Request("http://cllama:8080/v1/chat/completions", data=b'{"model":"anthropic/claude-sonnet-5-5","messages":[{"role":"user","content":"synthetic"}]}', headers={"Content-Type":"application/json", "Authorization":"Bearer " + token})
        try:
            print(urllib.request.urlopen(req, timeout=15).status)
        except urllib.error.HTTPError as e:
            print(e.code)
networks:
  runs-probe:
    external: true
    name: claw-runs-%s
`, unique, unique, workerImage, runs.PodNamespace(work)))
	previousDetach := composeUpDetach
	composeUpDetach = true
	t.Cleanup(func() { composeUpDetach = previousDetach })
	if err := runComposeUp(podFile); err != nil {
		t.Fatalf("compile/apply isolated live pod: %v", err)
	}
	spikeWaitHealthy(t, spikeComposeContainerID(t, generated, "cllama"), 60*time.Second)
	runsSpikePodNS = runs.PodNamespace(work)
	cb := newRunsSpikeCallbacks()
	callback := httptest.NewServer(cb)
	defer callback.Close()
	sock, err := os.MkdirTemp("/tmp", "claw-provider-live-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(sock)
	if err := os.Chmod(sock, 0o700); err != nil {
		t.Fatal(err)
	}
	sup = startRunsSpikeSupervisor(t, work, sock, callback.URL)
	client, err := runs.Dial(sock, uint32(os.Geteuid()))
	if err != nil {
		t.Fatal(err)
	}
	runsSpikeLaunch(t, client, "real-provider", 1, "Reply with exactly PROVIDER-LIVE-OK and nothing else.", 5*time.Minute, "synthetic-callback")
	// Terminal cleanup removes the bearer custody file; capture it while the
	// accepted run owns it, then prove that same credential is refused later.
	token := runsSpikeRunToken(t, work, runs.Key{Installation: "inst", RunID: "real-provider"})
	view := runsSpikeWait(t, client, "real-provider", "stopped")
	if view.State != runs.StateStopped || view.Failure != "" || !view.Revoked || view.RevocationProof != runs.ProofCllamaAck || view.ExitCode == nil || *view.ExitCode != 0 {
		t.Fatalf("real model run did not succeed/revoke: %+v", view)
	}
	if view.Callbacks.Start != runs.CallbackAcked || view.Callbacks.End != runs.EndAcked {
		t.Fatalf("callback ACKs: %+v", view.Callbacks)
	}
	var result []byte
	if err := client.Do(context.Background(), "GET", runs.RunPath(runs.Key{Installation: "inst", RunID: "real-provider"}, "/result"), nil, &result); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(result)) != "PROVIDER-LIVE-OK" {
		t.Fatalf("unexpected synthetic answer: %q", result)
	}
	runsSpikeAssertNoWorker(t, view)

	// Check only content-free telemetry, never print a raw history or log tail.
	audit, err := exec.Command("docker", "compose", "-f", generated, "logs", "--no-log-prefix", "cllama").Output()
	if err != nil {
		t.Fatal("read proxy audit")
	}
	foundResponse, foundAdapter := false, false
	scanner := bufio.NewScanner(bytes.NewReader(audit))
	for scanner.Scan() {
		var event struct {
			ClawID       string `json:"claw_id"`
			Type         string `json:"type"`
			Status       int    `json:"status_code"`
			TokensIn     int    `json:"tokens_in"`
			TokensOut    int    `json:"tokens_out"`
			Intervention string `json:"intervention"`
		}
		if json.Unmarshal(scanner.Bytes(), &event) != nil || event.ClawID != view.RunAgentID {
			continue
		}
		if event.Type == "response" && event.Status == 200 && event.TokensIn > 0 && event.TokensOut > 0 {
			foundResponse = true
		}
		if event.Intervention == "anthropic_messages_adapter" {
			foundAdapter = true
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if !foundResponse || !foundAdapter {
		t.Fatalf("missing successful adapter telemetry: response=%v adapter=%v", foundResponse, foundAdapter)
	}
	history, err := os.ReadFile(filepath.Join(work, ".claw-session-history", view.RunAgentID, "history.jsonl"))
	if err != nil {
		t.Fatal("successful per-run history missing")
	}
	var entry struct {
		Status   int    `json:"status_code"`
		Provider string `json:"effective_provider"`
		Model    string `json:"effective_model"`
	}
	line := bytes.Split(bytes.TrimSpace(history), []byte("\n"))
	if len(line) == 0 || json.Unmarshal(line[len(line)-1], &entry) != nil || entry.Status != 200 || entry.Provider != "anthropic" || entry.Model != "claude-sonnet-5-5" {
		t.Fatal("history did not record native provider success")
	}

	probe := exec.Command("docker", "compose", "-f", generated, "run", "--rm", "-T", "--no-deps", "revoked-probe")
	input, _ := json.Marshal(map[string]string{"token": token})
	probe.Stdin = bytes.NewReader(input)
	out, err := probe.Output()
	if err != nil || strings.TrimSpace(string(out)) != "403" {
		t.Fatalf("revoked identity was not refused: status=%q error=%v", out, err)
	}
	t.Log("real PicoClaw -> native Anthropic succeeded; result, usage/history, callback ACKs, durable revocation and container cleanup verified")
}
