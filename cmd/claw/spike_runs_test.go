//go:build spike

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mostlydev/clawdapus/internal/runs"
	"gopkg.in/yaml.v3"
)

// TestSpikePerRunWorkers is the live acceptance for issue #372. It runs a real
// claw up of a pod with a per-run PicoClaw template, a cllama built from the
// submodule under test (cllama#54) at a unique local tag, and the run
// supervisor over a real Unix socket. The provider is a local fake.
func TestSpikePerRunWorkers(t *testing.T) {
	if err := exec.Command("docker", "info").Run(); err != nil {
		t.Skipf("docker not available: %v", err)
	}
	_, thisFile, _, _ := runtime.Caller(0)
	repoRoot, _ := filepath.Abs(filepath.Join(filepath.Dir(thisFile), "..", ".."))

	workDir := t.TempDir()
	var sup *runsSpikeSupervisor
	generatedPath := filepath.Join(workDir, "compose.generated.yml")
	podFile := filepath.Join(workDir, "claw-pod.yml")
	t.Cleanup(func() {
		if sup != nil {
			sup.stop()
		}
		if t.Failed() {
			out, _ := exec.Command("docker", "compose", "-f", generatedPath, "logs", "--tail", "120").CombinedOutput()
			t.Logf("compose logs:\n%s", out)
		}
		spikeRunsCleanup(t, workDir, generatedPath)
	})

	// cllama from the working tree at a unique tag: no released tag is moved.
	// CLAW_SPIKE_PINNED_CLLAMA=1 instead proves the published, pinned image.
	unique := fmt.Sprintf("issue372-spike-%d", time.Now().UnixNano())
	if os.Getenv("CLAW_SPIKE_PINNED_CLLAMA") == "1" {
		spikeEnsurePulledImage(t, infraImageSpecFor(infraComponentCllama).ExpectedRef)
	} else {
		prevTag := infraCllamaTag
		infraCllamaTag = unique
		t.Cleanup(func() { infraCllamaTag = prevTag })
		cllamaRef := "ghcr.io/mostlydev/cllama:" + unique
		spikeBuildImage(t, filepath.Join(repoRoot, "cllama"), cllamaRef, "Dockerfile")
		t.Cleanup(func() { _ = exec.Command("docker", "image", "rm", "-f", cllamaRef).Run() })
	}
	// Use the published dashboard image; never build local bytes under a
	// released tag.
	spikeEnsurePulledImage(t, infraImageSpecFor(infraComponentClawdash).ExpectedRef)

	pythonImage := "python:3.12-alpine"
	spikeEnsurePulledImage(t, pythonImage)
	spikeEnsurePulledImage(t, "docker.io/sipeed/picoclaw:v0.2.7")
	workerImage := "claw-runs-spike-worker:" + unique
	workerCtx := filepath.Join(workDir, "worker-image")
	if err := os.MkdirAll(workerCtx, 0o755); err != nil {
		t.Fatal(err)
	}
	spikeWriteFile(t, filepath.Join(workerCtx, "Dockerfile"), "FROM docker.io/sipeed/picoclaw:v0.2.7\nLABEL claw.type=picoclaw\n")
	spikeBuildImage(t, workerCtx, workerImage, "Dockerfile")
	t.Cleanup(func() { _ = exec.Command("docker", "image", "rm", "-f", workerImage).Run() })

	spikeWriteFile(t, filepath.Join(workDir, "AGENTS.md"), "# Run worker\n\nAnswer the task in one line.")
	providerImage := "claw-runs-spike-provider:" + unique
	providerCtx := filepath.Join(workDir, "provider-image")
	if err := os.MkdirAll(providerCtx, 0o755); err != nil {
		t.Fatal(err)
	}
	spikeWriteFile(t, filepath.Join(providerCtx, "fake_provider.py"), runsSpikeFakeProvider)
	spikeWriteFile(t, filepath.Join(providerCtx, ".claw-describe.json"), runsSpikeToolDescriptor)
	spikeWriteFile(t, filepath.Join(providerCtx, "Dockerfile"), "FROM "+pythonImage+"\nCOPY fake_provider.py /app/fake_provider.py\nCOPY .claw-describe.json /.claw-describe.json\nLABEL claw.describe=/.claw-describe.json\nCMD [\"python\",\"-u\",\"/app/fake_provider.py\"]\n")
	spikeBuildImage(t, providerCtx, providerImage, "Dockerfile")
	t.Cleanup(func() { _ = exec.Command("docker", "image", "rm", "-f", providerImage).Run() })
	const ingressSecret = "spike-proxy-only-ingress"
	spikeWriteFile(t, filepath.Join(workDir, ".env"), "TOOL_INGRESS_TOKEN="+ingressSecret+"\n")
	spikeWriteFile(t, podFile, fmt.Sprintf(`name: %s

x-claw:
  pod: %s
  cllama-defaults:
    proxy: [passthrough]
    env:
      OPENAI_API_KEY: sk-local-fake
      OPENAI_BASE_URL: http://fake-provider:8080/v1
      TOOL_INGRESS_TOKEN: ${TOOL_INGRESS_TOKEN}
  models-defaults:
    primary: openai/gpt-4o

services:
  worker:
    image: %s
    x-claw:
      agent: ./AGENTS.md
      lifecycle: per-run
      tools: [fake-provider]
      run:
        max-runtime: 5m
        max-authority: 30m

  fake-provider:
    image: %s
    environment:
      TOOL_INGRESS_TOKEN: ${TOOL_INGRESS_TOKEN}
    expose:
      - "8080"
    networks:
      - claw-internal
`, "runs-spike-"+unique, "runs-spike-"+unique, workerImage, providerImage))

	t.Setenv("CLLAMA_UI_PORT", spikeFreePort(t))
	t.Setenv("CLAWDASH_ADDR", ":"+spikeFreePort(t))
	t.Setenv("CLAW_DASHBOARD_BIND", "127.0.0.1")
	t.Setenv("CLAWDASH_ENABLED", "true")
	prevDetach := composeUpDetach
	composeUpDetach = true
	defer func() { composeUpDetach = prevDetach }()

	if err := runComposeUp(podFile); err != nil {
		t.Fatalf("runComposeUp: %v", err)
	}
	spikeWaitHealthy(t, spikeComposeContainerID(t, generatedPath, "cllama"), 60*time.Second)
	dashboardID := spikeComposeContainerID(t, generatedPath, "clawdash")
	// Exercise disabling a previously running dashboard, not just omitting it
	// on a fresh pod. Keep cllama's local revoke/self-test endpoint working.
	t.Setenv("CLAWDASH_ENABLED", "false")
	if err := runComposeUp(podFile); err != nil {
		t.Fatalf("disable dashboard on existing pod: %v", err)
	}
	if err := exec.Command("docker", "inspect", dashboardID).Run(); err == nil {
		t.Fatal("disabled dashboard container remains")
	}
	spikeWaitHealthy(t, spikeComposeContainerID(t, generatedPath, "cllama"), 60*time.Second)
	if info, err := os.Stat(filepath.Join(workDir, ".claw-runtime", "context")); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("per-run-only context root must be 0700: %v", err)
	}

	// Compile-side checks: the per-run service is a template, not a service.
	generated := spikeReadFile(t, generatedPath)
	if strings.Contains(generated, "\n  clawdash:\n") || !strings.Contains(generated, "127.0.0.1:") {
		t.Fatalf("dashboard disable/host binding missing:\n%s", generated)
	}
	if strings.Contains(generated, "\n  worker:\n") {
		t.Fatalf("per-run template must not be a compose service:\n%s", generated)
	}
	if info, err := os.Stat(generatedPath); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("compose.generated.yml must be 0600 for per-run pods, got %v", info.Mode().Perm())
	}
	if info, err := os.Stat(filepath.Join(workDir, runs.RevocationDirName)); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf(".claw-revocations must be 0700: %v", err)
	}
	runsSpikePodNS = runs.PodNamespace(workDir)
	tpl, _, err := runs.LoadTemplate(workDir, "worker")
	if err != nil {
		t.Fatalf("template: %v", err)
	}
	publicTemplate := spikeReadFile(t, filepath.Join(runs.TemplateDir(workDir), "worker.json"))
	if strings.Contains(publicTemplate, ingressSecret) {
		t.Fatal("proxy-only ingress secret reached the public worker template")
	}
	if status := runsSpikeUnauthenticatedToolStatus(t, "runs-spike-"+unique+"_claw-internal", pythonImage); status != 401 {
		t.Fatalf("tool service must refuse requests without proxy bearer: %d", status)
	}
	netInspect, err := exec.Command("docker", "network", "inspect", tpl.RunNetwork, "--format", "{{.Internal}}").Output()
	if err != nil || strings.TrimSpace(string(netInspect)) != "true" {
		t.Fatalf("run network %s must be internal: %s %v", tpl.RunNetwork, netInspect, err)
	}

	// Callback receiver standing in for the orchestrator.
	cb := newRunsSpikeCallbacks()
	cbServer := httptest.NewServer(cb)
	defer cbServer.Close()

	sockDir, err := os.MkdirTemp("/tmp", "clawruns-spike-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(sockDir)
	_ = os.Chmod(sockDir, 0o700)

	sup = startRunsSpikeSupervisor(t, workDir, sockDir, cbServer.URL)
	client, err := runs.Dial(sockDir, uint32(os.Geteuid()))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	// 1. One real run launches, reports, and ends.
	view := runsSpikeLaunch(t, client, "a", 1, "say hello", 20*time.Minute, "")
	view = runsSpikeWait(t, client, "a", "stopped")
	if view.State != runs.StateStopped || view.Failure != "" || !view.Revoked || view.RevocationProof != runs.ProofCllamaAck {
		t.Fatalf("case 1: %+v", view)
	}
	var result []byte
	if err := client.Do(ctx, "GET", runs.RunPath(runs.Key{Installation: "inst", RunID: "a"}, "/result"), nil, &result); err != nil || !strings.Contains(string(result), "SPIKE-ANSWER") {
		t.Fatalf("case 1 result: %q %v", result, err)
	}
	runsSpikeAssertNoWorker(t, view)

	// A nonempty compiled grant must support real managed dispatch, while an
	// empty per-run subscription to that same template exposes no such tool.
	if len(tpl.ToolsGrant) != 1 || tpl.ToolsGrant[0] != "fake-provider.echo" {
		t.Fatalf("compiled tool grant: %v", tpl.ToolsGrant)
	}
	rawToolSpec, _ := json.Marshal(map[string]any{"template": "worker", "task": "MANAGED-PER-RUN-PROBE", "tools": []string{"fake-provider.echo"}})
	_, _, toolDigest, _ := runs.CanonicalSpec(rawToolSpec)
	if err := client.Do(ctx, "POST", "/v1/runs", map[string]any{"installation": "inst", "run_id": "tool-allowed", "generation": 1, "spec_digest": toolDigest, "spec": json.RawMessage(rawToolSpec), "authority_expires_at": time.Now().Add(20 * time.Minute)}, &view); err != nil {
		t.Fatal(err)
	}
	allowed := runsSpikeWait(t, client, "tool-allowed", "stopped")
	if allowed.State != runs.StateStopped || allowed.Failure != "" {
		t.Fatalf("allowed tool run: %+v", allowed)
	}
	if err := client.Do(ctx, "GET", runs.RunPath(runs.Key{Installation: "inst", RunID: "tool-allowed"}, "/result"), nil, &result); err != nil || !strings.Contains(string(result), "SPIKE-ANSWER-TOOL") {
		t.Fatalf("managed result: %q %v", result, err)
	}
	runsSpikeLaunch(t, client, "tool-empty", 1, "EMPTY-PER-RUN-PROBE", 20*time.Minute, "")
	empty := runsSpikeWait(t, client, "tool-empty", "stopped")
	if empty.State != runs.StateStopped || empty.Failure != "" {
		t.Fatalf("empty tool run: %+v", empty)
	}
	events := runsSpikeToolEvents(t, generatedPath)
	if !strings.Contains(events, "TOOL "+allowed.RunAgentID) || !strings.Contains(events, "MODEL EMPTY tools=[]") || strings.Contains(events, "TOOL "+empty.RunAgentID) {
		t.Fatalf("allowlist dispatch evidence: %s", events)
	}
	if !strings.Contains(events, "TOOL-AUTH-OK") || !strings.Contains(events, "TOOL-AUTH-DENIED") || strings.Contains(events, "MODEL-SECRET-LEAK") || strings.Contains(events, ingressSecret) {
		t.Fatal("managed-tool bearer or secret isolation evidence failed")
	}
	proxyAudit, err := exec.Command("docker", "compose", "-f", generatedPath, "logs", "--no-log-prefix", "cllama").Output()
	if err != nil || !runsSpikeHasDispatch(proxyAudit, allowed.RunAgentID) || runsSpikeHasDispatch(proxyAudit, empty.RunAgentID) {
		t.Fatalf("per-run dispatch audit did not preserve allowlists: %v", err)
	}
	if strings.Contains(string(proxyAudit), ingressSecret) {
		t.Fatal("proxy-only ingress secret reached proxy logs")
	}

	// 2. Cancel mid-run reaches revoked then stopped; the token is dead.
	runsSpikeLaunch(t, client, "b", 1, "SLOW task", 20*time.Minute, "")
	view = runsSpikeWait(t, client, "b", "running")
	if strings.Contains(runsSpikeInspectEnv(t, view), ingressSecret) {
		t.Fatal("proxy-only ingress secret reached worker env, argv or mounts")
	}
	project := spikeReadFile(t, filepath.Join(workDir, runs.StateDirName, "projects", runs.Key{Installation: "inst", RunID: "b"}.Hash(), "compose.yml"))
	if strings.Contains(project, ingressSecret) {
		t.Fatal("proxy-only ingress secret reached rendered run project")
	}
	token := runsSpikeRunToken(t, workDir, runs.Key{Installation: "inst", RunID: "b"})
	runsSpikeWaitProviderHit(t, cb, generatedPath, "SLOW")
	var cancelled runs.StatusView
	if err := client.Do(ctx, "POST", runs.RunPath(runs.Key{Installation: "inst", RunID: "b"}, "/cancel?wait=stopped"), map[string]any{"generation": 1}, &cancelled); err != nil {
		t.Fatal(err)
	}
	if cancelled.State != runs.StateStopped || !cancelled.Revoked || !cancelled.Cancelled || cancelled.Timestamps.Revoked == nil {
		t.Fatalf("case 2: %+v", cancelled)
	}
	runsSpikeAssertNoWorker(t, cancelled)
	if status := runsSpikeProxyStatus(t, tpl.RunNetwork, pythonImage, token); status != 403 {
		t.Fatalf("case 2: revoked token must be refused by cllama, got %d", status)
	}

	// 3. Cancel before launch leaves no container, and the launch is refused.
	if err := client.Do(ctx, "POST", runs.RunPath(runs.Key{Installation: "inst", RunID: "c"}, "/cancel"), map[string]any{"generation": 1}, &view); err != nil || view.State != runs.StateStopped {
		t.Fatalf("case 3 cancel: %v %+v", err, view)
	}
	if err := runsSpikeLaunchErr(client, "c", 1, "say hello"); runsSpikeCode(err) != "cancelled" {
		t.Fatalf("case 3 launch: %v", err)
	}
	runsSpikeAssertNoContainers(t, runs.Key{Installation: "inst", RunID: "c"}.RunKey(runs.PodNamespace(workDir)))

	// 4. A lost ACK and a duplicate launch give exactly one container. The
	// first launch is sent over a raw authenticated connection whose response
	// is never read.
	runsSpikeLaunchUnread(t, sockDir, "d", "SLOW dup")
	view = runsSpikeWait(t, client, "d", "running")
	runsSpikeLaunch(t, client, "d", 1, "SLOW dup", 20*time.Minute, "")
	runsSpikeAssertIsolation(t, view, tpl.RunNetwork)
	runsSpikeLaunch(t, client, "d", 1, "SLOW dup", 20*time.Minute, "")
	if n := runsSpikeContainerCount(t, view); n != 1 {
		t.Fatalf("case 4: expected exactly one container, got %d", n)
	}

	// 6. A mismatched digest is rejected.
	if err := runsSpikeLaunchErr(client, "d", 1, "different task"); runsSpikeCode(err) != "digest_mismatch" {
		t.Fatalf("case 6: %v", err)
	}

	// 5. A supervisor restart mid-run reconciles.
	sup.stop()
	sup = startRunsSpikeSupervisor(t, workDir, sockDir, cbServer.URL)
	client, _ = runs.Dial(sockDir, uint32(os.Geteuid()))
	view = runsSpikeWait(t, client, "d", "running")
	if view.State != runs.StateRunning || runsSpikeContainerCount(t, view) != 1 {
		t.Fatalf("case 5: %+v", view)
	}
	if err := client.Do(ctx, "POST", runs.RunPath(runs.Key{Installation: "inst", RunID: "d"}, "/cancel?wait=stopped"), map[string]any{"generation": 1}, &view); err != nil || view.State != runs.StateStopped {
		t.Fatalf("case 5 cancel: %v %+v", err, view)
	}

	// 7. A spec exceeding its grant creates no container or record.
	raw, _ := json.Marshal(map[string]any{"template": "worker", "task": "x", "tools": []string{"not-granted.tool"}})
	_, _, digest, _ := runs.CanonicalSpec(raw)
	err = client.Do(ctx, "POST", "/v1/runs", map[string]any{"installation": "inst", "run_id": "g", "generation": 1, "spec_digest": digest, "spec": json.RawMessage(raw), "authority_expires_at": time.Now().Add(20 * time.Minute)}, nil)
	if runsSpikeCode(err) != "grant_exceeded" {
		t.Fatalf("case 7: %v", err)
	}
	if err := client.Do(ctx, "GET", runs.RunPath(runs.Key{Installation: "inst", RunID: "g"}, ""), nil, nil); runsSpikeCode(err) != "not_found" {
		t.Fatalf("case 7: refused launch must leave no record, got %v", err)
	}

	// 8. Expired orchestrator authority cleans up the runtime and tool access.
	runsSpikeLaunch(t, client, "e", 1, "SLOW expiring", 20*time.Second, "")
	view = runsSpikeWait(t, client, "e", "failed")
	if view.Failure != runs.ReasonAuthorityExpired || !view.Revoked {
		t.Fatalf("case 8: %+v", view)
	}
	runsSpikeAssertNoWorker(t, view)

	// 9. Credential recovery: the custody file is lost across a supervisor
	// restart; a reissue under the same run identity gives no duplicate.
	runsSpikeLaunch(t, client, "f", 1, "SLOW custody", 20*time.Minute, "cred-old")
	view = runsSpikeWait(t, client, "f", "running")
	runsSpikeWaitCallback(t, cb, "f", "start", "cred-old")
	sup.stop()
	_ = os.Remove(filepath.Join(workDir, runs.StateDirName, "ledger", runs.Key{Installation: "inst", RunID: "f"}.Hash()+".credential"))
	sup = startRunsSpikeSupervisor(t, workDir, sockDir, cbServer.URL)
	client, _ = runs.Dial(sockDir, uint32(os.Geteuid()))
	if err := client.Do(ctx, "GET", runs.RunPath(runs.Key{Installation: "inst", RunID: "f"}, ""), nil, &view); err != nil || view.Credential != runs.CredentialMissing {
		t.Fatalf("case 9: credential must be reported missing: %v %+v", err, view)
	}
	runsSpikeLaunch(t, client, "f", 2, "SLOW custody", 20*time.Minute, "cred-new")
	runsSpikeWaitCallback(t, cb, "f", "heartbeat", "cred-new")
	if n := runsSpikeContainerCount(t, view); n != 1 {
		t.Fatalf("case 9: expected one worker after credential recovery, got %d", n)
	}
	if strings.Contains(runsSpikeInspectEnv(t, view), "cred-") {
		t.Fatalf("case 9: orchestrator credential reached the worker")
	}

	// Recovery: with cllama unavailable, claw down still completes and the
	// next claw up is clean.
	sup.stop()
	if out, err := exec.Command("docker", "compose", "-f", generatedPath, "stop", "cllama").CombinedOutput(); err != nil {
		t.Fatalf("stop cllama: %v\n%s", err, out)
	}
	skipStalenessCheck = true
	composePodFile = podFile
	defer func() { skipStalenessCheck = false; composePodFile = "" }()
	if err := composeDownCmd.RunE(composeDownCmd, nil); err != nil {
		t.Fatalf("claw down with cllama stopped: %v", err)
	}
	live, err := runs.HasLiveRuns(workDir)
	if err != nil || live {
		t.Fatalf("claw down must leave no unfinished runs: live=%v err=%v", live, err)
	}
	if err := runComposeUp(podFile); err != nil {
		t.Fatalf("claw up after recovery: %v", err)
	}
}

