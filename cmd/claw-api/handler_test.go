package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mostlydev/clawdapus/internal/clawapi"
	manifestpkg "github.com/mostlydev/clawdapus/internal/clawdash"
	schedulepkg "github.com/mostlydev/clawdapus/internal/schedule"
)

func TestHandlerHealthEndpoint(t *testing.T) {
	h := newHandler(&manifestpkg.PodManifest{PodName: "ops"}, nil, nil, nil, &clawapi.Store{}, nil, nil, clawapi.DefaultThresholds(), t.TempDir())
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
	h := newHandler(&manifestpkg.PodManifest{PodName: "ops"}, nil, nil, nil, &clawapi.Store{}, nil, nil, clawapi.DefaultThresholds(), t.TempDir())
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
		nil,
		nil,
		nil,
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
		nil,
		nil,
		nil,
		&clawapi.Store{Principals: principals},
		nil,
		nil,
		clawapi.DefaultThresholds(),
		govDir,
	)
	return h
}

func newTestScheduleStateStore(t *testing.T, manifest *schedulepkg.Manifest) *scheduleStateStore {
	t.Helper()
	store, err := newScheduleStateStore(t.TempDir(), manifest)
	if err != nil {
		t.Fatalf("newScheduleStateStore: %v", err)
	}
	return store
}

func newScheduleTestHandler(t *testing.T, manifest *schedulepkg.Manifest, state *scheduleStateStore, scheduler *scheduler, principals ...clawapi.Principal) http.Handler {
	t.Helper()
	return newHandler(
		&manifestpkg.PodManifest{PodName: "ops"},
		manifest,
		state,
		scheduler,
		&clawapi.Store{Principals: principals},
		nil,
		nil,
		clawapi.DefaultThresholds(),
		t.TempDir(),
	)
}

func decodeScheduleInvocationResponse(t *testing.T, w *httptest.ResponseRecorder) scheduleInvocationView {
	t.Helper()
	var resp struct {
		Invocation scheduleInvocationView `json:"invocation"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v body=%s", err, w.Body.String())
	}
	return resp.Invocation
}

func sampleScheduleManifest() *schedulepkg.Manifest {
	return &schedulepkg.Manifest{
		Version: 1,
		Pod:     "ops",
		Invocations: []schedulepkg.ManifestInvocation{{
			ID:       "westin-open",
			Service:  "westin",
			AgentID:  "westin",
			Schedule: "0 9 * * 1-5",
			Timezone: "America/New_York",
			Name:     "Market Open",
			Message:  "Post status.",
			When:     &schedulepkg.When{Calendar: "crypto-24-7", Session: schedulepkg.SessionRegular},
			Wake:     schedulepkg.Wake{Adapter: "openclaw-exec", Target: "westin", Command: []string{"openclaw", "cron", "run", "westin-open"}},
		}},
	}
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

func TestValidateGovernanceTarget(t *testing.T) {
	valid := []string{"trader-0", "analyst-1", "ops:trader-0", "svc"}
	for _, v := range valid {
		if err := validateGovernanceTarget(v); err != nil {
			t.Errorf("expected %q to be valid, got: %v", v, err)
		}
	}
	invalid := []string{"", "../etc/passwd", "foo/bar", ".", "..", ".hidden", "a\\b"}
	for _, v := range invalid {
		if err := validateGovernanceTarget(v); err == nil {
			t.Errorf("expected %q to be rejected", v)
		}
	}
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

func TestHandlerScheduleRejectsMissingBearer(t *testing.T) {
	scheduleManifest := &schedulepkg.Manifest{
		Version: 1,
		Pod:     "ops",
		Invocations: []schedulepkg.ManifestInvocation{{
			ID:       "westin-open",
			Service:  "westin",
			AgentID:  "westin",
			Schedule: "0 9 * * 1-5",
			Timezone: "America/New_York",
			Wake:     schedulepkg.Wake{Adapter: "openclaw-exec", Target: "westin", Command: []string{"openclaw", "cron", "run", "westin-open"}},
		}},
	}
	h := newHandler(
		&manifestpkg.PodManifest{PodName: "ops"},
		scheduleManifest,
		newTestScheduleStateStore(t, scheduleManifest),
		nil,
		&clawapi.Store{},
		nil,
		nil,
		clawapi.DefaultThresholds(),
		t.TempDir(),
	)
	req := httptest.NewRequest(http.MethodGet, "/schedule", nil)
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestHandlerScheduleListFiltersByServiceScope(t *testing.T) {
	scheduleManifest := &schedulepkg.Manifest{
		Version: 1,
		Pod:     "ops",
		Invocations: []schedulepkg.ManifestInvocation{
			{
				ID:       "westin-open",
				Service:  "westin",
				AgentID:  "westin",
				Schedule: "0 9 * * 1-5",
				Timezone: "America/New_York",
				Name:     "Market Open",
				Wake:     schedulepkg.Wake{Adapter: "openclaw-exec", Target: "westin", Command: []string{"openclaw", "cron", "run", "westin-open"}},
			},
			{
				ID:       "analyst-open",
				Service:  "analyst",
				AgentID:  "analyst",
				Schedule: "0 10 * * 1-5",
				Timezone: "America/New_York",
				Name:     "Research Open",
				Wake:     schedulepkg.Wake{Adapter: "hermes-exec", Target: "analyst", Command: []string{"hermes", "cron", "run", "analyst-open"}},
			},
		},
	}
	state := newTestScheduleStateStore(t, scheduleManifest)
	if err := state.Update(func(file *schedulepkg.StateFile) {
		inv := file.Invocations["westin-open"]
		inv.LastStatus = "fired"
		inv.LastDetail = "ok"
		file.Invocations["westin-open"] = inv
	}); err != nil {
		t.Fatalf("state update: %v", err)
	}
	h := newHandler(
		&manifestpkg.PodManifest{PodName: "ops"},
		scheduleManifest,
		state,
		nil,
		&clawapi.Store{Principals: []clawapi.Principal{{
			Name:     "westin-self",
			Token:    "capi_westin",
			Verbs:    []string{clawapi.VerbScheduleRead},
			Services: []string{"westin"},
		}}},
		nil,
		nil,
		clawapi.DefaultThresholds(),
		t.TempDir(),
	)
	req := httptest.NewRequest(http.MethodGet, "/schedule", nil)
	req.Header.Set("Authorization", "Bearer capi_westin")
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Invocations []scheduleInvocationView `json:"invocations"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Invocations) != 1 {
		t.Fatalf("expected 1 invocation, got %d", len(resp.Invocations))
	}
	if resp.Invocations[0].ID != "westin-open" {
		t.Fatalf("expected westin-open, got %+v", resp.Invocations[0])
	}
	if resp.Invocations[0].State.LastStatus != "fired" {
		t.Fatalf("expected persisted last_status, got %+v", resp.Invocations[0].State)
	}
}

