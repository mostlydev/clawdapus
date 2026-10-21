package runs

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func revokerFor(t *testing.T, handler http.HandlerFunc) *CllamaRevoker {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	h := newHarness(t, false)
	tpl, ctx, err := LoadTemplate(h.podDir, "worker")
	if err != nil {
		t.Fatal(err)
	}
	tpl.CllamaAPIURL = srv.URL
	if err := WriteTemplates(h.podDir, []*Template{tpl}, map[string]*ContextTemplate{"worker": ctx}); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(h.podDir, ".claw-auth"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.podDir, ".claw-auth", "ui-token"), []byte("ui-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return &CllamaRevoker{PodDir: h.podDir}
}

func TestCllamaRevokerRequiresTerminalAck(t *testing.T) {
	var gotAuth, gotPath string
	var gotBody map[string]uint64
	r := revokerFor(t, func(w http.ResponseWriter, req *http.Request) {
		gotAuth, gotPath = req.Header.Get("Authorization"), req.URL.Path
		_ = json.NewDecoder(req.Body).Decode(&gotBody)
		_, _ = io.WriteString(w, `{"agent_id":"worker-run-1","generation":3,"state":"revoked"}`)
	})
	if err := r.Revoke(context.Background(), "worker-run-1", 3); err != nil {
		t.Fatalf("valid ack rejected: %v", err)
	}
	if gotAuth != "Bearer ui-secret" || gotPath != "/internal/agents/worker-run-1/revoke" || gotBody["generation"] != 3 {
		t.Fatalf("auth=%q path=%q body=%v", gotAuth, gotPath, gotBody)
	}
}

func TestCllamaRevokerRejectsNonAcks(t *testing.T) {
	cases := map[string]http.HandlerFunc{
		"conflict": func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "revocation not acknowledged", http.StatusConflict)
		},
		"unavailable": func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "revocation not acknowledged", http.StatusServiceUnavailable)
		},
		"html":  func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "<html>ok</html>") },
		"empty": func(w http.ResponseWriter, _ *http.Request) {},
		"wrong agent": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, `{"agent_id":"other","generation":3,"state":"revoked"}`)
		},
		"lower generation": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, `{"agent_id":"worker-run-1","generation":2,"state":"revoked"}`)
		},
		"redirect": func(w http.ResponseWriter, req *http.Request) {
			http.Redirect(w, req, "/elsewhere", http.StatusTemporaryRedirect)
		},
	}
	for name, h := range cases {
		t.Run(name, func(t *testing.T) {
			if err := revokerFor(t, h).Revoke(context.Background(), "worker-run-1", 3); err == nil {
				t.Fatalf("%s must not count as a revocation", name)
			}
		})
	}
}

func TestHTTPNotifierContract(t *testing.T) {
	var paths []string
	var auths []string
	status := http.StatusOK
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		paths = append(paths, req.URL.Path)
		auths = append(auths, req.Header.Get("Authorization"))
		w.WriteHeader(status)
	}))
	defer srv.Close()
	n, err := NewHTTPNotifier(srv.URL, "/api/runs/{run_id}")
	if err != nil {
		t.Fatal(err)
	}
	rec := &Record{Key: Key{"inst", "run-1"}, Generation: 2, AuthorityExpiresAt: time.Now().Add(time.Hour)}
	if err := n.Started(context.Background(), rec, "orch"); err != nil {
		t.Fatal(err)
	}
	if err := n.Ended(context.Background(), rec, "orch", "succeeded", "", []byte("ok")); err != nil {
		t.Fatal(err)
	}
	if strings.Join(paths, ",") != "/api/runs/run-1/start,/api/runs/run-1/end" || auths[0] != "Bearer orch" {
		t.Fatalf("paths=%v auths=%v", paths, auths)
	}
	status = http.StatusUnauthorized
	if err := n.Heartbeat(context.Background(), rec, "orch"); err != ErrAuthority {
		t.Fatalf("401 must map to ErrAuthority, got %v", err)
	}
	status = http.StatusFound
	if err := n.Heartbeat(context.Background(), rec, "orch"); err == nil || err == ErrAuthority {
		t.Fatalf("redirects must not be followed or treated as success, got %v", err)
	}
	for _, bad := range []string{"ftp://x", "https://h/path", "https://u:p@h", "https://h?q=1", "notaurl"} {
		if _, err := NewHTTPNotifier(bad, "/r/{run_id}"); err == nil {
			t.Fatalf("origin %q must be rejected", bad)
		}
	}
	if _, err := NewHTTPNotifier(srv.URL, "/no-placeholder"); err == nil {
		t.Fatalf("path without {run_id} must be rejected")
	}
}

func TestEndCallbackReportsSummaryTruncation(t *testing.T) {
	var bodies []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		bodies = append(bodies, body)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	notifier, err := NewHTTPNotifier(server.URL, "/runs/{run_id}")
	if err != nil {
		t.Fatal(err)
	}
	notifier.MaxSummary = 3
	record := &Record{Key: Key{"inst", "r1"}, Generation: 1}
	if err := notifier.Ended(context.Background(), record, "credential", "succeeded", "", []byte("éé")); err != nil {
		t.Fatal(err)
	}
	record.Result = &ResultInfo{Bytes: 2, Truncated: true}
	if err := notifier.Ended(context.Background(), record, "credential", "succeeded", "", []byte("ok")); err != nil {
		t.Fatal(err)
	}
	record.Result.Truncated = false
	if err := notifier.Ended(context.Background(), record, "credential", "succeeded", "", []byte("ok")); err != nil {
		t.Fatal(err)
	}
	for i, want := range []bool{true, true, false} {
		if bodies[i]["summary_truncated"] != want {
			t.Fatalf("callback %d: %v", i, bodies[i])
		}
	}
	if bodies[0]["summary"] != "é" {
		t.Fatalf("split UTF-8 summary: %v", bodies[0])
	}
}
