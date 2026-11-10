//go:build spike

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/mostlydev/clawdapus/internal/infraimages"
	"github.com/mostlydev/clawdapus/internal/runs"
)

// TestSpikeTaskHomeWorkers is the live acceptance for the task-home per-run
// profile (#394, ADR-027). It runs a real claw up of a pod with a Hermes
// task-home template, cllama built from the submodule, and the run supervisor
// over a real socket. The provider is a local fake that lets a task drive the
// worker's native shell.
func TestSpikeTaskHomeWorkers(t *testing.T) {
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
			out, _ := exec.Command("docker", "compose", "-f", generatedPath, "logs", "--tail", "80").CombinedOutput()
			t.Logf("compose logs:\n%s", out)
		}
		spikeRunsCleanup(t, workDir, generatedPath)
	})

	unique := fmt.Sprintf("issue394-spike-%d", time.Now().UnixNano())
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

	pythonImage := "python:3.12-alpine"
	spikeEnsurePulledImage(t, pythonImage)
	hermesBase := "ghcr.io/mostlydev/hermes-base:" + infraimages.DefaultHermesBaseTag
	spikeEnsurePulledImage(t, hermesBase)
	workerImage := "claw-taskhome-spike-worker:" + unique
	workerCtx := filepath.Join(workDir, "worker-image")
	if err := os.MkdirAll(workerCtx, 0o755); err != nil {
		t.Fatal(err)
	}
	spikeWriteFile(t, filepath.Join(workerCtx, "Dockerfile"), "FROM "+hermesBase+"\nLABEL claw.type=hermes\n")
	spikeBuildImage(t, workerCtx, workerImage, "Dockerfile")
	t.Cleanup(func() { _ = exec.Command("docker", "image", "rm", "-f", workerImage).Run() })

	spikeWriteFile(t, filepath.Join(workDir, "AGENTS.md"), "# Task worker\n\nDo the task.")
	if err := os.MkdirAll(filepath.Join(workDir, "reference"), 0o755); err != nil {
		t.Fatal(err)
	}
	spikeWriteFile(t, filepath.Join(workDir, "reference", "handbook.md"), "REFERENCE-CONTENT\n")
	providerImage := "claw-taskhome-spike-provider:" + unique
	providerCtx := filepath.Join(workDir, "provider-image")
	if err := os.MkdirAll(providerCtx, 0o755); err != nil {
		t.Fatal(err)
	}
	spikeWriteFile(t, filepath.Join(providerCtx, "fake_provider.py"), taskHomeSpikeProvider)
	spikeWriteFile(t, filepath.Join(providerCtx, "Dockerfile"), "FROM "+pythonImage+"\nCOPY fake_provider.py /app/fake_provider.py\nCMD [\"python\",\"-u\",\"/app/fake_provider.py\"]\n")
	spikeBuildImage(t, providerCtx, providerImage, "Dockerfile")
	t.Cleanup(func() { _ = exec.Command("docker", "image", "rm", "-f", providerImage).Run() })

	spikeWriteFile(t, podFile, fmt.Sprintf(`name: %[1]s

x-claw:
  pod: %[1]s
  # Test fixture: fake provider endpoints cannot meet the conservative default.
  data-policy-defaults:
    regions: [global]
    training: allowed
    retention: provider-default
    local-retention: allowed
  cllama-defaults:
    proxy: [passthrough]
    env:
      OPENAI_API_KEY: sk-local-fake
      OPENAI_BASE_URL: http://fake-provider:8080/v1
  models-defaults:
    primary: openai/gpt-4o

services:
  worker:
    image: %[2]s
    x-claw:
      agent: ./AGENTS.md
      lifecycle: per-run
      run:
        max-runtime: 5m
        max-authority: 30m
        home: {}
        content:
          - source: ./reference
            target: /reference

  fake-provider:
    image: %[3]s
    expose:
      - "8080"
    networks:
      - claw-internal
`, "runs-spike-"+unique, workerImage, providerImage))

	t.Setenv("CLLAMA_UI_PORT", spikeFreePort(t))
	t.Setenv("CLAW_DASHBOARD_BIND", "127.0.0.1")
	t.Setenv("CLAWDASH_ENABLED", "false")
	prevDetach := composeUpDetach
	composeUpDetach = true
	defer func() { composeUpDetach = prevDetach }()
	if err := runComposeUp(podFile); err != nil {
		t.Fatalf("runComposeUp: %v", err)
	}
	spikeWaitHealthy(t, spikeComposeContainerID(t, generatedPath, "cllama"), 60*time.Second)

	tpl, _, err := runs.LoadTemplate(workDir, "worker")
	if err != nil {
		t.Fatalf("template: %v", err)
	}
	if tpl.Worker.Home == nil || tpl.Worker.Gate == nil || len(tpl.Content) != 1 || tpl.ClawType != "hermes" {
		t.Fatalf("template is not a task-home template: %+v", tpl)
	}
	runsSpikePodNS = runs.PodNamespace(workDir)

	sockDir, err := os.MkdirTemp("/tmp", "clawruns-spike-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(sockDir)
	_ = os.Chmod(sockDir, 0o700)
	// Callback receiver standing in for the orchestrator.
	cb := newRunsSpikeCallbacks()
	cbServer := httptest.NewServer(cb)
	defer cbServer.Close()
	sup = startRunsSpikeSupervisor(t, workDir, sockDir, cbServer.URL)
	client, err := runs.Dial(sockDir, uint32(os.Geteuid()))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	// Every run hands over a credential, so its end callback is delivered.
	launch := func(runID, home, task string) error {
		raw, _ := json.Marshal(map[string]any{"template": "worker", "task": task, "tools": []string{}, "home": home})
		_, _, digest, _ := runs.CanonicalSpec(raw)
		return client.Do(ctx, "POST", "/v1/runs", map[string]any{
			"installation": "inst", "run_id": runID, "generation": 1, "spec_digest": digest,
			"spec": json.RawMessage(raw), "authority_expires_at": time.Now().Add(20 * time.Minute),
			"credential": "cred-" + runID,
		}, nil)
	}
	// endBody returns the end callback a run delivered, decoded strictly
	// enough to see exactly which fields it carried.
	endBody := func(runID string) map[string]any {
		t.Helper()
		runsSpikeWaitCallback(t, cb, runID, "end", "cred-"+runID)
		cb.mu.Lock()
		raw := cb.bodies[runID+"/end"]
		cb.mu.Unlock()
		var body map[string]any
		if err := json.Unmarshal([]byte(raw), &body); err != nil {
			t.Fatalf("end callback of %s is not JSON: %q", runID, raw)
		}
		return body
	}
	finish := func(runID string) (runs.StatusView, string) {
		t.Helper()
		view := runsSpikeWait(t, client, runID, "stopped")
		if view.State != runs.StateStopped || view.Failure != "" || !view.Revoked || view.Result == nil {
			t.Fatalf("run %s: %+v", runID, view)
		}
		var body []byte
		if err := client.Do(ctx, "GET", runs.RunPath(runs.Key{Installation: "inst", RunID: runID}, "/result"), nil, &body); err != nil {
			t.Fatalf("run %s result: %v", runID, err)
		}
		return view, string(body)
	}
	run := func(runID, home, task string) (runs.StatusView, string) {
		t.Helper()
		if err := launch(runID, home, task); err != nil {
			t.Fatalf("launch %s: %v", runID, err)
		}
		return finish(runID)
	}
	homeDir := func(home string) string {
		return filepath.Join(workDir, runs.HomesDirName, runs.HomeHash("inst", home), "home")
	}

	// 1. First turn: a new session, stored, reported with its ID.
	started := time.Now()
	a1, body := run("a1", "task-a", "first turn")
	t.Logf("first run launch-to-stopped: %s", time.Since(started))
	if body != "MARK-ONE" || a1.Result.Outcome != runs.OutcomeAnswer || a1.Result.Session == "" {
		t.Fatalf("case 1: %q %+v", body, a1.Result)
	}

	// storedSession reads the locator the wrapper wrote in the home. On a
	// Linux host that file belongs to the worker user and may be unreadable.
	storedSession := func(home string) (string, bool) {
		raw, err := os.ReadFile(filepath.Join(homeDir(home), ".claw-run", "session"))
		if errors.Is(err, fs.ErrPermission) && runtime.GOOS == "linux" {
			t.Logf("session locator of %s belongs to the worker user and is not readable from this host: %v", home, err)
			return "", false
		}
		if err != nil {
			t.Fatalf("session locator of %s: %v", home, err)
		}
		return strings.TrimSpace(string(raw)), true
	}
	// The end callback of an answer carries the outcome and the same session
	// the status reports, with the answer as its summary (#396).
	if got := endBody("a1"); got["result_outcome"] != runs.OutcomeAnswer || got["session"] != a1.Result.Session ||
		got["summary"] != "MARK-ONE" || got["outcome"] != "succeeded" || len(got) != 9 {
		t.Fatalf("case 1: end callback: %v", got)
	}
	if stored, ok := storedSession("task-a"); ok && stored != a1.Result.Session {
		t.Fatalf("case 1: callback session %q is not the stored locator %q", a1.Result.Session, stored)
	}

	// 2. A second, separate container resumes that exact session.
	a2, body := run("a2", "task-a", "second turn")
	if body != "HISTORY-SEEN" || a2.Result.Session != a1.Result.Session {
		t.Fatalf("case 2 (resume): %q session %q vs %q", body, a2.Result.Session, a1.Result.Session)
	}

	// 3. Another home starts cold: nothing of task-a's history reaches it.
	b1, body := run("b1", "task-b", "first turn")
	if body != "MARK-ONE" || b1.Result.Session == a1.Result.Session {
		t.Fatalf("case 3 (separate home): %q %+v", body, b1.Result)
	}

	// 4. Hostile A against live B. B holds a secret and serves it on a
	// listener; B reaches its own listener (positive control). A, running at
	// the same time, cannot reach it, cannot see B's files, can read but not
	// write reference content, and can still reach the model.
	bTask := "RUN: python3 -c \"import http.server,threading,urllib.request,time,os\n" +
		"os.chdir('/home/task/workspace'); open('secret','w').write('B-SECRET')\n" +
		"s=http.server.ThreadingHTTPServer(('0.0.0.0',9000),http.server.SimpleHTTPRequestHandler)\n" +
		"threading.Thread(target=s.serve_forever,daemon=True).start()\n" +
		"print('SELF:'+urllib.request.urlopen('http://127.0.0.1:9000/secret',timeout=5).read().decode())\n" +
		"time.sleep(40)\" 2>/dev/null"
	if err := launch("b2", "task-b", bTask); err != nil {
		t.Fatal(err)
	}
	// A second run for a reserved home is refused, and the home cannot be deleted.
	if err := launch("b-dup", "task-b", "first turn"); runsSpikeCode(err) != "home_busy" {
		t.Fatalf("case 4: concurrent run for a reserved home: %v", err)
	}
	if err := client.Do(ctx, "DELETE", runs.HomePath("inst", "task-b"), nil, nil); runsSpikeCode(err) != "home_busy" {
		t.Fatalf("case 4: delete of a reserved home: %v", err)
	}
	bView := runsSpikeWait(t, client, "b2", "running")
	bKey := runsSpikeRunKey(t, bView)
	bToken := runsSpikeRunToken(t, workDir, runs.Key{Installation: "inst", RunID: "b2"})
	var bIPs []string
	bNetwork := ""
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(filepath.Join(homeDir("task-b"), "workspace", "secret")); err == nil {
			out, _ := exec.Command("docker", "ps", "-q", "--filter", "label="+runs.LabelRunKey+"="+bKey).Output()
			if id := strings.TrimSpace(string(out)); id != "" {
				ips, _ := exec.Command("docker", "inspect", id, "--format", "{{range .NetworkSettings.Networks}}{{.IPAddress}} {{end}}").Output()
				bIPs = strings.Fields(string(ips))
				nets, _ := exec.Command("docker", "inspect", id, "--format", "{{range $k, $v := .NetworkSettings.Networks}}{{$k}} {{end}}").Output()
				got := strings.Fields(string(nets))
				if len(got) != 1 || strings.Contains(got[0], "claw-runs") {
					t.Fatalf("case 4: worker must be on its private network only, got %v", got)
				}
				bNetwork = got[0]
				mounts, _ := exec.Command("docker", "inspect", id, "--format", "{{range .Mounts}}{{.Source}}={{.Destination}}:{{.RW}} {{end}}").Output()
				if !strings.Contains(string(mounts), "="+"/home/task:true") || !strings.Contains(string(mounts), "/reference:false") || len(strings.Fields(string(mounts))) != 2 {
					t.Fatalf("case 4: unexpected mounts: %s", mounts)
				}
				if !strings.Contains(string(mounts), runs.HomeHash("inst", "task-b")+"/home=/home/task") {
					t.Fatalf("case 4: worker does not mount its own home: %s", mounts)
				}
				break
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	if len(bIPs) != 1 {
		view := runsSpikeWait(t, client, "b2", "stopped")
		var body []byte
		_ = client.Do(ctx, "GET", runs.RunPath(runs.Key{Installation: "inst", RunID: "b2"}, "/result"), nil, &body)
		entries, _ := exec.Command("ls", "-laR", homeDir("task-b")+"/workspace").CombinedOutput()
		t.Fatalf("case 4: B worker address not found: %v\nview=%+v\nresult=%q\n%s", bIPs, view, body, entries)
	}
	time.Sleep(3 * time.Second) // let B's listener bind
	peerProbe := "import socket\ns=socket.socket();s.settimeout(5)\ntry:\n s.connect(('" + bIPs[0] + "',9000));print('PEER-OPEN')\nexcept Exception as e:\n print('PEER-CLOSED')"
	// Negative control: the same probe from a container placed on B's private
	// network reaches the listener. So A's PEER-CLOSED below is the network
	// boundary, not a listener that never came up or a probe that cannot work.
	control, err := exec.Command("docker", "run", "--rm", "--network", bNetwork, "--entrypoint", "python3", workerImage, "-c", peerProbe).CombinedOutput()
	if err != nil || !strings.Contains(string(control), "PEER-OPEN") {
		t.Fatalf("case 4: negative control: a peer on B's network must reach B's listener: %s %v", control, err)
	}
	bHash := runs.HomeHash("inst", "task-b")
	aTask := "RUN: python3 -c \"" + peerProbe + "\"; " +
		"cat /reference/handbook.md; (touch /reference/x 2>/dev/null && echo REF-WRITABLE || echo REF-READONLY); " +
		"(find / -xdev -name secret -not -path '/proc/*' 2>/dev/null | grep -q . && echo FOUND-SECRET || echo NO-SECRET); " +
		"ls /home; ls -la /home/task/..; (ls /var/run/docker.sock 2>/dev/null && echo SOCKET || echo NO-SOCKET); " +
		// Parent, symlink and /proc/<pid>/root routes all stay inside A's own container.
		"ln -sfn /home/task/../.. /home/task/workspace/up; ls /home/task/workspace/up/home; " +
		"(cat /proc/1/root/home/task/workspace/secret /home/task/../*/workspace/secret /home/task/workspace/up/home/*/workspace/secret 2>/dev/null; true); " +
		"(grep -q " + bHash + " /proc/self/mountinfo && echo SEES-B-MOUNT || echo NO-B-MOUNT); " +
		"(grep -q " + runs.HomeHash("inst", "task-a") + "/home /proc/self/mountinfo && echo OWN-MOUNT || echo NO-OWN-MOUNT); " +
		"cat /proc/1/environ 2>/dev/null | tr '\\0' '\\n' | grep -c B-SECRET; id -u"
	_, aBody := run("a3", "task-a", aTask)
	for _, want := range []string{"PEER-CLOSED", "REFERENCE-CONTENT", "REF-READONLY", "NO-SECRET", "NO-SOCKET", "NO-B-MOUNT", "65534"} {
		if !strings.Contains(aBody, want) {
			t.Errorf("case 4: A's probe lacks %s:\n%s", want, aBody)
		}
	}
	for _, bad := range []string{"B-SECRET", "PEER-OPEN", "REF-WRITABLE", "FOUND-SECRET", "SEES-B-MOUNT", "task-b", bHash} {
		if strings.Contains(aBody, bad) {
			t.Errorf("case 4: A's probe shows %s:\n%s", bad, aBody)
		}
	}
	_, bBody := finish("b2")
	if !strings.Contains(bBody, "SELF:B-SECRET") {
		t.Fatalf("case 4: positive control failed, B could not reach its own listener:\n%s", bBody)
	}
	// The run token never reached the persistent home.
	secret := bToken[strings.LastIndex(bToken, ":")+1:]
	// grep exits 1 for "no match" and 2 for a read error. Only the first is
	// a pass: an unreadable directory must not look like an absent token.
	grepOut, grepErr := exec.Command("grep", "-rl", secret, filepath.Join(workDir, runs.HomesDirName)).CombinedOutput()
	var grepExit *exec.ExitError
	if grepErr == nil || !errors.As(grepErr, &grepExit) || grepExit.ExitCode() != 1 {
		t.Fatalf("case 4: token scan of the homes did not come back clean (%v): %s", grepErr, grepOut)
	}
	// A's own persistence is a positive control: its files survive the run.
	if _, err := os.Stat(filepath.Join(homeDir("task-a"), "hermes", "state.db")); err != nil {
		t.Fatalf("case 4: A's session store not in its home: %v", err)
	}

	// 5. A question is an outcome, and the answer continues the same session.
	q, body := run("a4", "task-a", "RUN: printf 'Which branch?' > /home/task/.claw-run/question.md")
	if q.Result.Outcome != runs.OutcomeQuestion || body != "Which branch?" {
		t.Fatalf("case 5 (question): %q %+v", body, q.Result)
	}
	if got := endBody("a4"); got["result_outcome"] != runs.OutcomeQuestion || got["session"] != q.Result.Session ||
		got["summary"] != "Which branch?" || got["outcome"] != "succeeded" || len(got) != 9 {
		t.Fatalf("case 5: end callback of a question: %v", got)
	}
	if stored, ok := storedSession("task-a"); ok && stored != q.Result.Session {
		t.Fatalf("case 5: callback session %q is not the stored locator %q", q.Result.Session, stored)
	}
	a5, body := run("a5", "task-a", "the answer is main")
	if a5.Result.Outcome != runs.OutcomeAnswer || body != "HISTORY-SEEN" || a5.Result.Session != a1.Result.Session {
		t.Fatalf("case 5 (answer): %q %+v", body, a5.Result)
	}

	// 6. Kill after a durable effect but before its result. The follow-up
	// inspects that effect rather than blindly repeating it. Hermes may merge
	// the unanswered request and new brief into one user message.
	if err := launch("a6", "task-a", "RUN: printf EFFECT-ONCE >> /home/task/workspace/effects; sleep 300"); err != nil {
		t.Fatal(err)
	}
	a6 := runsSpikeWait(t, client, "a6", "running")
	deadline = time.Now().Add(40 * time.Second)
	for time.Now().Before(deadline) {
		if raw, err := os.ReadFile(filepath.Join(homeDir("task-a"), "workspace", "effects")); err == nil && string(raw) == "EFFECT-ONCE" {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if raw, err := os.ReadFile(filepath.Join(homeDir("task-a"), "workspace", "effects")); err != nil || string(raw) != "EFFECT-ONCE" {
		t.Fatalf("case6 effect not observed before kill: %q %v", raw, err)
	}
	out, _ := exec.Command("docker", "ps", "-q", "--filter", "label="+runs.LabelRunKey+"="+runsSpikeRunKey(t, a6)).Output()
	if err := exec.Command("docker", "kill", strings.TrimSpace(string(out))).Run(); err != nil {
		t.Fatalf("case 6: kill: %v", err)
	}
	if view := runsSpikeWait(t, client, "a6", "failed"); view.State != runs.StateFailed || view.Result != nil {
		t.Fatalf("case 6: killed run: %+v", view)
	}
	// A run with no result delivers the unchanged seven-field end body.
	if got := endBody("a6"); got["outcome"] != "failed" || len(got) != 7 {
		t.Fatalf("case 6: end callback of a killed run: %v", got)
	}
	a7, body := run("a7", "task-a", "RUN: cat /home/task/workspace/effects")
	if a7.Result.Session != a1.Result.Session || a7.Result.Outcome != runs.OutcomeAnswer || strings.Count(body, "EFFECT-ONCE") != 1 {
		t.Fatalf("case 6 (resume inspects prior effect): %q %+v", body, a7.Result)
	}
	if raw, err := os.ReadFile(filepath.Join(homeDir("task-a"), "workspace", "effects")); err != nil || string(raw) != "EFFECT-ONCE" {
		t.Fatalf("case6 effect repeated: %q %v", raw, err)
	}

	// 7. Reset: deleting the home removes workspace and history; B is intact.
	var status runs.HomeStatus
	if err := client.Do(ctx, "GET", runs.HomePath("inst", "task-a"), nil, &status); err != nil || !status.Exists || status.ReservedBy != "" || status.LastRunID != "a7" {
		t.Fatalf("case 7: status: %+v %v", status, err)
	}
	if err := client.Do(ctx, "DELETE", runs.HomePath("inst", "task-a"), nil, nil); err != nil {
		t.Fatalf("case 7: delete: %v", err)
	}
	if _, err := os.Stat(filepath.Dir(homeDir("task-a"))); !os.IsNotExist(err) {
		t.Fatalf("case 7: home still present: %v", err)
	}
	a8, body := run("a8", "task-a", "first turn again")
	if body != "MARK-ONE" || a8.Result.Session == a1.Result.Session {
		t.Fatalf("case 7 (reset): %q %+v", body, a8.Result)
	}
	if raw, err := os.ReadFile(filepath.Join(homeDir("task-b"), "workspace", "secret")); err != nil || !strings.Contains(string(raw), "B-SECRET") {
		t.Fatalf("case 7: reset of A disturbed B: %q %v", raw, err)
	}

	// 8. A sparse file crosses the logical-byte watchdog threshold without
	// consuming a GiB of host storage. The independent meter must fence it.
	if err := launch("full", "task-full", `RUN: python3 -c "f=open('/home/task/workspace/big','wb');f.truncate(1073741825)"; sleep 300`); err != nil {
		t.Fatal(err)
	}
	full := runsSpikeWait(t, client, "full", "failed")
	if full.Failure != runs.ReasonHomeFull || !full.Revoked || full.Result != nil {
		t.Fatalf("case8 watchdog: %+v", full)
	}

	// No gate or worker is left behind.
	if out, _ := exec.Command("docker", "ps", "-aq", "--filter", "label="+runs.LabelPodNS+"="+runsSpikePodNS).Output(); strings.TrimSpace(string(out)) != "" {
		t.Fatalf("containers left behind: %s", out)
	}
}

// taskHomeSpikeProvider is a fake chat-completions provider. It answers
// MARK-ONE until that marker is in the assistant history, then HISTORY-SEEN.
// A user message starting with "RUN: " makes it call the terminal tool with
// the rest of the message, and return the tool's output as the answer.
const taskHomeSpikeProvider = `import json
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

class Handler(BaseHTTPRequestHandler):
    def log_message(self, *a):
        pass
    def do_GET(self):
        self.send_response(200 if self.path == "/health" else 404)
        self.end_headers()
    def do_POST(self):
        body = json.loads(self.rfile.read(int(self.headers.get("Content-Length", "0"))))
        messages = body.get("messages", [])
        last = messages[-1] if messages else {}
        users = [m for m in messages if m.get("role") == "user"]
        task = str(users[-1].get("content", "")) if users else ""
        # Adjacent unanswered and new user inputs may be merged by Hermes.
        # This deterministic fixture obeys the latest explicit control command.
        if "RUN: " in task:
            task = task[task.rfind("RUN: "):]
        seen = any("MARK-ONE" in str(m.get("content", "")) for m in messages if m.get("role") == "assistant")
        msg, finish = {"role": "assistant", "content": "HISTORY-SEEN" if seen else "MARK-ONE"}, "stop"
        if "RUN: " in task:
            if last.get("role") == "tool":
                msg["content"] = str(last.get("content", "")).strip() or "EMPTY-TOOL-OUTPUT"
            else:
                names = [t["function"]["name"] for t in body.get("tools", []) if t.get("function", {}).get("name") == "terminal"]
                if names:
                    msg = {"role": "assistant", "content": None, "tool_calls": [{"id": "call-spike", "type": "function", "function": {
                        "name": "terminal", "arguments": json.dumps({"command": task.split("RUN: ", 1)[1].split("\n\n")[0], "timeout": 120})}}]}
                    finish = "tool_calls"
                else:
                    msg["content"] = "NO-TERMINAL-TOOL"
        data = {"id": "chatcmpl-taskhome", "object": "chat.completion", "created": 1, "model": "gpt-4o",
                "choices": [{"index": 0, "message": msg, "finish_reason": finish}],
                "usage": {"prompt_tokens": 10, "completion_tokens": 2, "total_tokens": 12}}
        if body.get("stream"):
            delta = dict(msg)
            if delta.get("tool_calls"):
                delta["tool_calls"] = [dict(delta["tool_calls"][0], index=0)]
            chunks = [dict(data, object="chat.completion.chunk", choices=[{"index": 0, "delta": delta, "finish_reason": None}]),
                      dict(data, object="chat.completion.chunk", choices=[{"index": 0, "delta": {}, "finish_reason": finish}])]
            raw = ("".join("data: " + json.dumps(c) + "\n\n" for c in chunks) + "data: [DONE]\n\n").encode()
            ctype = "text/event-stream"
        else:
            raw, ctype = json.dumps(data).encode(), "application/json"
        print("REQUEST stream=%s seen=%s roles=%s task=%r" % (body.get("stream"), seen, "".join(str(m.get("role", "?"))[0] for m in messages), task[:60]), flush=True)
        self.send_response(200)
        self.send_header("Content-Type", ctype)
        self.send_header("Content-Length", str(len(raw)))
        self.end_headers()
        self.wfile.write(raw)

ThreadingHTTPServer(("0.0.0.0", 8080), Handler).serve_forever()
`
