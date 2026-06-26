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

func (f *fakeScheduleSource) Get(_ context.Context, id string) (scheduleInvocationView, error) {
	if f.err != nil {
		return scheduleInvocationView{}, f.err
	}
	for _, inv := range f.invocations {
		if inv.ID == id {
			return inv, nil
		}
	}
	return scheduleInvocationView{}, fmt.Errorf("schedule %q not found", id)
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

func (f *fakeScheduleSource) ClearSkipNext(_ context.Context, id string) error {
	f.actions = append(f.actions, "clear-skip-next:"+id)
	return f.err
}

func (f *fakeScheduleSource) Fire(_ context.Context, id string, bypassWhen, bypassPause bool) error {
	f.actions = append(f.actions, fmt.Sprintf("fire:%s:%t:%t", id, bypassWhen, bypassPause))
	return f.err
}

type fakeAgentContextSource struct {
	agents      []agentContextIndexEntry
	contract    agentContractView
	liveContext any
	listErr     error
	contractErr error
	liveErr     error
}

func (f *fakeAgentContextSource) List(_ context.Context) ([]agentContextIndexEntry, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.agents, nil
}

func (f *fakeAgentContextSource) Contract(_ context.Context, _ string) (agentContractView, error) {
	if f.contractErr != nil {
		return agentContractView{}, f.contractErr
	}
	return f.contract, nil
}

func (f *fakeAgentContextSource) LiveContext(_ context.Context, _ string) (any, error) {
	if f.liveErr != nil {
		return nil, f.liveErr
	}
	return f.liveContext, nil
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
	openingBell := time.Date(2026, 4, 6, 13, 30, 0, 0, time.UTC)
	openingLast := openingBell.Add(-24 * time.Hour)
	skipSlot := time.Date(2026, 4, 5, 15, 0, 0, 0, time.UTC)
	skipLast := skipSlot.Add(-2 * time.Hour)
	pausedSlot := time.Date(2026, 4, 5, 16, 0, 0, 0, time.UTC)
	pausedUntil := time.Date(2026, 4, 5, 15, 20, 0, 0, time.UTC)
	degradedSlot := time.Date(2026, 4, 5, 17, 30, 0, 0, time.UTC)
	degradedLast := degradedSlot.Add(-30 * time.Minute)
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
				NextFireAt:  &openingBell,
				LastFiredAt: &openingLast,
				LastStatus:  "fired",
			},
		},
		{
			ManifestInvocation: schedulepkg.ManifestInvocation{
				ID:       "research-pulse",
				Service:  "bot",
				AgentID:  "bot",
				Schedule: "0 11 * * 1-5",
				Timezone: "America/New_York",
				Message:  "Post a research pulse.",
				Name:     "Research Pulse",
				Wake: schedulepkg.Wake{
					Adapter: "openclaw-exec",
					Target:  "bot",
					Command: []string{"openclaw", "cron", "run", "research-pulse"},
				},
			},
			State: schedulepkg.InvocationState{
				NextFireAt:    &skipSlot,
				LastSkippedAt: &skipLast,
				LastStatus:    "skipped",
				LastDetail:    "skip-next",
				SkipNext:      true,
			},
		},
		{
			ManifestInvocation: schedulepkg.ManifestInvocation{
				ID:       "midday-review",
				Service:  "bot",
				AgentID:  "bot",
				Schedule: "0 12 * * 1-5",
				Timezone: "America/New_York",
				Message:  "Review the mid-session state.",
				Name:     "Midday Review",
				Wake: schedulepkg.Wake{
					Adapter: "openclaw-exec",
					Target:  "bot",
					Command: []string{"openclaw", "cron", "run", "midday-review"},
				},
			},
			State: schedulepkg.InvocationState{
				NextFireAt:      &pausedSlot,
				LastStatus:      "scheduled",
				Paused:          true,
				PausedUntil:     &pausedUntil,
				PauseReason:     "operator hold",
				LastEvaluatedAt: &skipLast,
			},
		},
		{
			ManifestInvocation: schedulepkg.ManifestInvocation{
				ID:       "close-watch",
				Service:  "bot",
				AgentID:  "bot",
				Schedule: "30 13 * * 1-5",
				Timezone: "America/New_York",
				Message:  "Watch the close setup.",
				Name:     "Close Watch",
				When: &schedulepkg.When{
					Calendar: "us-equities",
					Session:  schedulepkg.SessionRegular,
				},
				Wake: schedulepkg.Wake{
					Adapter: "openclaw-exec",
					Target:  "bot",
					Command: []string{"openclaw", "cron", "run", "close-watch"},
				},
			},
			State: schedulepkg.InvocationState{
				NextFireAt:          &degradedSlot,
				LastAttemptedAt:     &degradedLast,
				LastStatus:          "wake-error",
				LastDetail:          "docker exec timeout",
				Degraded:            true,
				ConsecutiveFailures: 3,
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

func TestAgentsPageRenders(t *testing.T) {
	source := &fakeAgentContextSource{
		agents: []agentContextIndexEntry{{
			ClawID:         "bot-0",
			Service:        "bot",
			ClawType:       "openclaw",
			HasLiveContext: true,
		}},
	}
	h := newHandler(testManifest(), fakeStatusSource{statuses: testStatuses()}, nil, "http://localhost:8181", false, withAgentContextSource(source))
	req := httptest.NewRequest(http.MethodGet, "/agents", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, "Agent Context") || !strings.Contains(body, "bot-0") {
		t.Fatalf("expected agent context list in body:\n%s", body)
	}
	if !strings.Contains(body, "live snapshot") {
		t.Fatalf("expected live snapshot badge in body:\n%s", body)
	}
}

func TestAgentContextDetailRendersContractAndLiveSnapshot(t *testing.T) {
	source := &fakeAgentContextSource{
		contract: agentContractView{
			ClawID:      "bot-0",
			AgentsMD:    "# Contract",
			ClawdapusMD: "# Infrastructure",
			Metadata: map[string]any{
				"service": "bot",
				"type":    "openclaw",
			},
			Feeds: map[string]any{"feeds": []any{"alerts"}},
			ContextBlocks: map[string]any{
				"version": float64(1),
				"blocks": []any{
					map[string]any{
						"id":        "focus",
						"kind":      "runtime_motivation",
						"text":      "Stay on the active operating contract.",
						"enabled":   true,
						"placement": "after_feeds",
						"cadence":   "every_turn",
						"max_chars": float64(800),
					},
				},
			},
		},
		liveContext: map[string]any{
			"agent_id":   "bot-0",
			"chosen_ref": "openrouter/anthropic/claude-sonnet-4",
			"turn_count": float64(2),
			"placements": []any{
				map[string]any{
					"order":         float64(1),
					"kind":          "feed",
					"label":         "market-open",
					"carrier":       "openai.messages[0].content",
					"message_index": float64(0),
					"block_index":   float64(-1),
					"start_char":    float64(12),
					"end_char":      float64(42),
					"occurrences":   float64(1),
					"persistence":   "per_request",
					"relation":      "system_context_before_conversation",
				},
			},
			"recent_captures": []any{
				map[string]any{
					"sequence":        float64(1),
					"captured_at":     "2026-04-17T20:00:00Z",
					"chosen_ref":      "openrouter/anthropic/claude-sonnet-4",
					"dynamic_inputs":  float64(2),
					"feed_blocks":     float64(1),
					"memory_recall":   true,
					"time_context":    true,
					"placement_count": float64(2),
					"turn_count":      float64(1),
				},
				map[string]any{
					"sequence":        float64(2),
					"captured_at":     "2026-04-17T20:05:00Z",
					"chosen_ref":      "openrouter/anthropic/claude-sonnet-4",
					"dynamic_inputs":  float64(1),
					"feed_blocks":     float64(0),
					"time_context":    true,
					"placement_count": float64(1),
					"turn_count":      float64(2),
					"managed_tool":    true,
				},
			},
		},
	}
	h := newHandler(testManifest(), fakeStatusSource{statuses: testStatuses()}, nil, "http://localhost:8181", false, withAgentContextSource(source))
	req := httptest.NewRequest(http.MethodGet, "/agents/bot-0", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	for _, want := range []string{"# Contract", "# Infrastructure", "alerts", "openclaw", "Context blocks", "context-blocks.json", "Compiled manifests", "Runtime inputs"} {
		if !strings.Contains(body, want) {
			t.Fatalf("expected %q in body:\n%s", want, body)
		}
	}
	if strings.Contains(body, "chosen_ref") {
		t.Fatalf("did not expect live snapshot raw JSON on default contract tab")
	}

	req = httptest.NewRequest(http.MethodGet, "/agents/bot-0?tab=live", nil)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for live tab, got %d body=%s", w.Code, w.Body.String())
	}
	body = w.Body.String()
	for _, want := range []string{"Live context", "chosen_ref", "openrouter/anthropic/claude-sonnet-4", "mediated", "Recent captures", "5m"} {
		if !strings.Contains(body, want) {
			t.Fatalf("expected %q in live tab body:\n%s", want, body)
		}
	}
}

func TestAgentContextPageRequiresSource(t *testing.T) {
	h := newHandler(testManifest(), fakeStatusSource{statuses: testStatuses()}, nil, "http://localhost:8181", false)
	req := httptest.NewRequest(http.MethodGet, "/agents", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestAgentContextHTTPClientUsesBearerAndPaths(t *testing.T) {
	var paths []string
	var auth []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		auth = append(auth, r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/agents":
			_, _ = w.Write([]byte(`{"agents":[{"claw_id":"bot-0","service":"bot","claw_type":"openclaw","has_live_context":true}]}`))
		case "/agents/bot-0/contract":
			_, _ = w.Write([]byte(`{"claw_id":"bot-0","agents_md":"# Contract","clawdapus_md":"# Infra","metadata":{"service":"bot","type":"openclaw"}}`))
		case "/agents/bot-0/context":
			_, _ = w.Write([]byte(`{"agent_id":"bot-0","turn_count":1}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := newAgentContextHTTPClient(server.URL, "dash-token")
	if _, err := client.List(context.Background()); err != nil {
		t.Fatalf("list agents: %v", err)
	}
	if _, err := client.Contract(context.Background(), "bot-0"); err != nil {
		t.Fatalf("contract: %v", err)
	}
	if _, err := client.LiveContext(context.Background(), "bot-0"); err != nil {
		t.Fatalf("live context: %v", err)
	}

	wantPaths := []string{"/agents", "/agents/bot-0/contract", "/agents/bot-0/context"}
	if strings.Join(paths, ",") != strings.Join(wantPaths, ",") {
		t.Fatalf("unexpected paths: got %v want %v", paths, wantPaths)
	}
	for _, got := range auth {
		if got != "Bearer dash-token" {
			t.Fatalf("expected bearer auth, got %q", got)
		}
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
	raw := newHandler(
		testManifest(),
		fakeStatusSource{statuses: testStatuses()},
		&fakeScheduleSource{invocations: testScheduleViews()},
		"http://localhost:8181",
		false,
	)
	h, ok := raw.(*handler)
	if !ok {
		t.Fatal("expected *handler")
	}
	h.now = func() time.Time {
		return time.Date(2026, 4, 5, 14, 0, 0, 0, time.UTC)
	}
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
	if !strings.Contains(body, "Next slot") || !strings.Contains(body, "skip-next armed") {
		t.Fatalf("expected slot-centric card copy in body:\n%s", body)
	}
	if !strings.Contains(body, "Midday Review") || !strings.Contains(body, "Resume") {
		t.Fatalf("expected paused card controls in body:\n%s", body)
	}
	if !strings.Contains(body, "Clear skip-next") || !strings.Contains(body, "Force fire") {
		t.Fatalf("expected overflow actions in body:\n%s", body)
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

func TestScheduleActionClearSkipNextPostsAndRedirects(t *testing.T) {
	scheduleSource := &fakeScheduleSource{invocations: testScheduleViews()}
	h := newHandler(
		testManifest(),
		fakeStatusSource{statuses: testStatuses()},
		scheduleSource,
		"http://localhost:8181",
		false,
	)
	req := httptest.NewRequest(http.MethodPost, "/schedule/research-pulse/clear-skip-next", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected 303, got %d", w.Code)
	}
	if len(scheduleSource.actions) != 1 || scheduleSource.actions[0] != "clear-skip-next:research-pulse" {
		t.Fatalf("expected clear-skip-next action, got %v", scheduleSource.actions)
	}
}

func TestSchedulePauseConvertsDatetimeLocalToUTC(t *testing.T) {
	scheduleSource := &fakeScheduleSource{invocations: testScheduleViews()}
	h := newHandler(
		testManifest(),
		fakeStatusSource{statuses: testStatuses()},
		scheduleSource,
		"http://localhost:8181",
		false,
	)

	req := httptest.NewRequest(
		http.MethodPost,
		"/schedule/opening-bell/pause",
		strings.NewReader("until_local=2026-04-05T10%3A30&reason=market+holiday"),
	)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected 303, got %d", w.Code)
	}
	if len(scheduleSource.actions) != 1 {
		t.Fatalf("expected one action, got %v", scheduleSource.actions)
	}
	if got, want := scheduleSource.actions[0], "pause:opening-bell:2026-04-05T14:30:00Z:market holiday"; got != want {
		t.Fatalf("unexpected pause action:\n got: %s\nwant: %s", got, want)
	}
}

func TestBuildSchedulePageDataPinsPausedAndDegraded(t *testing.T) {
	now := time.Date(2026, 4, 5, 14, 0, 0, 0, time.UTC)
	data := buildSchedulePageData("fleet", testScheduleViews(), "", "", now)
	if len(data.Cards) < 4 {
		t.Fatalf("expected cards, got %d", len(data.Cards))
	}
	if got := data.Cards[0].Name; got != "Midday Review" {
		t.Fatalf("expected paused card pinned first, got %q", got)
	}
	if got := data.Cards[1].Name; got != "Close Watch" {
		t.Fatalf("expected degraded card pinned next, got %q", got)
	}
	if data.Summary[3].Label != "Next slot" {
		t.Fatalf("expected next-slot summary label, got %+v", data.Summary[3])
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}