const runsSpikeFakeProvider = `import json, os, time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

class Handler(BaseHTTPRequestHandler):
    def log_message(self, *a):
        pass
    def do_GET(self):
        self.send_response(200 if self.path == "/health" else 404)
        self.end_headers()
    def do_POST(self):
        body = self.rfile.read(int(self.headers.get("Content-Length", "0"))).decode("utf-8", "replace")
        payload = json.loads(body)
        if self.path.startswith("/tool/"):
            token = os.environ["TOOL_INGRESS_TOKEN"]
            if self.headers.get("Authorization") != "Bearer " + token:
                print("TOOL-AUTH-DENIED", flush=True)
                self.reply({"error": "unauthorized"}, 401)
                return
            print("TOOL-AUTH-OK", flush=True)
            print("TOOL " + self.path[len("/tool/"):], flush=True)
            self.reply({"value": "TOOL-ANSWER"})
            return
        if os.environ["TOOL_INGRESS_TOKEN"] in body:
            print("MODEL-SECRET-LEAK", flush=True)
        managed = [t["function"]["name"] for t in payload.get("tools", [])
                   if "echo" in t.get("function", {}).get("name", "")]
        if "EMPTY-PER-RUN-PROBE" in body:
            print("MODEL EMPTY tools=" + json.dumps(managed), flush=True)
        print("REQUEST", "SLOW" if "SLOW" in body else "FAST", flush=True)
        if "SLOW" in body:
            time.sleep(600)
        message = {"role": "assistant", "content": "SPIKE-ANSWER"}
        finish = "stop"
        if "MANAGED-PER-RUN-PROBE" in body:
            if any(m.get("role") == "tool" and "TOOL-ANSWER" in str(m.get("content", "")) for m in payload.get("messages", [])):
                message["content"] = "SPIKE-ANSWER-TOOL"
            elif managed:
                message = {"role": "assistant", "content": "", "tool_calls": [
                    {"id": "call-spike", "type": "function", "function": {
                        "name": managed[0], "arguments": json.dumps({"message": "hello"})}}]}
                finish = "tool_calls"
            else:
                message["content"] = "MISSING-MANAGED-TOOL"
        self.reply({"id": "chatcmpl-runs-spike", "object": "chat.completion", "created": 1, "model": "gpt-4o",
                    "choices": [{"index": 0, "message": message, "finish_reason": finish}],
                    "usage": {"prompt_tokens": 10, "completion_tokens": 2, "total_tokens": 12}})
    def reply(self, out, status=200):
        data = json.dumps(out).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

ThreadingHTTPServer(("0.0.0.0", 8080), Handler).serve_forever()
`

