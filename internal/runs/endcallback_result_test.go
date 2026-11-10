package runs

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The end callback carries a task-home result's outcome and session (#396),
// and a stateless run's body is byte-for-byte what it was before.
func TestEndCallbackTaskHomeResultFields(t *testing.T) {
	var bodies []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(raw))
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	notifier, err := NewHTTPNotifier(server.URL, "/runs/{run_id}")
	if err != nil {
		t.Fatal(err)
	}
	end := func(rec *Record, outcome, reason, summary string) string {
		t.Helper()
		if err := notifier.Ended(context.Background(), rec, "credential", outcome, reason, []byte(summary)); err != nil {
			t.Fatal(err)
		}
		return bodies[len(bodies)-1]
	}
	key := Key{"inst", "r1"}

	// Stateless result, no result at all, and a failed run: unchanged bodies.
	for name, tc := range map[string]struct {
		rec                      *Record
		outcome, reason, summary string
		want                     string
	}{
		"stateless result": {
			&Record{Key: key, Generation: 1, Result: &ResultInfo{Bytes: 2}}, "succeeded", "", "ok",
			`{"generation":1,"installation":"inst","outcome":"succeeded","reason":"","run_id":"r1","summary":"ok","summary_truncated":false}`,
		},
		"no result": {
			&Record{Key: key, Generation: 3}, "failed", "exit_nonzero", "",
			`{"generation":3,"installation":"inst","outcome":"failed","reason":"exit_nonzero","run_id":"r1","summary":"","summary_truncated":false}`,
		},
		"header without session": {
			// Never produced by the supervisor; a partial header adds nothing.
			&Record{Key: key, Generation: 1, Result: &ResultInfo{Bytes: 2, Outcome: OutcomeAnswer}}, "succeeded", "", "ok",
			`{"generation":1,"installation":"inst","outcome":"succeeded","reason":"","run_id":"r1","summary":"ok","summary_truncated":false}`,
		},
	} {
		if got := end(tc.rec, tc.outcome, tc.reason, tc.summary); got != tc.want {
			t.Errorf("%s:\n got %s\nwant %s", name, got, tc.want)
		}
	}

	answer := &Record{Key: key, Generation: 1, Result: &ResultInfo{Bytes: 4, Outcome: OutcomeAnswer, Session: "sess-1"}}
	if got, want := end(answer, "succeeded", "", "done"), `{"generation":1,"installation":"inst","outcome":"succeeded","reason":"","result_outcome":"answer","run_id":"r1","session":"sess-1","summary":"done","summary_truncated":false}`; got != want {
		t.Errorf("answer:\n got %s\nwant %s", got, want)
	}
	// A retried delivery sends the same bytes.
	if first, again := end(answer, "succeeded", "", "done"), end(answer, "succeeded", "", "done"); first != again {
		t.Errorf("retry changed the body:\n%s\n%s", first, again)
	}
	// A result whose stored values are not a valid header adds nothing.
	for _, bad := range []*ResultInfo{
		{Bytes: 1, Outcome: "done", Session: "sess-1"},
		{Bytes: 1, Outcome: OutcomeAnswer, Session: "../x"},
		{Bytes: 1, Session: "sess-1"},
	} {
		got := end(&Record{Key: key, Generation: 1, Result: bad}, "succeeded", "", "ok")
		if got != `{"generation":1,"installation":"inst","outcome":"succeeded","reason":"","run_id":"r1","summary":"ok","summary_truncated":false}` {
			t.Errorf("invalid stored result %+v changed the body: %s", bad, got)
		}
	}
	question := &Record{Key: key, Generation: 2, Result: &ResultInfo{Bytes: 13, Outcome: OutcomeQuestion, Session: "sess-1", Truncated: true}}
	if got, want := end(question, "succeeded", "", "Which branch?"), `{"generation":2,"installation":"inst","outcome":"succeeded","reason":"","result_outcome":"question","run_id":"r1","session":"sess-1","summary":"Which branch?","summary_truncated":true}`; got != want {
		t.Errorf("question:\n got %s\nwant %s", got, want)
	}
}
