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
	schedulepkg "github.com/mostlydev/clawdapus/internal/schedule"
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

type fakeScheduleSource struct {
	invocations []scheduleInvocationView
	err         error
	actions     []string
}

func (f *fakeScheduleSource) List(_ context.Context) ([]scheduleInvocationView, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.invocations, nil
}

func (f *fakeScheduleSource) Pause(_ context.Context, id, until, reason string) error {
	f.actions = append(f.actions, fmt.Sprintf("pause:%s:%s:%s", id, until, reason))
	return f.err
}

func (f *fakeScheduleSource) Resume(_ context.Context, id string) error {
	f.actions = append(f.actions, "resume:"+id)
	return f.err
}

func (f *fakeScheduleSource) SkipNext(_ context.Context, id string) error {
	f.actions = append(f.actions, "skip-next:"+id)
	return f.err
}

func (f *fakeScheduleSource) Fire(_ context.Context, id string, bypassWhen, bypassPause bool) error {
	f.actions = append(f.actions, fmt.Sprintf("fire:%s:%t:%t", id, bypassWhen, bypassPause))
	return f.err
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

func testScheduleViews() []scheduleInvocationView {
	nextFire := time.Date(2026, 4, 6, 9, 30, 0, 0, time.UTC)
	lastFire := nextFire.Add(-24 * time.Hour)
	return []scheduleInvocationView{
		{
			ManifestInvocation: schedulepkg.ManifestInvocation{
				ID:       "opening-bell",
				Service:  "bot",
				AgentID:  "bot",
				Schedule: "30 9 * * 1-5",
				Timezone: "America/New_York",
				Message:  "Open the market.",
				Name:     "Opening Bell",
				When: &schedulepkg.When{
					Calendar: "us-equities",
					Session:  schedulepkg.SessionRegular,
				},
				Wake: schedulepkg.Wake{
					Adapter: "openclaw-exec",
					Target:  "bot",
					Command: []string{"openclaw", "cron", "run", "opening-bell"},
				},
			},
			State: schedulepkg.InvocationState{
				NextFireAt:  &nextFire,
				LastFiredAt: &lastFire,
				LastStatus:  "fired",
			},
		},
	}
}

func TestFleetPageRenders(t *testing.T) {
	h := newHandler(testManifest(), fakeStatusSource{statuses: testStatuses()}, nil, "http://localhost:8181", false)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "Fleet Command") {
		t.Fatalf("expected fleet heading in body")
	}
	if !strings.Contains(body, "bot") {
		t.Fatalf("expected service name in body")
	}
	if !strings.Contains(body, "Cllama rollup") {
		t.Fatalf("expected cost rollup panel in body")
	}
	if !strings.Contains(body, "Cost emission not available yet") {
		t.Fatalf("expected costs emission warning in body")
	}
	if !strings.Contains(body, "Priority queue") {
		t.Fatalf("expected attention section in body")
	}
	if strings.Contains(body, "Open cllama dashboard") {
		t.Fatalf("expected costs link to be hidden when /costs/api is unavailable")
	}
}

func TestFleetPageShowsCostLinkWhenCostAPIAvailable(t *testing.T) {
	raw := newHandler(testManifest(), fakeStatusSource{statuses: testStatuses()}, nil, "http://localhost:8181", false)
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
				Body:       io.NopCloser(strings.NewReader(`{"total_cost_usd":1.2345,"total_requests":42,"unpriced_requests":3}`)),
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
	if !strings.Contains(body, "3 request(s) are missing pricing coverage") {
		t.Fatalf("expected unpriced request warning, got body:\n%s", body)
	}
}

func TestTopologyPageRenders(t *testing.T) {
	h := newHandler(testManifest(), fakeStatusSource{statuses: testStatuses()}, nil, "http://localhost:8181", false)
	req := httptest.NewRequest(http.MethodGet, "/topology", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "System Wiring") {
		t.Fatalf("expected topology title in body")
	}
	if !strings.Contains(body, `href="/detail/bot"`) {
		t.Fatalf("expected service detail link in topology body")
	}
}

func TestAPIStatusJSON(t *testing.T) {
	h := newHandler(testManifest(), fakeStatusSource{statuses: testStatuses()}, nil, "http://localhost:8181", false)
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
	h := newHandler(testManifest(), fakeStatusSource{statuses: testStatuses()}, nil, "http://localhost:8181", false)
	req := httptest.NewRequest(http.MethodGet, "/detail/missing", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestSchedulePageRenders(t *testing.T) {
	h := newHandler(
		testManifest(),
		fakeStatusSource{statuses: testStatuses()},
		&fakeScheduleSource{invocations: testScheduleViews()},
		"http://localhost:8181",
		false,
	)
	req := httptest.NewRequest(http.MethodGet, "/schedule", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "Schedule Control") {
		t.Fatalf("expected schedule heading in body")
	}
	if !strings.Contains(body, "Opening Bell") {
		t.Fatalf("expected invocation name in body")
	}
	if !strings.Contains(body, "Fire now") || !strings.Contains(body, "Force fire") {
		t.Fatalf("expected schedule action buttons in body")
	}
}

func TestScheduleActionPostsAndRedirects(t *testing.T) {
	scheduleSource := &fakeScheduleSource{invocations: testScheduleViews()}
	h := newHandler(
		testManifest(),
		fakeStatusSource{statuses: testStatuses()},
		scheduleSource,
		"http://localhost:8181",
		false,
	)
	req := httptest.NewRequest(http.MethodPost, "/schedule/opening-bell/skip-next", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected 303, got %d", w.Code)
	}
	if got := w.Header().Get("Location"); !strings.Contains(got, "/schedule?notice=") {
		t.Fatalf("expected redirect back to /schedule with notice, got %q", got)
	}
	if len(scheduleSource.actions) != 1 || scheduleSource.actions[0] != "skip-next:opening-bell" {
		t.Fatalf("expected skip-next action, got %v", scheduleSource.actions)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}
