package main

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRunLocalRequestUsesNamedPrincipal(t *testing.T) {
	var gotAuth string
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read request body: %v", err)
		}
		gotBody = string(body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	principalsPath := writePrincipalsFixture(t, `{"principals":[{"name":"claw-scheduler","token":"capi_sched","verbs":["schedule.read"],"pods":["ops"]}]}`)
	cfg := config{
		Addr:           strings.TrimPrefix(srv.URL, "http://"),
		PrincipalsPath: principalsPath,
	}

	var stdout bytes.Buffer
	err := runLocalRequest(cfg, &stdout, http.MethodPost, "/schedule/test/fire", `{"bypass_when":true}`, "claw-scheduler", time.Second)
	if err != nil {
		t.Fatalf("runLocalRequest: %v", err)
	}
	if gotAuth != "Bearer capi_sched" {
		t.Fatalf("unexpected auth header: %q", gotAuth)
	}
	if gotBody != `{"bypass_when":true}` {
		t.Fatalf("unexpected request body: %q", gotBody)
	}
	if !strings.Contains(stdout.String(), "\n  \"ok\": true\n") {
		t.Fatalf("expected pretty JSON output, got %q", stdout.String())
	}
}

func TestRunLocalRequestRejectsUnknownPrincipal(t *testing.T) {
	principalsPath := writePrincipalsFixture(t, `{"principals":[{"name":"claw-scheduler","token":"capi_sched","verbs":["schedule.read"],"pods":["ops"]}]}`)
	cfg := config{Addr: "127.0.0.1:8080", PrincipalsPath: principalsPath}
	err := runLocalRequest(cfg, io.Discard, http.MethodGet, "/schedule", "", "missing", time.Second)
	if err == nil || !strings.Contains(err.Error(), `principal "missing" not found`) {
		t.Fatalf("expected missing principal error, got %v", err)
	}
}

func TestRunRequestModeSkipsManifestLoad(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	principalsPath := writePrincipalsFixture(t, `{"principals":[{"name":"claw-scheduler","token":"capi_sched","verbs":["schedule.read"],"pods":["ops"]}]}`)
	t.Setenv("CLAW_API_ADDR", strings.TrimPrefix(srv.URL, "http://"))
	t.Setenv("CLAW_API_PRINCIPALS", principalsPath)
	t.Setenv("CLAW_API_MANIFEST", filepath.Join(t.TempDir(), "missing-manifest.json"))

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := run([]string{"-request-method", "GET", "-request-path", "/schedule", "-request-principal", "claw-scheduler"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("run request mode: %v stderr=%s", err, stderr.String())
	}
	if gotAuth != "Bearer capi_sched" {
		t.Fatalf("unexpected auth header: %q", gotAuth)
	}
}

func writePrincipalsFixture(t *testing.T, raw string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "principals.json")
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatalf("write principals fixture: %v", err)
	}
	return path
}
