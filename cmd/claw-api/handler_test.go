package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	manifestpkg "github.com/mostlydev/clawdapus/internal/clawdash"
	"github.com/mostlydev/clawdapus/internal/clawapi"
)

func TestHandlerHealthEndpoint(t *testing.T) {
	h := newHandler(&manifestpkg.PodManifest{PodName: "ops"}, &clawapi.Store{}, nil, nil, clawapi.DefaultThresholds(), t.TempDir())
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), `"ok":true`) {
		t.Fatalf("unexpected health body: %s", w.Body.String())
	}
}

func TestHandlerStatusRejectsMissingBearer(t *testing.T) {
	h := newHandler(&manifestpkg.PodManifest{PodName: "ops"}, &clawapi.Store{}, nil, nil, clawapi.DefaultThresholds(), t.TempDir())
	req := httptest.NewRequest(http.MethodGet, "/fleet/status", nil)
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestHandlerLogsRejectsInvalidLinesValue(t *testing.T) {
	h := newHandler(
		&manifestpkg.PodManifest{PodName: "ops"},
		&clawapi.Store{Principals: []clawapi.Principal{{
			Name:  "octopus",
			Token: "capi_deadbeef",
			Verbs: []string{clawapi.VerbFleetLogs},
			Pods:  []string{"ops"},
		}}},
		nil,
		nil,
		clawapi.DefaultThresholds(),
		t.TempDir(),
	)
	req := httptest.NewRequest(http.MethodGet, "/fleet/logs?service=westin&lines=bad", nil)
	req.Header.Set("Authorization", "Bearer capi_deadbeef")
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestLogDecisionFallsBackWhenAuditEncodingFails(t *testing.T) {
	var fallback bytes.Buffer
	h := &apiHandler{
		auditLog: json.NewEncoder(failingWriter{}),
		auditErr: &fallback,
	}
	h.logDecision("octopus", clawapi.VerbFleetStatus, "", true, "")
	if !strings.Contains(fallback.String(), "audit logging failed") {
		t.Fatalf("expected fallback audit error, got %q", fallback.String())
	}
}

// --- Write plane tests ---

func newWriteHandler(t *testing.T, govDir string, principals ...clawapi.Principal) http.Handler {
	t.Helper()
	h := newHandler(
		&manifestpkg.PodManifest{PodName: "ops"},
		&clawapi.Store{Principals: principals},
		nil,
		nil,
		clawapi.DefaultThresholds(),
		govDir,
	)
	return h
}

func postJSON(t *testing.T, h http.Handler, path string, body any, token string) *httptest.ResponseRecorder {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func TestHandlerRestartRejectsPathTraversalService(t *testing.T) {
	h := newWriteHandler(t, t.TempDir(), clawapi.Principal{
		Name:  "governor",
		Token: "capi_gov",
		Verbs: clawapi.AllWriteVerbs,
		Pods:  []string{"ops"},
	})
	for _, bad := range []string{"../etc/passwd", "foo/bar", ".", ".."} {
		w := postJSON(t, h, "/fleet/restart", map[string]string{"service": bad}, "capi_gov")
		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 for %q, got %d body=%s", bad, w.Code, w.Body.String())
		}
	}
}

func TestHandlerRestartRequiresAuth(t *testing.T) {
	h := newWriteHandler(t, t.TempDir())
	w := postJSON(t, h, "/fleet/restart", map[string]string{"service": "trader-0"}, "")
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestHandlerRestartRequiresRestartVerb(t *testing.T) {
	h := newWriteHandler(t, t.TempDir(), clawapi.Principal{
		Name:  "reader",
		Token: "capi_reader",
		Verbs: clawapi.AllReadVerbs,
		Pods:  []string{"ops"},
	})
	w := postJSON(t, h, "/fleet/restart", map[string]string{"service": "trader-0"}, "capi_reader")
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestHandlerRestartRejectsOutOfScopeService(t *testing.T) {
	// Principal scoped only to trader-0 (no pod-level scope) should not restart analyst-0.
	h := newWriteHandler(t, t.TempDir(), clawapi.Principal{
		Name:            "governor",
		Token:           "capi_gov",
		Verbs:           clawapi.AllWriteVerbs,
		ComposeServices: []string{"trader-0"},
	})
	w := postJSON(t, h, "/fleet/restart", map[string]string{"service": "analyst-0"}, "capi_gov")
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestHandlerQuarantineWritesMarkerFile(t *testing.T) {
	govDir := t.TempDir()
	h := newWriteHandler(t, govDir, clawapi.Principal{
		Name:            "governor",
		Token:           "capi_gov",
		Verbs:           clawapi.AllWriteVerbs,
		ComposeServices: []string{"trader-0"},
	})
	w := postJSON(t, h, "/fleet/quarantine", map[string]string{"service": "trader-0"}, "capi_gov")
	// No docker client — expect an error response, but the marker file should still be written.
	markerPath := govDir + "/trader-0/quarantine.json"
	data, err := os.ReadFile(markerPath)
	if err != nil {
		t.Fatalf("expected quarantine marker at %s: %v (response: %d %s)", markerPath, err, w.Code, w.Body.String())
	}
	var marker map[string]any
	if err := json.Unmarshal(data, &marker); err != nil {
		t.Fatalf("bad quarantine JSON: %v", err)
	}
	if marker["quarantined"] != true {
		t.Fatalf("expected quarantined=true, got %v", marker["quarantined"])
	}
}

func TestHandlerBudgetSetWritesOverrideFile(t *testing.T) {
	govDir := t.TempDir()
	h := newWriteHandler(t, govDir, clawapi.Principal{
		Name:    "governor",
		Token:   "capi_gov",
		Verbs:   clawapi.AllWriteVerbs,
		ClawIDs: []string{"ops:trader-0"},
	})
	w := postJSON(t, h, "/fleet/budget/set", map[string]any{
		"claw_id":   "ops:trader-0",
		"limit_usd": 2.00,
		"window":    "1h",
		"behavior":  "rate_limit",
	}, "capi_gov")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	data, err := os.ReadFile(govDir + "/ops:trader-0/budget.json")
	if err != nil {
		t.Fatalf("expected budget file: %v", err)
	}
	var budget map[string]any
	if err := json.Unmarshal(data, &budget); err != nil {
		t.Fatalf("bad budget JSON: %v", err)
	}
	if budget["limit_usd"] != 2.0 {
		t.Fatalf("expected limit_usd=2.0, got %v", budget["limit_usd"])
	}
	if budget["behavior"] != "rate_limit" {
		t.Fatalf("expected behavior=rate_limit, got %v", budget["behavior"])
	}
}

func TestHandlerBudgetSetRejectsUnknownBehavior(t *testing.T) {
	govDir := t.TempDir()
	h := newWriteHandler(t, govDir, clawapi.Principal{
		Name:    "governor",
		Token:   "capi_gov",
		Verbs:   clawapi.AllWriteVerbs,
		ClawIDs: []string{"ops:trader-0"},
	})
	w := postJSON(t, h, "/fleet/budget/set", map[string]any{
		"claw_id":   "ops:trader-0",
		"limit_usd": 2.00,
		"window":    "1h",
		"behavior":  "magic",
	}, "capi_gov")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestHandlerModelRestrictWritesOverrideFile(t *testing.T) {
	govDir := t.TempDir()
	h := newWriteHandler(t, govDir, clawapi.Principal{
		Name:    "governor",
		Token:   "capi_gov",
		Verbs:   clawapi.AllWriteVerbs,
		ClawIDs: []string{"ops:trader-0"},
	})
	w := postJSON(t, h, "/fleet/model/restrict", map[string]any{
		"claw_id":        "ops:trader-0",
		"allowed_models": []string{"claude-haiku-4-5"},
	}, "capi_gov")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	data, err := os.ReadFile(govDir + "/ops:trader-0/model-restrict.json")
	if err != nil {
		t.Fatalf("expected model-restrict file: %v", err)
	}
	var restrict map[string]any
	if err := json.Unmarshal(data, &restrict); err != nil {
		t.Fatalf("bad model-restrict JSON: %v", err)
	}
	models, _ := restrict["allowed_models"].([]any)
	if len(models) != 1 || models[0] != "claude-haiku-4-5" {
		t.Fatalf("unexpected allowed_models: %v", models)
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) {
	return 0, errors.New("boom")
}