func TestHandlerScheduleGetReturnsInvocationDetail(t *testing.T) {
	scheduleManifest := &schedulepkg.Manifest{
		Version: 1,
		Pod:     "ops",
		Invocations: []schedulepkg.ManifestInvocation{{
			ID:       "westin-open",
			Service:  "westin",
			AgentID:  "westin",
			Schedule: "0 9 * * 1-5",
			Timezone: "America/New_York",
			Name:     "Market Open",
			Wake:     schedulepkg.Wake{Adapter: "openclaw-exec", Target: "westin", Command: []string{"openclaw", "cron", "run", "westin-open"}},
		}},
	}
	state := newTestScheduleStateStore(t, scheduleManifest)
	if err := state.Update(func(file *schedulepkg.StateFile) {
		inv := file.Invocations["westin-open"]
		inv.LastStatus = "scheduled"
		inv.LastDetail = "ready"
		file.Invocations["westin-open"] = inv
	}); err != nil {
		t.Fatalf("state update: %v", err)
	}
	h := newHandler(
		&manifestpkg.PodManifest{PodName: "ops"},
		scheduleManifest,
		state,
		nil,
		&clawapi.Store{Principals: []clawapi.Principal{{
			Name:  "master",
			Token: "capi_master",
			Verbs: []string{clawapi.VerbScheduleRead},
			Pods:  []string{"ops"},
		}}},
		nil,
		nil,
		clawapi.DefaultThresholds(),
		t.TempDir(),
	)
	req := httptest.NewRequest(http.MethodGet, "/schedule/westin-open", nil)
	req.Header.Set("Authorization", "Bearer capi_master")
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Invocation scheduleInvocationView `json:"invocation"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Invocation.ID != "westin-open" {
		t.Fatalf("expected westin-open, got %+v", resp.Invocation)
	}
	if resp.Invocation.State.LastDetail != "ready" {
		t.Fatalf("expected state detail=ready, got %+v", resp.Invocation.State)
	}
}

func TestHandlerScheduleGetReturnsNotFoundWhenOutOfScope(t *testing.T) {
	scheduleManifest := &schedulepkg.Manifest{
		Version: 1,
		Pod:     "ops",
		Invocations: []schedulepkg.ManifestInvocation{{
			ID:       "westin-open",
			Service:  "westin",
			AgentID:  "westin",
			Schedule: "0 9 * * 1-5",
			Timezone: "America/New_York",
			Name:     "Market Open",
			Wake:     schedulepkg.Wake{Adapter: "openclaw-exec", Target: "westin", Command: []string{"openclaw", "cron", "run", "westin-open"}},
		}},
	}
	h := newHandler(
		&manifestpkg.PodManifest{PodName: "ops"},
		scheduleManifest,
		newTestScheduleStateStore(t, scheduleManifest),
		nil,
		&clawapi.Store{Principals: []clawapi.Principal{{
			Name:     "analyst-self",
			Token:    "capi_analyst",
			Verbs:    []string{clawapi.VerbScheduleRead},
			Services: []string{"analyst"},
		}}},
		nil,
		nil,
		clawapi.DefaultThresholds(),
		t.TempDir(),
	)
	req := httptest.NewRequest(http.MethodGet, "/schedule/westin-open", nil)
	req.Header.Set("Authorization", "Bearer capi_analyst")
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestHandlerSchedulePauseUpdatesState(t *testing.T) {
	manifest := sampleScheduleManifest()
	state := newTestScheduleStateStore(t, manifest)
	h := newScheduleTestHandler(t, manifest, state, nil, clawapi.Principal{
		Name:     "westin-ops",
		Token:    "capi_westin_ops",
		Verbs:    []string{clawapi.VerbScheduleControl},
		Services: []string{"westin"},
	})

	w := postJSON(t, h, "/schedule/westin-open/pause", map[string]any{"reason": "incident"}, "capi_westin_ops")

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	inv := decodeScheduleInvocationResponse(t, w)
	if !inv.State.Paused {
		t.Fatalf("expected paused state, got %+v", inv.State)
	}
	if inv.State.PauseReason != "incident" {
		t.Fatalf("expected pause reason incident, got %+v", inv.State)
	}
}

func TestHandlerScheduleResumeClearsPause(t *testing.T) {
	manifest := sampleScheduleManifest()
	state := newTestScheduleStateStore(t, manifest)
	until := time.Now().UTC().Add(time.Hour)
	if _, err := state.UpdateInvocation("westin-open", func(state *schedulepkg.InvocationState) error {
		state.Paused = true
		state.PausedUntil = &until
		state.PauseReason = "incident"
		return nil
	}); err != nil {
		t.Fatalf("seed state: %v", err)
	}
	h := newScheduleTestHandler(t, manifest, state, nil, clawapi.Principal{
		Name:     "westin-ops",
		Token:    "capi_westin_ops",
		Verbs:    []string{clawapi.VerbScheduleControl},
		Services: []string{"westin"},
	})

	w := postJSON(t, h, "/schedule/westin-open/resume", map[string]any{}, "capi_westin_ops")

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	inv := decodeScheduleInvocationResponse(t, w)
	if inv.State.Paused || inv.State.PausedUntil != nil || inv.State.PauseReason != "" {
		t.Fatalf("expected cleared pause state, got %+v", inv.State)
	}
}

func TestHandlerScheduleSkipNextSetsFlag(t *testing.T) {
	manifest := sampleScheduleManifest()
	state := newTestScheduleStateStore(t, manifest)
	h := newScheduleTestHandler(t, manifest, state, nil, clawapi.Principal{
		Name:     "westin-ops",
		Token:    "capi_westin_ops",
		Verbs:    []string{clawapi.VerbScheduleControl},
		Services: []string{"westin"},
	})

	w := postJSON(t, h, "/schedule/westin-open/skip-next", map[string]any{}, "capi_westin_ops")

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	inv := decodeScheduleInvocationResponse(t, w)
	if !inv.State.SkipNext {
		t.Fatalf("expected skip_next=true, got %+v", inv.State)
	}
}

func TestHandlerScheduleFireRespectsPauseWithoutBypass(t *testing.T) {
	manifest := sampleScheduleManifest()
	state := newTestScheduleStateStore(t, manifest)
	if _, err := state.UpdateInvocation("westin-open", func(state *schedulepkg.InvocationState) error {
		state.Paused = true
		state.PauseReason = "incident"
		return nil
	}); err != nil {
		t.Fatalf("seed state: %v", err)
	}
	scheduler, err := newScheduler(manifest, nil, state, io.Discard)
	if err != nil {
		t.Fatalf("newScheduler: %v", err)
	}
	h := newScheduleTestHandler(t, manifest, state, scheduler, clawapi.Principal{
		Name:     "westin-ops",
		Token:    "capi_westin_ops",
		Verbs:    []string{clawapi.VerbScheduleControl},
		Services: []string{"westin"},
	})

	w := postJSON(t, h, "/schedule/westin-open/fire", map[string]any{}, "capi_westin_ops")

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	inv := decodeScheduleInvocationResponse(t, w)
	if inv.State.LastStatus != "skipped" || inv.State.LastDetail != "paused-by-operator" {
		t.Fatalf("expected paused skip outcome, got %+v", inv.State)
	}
	if !inv.State.Paused {
		t.Fatalf("expected pause to remain set, got %+v", inv.State)
	}
}

func TestHandlerScheduleFireBypassesPauseWhenRequested(t *testing.T) {
	manifest := sampleScheduleManifest()
	state := newTestScheduleStateStore(t, manifest)
	if _, err := state.UpdateInvocation("westin-open", func(state *schedulepkg.InvocationState) error {
		state.Paused = true
		state.PauseReason = "incident"
		return nil
	}); err != nil {
		t.Fatalf("seed state: %v", err)
	}
	scheduler, err := newScheduler(manifest, nil, state, io.Discard)
	if err != nil {
		t.Fatalf("newScheduler: %v", err)
	}
	h := newScheduleTestHandler(t, manifest, state, scheduler, clawapi.Principal{
		Name:     "westin-ops",
		Token:    "capi_westin_ops",
		Verbs:    []string{clawapi.VerbScheduleControl},
		Services: []string{"westin"},
	})

	w := postJSON(t, h, "/schedule/westin-open/fire", map[string]any{"bypass_pause": true}, "capi_westin_ops")

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	inv := decodeScheduleInvocationResponse(t, w)
	if inv.State.LastStatus != "wake-target-error" {
		t.Fatalf("expected wake-target-error after bypassed pause, got %+v", inv.State)
	}
	if !strings.Contains(inv.State.LastDetail, "docker client unavailable") {
		t.Fatalf("expected docker client error detail, got %+v", inv.State)
	}
	if !inv.State.Paused {
		t.Fatalf("expected pause to remain set, got %+v", inv.State)
	}
}

func TestHandlerScheduleFireBypassesCalendarWhenRequested(t *testing.T) {
	manifest := sampleScheduleManifest()
	manifest.Invocations[0].When = &schedulepkg.When{Calendar: "us-equities", Session: schedulepkg.SessionRegular}
	state := newTestScheduleStateStore(t, manifest)
	scheduler, err := newScheduler(manifest, nil, state, io.Discard)
	if err != nil {
		t.Fatalf("newScheduler: %v", err)
	}
	scheduler.now = func() time.Time {
		return time.Date(2026, time.July, 3, 15, 0, 0, 0, time.UTC)
	}
	h := newScheduleTestHandler(t, manifest, state, scheduler, clawapi.Principal{
		Name:     "westin-ops",
		Token:    "capi_westin_ops",
		Verbs:    []string{clawapi.VerbScheduleControl},
		Services: []string{"westin"},
	})

	skipped := postJSON(t, h, "/schedule/westin-open/fire", map[string]any{}, "capi_westin_ops")
	if skipped.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", skipped.Code, skipped.Body.String())
	}
	skippedInv := decodeScheduleInvocationResponse(t, skipped)
	if skippedInv.State.LastStatus != "skipped" || skippedInv.State.LastDetail != "holiday" {
		t.Fatalf("expected holiday skip, got %+v", skippedInv.State)
	}

	bypassed := postJSON(t, h, "/schedule/westin-open/fire", map[string]any{"bypass_when": true}, "capi_westin_ops")
	if bypassed.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", bypassed.Code, bypassed.Body.String())
	}
	bypassedInv := decodeScheduleInvocationResponse(t, bypassed)
	if bypassedInv.State.LastStatus != "wake-target-error" {
		t.Fatalf("expected wake-target-error after bypassed calendar, got %+v", bypassedInv.State)
	}
	if !strings.Contains(bypassedInv.State.LastDetail, "docker client unavailable") {
		t.Fatalf("expected docker client error detail, got %+v", bypassedInv.State)
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
