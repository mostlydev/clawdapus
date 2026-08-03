//go:build integration

package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	schedulepkg "github.com/mostlydev/clawdapus/internal/schedule"
)

func TestManualFireRequestOutlivesOldFifteenSecondDeadline(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(16 * time.Second)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	principalsPath := writePrincipalsFixture(t, `{"principals":[{"name":"claw-scheduler","token":"capi_sched","verbs":["schedule.control"],"pods":["ops"]}]}`)
	cfg := config{
		Addr:           strings.TrimPrefix(srv.URL, "http://"),
		PrincipalsPath: principalsPath,
	}

	started := time.Now()
	var stdout bytes.Buffer
	err := runLocalRequest(cfg, &stdout, http.MethodPost, "/schedule/test/fire", "", "claw-scheduler", schedulepkg.ManualFireRequestTimeout)
	if err != nil {
		t.Fatalf("manual fire request failed after the old deadline: %v", err)
	}
	if elapsed := time.Since(started); elapsed <= 15*time.Second {
		t.Fatalf("delayed response returned in %v; test must cross the old 15s deadline", elapsed)
	}
	if !strings.Contains(stdout.String(), `"ok": true`) {
		t.Fatalf("unexpected delayed response: %q", stdout.String())
	}
}
