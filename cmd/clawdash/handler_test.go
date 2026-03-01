package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	manifestpkg "github.com/mostlydev/clawdapus/internal/clawdash"
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
			{ProxyType: "passthrough", ServiceName: "cllama", Image: "cllama:latest"},
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
		"cllama": {
			Service:   "cllama",
			Status:    "healthy",
			State:     "running",
			Uptime:    "3m 1s",
			Instances: 1,
			Running:   1,
		},
	}
}

func TestFleetPageRenders(t *testing.T) {
	h := newHandler(testManifest(), fakeStatusSource{statuses: testStatuses()}, "http://localhost:8181", false)
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
	if !strings.Contains(body, "Costs") {
		t.Fatalf("expected costs panel in body")
	}
	if !strings.Contains(body, "Cost emission not available yet") {
		t.Fatalf("expected costs emission warning in body")
	}
	if strings.Contains(body, "Open cllama dashboard") {
		t.Fatalf("expected costs link to be hidden when /costs/api is unavailable")
	}
}

func TestFleetPageShowsCostLinkWhenCostAPIAvailable(t *testing.T) {
	raw := newHandler(testManifest(), fakeStatusSource{statuses: testStatuses()}, "http://localhost:8181", false)
	h, ok := raw.(*handler)
	if !ok {
		t.Fatal("expected *handler")
	}
	h.httpClient = &http.Client{
		Timeout: time.Second,
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if req.URL.String() != "http://cllama:8081/costs/api" {
				return nil, fmt.Errorf("unexpected URL: %s", req.URL.String())
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(`{"total_cost_usd":1.2345,"total_requests":42}`)),
			}, nil
		}),
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "$1.2345") {
		t.Fatalf("expected rendered API cost summary, got body:\n%s", body)
	}
	if !strings.Contains(body, "Open cllama dashboard") {
		t.Fatalf("expected costs link when API summary is available")
	}
}

func TestTopologyPageRenders(t *testing.T) {
	h := newHandler(testManifest(), fakeStatusSource{statuses: testStatuses()}, "http://localhost:8181", false)
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
	h := newHandler(testManifest(), fakeStatusSource{statuses: testStatuses()}, "http://localhost:8181", false)
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
	h := newHandler(testManifest(), fakeStatusSource{statuses: testStatuses()}, "http://localhost:8181", false)
	req := httptest.NewRequest(http.MethodGet, "/detail/missing", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}
