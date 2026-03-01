package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	manifestpkg "github.com/mostlydev/clawdapus/internal/clawctl"
	"github.com/mostlydev/clawdapus/internal/driver"
)

type fakeStatusSource struct {
	statuses map[string]serviceStatus
	err      error
}

func (f fakeStatusSource) Snapshot(_ context.Context, _ []string) (map[string]serviceStatus, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.statuses, nil
}

func testManifest() *manifestpkg.PodManifest {
	return &manifestpkg.PodManifest{
		PodName: "fleet",
		Services: map[string]manifestpkg.ServiceManifest{
			"bot": {
				ClawType: "openclaw",
				ImageRef: "bot:latest",
				Count:    1,
				Surfaces: []manifestpkg.SurfaceManifest{
					{Scheme: "channel", Target: "discord"},
					{Scheme: "service", Target: "api"},
					{Scheme: "volume", Target: "shared-data"},
				},
				Cllama: []string{"passthrough"},
				Handles: map[string]*driver.HandleInfo{
					"discord": {ID: "123", Username: "fleet-bot"},
				},
			},
			"api": {
				ImageRef: "api:latest",
				Count:    1,
			},
		},
		Proxies: []manifestpkg.ProxyManifest{
			{ProxyType: "passthrough", ServiceName: "cllama-passthrough", Image: "cllama:latest"},
		},
	}
}

func testStatuses() map[string]serviceStatus {
	return map[string]serviceStatus{
		"bot": {
			Service:   "bot",
			Status:    "healthy",
			State:     "running",
			Uptime:    "3m 2s",
			Instances: 1,
			Running:   1,
		},
		"api": {
			Service:   "api",
			Status:    "running",
			State:     "running",
			Uptime:    "8m 10s",
			Instances: 1,
			Running:   1,
		},
		"cllama-passthrough": {
			Service:   "cllama-passthrough",
			Status:    "healthy",
			State:     "running",
			Uptime:    "3m 1s",
			Instances: 1,
			Running:   1,
		},
	}
}

func TestFleetPageRenders(t *testing.T) {
	h := newHandler(testManifest(), fakeStatusSource{statuses: testStatuses()})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "Fleet Overview") {
		t.Fatalf("expected fleet heading in body")
	}
	if !strings.Contains(body, "bot") {
		t.Fatalf("expected service name in body")
	}
}

func TestTopologyPageRenders(t *testing.T) {
	h := newHandler(testManifest(), fakeStatusSource{statuses: testStatuses()})
	req := httptest.NewRequest(http.MethodGet, "/topology", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "Topology") {
		t.Fatalf("expected topology title in body")
	}
}

func TestAPIStatusJSON(t *testing.T) {
	h := newHandler(testManifest(), fakeStatusSource{statuses: testStatuses()})
	req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var payload struct {
		Services map[string]serviceStatus `json:"services"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if payload.Services["bot"].Status != "healthy" {
		t.Fatalf("expected bot healthy, got %q", payload.Services["bot"].Status)
	}
}

func TestDetailMissingServiceNotFound(t *testing.T) {
	h := newHandler(testManifest(), fakeStatusSource{statuses: testStatuses()})
	req := httptest.NewRequest(http.MethodGet, "/detail/missing", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}