type runsSpikeSupervisor struct {
	cancel context.CancelFunc
	done   chan struct{}
}

func (s *runsSpikeSupervisor) stop() {
	s.cancel()
	<-s.done
}

func startRunsSpikeSupervisor(t *testing.T, podDir, sockDir, callbackOrigin string) *runsSpikeSupervisor {
	t.Helper()
	lock, err := runs.AcquirePodLock(podDir)
	if err != nil {
		t.Fatalf("pod lock: %v", err)
	}
	exec, err := runs.NewDockerExecutor()
	if err != nil {
		t.Fatal(err)
	}
	exec.Hold = lock.File()
	notifier, err := runs.NewHTTPNotifier(callbackOrigin, "/api/runs/{run_id}")
	if err != nil {
		t.Fatal(err)
	}
	sup, err := runs.NewSupervisor(runs.Config{
		PodDir: podDir, Executor: exec, Revoker: &runs.CllamaRevoker{PodDir: podDir},
		Notifier: notifier, HeartbeatInterval: 2 * time.Second,
		Logf: func(format string, args ...any) { t.Logf(format, args...) },
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	if orphans, err := sup.Orphans(ctx); err != nil || len(orphans) > 0 {
		t.Fatalf("orphans: %v %d", err, len(orphans))
	}
	if err := sup.CheckRevocationSupport(ctx); err != nil {
		t.Fatalf("revocation probe: %v", err)
	}
	sup.AdvanceAll(ctx)
	done := make(chan struct{})
	advancing := make(chan struct{})
	go func() {
		defer close(advancing)
		sup.Run(ctx, time.Second)
	}()
	srv := &runs.Server{Sup: sup, Pod: "runs-spike"}
	go func() {
		defer close(done)
		if err := srv.Listen(ctx, sockDir); err != nil {
			t.Errorf("listen: %v", err)
		}
		<-advancing
		exec.Close()
		lock.Release()
	}()
	for i := 0; i < 200; i++ {
		if _, err := os.Stat(filepath.Join(sockDir, runs.SocketName)); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	return &runsSpikeSupervisor{cancel: cancel, done: done}
}

func runsSpikeLaunchErr(client *runs.Client, runID string, gen uint64, task string) error {
	raw, _ := json.Marshal(map[string]any{"template": "worker", "task": task, "tools": []string{}})
	_, _, digest, _ := runs.CanonicalSpec(raw)
	return client.Do(context.Background(), "POST", "/v1/runs", map[string]any{
		"installation": "inst", "run_id": runID, "generation": gen, "spec_digest": digest,
		"spec": json.RawMessage(raw), "authority_expires_at": time.Now().Add(20 * time.Minute),
	}, nil)
}

func runsSpikeLaunch(t *testing.T, client *runs.Client, runID string, gen uint64, task string, ttl time.Duration, credential string) runs.StatusView {
	t.Helper()
	raw, _ := json.Marshal(map[string]any{"template": "worker", "task": task, "tools": []string{}})
	_, _, digest, _ := runs.CanonicalSpec(raw)
	body := map[string]any{"installation": "inst", "run_id": runID, "generation": gen, "spec_digest": digest,
		"spec": json.RawMessage(raw), "authority_expires_at": time.Now().Add(ttl)}
	if credential != "" {
		body["credential"] = credential
	}
	var view runs.StatusView
	if err := client.Do(context.Background(), "POST", "/v1/runs", body, &view); err != nil {
		t.Fatalf("launch %s: %v", runID, err)
	}
	return view
}

func runsSpikeWait(t *testing.T, client *runs.Client, runID, state string) runs.StatusView {
	t.Helper()
	deadline := time.Now().Add(3 * time.Minute)
	var view runs.StatusView
	for time.Now().Before(deadline) {
		if err := client.Do(context.Background(), "GET", runs.RunPath(runs.Key{Installation: "inst", RunID: runID}, "?wait="+state+"&timeout=30s"), nil, &view); err != nil {
			t.Fatalf("status %s: %v", runID, err)
		}
		if string(view.State) == state || view.State.Terminal() {
			return view
		}
	}
	t.Fatalf("run %s never reached %s: %+v", runID, state, view)
	return view
}

func runsSpikeCode(err error) string {
	var apiErr *runs.APIError
	if errors.As(err, &apiErr) {
		return apiErr.Code
	}
	return ""
}

func runsSpikeContainers(t *testing.T, runKey string) []string {
	t.Helper()
	out, err := exec.Command("docker", "ps", "-aq", "--filter", "label="+runs.LabelRunKey+"="+runKey).Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.Fields(string(out))
}

func runsSpikeContainerCount(t *testing.T, view runs.StatusView) int {
	t.Helper()
	return len(runsSpikeContainers(t, runsSpikeRunKey(t, view)))
}

func runsSpikeRunKey(t *testing.T, view runs.StatusView) string {
	t.Helper()
	return runs.Key{Installation: view.Installation, RunID: view.RunID}.RunKey(runsSpikePodNS)
}

var runsSpikePodNS string

func runsSpikeAssertNoWorker(t *testing.T, view runs.StatusView) {
	t.Helper()
	runsSpikeAssertNoContainers(t, runsSpikeRunKey(t, view))
}

func runsSpikeAssertNoContainers(t *testing.T, runKey string) {
	t.Helper()
	if ids := runsSpikeContainers(t, runKey); len(ids) != 0 {
		t.Fatalf("expected no worker containers, found %v", ids)
	}
}

func runsSpikeInspectEnv(t *testing.T, view runs.StatusView) string {
	t.Helper()
	ids := runsSpikeContainers(t, runsSpikeRunKey(t, view))
	if len(ids) == 0 {
		t.Fatal("no worker container")
	}
	out, err := exec.Command("docker", "inspect", ids[0], "--format", "{{json .Config.Env}} {{json .Config.Cmd}} {{json .Mounts}}").Output()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "[]") {
		t.Fatalf("worker must have no mounts: %s", out)
	}
	return string(out)
}

func runsSpikeRunToken(t *testing.T, podDir string, k runs.Key) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(podDir, runs.StateDirName, "ledger", k.Hash()+".cllama"))
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(raw))
}

