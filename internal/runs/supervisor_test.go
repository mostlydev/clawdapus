package runs

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mostlydev/clawdapus/internal/cllama"
	"github.com/mostlydev/clawdapus/internal/driver"
	"gopkg.in/yaml.v3"
)

type fakeExec struct {
	mu         sync.Mutex
	containers map[string][]Container
	ups        int
	downs      int
	lastEnv    map[string]string
	lastFile   []byte
	stdout     []byte
	upErr      error
	upNoop     bool
}

func newFakeExec() *fakeExec { return &fakeExec{containers: map[string][]Container{}} }

func (f *fakeExec) Up(_ context.Context, project, file string, env map[string]string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ups++
	f.lastEnv = env
	raw, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	f.lastFile = raw
	if f.upErr != nil {
		return f.upErr
	}
	if f.upNoop {
		return nil
	}
	var doc struct {
		Services map[string]struct {
			Image  string            `yaml:"image"`
			Labels map[string]string `yaml:"labels"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return err
	}
	w := doc.Services[workerService]
	key := w.Labels[LabelRunKey]
	f.containers[key] = append(f.containers[key], Container{ID: project + "-worker", Running: true, ImageID: w.Image, Labels: w.Labels})
	return nil
}

func (f *fakeExec) Down(_ context.Context, project, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.downs++
	for key, cs := range f.containers {
		if len(cs) > 0 && strings.HasPrefix(cs[0].ID, project) {
			delete(f.containers, key)
		}
	}
	return nil
}

func (f *fakeExec) Containers(_ context.Context, keyHash string) ([]Container, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Container(nil), f.containers[keyHash]...), nil
}

func (f *fakeExec) Stdout(_ context.Context, _ string, limit int) ([]byte, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.stdout) > limit {
		return f.stdout[:limit], true, nil
	}
	return f.stdout, false, nil
}

func (f *fakeExec) exit(keyHash string, code int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.containers[keyHash] {
		f.containers[keyHash][i].Running = false
		f.containers[keyHash][i].Exited = true
		f.containers[keyHash][i].ExitCode = code
	}
}

func (f *fakeExec) count(keyHash string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.containers[keyHash])
}

type fakeRevoker struct {
	mu    sync.Mutex
	calls []string
	err   error
}

func (r *fakeRevoker) Revoke(_ context.Context, agentID string, _ uint64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil {
		return r.err
	}
	if !strings.HasPrefix(agentID, "_claw-runs-probe-") {
		r.calls = append(r.calls, agentID)
	}
	return nil
}

type fakeNotifier struct {
	order                 []string
	mu                    sync.Mutex
	started, beats, ended int
	lastOutcome, lastCred string
	err                   error
}

func (n *fakeNotifier) Started(_ context.Context, _ *Record, cred string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.lastCred = cred
	if n.err != nil {
		return n.err
	}
	n.order = append(n.order, "start")
	n.started++
	return nil
}
func (n *fakeNotifier) Heartbeat(_ context.Context, _ *Record, cred string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.lastCred = cred
	if n.err != nil {
		return n.err
	}
	n.beats++
	return nil
}
func (n *fakeNotifier) Ended(_ context.Context, _ *Record, cred string, outcome, _ string, _ []byte) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.err != nil {
		return n.err
	}
	n.order = append(n.order, "end")
	n.ended++
	n.lastOutcome = outcome
	return nil
}

type harness struct {
	t      *testing.T
	podDir string
	exec   *fakeExec
	rev    *fakeRevoker
	notify *fakeNotifier
	sup    *Supervisor
	now    time.Time
}

func newHarness(t *testing.T, withNotifier bool) *harness {
	t.Helper()
	h := &harness{t: t, podDir: t.TempDir(), exec: newFakeExec(), rev: &fakeRevoker{}, now: time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)}
	tpl := &Template{
		Name: "worker", Pod: "pod", ClawType: "picoclaw", ImageRef: "worker:latest", ImageID: "sha256:img",
		RunNetwork: "claw-runs-test", CllamaURL: "http://cllama:8080/v1",
		ToolsGrant: []string{"api.get", "api.put"},
		Limits:     Limits{MaxRuntimeSeconds: 600, MaxAuthoritySeconds: 3600, MaxTaskBytes: 1024, MaxResultBytes: 16, Memory: "1g", Pids: 64},
		Worker: driver.RunWorkerSpec{
			Entrypoint:  []string{"/bin/sh", "-c", `printf '%s' "$CLAW_RUN_CONFIG" > /tmp/c && exec run "$1"`, "claw-run"},
			Command:     []string{"${CLAW_RUN_TASK}"},
			Environment: map[string]string{"HOME": "/tmp/home", "ODD": "a$b"},
			Inputs: map[string]string{
				"CLAW_RUN_TASK":   driver.RunPlaceholderTask,
				"CLAW_RUN_CONFIG": `{"key":"` + driver.RunPlaceholderCllamaToken + `"}`,
			},
			Tmpfs:  []string{"/tmp:mode=1777"},
			Result: driver.RunResultContract{Source: "stdout", Marker: "\n>> "},
		},
	}
	ctx := &ContextTemplate{Input: cllama.AgentContextInput{
		AgentID: "worker", AgentsMD: "# worker", ClawdapusMD: "# clawdapus",
		Metadata: map[string]interface{}{"service": "worker", "pod": "pod", "token": ""},
		Tools: []cllama.ToolManifestEntry{
			{Name: "api.get", Execution: cllama.ToolExecution{Transport: "http", Service: "api", BaseURL: "http://api:80", Path: "/get"}},
			{Name: "api.put", Execution: cllama.ToolExecution{Transport: "http", Service: "api", BaseURL: "http://api:80", Path: "/put"}},
		},
		ServiceAuth: []cllama.ServiceAuthEntry{{Service: "api", AuthType: "bearer", Token: "svc-secret"}, {Service: "other", AuthType: "bearer", Token: "x"}},
	}}
	if err := WriteTemplates(h.podDir, []*Template{tpl}, map[string]*ContextTemplate{"worker": ctx}); err != nil {
		t.Fatal(err)
	}
	cfg := Config{PodDir: h.podDir, Executor: h.exec, Revoker: h.rev, Now: func() time.Time { return h.now }, HeartbeatInterval: time.Minute}
	if withNotifier {
		h.notify = &fakeNotifier{}
		cfg.Notifier = h.notify
	}
	sup, err := NewSupervisor(cfg)
	if err != nil {
		t.Fatal(err)
	}
	h.sup = sup
	return h
}

func specJSON(t *testing.T, task string, tools ...string) (json.RawMessage, string) {
	t.Helper()
	if tools == nil {
		tools = []string{}
	}
	raw, _ := json.Marshal(map[string]any{"template": "worker", "task": task, "tools": tools})
	_, _, digest, err := CanonicalSpec(raw)
	if err != nil {
		t.Fatal(err)
	}
	return raw, digest
}

func (h *harness) launch(runID string, gen uint64, task string, tools ...string) (*Record, error) {
	raw, digest := specJSON(h.t, task, tools...)
	rec, _, err := h.sup.Launch(context.Background(), LaunchRequest{
		Key: Key{"inst", runID}, Generation: gen, SpecDigest: digest, Spec: raw, AuthorityExpiresAt: h.now.Add(30 * time.Minute),
	})
	return rec, err
}

func (h *harness) advance(runID string) *Record {
	h.sup.Advance(context.Background(), Key{"inst", runID})
	rec, err := h.sup.Get(Key{"inst", runID})
	if err != nil {
		h.t.Fatal(err)
	}
	return rec
}

func apiCode(err error) string {
	var e *APIError
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

func TestRunLifecycleNaturalExit(t *testing.T) {
	h := newHarness(t, false)
	rec, err := h.launch("r1", 1, "do it", "api.get")
	if err != nil {
		t.Fatal(err)
	}
	if rec.State != StateAccepted || rec.RunAgentID == "" || !strings.HasPrefix(rec.RunAgentID, "worker-run-") {
		t.Fatalf("unexpected accepted record %+v", rec)
	}
	rec = h.advance("r1")
	if rec.State != StateRunning || h.exec.count(rec.RunKey) != 1 {
		t.Fatalf("expected running with one container, got %s", rec.State)
	}

	// The project file carries neither the task nor the token; both arrive by
	// interpolation only. Static "$" is escaped so Compose never expands it.
	file := string(h.exec.lastFile)
	if strings.Contains(file, "do it") || strings.Contains(file, rec.RunAgentID+":") {
		t.Fatalf("project file leaks task or token:\n%s", file)
	}
	if !strings.Contains(file, `"$$CLAW_RUN_CONFIG"`) || !strings.Contains(file, "a$$b") || !strings.Contains(file, "${CLAW_RUN_TASK:-}") {
		t.Fatalf("project file escaping is wrong:\n%s", file)
	}
	if h.exec.lastEnv["CLAW_RUN_TASK"] != "do it" || !strings.Contains(h.exec.lastEnv["CLAW_RUN_CONFIG"], rec.RunAgentID+":") {
		t.Fatalf("interpolation env missing task/token: %v", h.exec.lastEnv)
	}

	// Per-run cllama context: fresh identity, filtered tools and auth.
	ctxDir := filepath.Join(h.podDir, ".claw-runtime", "context", rec.RunAgentID)
	var meta map[string]any
	raw, _ := os.ReadFile(filepath.Join(ctxDir, "metadata.json"))
	_ = json.Unmarshal(raw, &meta)
	if !strings.HasPrefix(meta["token"].(string), rec.RunAgentID+":") || meta["run_id"] != "r1" {
		t.Fatalf("metadata = %v", meta)
	}
	var tools cllama.ToolManifest
	raw, _ = os.ReadFile(filepath.Join(ctxDir, "tools.json"))
	_ = json.Unmarshal(raw, &tools)
	if len(tools.Tools) != 1 || tools.Tools[0].Name != "api.get" {
		t.Fatalf("tools not filtered to allowlist: %+v", tools.Tools)
	}
	if _, err := os.Stat(filepath.Join(ctxDir, "service-auth", "other.json")); err == nil {
		t.Fatalf("service auth for unused services must not be copied")
	}

	h.exec.stdout = []byte("BANNER\r\n\n>> the answer with >> inside and more bytes\n")
	h.exec.exit(rec.RunKey, 0)
	rec = h.advance("r1")
	if rec.State != StateStopped || !rec.Revoked || rec.Exit == nil || rec.Failure != "" {
		t.Fatalf("expected stopped after natural exit, got %+v", rec)
	}
	if h.exec.count(rec.RunKey) != 0 || len(h.rev.calls) != 1 || h.rev.calls[0] != rec.RunAgentID {
		t.Fatalf("expected revoke then teardown; containers=%d revokes=%v", h.exec.count(rec.RunKey), h.rev.calls)
	}
	if _, err := os.Stat(ctxDir); !os.IsNotExist(err) {
		t.Fatalf("per-run context must be removed at terminal state")
	}
	if _, ok, _ := h.sup.ledger.Secret(rec.Key, "cllama"); ok {
		t.Fatalf("per-run cllama secret must be removed at terminal state")
	}
	result, rec, err := h.sup.Result(rec.Key)
	if err != nil || string(result) != "the answer with " || !rec.Result.Truncated {
		t.Fatalf("result = %q truncated=%v err=%v", result, rec.Result, err)
	}
}

func TestRunNonZeroExitFails(t *testing.T) {
	h := newHarness(t, false)
	rec, _ := h.launch("r1", 1, "x")
	h.advance("r1")
	h.exec.exit(rec.RunKey, 1)
	rec = h.advance("r1")
	if rec.State != StateFailed || rec.Failure != ReasonExitNonZero || rec.Exit.Code != 1 {
		t.Fatalf("got %+v", rec)
	}
}

// Acceptance 2 (unit): cancel mid-run reaches revoked then stopped.
func TestRunCancelMidRun(t *testing.T) {
	h := newHarness(t, false)
	rec, _ := h.launch("r1", 1, "x", "api.get")
	h.advance("r1")
	rec, err := h.sup.Cancel(context.Background(), rec.Key, 1)
	if err != nil || rec.State != StateRevoking {
		t.Fatalf("cancel: %v %+v", err, rec)
	}
	// While cllama does not acknowledge, the worker is still physically
	// removed, but the run never claims revoked or terminal.
	h.rev.err = errors.New("503")
	rec = h.advance("r1")
	if rec.State != StateRevoking || rec.Revoked || rec.State.Terminal() {
		t.Fatalf("revoked claimed without cllama ack: %+v", rec)
	}
	if h.exec.count(rec.RunKey) != 0 {
		t.Fatalf("worker must be removed even while revoke is unacknowledged")
	}
	var tools cllama.ToolManifest
	raw, _ := os.ReadFile(filepath.Join(h.podDir, ".claw-runtime", "context", rec.RunAgentID, "tools.json"))
	_ = json.Unmarshal(raw, &tools)
	if len(tools.Tools) != 0 {
		t.Fatalf("tools must be emptied before revocation is attempted")
	}
	h.rev.err = nil
	rec = h.advance("r1")
	if rec.State != StateStopped || !rec.Cancelled || !rec.Revoked || rec.Timestamps.Revoked == nil {
		t.Fatalf("expected stopped after revoke, got %+v", rec)
	}
	if rec.Callbacks.End != CallbackSkipped {
		t.Fatalf("cancelled runs send no end callback, got %q", rec.Callbacks.End)
	}
}

// Acceptance 3: cancel before launch leaves no container, and a later launch
// is refused.
func TestRunCancelBeforeLaunch(t *testing.T) {
	h := newHarness(t, false)
	rec, err := h.sup.Cancel(context.Background(), Key{"inst", "r1"}, 1)
	if err != nil || rec.State != StateStopped || !rec.Cancelled {
		t.Fatalf("tombstone: %v %+v", err, rec)
	}
	if _, err := h.launch("r1", 1, "x"); apiCode(err) != "cancelled" {
		t.Fatalf("delayed launch must be refused, got %v", err)
	}
	if _, err := h.launch("r1", 5, "x"); apiCode(err) != "cancelled" {
		t.Fatalf("higher generation must not revive a cancelled run, got %v", err)
	}
	if h.exec.ups != 0 {
		t.Fatalf("no container may be created")
	}
}

// Acceptance 4: lost ack and duplicate launch give exactly one container.
func TestRunDuplicateLaunchIsIdempotent(t *testing.T) {
	h := newHarness(t, false)
	first, _ := h.launch("r1", 1, "x")
	second, err := h.launch("r1", 1, "x")
	if err != nil || second.RunAgentID != first.RunAgentID {
		t.Fatalf("retry must return the same run: %v", err)
	}
	h.advance("r1")
	if _, err := h.launch("r1", 1, "x"); err != nil {
		t.Fatal(err)
	}
	h.advance("r1")
	if h.exec.ups != 1 || h.exec.count(first.RunKey) != 1 {
		t.Fatalf("expected exactly one up and one container, got ups=%d", h.exec.ups)
	}
}

// Crash between `compose up` and recording running: the container is adopted,
// never duplicated.
func TestRunAdoptsContainerAfterCrashBeforeRunningRecorded(t *testing.T) {
	h := newHarness(t, false)
	rec, _ := h.launch("r1", 1, "x")
	// Simulate: spawn wrote SpawnAttempted and ran up, then crashed.
	h.sup.Advance(context.Background(), rec.Key) // accepted -> up -> running
	stored, _ := h.sup.ledger.Get(rec.Key)
	stored.State = StateAccepted
	stored.Timestamps.Running = nil
	_ = h.sup.ledger.Put(stored)
	sup2, _ := NewSupervisor(h.sup.cfg)
	sup2.Advance(context.Background(), rec.Key)
	got, _ := sup2.Get(rec.Key)
	if got.State != StateRunning || h.exec.ups != 1 || h.exec.count(rec.RunKey) != 1 {
		t.Fatalf("expected adoption without a second up: state=%s ups=%d", got.State, h.exec.ups)
	}
}

// A spawn attempt that left no container fails closed instead of re-running.
func TestRunSpawnAttemptWithoutContainerFailsClosed(t *testing.T) {
	h := newHarness(t, false)
	h.exec.upNoop = true
	rec, _ := h.launch("r1", 1, "x")
	rec = h.advance("r1")
	if rec.State != StateFailed || rec.Failure != ReasonSpawnError || h.exec.ups != 1 {
		t.Fatalf("expected spawn_error after one attempt, got %+v ups=%d", rec, h.exec.ups)
	}
	if len(h.rev.calls) != 1 {
		t.Fatalf("a failed spawn must still revoke its identity")
	}
}

// Acceptance 6: a mismatched digest is rejected.
func TestRunDigestMismatchRejected(t *testing.T) {
	h := newHarness(t, false)
	if _, err := h.launch("r1", 1, "x"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.launch("r1", 1, "y"); apiCode(err) != "digest_mismatch" {
		t.Fatalf("expected digest_mismatch, got %v", err)
	}
	raw, _ := specJSON(t, "x")
	_, _, err := h.sup.Launch(context.Background(), LaunchRequest{Key: Key{"inst", "r2"}, Generation: 1, SpecDigest: "sha256:00", Spec: raw, AuthorityExpiresAt: h.now.Add(time.Minute)})
	if apiCode(err) != "digest_invalid" {
		t.Fatalf("expected digest_invalid, got %v", err)
	}
}

// Acceptance 7: a spec exceeding its grant creates no container or record.
func TestRunGrantExceededCreatesNothing(t *testing.T) {
	h := newHarness(t, false)
	cases := map[string]func() error{
		"tool": func() error { _, err := h.launch("r1", 1, "x", "api.delete"); return err },
		"task": func() error { _, err := h.launch("r2", 1, strings.Repeat("a", 2000)); return err },
		"authority": func() error {
			raw, digest := specJSON(t, "x")
			_, _, err := h.sup.Launch(context.Background(), LaunchRequest{Key: Key{"inst", "r3"}, Generation: 1, SpecDigest: digest, Spec: raw, AuthorityExpiresAt: h.now.Add(48 * time.Hour)})
			return err
		},
		"template": func() error {
			raw, _ := json.Marshal(map[string]any{"template": "other", "task": "x", "tools": []string{}})
			_, _, digest, _ := CanonicalSpec(raw)
			_, _, err := h.sup.Launch(context.Background(), LaunchRequest{Key: Key{"inst", "r4"}, Generation: 1, SpecDigest: digest, Spec: raw, AuthorityExpiresAt: h.now.Add(time.Minute)})
			return err
		},
	}
	for name, fn := range cases {
		if err := fn(); err == nil {
			t.Fatalf("%s: expected refusal", name)
		}
	}
	recs, _ := h.sup.ledger.All()
	if len(recs) != 0 || h.exec.ups != 0 {
		t.Fatalf("refused launches must leave no record or container")
	}
}

func TestRunStaleGenerationRejected(t *testing.T) {
	h := newHarness(t, false)
	rec, _ := h.launch("r1", 3, "x")
	if _, err := h.launch("r1", 2, "x"); apiCode(err) != "stale_generation" {
		t.Fatalf("launch: %v", err)
	}
	if _, err := h.sup.Cancel(context.Background(), rec.Key, 2); apiCode(err) != "stale_generation" {
		t.Fatalf("cancel: %v", err)
	}
}

// Acceptance 8 (unit): expired orchestrator authority tears the run down even
// when no callback ever succeeds.
func TestRunAuthorityExpiryTearsDown(t *testing.T) {
	h := newHarness(t, false)
	rec, _ := h.launch("r1", 1, "x")
	h.advance("r1")
	h.now = h.now.Add(31 * time.Minute)
	rec = h.advance("r1")
	if rec.State != StateFailed || rec.Failure != ReasonAuthorityExpired || !rec.Revoked || h.exec.count(rec.RunKey) != 0 {
		t.Fatalf("got %+v", rec)
	}
}

func TestRunTimeoutTearsDown(t *testing.T) {
	h := newHarness(t, false)
	raw, _ := json.Marshal(map[string]any{"template": "worker", "task": "x", "tools": []string{}, "max_runtime_seconds": 60})
	_, _, digest, _ := CanonicalSpec(raw)
	rec, _, err := h.sup.Launch(context.Background(), LaunchRequest{Key: Key{"inst", "r1"}, Generation: 1, SpecDigest: digest, Spec: raw, AuthorityExpiresAt: h.now.Add(30 * time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	h.advance("r1")
	h.now = h.now.Add(61 * time.Second)
	rec = h.advance("r1")
	if rec.State != StateFailed || rec.Failure != ReasonTimeout {
		t.Fatalf("got %+v", rec)
	}
}

// Restart reconciliation: a mismatched container is torn down, not adopted.
func TestRunReconcileMismatchTearsDown(t *testing.T) {
	h := newHarness(t, false)
	rec, _ := h.launch("r1", 1, "x")
	h.advance("r1")
	h.exec.mu.Lock()
	h.exec.containers[rec.RunKey][0].ImageID = "sha256:other"
	h.exec.mu.Unlock()
	rec = h.advance("r1")
	if rec.State != StateFailed || rec.Failure != ReasonMismatch || h.exec.count(rec.RunKey) != 0 {
		t.Fatalf("got %+v", rec)
	}
}

// Credential custody: callbacks use the held credential; 401/403 ends the run.
func TestRunCallbacksAndAuthorityRejection(t *testing.T) {
	h := newHarness(t, true)
	raw, digest := specJSON(t, "x")
	rec, _, err := h.sup.Launch(context.Background(), LaunchRequest{Key: Key{"inst", "r1"}, Generation: 1, SpecDigest: digest, Spec: raw, Credential: "orch-token", AuthorityExpiresAt: h.now.Add(30 * time.Minute)})
	if err != nil || rec.Credential != CredentialHeld {
		t.Fatalf("launch: %v %+v", err, rec)
	}
	h.advance("r1")
	rec = h.advance("r1")
	if h.notify.started != 1 || h.notify.lastCred != "orch-token" {
		t.Fatalf("start callback not delivered with credential")
	}
	if strings.Contains(string(h.exec.lastFile), "orch-token") || strings.Contains(strings.Join(mapValues(h.exec.lastEnv), "\n"), "orch-token") {
		t.Fatalf("orchestrator credential reached the worker")
	}
	h.now = h.now.Add(2 * time.Minute)
	h.advance("r1")
	if h.notify.beats != 1 {
		t.Fatalf("heartbeat not sent")
	}
	h.notify.err = ErrAuthority
	h.now = h.now.Add(2 * time.Minute)
	rec = h.advance("r1")
	if rec.State != StateFailed || rec.Failure != ReasonAuthorityExpired || h.exec.count(rec.RunKey) != 0 {
		t.Fatalf("got %+v", rec)
	}
	if _, ok, _ := h.sup.ledger.Secret(rec.Key, "credential"); ok {
		t.Fatalf("credential must be removed at terminal state")
	}
}

func TestRunEndCallbackOnNaturalExit(t *testing.T) {
	h := newHarness(t, true)
	raw, digest := specJSON(t, "x")
	rec, _, _ := h.sup.Launch(context.Background(), LaunchRequest{Key: Key{"inst", "r1"}, Generation: 1, SpecDigest: digest, Spec: raw, Credential: "orch-token", AuthorityExpiresAt: h.now.Add(30 * time.Minute)})
	h.advance("r1")
	h.exec.stdout = []byte("\n>> ok\n")
	h.exec.exit(rec.RunKey, 0)
	rec = h.advance("r1")
	if rec.State != StateStopped || h.notify.ended != 1 || h.notify.lastOutcome != "succeeded" || rec.Callbacks.End != EndAcked {
		t.Fatalf("got %+v ended=%d", rec, h.notify.ended)
	}
}

// Acceptance 9 (unit): credential recovery after a crash gives no duplicate.
func TestRunCredentialRecoveryNoDuplicate(t *testing.T) {
	h := newHarness(t, true)
	raw, digest := specJSON(t, "x")
	rec, _, _ := h.sup.Launch(context.Background(), LaunchRequest{Key: Key{"inst", "r1"}, Generation: 1, SpecDigest: digest, Spec: raw, Credential: "old", AuthorityExpiresAt: h.now.Add(30 * time.Minute)})
	h.advance("r1")
	// Crash lost the credential file.
	_ = h.sup.ledger.DeleteSecret(rec.Key, "credential")
	rec = h.advance("r1")
	if rec.Credential != CredentialMissing {
		t.Fatalf("expected credential missing, got %q", rec.Credential)
	}
	// The orchestrator reissues by retrying the launch with a new credential
	// under the same identity.
	rec, _, err := h.sup.Launch(context.Background(), LaunchRequest{Key: Key{"inst", "r1"}, Generation: 2, SpecDigest: digest, Spec: raw, Credential: "new", AuthorityExpiresAt: h.now.Add(30 * time.Minute)})
	if err != nil || rec.Credential != CredentialHeld || rec.Generation != 2 {
		t.Fatalf("reissue: %v %+v", err, rec)
	}
	h.now = h.now.Add(2 * time.Minute)
	h.advance("r1")
	if h.exec.ups != 1 || h.exec.count(rec.RunKey) != 1 || h.notify.lastCred != "new" {
		t.Fatalf("expected one container and new credential in use; ups=%d cred=%q", h.exec.ups, h.notify.lastCred)
	}
}

func TestRunCredentialRefusedWithoutCallbacks(t *testing.T) {
	h := newHarness(t, false)
	raw, digest := specJSON(t, "x")
	_, _, err := h.sup.Launch(context.Background(), LaunchRequest{Key: Key{"inst", "r1"}, Generation: 1, SpecDigest: digest, Spec: raw, Credential: "c", AuthorityExpiresAt: h.now.Add(time.Minute)})
	if apiCode(err) != "callbacks_disabled" {
		t.Fatalf("got %v", err)
	}
}

func TestCanonicalSpecRejectsFloatsAndUnknownFields(t *testing.T) {
	for _, raw := range []string{
		`{"template":"w","task":"x","max_runtime_seconds":1.5}`,
		`{"template":"w","task":"x","extra":1}`,
		`{"template":"w","task":"x","tools":[1]}`,
		`[1]`,
	} {
		if _, _, _, err := CanonicalSpec(json.RawMessage(raw)); err == nil {
			t.Fatalf("expected rejection for %s", raw)
		}
	}
	a, _, da, _ := CanonicalSpec(json.RawMessage(`{"task":"<x>&","template":"w"}`))
	b, _, db, _ := CanonicalSpec(json.RawMessage(`{ "template" : "w", "task" : "<x>&" }`))
	if string(a) != `{"task":"<x>&","template":"w"}` || da != db || string(a) != string(b) {
		t.Fatalf("canonical form unstable: %s %s", a, b)
	}
}

func TestLedgerFailsClosedOnForeignEntry(t *testing.T) {
	h := newHarness(t, false)
	if _, err := h.launch("r1", 1, "x"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/etc/passwd", filepath.Join(h.podDir, StateDirName, "ledger", "evil.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := HasLiveRuns(h.podDir); err == nil {
		t.Fatalf("a symlinked record must make the ledger unreadable")
	}
}

func TestTemplateContextTamperDetected(t *testing.T) {
	h := newHarness(t, false)
	path := filepath.Join(TemplateDir(h.podDir), "worker.context.json")
	raw, _ := os.ReadFile(path)
	tampered := strings.Replace(string(raw), "http://api:80", "http://evil:80", 1)
	if err := os.WriteFile(path, []byte(tampered), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := LoadTemplate(h.podDir, "worker"); err == nil || !strings.Contains(err.Error(), "context digest") {
		t.Fatalf("expected context digest mismatch, got %v", err)
	}
	// Rotating only a credential keeps the identity.
	_ = os.WriteFile(path, raw, 0o600)
	rotated := strings.Replace(string(raw), "svc-secret", "svc-rotated", 1)
	_ = os.WriteFile(path, []byte(rotated), 0o600)
	if _, _, err := LoadTemplate(h.podDir, "worker"); err != nil {
		t.Fatalf("credential rotation must not change template identity: %v", err)
	}
}

func mapValues(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	return out
}

func TestRunEndCallbackRejectedIsRecordedNotAcked(t *testing.T) {
	h := newHarness(t, true)
	raw, digest := specJSON(t, "x")
	rec, _, _ := h.sup.Launch(context.Background(), LaunchRequest{Key: Key{"inst", "r1"}, Generation: 1, SpecDigest: digest, Spec: raw, Credential: "c", AuthorityExpiresAt: h.now.Add(30 * time.Minute)})
	h.advance("r1")
	h.advance("r1")
	h.exec.stdout = []byte("\n>> ok\n")
	h.exec.exit(rec.RunKey, 0)
	h.notify.err = ErrAuthority
	rec = h.advance("r1")
	if rec.State != StateStopped || rec.Callbacks.End != EndRejected {
		t.Fatalf("got state=%s end=%q", rec.State, rec.Callbacks.End)
	}
}

func TestRunEndCallbackTransientRetriesUntilExpiry(t *testing.T) {
	h := newHarness(t, true)
	raw, digest := specJSON(t, "x")
	rec, _, _ := h.sup.Launch(context.Background(), LaunchRequest{Key: Key{"inst", "r1"}, Generation: 1, SpecDigest: digest, Spec: raw, Credential: "c", AuthorityExpiresAt: h.now.Add(30 * time.Minute)})
	h.advance("r1")
	h.advance("r1")
	h.exec.stdout = []byte("\n>> ok\n")
	h.exec.exit(rec.RunKey, 0)
	h.notify.err = errors.New("connection refused")
	rec = h.advance("r1")
	if rec.State.Terminal() || rec.Callbacks.End != "" {
		t.Fatalf("transient end failure must retry, got state=%s end=%q", rec.State, rec.Callbacks.End)
	}
	h.now = h.now.Add(31 * time.Minute)
	rec = h.advance("r1")
	if rec.State != StateStopped || rec.Callbacks.End != EndAbandoned {
		t.Fatalf("got state=%s end=%q", rec.State, rec.Callbacks.End)
	}
}

// A cancel racing a launch's final check either refuses the launch or, if the
// launch was already accepted, tears it down; it never reports stopped for a
// run that later becomes accepted.
func TestCancelLinearizesWithLaunchAccept(t *testing.T) {
	h := newHarness(t, false)
	entered := make(chan struct{})
	release := make(chan struct{})
	h.sup.testHookBeforeAccept = func() {
		close(entered)
		<-release
	}
	raw, digest := specJSON(t, "x")
	launchDone := make(chan error, 1)
	go func() {
		_, _, err := h.sup.Launch(context.Background(), LaunchRequest{Key: Key{"inst", "r1"}, Generation: 1, SpecDigest: digest, Spec: raw, AuthorityExpiresAt: h.now.Add(time.Minute)})
		launchDone <- err
	}()
	<-entered
	cancelDone := make(chan *Record, 1)
	go func() {
		rec, _ := h.sup.Cancel(context.Background(), Key{"inst", "r1"}, 1)
		cancelDone <- rec
	}()
	select {
	case <-cancelDone:
		t.Fatalf("cancel must wait for the in-flight accept to linearize")
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	if err := <-launchDone; err != nil {
		t.Fatal(err)
	}
	rec := <-cancelDone
	if rec.State != StateRevoking || !rec.Cancelled {
		t.Fatalf("cancel after accept must report revoking, got %+v", rec)
	}
	h.sup.testHookBeforeAccept = nil
	rec = h.advance("r1")
	if rec.State != StateStopped || h.exec.ups != 0 {
		t.Fatalf("accepted-then-cancelled run must stop without a container, got %+v ups=%d", rec, h.exec.ups)
	}
}

func TestConcurrentReversedCancelsKeepHighestGeneration(t *testing.T) {
	h := newHarness(t, false)
	k := Key{"inst", "r1"}
	var wg sync.WaitGroup
	for _, gen := range []uint64{10, 9, 10, 9} {
		wg.Add(1)
		go func(g uint64) {
			defer wg.Done()
			_, _ = h.sup.Cancel(context.Background(), k, g)
		}(gen)
	}
	wg.Wait()
	gen, ok, err := h.sup.ledger.Tombstone(k)
	if err != nil || !ok || gen != 10 {
		t.Fatalf("tombstone generation = %d ok=%v err=%v", gen, ok, err)
	}
}

type blockingRevoker struct {
	block   string
	release chan struct{}
	fakeRevoker
}

func (b *blockingRevoker) Revoke(ctx context.Context, agentID string, gen uint64) error {
	if strings.Contains(agentID, b.block) {
		select {
		case <-b.release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return b.fakeRevoker.Revoke(ctx, agentID, gen)
}

// One run blocked in revoke must not delay another run's cancel or expiry.
func TestSlowRunDoesNotBlockOthers(t *testing.T) {
	h := newHarness(t, false)
	a, _ := h.launch("a", 1, "x")
	b, _ := h.launch("b", 1, "x")
	h.advance("a")
	h.advance("b")
	br := &blockingRevoker{block: a.RunAgentID, release: make(chan struct{})}
	h.sup.cfg.Revoker = br
	if _, err := h.sup.Cancel(context.Background(), a.Key, 1); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	bgDone := make(chan struct{})
	defer func() {
		close(br.release)
		cancel()
		<-bgDone
	}()
	go func() {
		defer close(bgDone)
		h.sup.AdvanceAll(ctx) // a blocks in revoke
	}()
	time.Sleep(20 * time.Millisecond)

	start := time.Now()
	if _, err := h.sup.Cancel(context.Background(), b.Key, 1); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > time.Second {
		t.Fatalf("cancel was delayed by another run")
	}
	h.sup.AdvanceAll(ctx)
	got, _ := h.sup.Get(b.Key)
	if got.State != StateStopped {
		t.Fatalf("run b should stop while a is blocked, got %s", got.State)
	}
	gotA, _ := h.sup.Get(a.Key)
	if gotA.State != StateRevoking || gotA.Revoked {
		t.Fatalf("run a must still be revoking, got %s", gotA.State)
	}
}

// The deadline is fixed before the worker starts; a restart after a crash
// between compose up and the running record cannot extend it.
func TestDeadlineSurvivesCrashBeforeRunning(t *testing.T) {
	h := newHarness(t, false)
	rec, _ := h.launch("r1", 1, "x")
	var deadline time.Time
	h.sup.testHookBeforeUp = func() {
		stored, _ := h.sup.ledger.Get(rec.Key)
		if stored.Deadline == nil {
			t.Fatalf("deadline must be persisted before Up")
		}
		deadline = *stored.Deadline
	}
	h.advance("r1")
	stored, _ := h.sup.ledger.Get(rec.Key)
	stored.State = StateAccepted
	_ = h.sup.ledger.Put(stored)
	h.now = h.now.Add(5 * time.Minute)
	sup2, _ := NewSupervisor(h.sup.cfg)
	sup2.Advance(context.Background(), rec.Key)
	got, _ := sup2.Get(rec.Key)
	if got.Deadline == nil || !got.Deadline.Equal(deadline) {
		t.Fatalf("deadline moved from %v to %v", deadline, got.Deadline)
	}
}

func TestCanonicalSpecGolden(t *testing.T) {
	cases := []struct{ in, want string }{
		{`{"tools":[],"template":"w","task":"q\"b\\s<>&é\u2028\u2029\u0001\n","max_runtime_seconds":-0}`,
			"{\"max_runtime_seconds\":0,\"task\":\"q\\\"b\\\\s<>&é\u2028\u2029\\u0001\\n\",\"template\":\"w\",\"tools\":[]}"},
		{`{"task":"😀 \ud83d\ude00","template":"w"}`, "{\"task\":\"😀 😀\",\"template\":\"w\"}"},
	}
	for _, tc := range cases {
		got, _, _, err := CanonicalSpec(json.RawMessage(tc.in))
		if err != nil {
			t.Fatalf("%s: %v", tc.in, err)
		}
		if string(got) != tc.want {
			t.Fatalf("canonical(%s)\n got %s\nwant %s", tc.in, got, tc.want)
		}
	}
	for _, bad := range []string{
		`{"template":"w","template":"x","task":"t"}`,
		`{"template":"w","task":"\ud800"}`,
		`{"template":"w","task":"\udc00"}`,
		`{"template":"w","task":"t","max_runtime_seconds":9007199254740992}`,
		`{"template":"w","task":"t"} {}`,
		"{\"template\":\"w\",\"task\":\"\xff\"}",
	} {
		if _, _, _, err := CanonicalSpec(json.RawMessage(bad)); err == nil {
			t.Fatalf("expected rejection for %q", bad)
		}
	}
}

// exitingExec creates containers that have already exited, as a fast
// one-shot worker would before the first monitor step.
type exitingExec struct{ *fakeExec }

func (e exitingExec) Up(ctx context.Context, project, file string, env map[string]string) error {
	if err := e.fakeExec.Up(ctx, project, file, env); err != nil {
		return err
	}
	e.fakeExec.mu.Lock()
	defer e.fakeExec.mu.Unlock()
	for k, cs := range e.fakeExec.containers {
		for i := range cs {
			cs[i].Running, cs[i].Exited, cs[i].ExitCode = false, true, 0
		}
		e.fakeExec.containers[k] = cs
	}
	return nil
}

func TestFastExitSendsStartBeforeEnd(t *testing.T) {
	h := newHarness(t, true)
	h.sup.cfg.Executor = exitingExec{h.exec}
	h.exec.stdout = []byte("\n>> done\n")
	raw, digest := specJSON(t, "x")
	_, _, err := h.sup.Launch(context.Background(), LaunchRequest{Key: Key{"inst", "r1"}, Generation: 1, SpecDigest: digest, Spec: raw, Credential: "c", AuthorityExpiresAt: h.now.Add(30 * time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	rec := h.advance("r1")
	if rec.State != StateStopped || strings.Join(h.notify.order, ",") != "start,end" {
		t.Fatalf("state=%s order=%v", rec.State, h.notify.order)
	}
	if rec.Callbacks.Start != CallbackAcked || rec.Callbacks.End != CallbackAcked {
		t.Fatalf("callbacks = %+v", rec.Callbacks)
	}
}

func TestRestartAdoptedExitedContainerSendsStartBeforeEnd(t *testing.T) {
	h := newHarness(t, true)
	raw, digest := specJSON(t, "x")
	rec, _, _ := h.sup.Launch(context.Background(), LaunchRequest{Key: Key{"inst", "r1"}, Generation: 1, SpecDigest: digest, Spec: raw, Credential: "c", AuthorityExpiresAt: h.now.Add(30 * time.Minute)})
	// Crash right after compose up: the worker ran and exited while the
	// supervisor was down, and the record still says accepted.
	h.sup.testHookBeforeUp = func() { h.sup.testHookBeforeUp = nil }
	h.sup.Advance(context.Background(), rec.Key)
	stored, _ := h.sup.ledger.Get(rec.Key)
	stored.State = StateAccepted
	stored.Timestamps.Running = nil
	stored.Callbacks = Callbacks{}
	_ = h.sup.ledger.Put(stored)
	h.notify.order = nil
	h.exec.stdout = []byte("\n>> done\n")
	h.exec.exit(rec.RunKey, 0)
	sup2, _ := NewSupervisor(h.sup.cfg)
	sup2.Advance(context.Background(), rec.Key)
	h.sup = sup2
	if h.exec.ups != 1 {
		t.Fatalf("adoption must not run compose up again, ups=%d", h.exec.ups)
	}
	got, _ := h.sup.Get(rec.Key)
	if got.State != StateStopped || strings.Join(h.notify.order, ",") != "start,end" {
		t.Fatalf("state=%s order=%v", got.State, h.notify.order)
	}
}

func TestRestartWithCreatedOnlyContainerDoesNotClaimRunning(t *testing.T) {
	h := newHarness(t, true)
	raw, digest := specJSON(t, "x")
	rec, _, _ := h.sup.Launch(context.Background(), LaunchRequest{Key: Key{"inst", "r1"}, Generation: 1, SpecDigest: digest, Spec: raw, Credential: "c", AuthorityExpiresAt: h.now.Add(30 * time.Minute)})
	h.sup.testHookBeforeUp = func() { h.sup.testHookBeforeUp = nil }
	h.sup.Advance(context.Background(), rec.Key)
	stored, _ := h.sup.ledger.Get(rec.Key)
	stored.State = StateAccepted
	stored.Timestamps.Running = nil
	stored.Callbacks = Callbacks{}
	_ = h.sup.ledger.Put(stored)
	h.exec.mu.Lock()
	for i := range h.exec.containers[rec.RunKey] {
		h.exec.containers[rec.RunKey][i].Running = false // created, never started
	}
	h.exec.mu.Unlock()
	h.notify.order = nil
	sup2, _ := NewSupervisor(h.sup.cfg)
	sup2.Advance(context.Background(), rec.Key)
	got, _ := sup2.Get(rec.Key)
	if got.State != StateFailed || got.Failure != ReasonSpawnError || got.Timestamps.Running != nil {
		t.Fatalf("created-only container must fail closed, got state=%s failure=%s", got.State, got.Failure)
	}
	if h.exec.count(rec.RunKey) != 0 || h.exec.ups != 1 {
		t.Fatalf("container must be removed without a second up; count=%d ups=%d", h.exec.count(rec.RunKey), h.exec.ups)
	}
	for _, ev := range h.notify.order {
		if ev == "start" {
			t.Fatalf("no start callback may be acked for an unstarted worker: %v", h.notify.order)
		}
	}
}

func TestExtractAnswer(t *testing.T) {
	cases := []struct {
		name, in         string
		limit            int
		want             string
		truncated, found bool
	}{
		{"simple", "BANNER\n>> hi\n", 16, "hi", false, true},
		{"marker inside answer", "\n>> a\n>> b\n", 16, "a\n>> b", false, true},
		{"missing marker", "no answer here\n", 16, "", false, false},
		{"exact cap", "\n>> 0123456789abcdef\n", 16, "0123456789abcdef", false, true},
		{"oversized", "\n>> 0123456789abcdefXYZ", 16, "0123456789abcdef", true, true},
		{"multibyte boundary", "\n>> 012345678901234é", 16, "012345678901234", true, true},
	}
	for _, tc := range cases {
		got, truncated, found := extractAnswer([]byte(tc.in), "\n>> ", tc.limit)
		if string(got) != tc.want || truncated != tc.truncated || found != tc.found {
			t.Fatalf("%s: got %q truncated=%v found=%v", tc.name, got, truncated, found)
		}
	}
}

func TestResultMissingMarkerFailsExplicitly(t *testing.T) {
	h := newHarness(t, false)
	rec, _ := h.launch("r1", 1, "x")
	h.advance("r1")
	h.exec.stdout = []byte("BANNER only, no marker\n")
	h.exec.exit(rec.RunKey, 0)
	rec = h.advance("r1")
	if rec.State != StateFailed || rec.Failure != ReasonResultMissing || rec.Result != nil {
		t.Fatalf("got %+v", rec)
	}
}

func TestResultMarkerBeyondPreambleIsMissing(t *testing.T) {
	h := newHarness(t, false)
	rec, _ := h.launch("r1", 1, "x")
	h.advance("r1")
	h.exec.stdout = []byte(strings.Repeat("b", resultPreambleBytes+64) + "\n>> late answer\n")
	h.exec.exit(rec.RunKey, 0)
	rec = h.advance("r1")
	if rec.Failure != ReasonResultMissing {
		t.Fatalf("marker past the read bound must be result_missing, got %+v", rec)
	}
}

func TestResultSourceTruncationIsReported(t *testing.T) {
	h := newHarness(t, false)
	rec, _ := h.launch("r1", 1, "x")
	h.advance("r1")
	// Answer fits the result limit only because the source stream was cut.
	h.exec.stdout = []byte(strings.Repeat("b", resultPreambleBytes) + "\n>> abc" + strings.Repeat("z", 64))
	h.exec.exit(rec.RunKey, 0)
	rec = h.advance("r1")
	if rec.Result == nil || !rec.Result.Truncated {
		t.Fatalf("source truncation must be reported, got %+v", rec.Result)
	}
}

// Two pods on one daemon accepting the same run key use separate projects
// and container queries; one never touches the other's worker.
func TestTwoPodsSameKeyAreIsolated(t *testing.T) {
	a := newHarness(t, false)
	b := newHarness(t, false)
	b.exec = a.exec
	b.sup.cfg.Executor = a.exec
	ra, _ := a.launch("same", 1, "x")
	rb, _ := b.launch("same", 1, "x")
	if ra.Project == rb.Project || ra.RunKey == rb.RunKey {
		t.Fatalf("same key in two pods must not share project %q or run key", ra.Project)
	}
	a.advance("same")
	b.advance("same")
	if a.exec.count(ra.RunKey) != 1 || a.exec.count(rb.RunKey) != 1 {
		t.Fatalf("expected one container per pod")
	}
	if _, err := a.sup.Cancel(context.Background(), ra.Key, 1); err != nil {
		t.Fatal(err)
	}
	if got := a.advance("same"); got.State != StateStopped {
		t.Fatalf("pod a run: %s", got.State)
	}
	if a.exec.count(rb.RunKey) != 1 {
		t.Fatalf("cancelling pod a's run removed pod b's worker")
	}
	if got := b.advance("same"); got.State != StateRunning {
		t.Fatalf("pod b run must keep running, got %s", got.State)
	}
}

func TestFinalizeWithoutProxy(t *testing.T) {
	h := newHarness(t, false)
	rec, _ := h.launch("r1", 1, "x")
	h.advance("r1")
	if _, err := h.sup.Cancel(context.Background(), rec.Key, 1); err != nil {
		t.Fatal(err)
	}
	h.rev.err = errors.New("cllama unreachable")
	ctx := context.Background()
	gone := func(context.Context) (bool, error) { return true, nil }
	present := func(context.Context) (bool, error) { return false, nil }
	// The worker is removed by teardown even without the ACK.
	rec = h.advance("r1")
	if rec.State != StateRevoking || h.exec.count(rec.RunKey) != 0 {
		t.Fatalf("got %+v", rec)
	}
	if err := h.sup.FinalizeWithoutProxy(ctx, rec.Key, present); err == nil {
		t.Fatalf("must refuse while the proxy is present")
	}
	h.exec.mu.Lock()
	h.exec.containers[rec.RunKey] = []Container{{ID: "stray"}}
	h.exec.mu.Unlock()
	if err := h.sup.FinalizeWithoutProxy(ctx, rec.Key, gone); err == nil {
		t.Fatalf("must refuse while a worker container exists")
	}
	h.exec.mu.Lock()
	delete(h.exec.containers, rec.RunKey)
	h.exec.mu.Unlock()
	if err := h.sup.FinalizeWithoutProxy(ctx, rec.Key, gone); err != nil {
		t.Fatal(err)
	}
	got, _ := h.sup.Get(rec.Key)
	if got.State != StateStopped || !got.Revoked || got.RevocationProof != ProofProxyStopped {
		t.Fatalf("got %+v", got)
	}
	if _, err := os.Stat(filepath.Join(h.podDir, ".claw-runtime", "context", got.RunAgentID)); !os.IsNotExist(err) {
		t.Fatalf("run context must be deleted")
	}
	if live, _ := HasLiveRuns(h.podDir); live {
		t.Fatalf("a later claw up must not be blocked")
	}
}

func TestSpawnRefusedWhenProxyCannotRevoke(t *testing.T) {
	h := newHarness(t, false)
	h.rev.err = errors.New("404")
	rec, _ := h.launch("r1", 1, "x")
	rec = h.advance("r1")
	if rec.State != StateAccepted || h.exec.ups != 0 || rec.SpawnAttempted {
		t.Fatalf("no worker may start without revocation support: %+v ups=%d", rec, h.exec.ups)
	}
	h.rev.err = nil
	rec = h.advance("r1")
	if rec.State != StateRunning || h.exec.ups != 1 {
		t.Fatalf("spawn must proceed once the proxy supports revocation: %+v", rec)
	}
}

// Struct-typed metadata (as claw up compiles it) must digest the same after a
// disk round trip.
func TestTemplateDigestStableAcrossRoundTrip(t *testing.T) {
	h := newHarness(t, false)
	tpl, ctx, err := LoadTemplate(h.podDir, "worker")
	if err != nil {
		t.Fatal(err)
	}
	type policy struct {
		Zeta  string `json:"zeta"`
		Alpha int    `json:"alpha"`
	}
	ctx.Input.Metadata["model_policy"] = policy{Zeta: "z", Alpha: 1}
	if err := WriteTemplates(h.podDir, []*Template{tpl}, map[string]*ContextTemplate{"worker": ctx}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := LoadTemplate(h.podDir, "worker"); err != nil {
		t.Fatalf("round trip changed the digest: %v", err)
	}
}

func TestExpiryWithUnavailableRevokeReportsRevoking(t *testing.T) {
	h := newHarness(t, false)
	record, err := h.launch("expiry-pending", 1, "x")
	if err != nil {
		t.Fatal(err)
	}
	h.advance("expiry-pending")
	h.now = h.now.Add(31 * time.Minute)
	h.rev.err = errors.New("proxy unavailable")
	record = h.advance("expiry-pending")
	if record.State != StateRevoking || record.Cancelled || record.Revoked || record.Failure != ReasonAuthorityExpired || h.exec.count(record.RunKey) != 0 {
		t.Fatalf("false running/terminal state: %+v", record)
	}
	h.rev.err = nil
	record = h.advance("expiry-pending")
	if record.State != StateFailed || record.Timestamps.Revoked == nil || record.Cancelled {
		t.Fatalf("expiry cleanup: %+v", record)
	}
}
