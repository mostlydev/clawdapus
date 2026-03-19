package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	manifestpkg "github.com/mostlydev/clawdapus/internal/clawdash"
	"github.com/mostlydev/clawdapus/internal/clawapi"
)

func TestHandlerHealthEndpoint(t *testing.T) {
	h := newHandler(&manifestpkg.PodManifest{PodName: "ops"}, &clawapi.Store{}, nil, nil)
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
	h := newHandler(&manifestpkg.PodManifest{PodName: "ops"}, &clawapi.Store{}, nil, nil)
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

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) {
	return 0, errors.New("boom")
}