func runsSpikeWaitProviderHit(t *testing.T, cb *runsSpikeCallbacks, generatedPath, marker string) {
	t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		out, _ := exec.Command("docker", "compose", "-f", generatedPath, "logs", "fake-provider").CombinedOutput()
		if strings.Contains(string(out), "REQUEST "+marker) {
			return
		}
		time.Sleep(time.Second)
	}
	t.Fatalf("provider never saw a %s request", marker)
}

const runsSpikeProbe = `
import sys, urllib.error, urllib.request
req = urllib.request.Request("http://cllama:8080/v1/chat/completions", data=b'{"model":"openai/gpt-4o","messages":[{"role":"user","content":"hi"}]}',
    headers={"Content-Type": "application/json", "Authorization": "Bearer " + sys.argv[1]})
try:
    print(urllib.request.urlopen(req, timeout=15).status)
except urllib.error.HTTPError as e:
    print(e.code)
`

func runsSpikeProxyStatus(t *testing.T, network, image, token string) int {
	t.Helper()
	// Docker may print a platform warning to stderr; only stdout is the
	// probe's status protocol.
	out, err := exec.Command("docker", "run", "--rm", "--network", network, image, "python", "-c", runsSpikeProbe, token).Output()
	if err != nil {
		t.Fatalf("probe: %v\n%s", err, out)
	}
	var status int
	if _, err := fmt.Sscanf(strings.TrimSpace(string(out)), "%d", &status); err != nil {
		t.Fatalf("invalid proxy status probe output: %v", err)
	}
	return status
}

