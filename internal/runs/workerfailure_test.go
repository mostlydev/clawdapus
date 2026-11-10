package runs

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// A task-home worker that exits non-zero can say which of its own checks
// ended the run. The supervisor keeps that name as the failure reason, and
// nothing else from the worker.
func TestWorkerFailureReasonIsKept(t *testing.T) {
	two := 2
	cases := map[string]struct {
		stdout string
		exit   int
		want   string
		runner *int
	}{
		"session unproven":   {`{"outcome":"failed","reason":"session_unproven"}`, 70, ReasonWorkerSessionUnproven, nil},
		"turn not stored":    {`{"outcome":"failed","reason":"turn_not_stored"}`, 70, ReasonWorkerTurnNotStored, nil},
		"runner failed":      {`{"outcome":"failed","reason":"runner_exit","runner_exit":2}`, 2, ReasonWorkerRunnerExit, &two},
		"after runner noise": {"banner and noise\n" + taskHomeMarker + `{"outcome":"failed","reason":"usage"}`, 64, ReasonWorkerUsage, nil},

		// Anything that is not a well-formed report of a known reason leaves
		// the generic reason in place.
		"unknown reason":           {`{"outcome":"failed","reason":"something_new"}`, 70, ReasonExitNonZero, nil},
		"reason with other text":   {`{"outcome":"failed","reason":"sk-secret-token-value"}`, 70, ReasonExitNonZero, nil},
		"reason too long":          {`{"outcome":"failed","reason":"` + strings.Repeat("a", 200) + `"}`, 70, ReasonExitNonZero, nil},
		"reason not a string":      {`{"outcome":"failed","reason":{"a":1}}`, 70, ReasonExitNonZero, nil},
		"extra field":              {`{"outcome":"failed","reason":"usage","detail":"/home/x"}`, 70, ReasonExitNonZero, nil},
		"carries a session":        {`{"outcome":"failed","reason":"usage","session":"sess-1"}`, 70, ReasonExitNonZero, nil},
		"carries a body":           {`{"outcome":"failed","reason":"usage"}` + "\nstderr text and a token\n", 70, ReasonExitNonZero, nil},
		"runner exit out of range": {`{"outcome":"failed","reason":"runner_exit","runner_exit":100000}`, 70, ReasonExitNonZero, nil},
		"no frame":                 {"just noise\n", 70, ReasonExitNonZero, nil},
		"answer reason mix":        {`{"outcome":"answer","session":"sess-1","truncated":false,"reason":"usage"}` + "\nbody\n", 70, ReasonExitNonZero, nil},

		// A failure report never makes a zero exit a failure of the worker's
		// choosing, and never a result.
		"zero exit": {`{"outcome":"failed","reason":"session_unproven"}`, 0, ReasonResultMissing, nil},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			h := taskHomeHarness(t)
			if _, err := launchTaskHome(h, "a", "task-a"); err != nil {
				t.Fatal(err)
			}
			a := h.advance("a")
			out := tc.stdout
			if !strings.Contains(out, taskHomeMarker) && strings.HasPrefix(out, "{") {
				out = taskHomeMarker + out + "\n"
			}
			h.exec.mu.Lock()
			h.exec.stdout = []byte(out)
			h.exec.mu.Unlock()
			h.exec.exit(a.RunKey, tc.exit)
			a = h.advance("a")
			if a.Failure != tc.want {
				t.Fatalf("failure = %q, want %q", a.Failure, tc.want)
			}
			if a.Result != nil {
				t.Fatalf("a failure report became a result: %+v", a.Result)
			}
			if a.Exit == nil || a.Exit.Code != tc.exit {
				t.Fatalf("exit = %+v, want code %d", a.Exit, tc.exit)
			}
			switch {
			case tc.runner == nil && a.Exit.RunnerCode != nil:
				t.Fatalf("runner code = %d, want none", *a.Exit.RunnerCode)
			case tc.runner != nil && (a.Exit.RunnerCode == nil || *a.Exit.RunnerCode != *tc.runner):
				t.Fatalf("runner code = %v, want %d", a.Exit.RunnerCode, *tc.runner)
			}
			if body, _, err := h.sup.Result(Key{"inst", "a"}); err == nil && len(body) != 0 {
				t.Fatalf("a result body was stored: %q", body)
			}
		})
	}
}

// A worker without a task home has no wrapper. Its output cannot name a reason.
func TestWorkerFailureReasonNeedsATaskHome(t *testing.T) {
	h := newHarness(t, false)
	if _, err := h.launch("a", 1, "task"); err != nil {
		t.Fatal(err)
	}
	rec := h.advance("a")
	h.exec.mu.Lock()
	h.exec.stdout = []byte(taskHomeMarker + `{"outcome":"failed","reason":"session_unproven"}` + "\n")
	h.exec.mu.Unlock()
	h.exec.exit(rec.RunKey, 70)
	rec = h.advance("a")
	if rec.Failure != ReasonExitNonZero {
		t.Fatalf("failure = %q", rec.Failure)
	}
}

func TestWorkerFailureReasonReachesEndCallback(t *testing.T) {
	h := taskHomeHarness(t)
	ends := make(chan map[string]any, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/end") {
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			ends <- body
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	notifier, err := NewHTTPNotifier(server.URL, "/runs/{run_id}")
	if err != nil {
		t.Fatal(err)
	}
	h.sup.cfg.Notifier = notifier
	raw := json.RawMessage(`{"template":"worker","task":"task","tools":[],"home":"task-a"}`)
	_, _, digest, err := CanonicalSpec(raw)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = h.sup.Launch(context.Background(), LaunchRequest{
		Key: Key{"inst", "a"}, Generation: 1, Spec: raw, SpecDigest: digest,
		Credential: "test-credential", AuthorityExpiresAt: h.now.Add(30 * time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	rec := h.advance("a")
	h.exec.mu.Lock()
	h.exec.stdout = []byte(taskHomeMarker + `{"outcome":"failed","reason":"session_unproven"}` + "\n")
	h.exec.mu.Unlock()
	h.exec.exit(rec.RunKey, 70)
	rec = h.advance("a")
	if rec.Failure != ReasonWorkerSessionUnproven || rec.Callbacks.End != CallbackAcked {
		t.Fatalf("failure=%q callback=%q", rec.Failure, rec.Callbacks.End)
	}
	select {
	case body := <-ends:
		if body["reason"] != ReasonWorkerSessionUnproven || body["outcome"] != "failed" || body["summary"] != "" {
			t.Fatalf("wrong callback: %+v", body)
		}
		for _, field := range []string{"session", "result_outcome", "stderr"} {
			if _, exists := body[field]; exists {
				t.Fatalf("unexpected %s in callback", field)
			}
		}
	default:
		t.Fatal("no end callback delivered")
	}
}