type runsSpikeCallbacks struct {
	mu    sync.Mutex
	calls []string // "<run>/<verb>/<credential>"
}

func newRunsSpikeCallbacks() *runsSpikeCallbacks { return &runsSpikeCallbacks{} }

func (c *runsSpikeCallbacks) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) == 4 {
		c.mu.Lock()
		c.calls = append(c.calls, parts[2]+"/"+parts[3]+"/"+strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
		c.mu.Unlock()
	}
	w.WriteHeader(http.StatusOK)
}

func runsSpikeWaitCallback(t *testing.T, cb *runsSpikeCallbacks, runID, verb, cred string) {
	t.Helper()
	want := runID + "/" + verb + "/" + cred
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		cb.mu.Lock()
		for _, c := range cb.calls {
			if c == want {
				cb.mu.Unlock()
				return
			}
		}
		cb.mu.Unlock()
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("callback %s never arrived; got %v", want, cb.calls)
}

func spikeRunsCleanup(t *testing.T, podDir, generatedPath string) {
	t.Helper()
	lock, err := runs.AcquirePodLock(podDir)
	if err != nil {
		t.Errorf("cleanup cannot acquire writer authority: %v", err)
		return
	}
	defer lock.Release()
	executor, err := runs.NewDockerExecutor()
	if err != nil {
		t.Error(err)
		return
	}
	defer executor.Close()
	executor.Hold = lock.File()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	ns := runs.PodNamespace(podDir)
	out, err := exec.CommandContext(ctx, "docker", "ps", "-a", "--filter", "label="+runs.LabelPodNS+"="+ns, "--format", `{{.Label "com.docker.compose.project"}}`).Output()
	if err != nil {
		t.Error(err)
		return
	}
	seen := map[string]bool{}
	for _, project := range strings.Fields(string(out)) {
		if !strings.HasPrefix(project, "clawrun-"+ns+"-") {
			t.Errorf("foreign cleanup project %q", project)
			continue
		}
		if seen[project] {
			continue
		}
		seen[project] = true
		if err := executor.Down(ctx, project, filepath.Join(podDir, "absent-cleanup-project.yml")); err != nil {
			t.Errorf("worker cleanup: %v", err)
		}
	}
	raw, err := os.ReadFile(generatedPath)
	if os.IsNotExist(err) {
		return
	}
	if err != nil {
		t.Error(err)
		return
	}
	var doc struct {
		Name string `yaml:"name"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil || !strings.HasPrefix(doc.Name, "runs-spike-") {
		t.Errorf("invalid fixture project identity: %v", err)
		return
	}
	if err := executor.Down(ctx, doc.Name, generatedPath); err != nil {
		t.Errorf("pod cleanup: %v", err)
	}
}

// runsSpikeLaunchUnread sends a launch and closes the connection without
// reading the response, as an orchestrator whose ACK was lost would.
func runsSpikeLaunchUnread(t *testing.T, sockDir, runID, task string) {
	t.Helper()
	raw, _ := json.Marshal(map[string]any{"template": "worker", "task": task, "tools": []string{}})
	_, _, digest, _ := runs.CanonicalSpec(raw)
	body, _ := json.Marshal(map[string]any{"installation": "inst", "run_id": runID, "generation": 1, "spec_digest": digest,
		"spec": json.RawMessage(raw), "authority_expires_at": time.Now().Add(20 * time.Minute)})
	conn, err := net.Dial("unix", filepath.Join(sockDir, runs.SocketName))
	if err != nil {
		t.Fatal(err)
	}
	_, err = fmt.Fprintf(conn, "POST /v1/runs HTTP/1.1\r\nHost: claw-runs\r\nContent-Type: application/json\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s", len(body), body)
	if err != nil {
		conn.Close()
		t.Fatal(err)
	}
	defer conn.Close()
	// Prove acceptance through the separate control path while the original
	// client deliberately consumes none of its launch response.
	check, err := runs.Dial(sockDir, uint32(os.Geteuid()))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for ctx.Err() == nil {
		var view runs.StatusView
		err := check.Do(ctx, "GET", runs.RunPath(runs.Key{Installation: "inst", RunID: runID}, ""), nil, &view)
		if err == nil {
			return
		}
		if runsSpikeCode(err) != "not_found" {
			t.Fatal(err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("unread launch never became durably accepted")
}

// runsSpikeAssertIsolation inspects a live worker container.
func runsSpikeAssertIsolation(t *testing.T, view runs.StatusView, runNetwork string) {
	t.Helper()
	ids := runsSpikeContainers(t, runsSpikeRunKey(t, view))
	if len(ids) != 1 {
		t.Fatalf("expected one worker, got %v", ids)
	}
	out, err := exec.Command("docker", "inspect", ids[0]).Output()
	if err != nil {
		t.Fatal(err)
	}
	var info []struct {
		Config struct {
			User string
		}
		HostConfig struct {
			ReadonlyRootfs bool
			CapDrop        []string
			SecurityOpt    []string
			PidMode        string
			Privileged     bool
			Binds          []string
		}
		Mounts          []struct{ Type, Source string }
		NetworkSettings struct {
			Networks map[string]any
		}
	}
	if err := json.Unmarshal(out, &info); err != nil || len(info) != 1 {
		t.Fatalf("inspect: %v", err)
	}
	c := info[0]
	if c.Config.User != "65534:65534" || !c.HostConfig.ReadonlyRootfs || c.HostConfig.Privileged || c.HostConfig.PidMode == "host" {
		t.Fatalf("worker isolation: user=%q ro=%v priv=%v pid=%q", c.Config.User, c.HostConfig.ReadonlyRootfs, c.HostConfig.Privileged, c.HostConfig.PidMode)
	}
	if strings.Join(c.HostConfig.CapDrop, ",") != "ALL" || !strings.Contains(strings.Join(c.HostConfig.SecurityOpt, ","), "no-new-privileges") {
		t.Fatalf("worker caps/security: %v %v", c.HostConfig.CapDrop, c.HostConfig.SecurityOpt)
	}
	if len(c.Mounts) != 0 || len(c.HostConfig.Binds) != 0 {
		t.Fatalf("worker must have no mounts or binds: %+v %v", c.Mounts, c.HostConfig.Binds)
	}
	if len(c.NetworkSettings.Networks) != 1 || c.NetworkSettings.Networks[runNetwork] == nil {
		t.Fatalf("worker must be on exactly %s: %v", runNetwork, c.NetworkSettings.Networks)
	}
}

// TestSpikePerRunRejectsImageVolumes proves an image VOLUME fails closed at
// claw up instead of giving workers anonymous writable volumes.
func TestSpikePerRunRejectsImageVolumes(t *testing.T) {
	if err := exec.Command("docker", "info").Run(); err != nil {
		t.Skipf("docker not available: %v", err)
	}
	workDir := t.TempDir()
	spikeEnsurePulledImage(t, "docker.io/sipeed/picoclaw:v0.2.7")
	image := fmt.Sprintf("claw-runs-volume-spike:%d", time.Now().UnixNano())
	_ = os.MkdirAll(filepath.Join(workDir, "img"), 0o755)
	spikeWriteFile(t, filepath.Join(workDir, "img", "Dockerfile"), "FROM docker.io/sipeed/picoclaw:v0.2.7\nLABEL claw.type=picoclaw\nVOLUME /data\n")
	spikeBuildImage(t, filepath.Join(workDir, "img"), image, "Dockerfile")
	t.Cleanup(func() { _ = exec.Command("docker", "image", "rm", "-f", image).Run() })
	spikeWriteFile(t, filepath.Join(workDir, "AGENTS.md"), "# w")
	spikeWriteFile(t, filepath.Join(workDir, "claw-pod.yml"), fmt.Sprintf(`name: runs-volume-spike
x-claw:
  pod: runs-volume-spike
  cllama-defaults:
    proxy: [passthrough]
  models-defaults:
    primary: openai/gpt-4o
services:
  worker:
    image: %s
    x-claw:
      agent: ./AGENTS.md
      lifecycle: per-run
`, image))
	prevDetach := composeUpDetach
	composeUpDetach = true
	defer func() { composeUpDetach = prevDetach }()
	err := runComposeUp(filepath.Join(workDir, "claw-pod.yml"))
	if err == nil || !strings.Contains(err.Error(), "declares VOLUME") {
		t.Fatalf("expected VOLUME rejection, got %v", err)
	}
}
