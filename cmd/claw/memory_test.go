package main

import (
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

func TestNormalizeBackfillLimit(t *testing.T) {
	limit, err := normalizeBackfillLimit(0)
	if err != nil {
		t.Fatalf("normalizeBackfillLimit(0): %v", err)
	}
	if limit != 0 {
		t.Fatalf("expected 0 to mean unlimited, got %d", limit)
	}
	if _, err := normalizeBackfillLimit(-1); err == nil {
		t.Fatal("expected negative limit to fail")
	}
}

func TestDiscoverMemoryBackfillTargets(t *testing.T) {
	podDir := t.TempDir()
	writeMemoryContextFixture(t, podDir, "analyst", memoryManifestFile{
		Service: "team-memory",
		BaseURL: "http://team-memory:8081",
		Retain:  &memoryOpEntry{Path: "/retain"},
		Auth:    &memoryAuthEntry{Type: "bearer", Token: "memory-token"},
	}, map[string]any{
		"pod":      "desk",
		"service":  "analyst",
		"type":     "openclaw",
		"timezone": "America/New_York",
	})
	writeMemoryContextFixture(t, podDir, "reviewer", memoryManifestFile{
		Service: "other-memory",
		BaseURL: "http://other-memory:9090",
		Retain:  &memoryOpEntry{Path: "/retain"},
	}, map[string]any{
		"pod": "desk",
	})

	targets, err := discoverMemoryBackfillTargets(podDir, "team-memory", []string{"analyst"})
	if err != nil {
		t.Fatalf("discoverMemoryBackfillTargets: %v", err)
	}
	if len(targets) != 1 {
		t.Fatalf("expected 1 target, got %d", len(targets))
	}
	if targets[0].AgentID != "analyst" {
		t.Fatalf("unexpected target agent: %+v", targets[0])
	}
	if targets[0].Manifest.Auth == nil || targets[0].Manifest.Auth.Token != "memory-token" {
		t.Fatalf("expected memory auth token, got %+v", targets[0].Manifest.Auth)
	}
	if !strings.HasSuffix(targets[0].HistoryPath, filepath.Join(".claw-session-history", "analyst", "history.jsonl")) {
		t.Fatalf("unexpected history path: %q", targets[0].HistoryPath)
	}
}

func TestResolveMemoryBackfillURLUsesComposePublishedPort(t *testing.T) {
	dir := t.TempDir()
	composePath := filepath.Join(dir, "compose.generated.yml")
	content := `
services:
  team-memory:
    ports:
      - "127.0.0.1:7400:8081"
`
	if err := os.WriteFile(composePath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := resolveMemoryBackfillURL(composePath, memoryManifestFile{
		Service: "team-memory",
		BaseURL: "http://team-memory:8081",
		Retain:  &memoryOpEntry{Path: "/retain"},
	}, "")
	if err != nil {
		t.Fatalf("resolveMemoryBackfillURL: %v", err)
	}
	if got != "http://127.0.0.1:7400/retain" {
		t.Fatalf("unexpected retain URL: %q", got)
	}
}

func TestResolveMemoryBackfillURLFallsBackToComposePortCommand(t *testing.T) {
	dir := t.TempDir()
	composePath := filepath.Join(dir, "compose.generated.yml")
	content := `
services:
  team-memory:
    ports:
      - "8081"
`
	if err := os.WriteFile(composePath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	prev := runComposeOutputCommand
	runComposeOutputCommand = func(args ...string) ([]byte, error) {
		return []byte("0.0.0.0:49153\n"), nil
	}
	defer func() { runComposeOutputCommand = prev }()

	got, err := resolveMemoryBackfillURL(composePath, memoryManifestFile{
		Service: "team-memory",
		BaseURL: "http://team-memory:8081",
		Retain:  &memoryOpEntry{Path: "/retain"},
	}, "")
	if err != nil {
		t.Fatalf("resolveMemoryBackfillURL: %v", err)
	}
	if got != "http://127.0.0.1:49153/retain" {
		t.Fatalf("unexpected retain URL: %q", got)
	}
}

func TestResolveMemoryForgetURLUsesComposePublishedPort(t *testing.T) {
	dir := t.TempDir()
	composePath := filepath.Join(dir, "compose.generated.yml")
	content := `
services:
  team-memory:
    ports:
      - "127.0.0.1:7400:8081"
`
	if err := os.WriteFile(composePath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := resolveMemoryForgetURL(composePath, memoryManifestFile{
		Service: "team-memory",
		BaseURL: "http://team-memory:8081",
		Forget:  &memoryOpEntry{Path: "/forget"},
	}, "")
	if err != nil {
		t.Fatalf("resolveMemoryForgetURL: %v", err)
	}
	if got != "http://127.0.0.1:7400/forget" {
		t.Fatalf("unexpected forget URL: %q", got)
	}
}

func TestReplayHistoryFileToMemory(t *testing.T) {
	dir := t.TempDir()
	historyPath := filepath.Join(dir, "history.jsonl")
	content := strings.Join([]string{
		`{"ts":"2026-03-31T12:00:00Z","claw_id":"analyst","requested_model":"claude-3-7-sonnet"}`,
		`{"ts":"2026-03-31T12:01:00Z","claw_id":"analyst","requested_model":"claude-3-7-sonnet"}`,
	}, "\n") + "\n"
	if err := os.WriteFile(historyPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	var authHeader string
	var calls []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader = r.Header.Get("Authorization")
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read request body: %v", err)
		}
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatalf("unmarshal request payload: %v", err)
		}
		calls = append(calls, payload)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	target := memoryTarget{
		AgentID:     "analyst",
		HistoryPath: historyPath,
		Pod:         "desk",
		Manifest: memoryManifestFile{
			Service: "team-memory",
			Retain:  &memoryOpEntry{Path: "/retain", TimeoutMS: 1000},
		},
		Metadata: map[string]any{
			"service":  "analyst",
			"type":     "openclaw",
			"timezone": "America/New_York",
		},
	}
	after := time.Date(2026, 3, 31, 12, 0, 0, 0, time.UTC)
	replayed, err := replayHistoryFileToMemory(srv.Client(), srv.URL, "secret-token", backfillRetainTimeout(target.Manifest), target, nil, &after, 0)
	if err != nil {
		t.Fatalf("replayHistoryFileToMemory: %v", err)
	}
	if replayed != 1 {
		t.Fatalf("expected 1 replayed entry, got %d", replayed)
	}
	if authHeader != "Bearer secret-token" {
		t.Fatalf("unexpected auth header: %q", authHeader)
	}
	if len(calls) != 1 {
		t.Fatalf("expected 1 retain call, got %d", len(calls))
	}
	if calls[0]["agent_id"] != "analyst" || calls[0]["pod"] != "desk" {
		t.Fatalf("unexpected top-level retain payload: %+v", calls[0])
	}
	metadata := calls[0]["metadata"].(map[string]any)
	if metadata["path"] != "retain" || metadata["requested_model"] != "claude-3-7-sonnet" || metadata["timezone"] != "America/New_York" {
		t.Fatalf("unexpected metadata payload: %+v", metadata)
	}
	entry := calls[0]["entry"].(map[string]any)
	if entry["claw_id"] != "analyst" {
		t.Fatalf("unexpected replayed entry: %+v", entry)
	}
	if entry["id"] == "" {
		t.Fatalf("expected replayed entry ID, got %+v", entry)
	}
}

func TestReplayHistoryFileToMemoryHonorsLimit(t *testing.T) {
	dir := t.TempDir()
	historyPath := filepath.Join(dir, "history.jsonl")
	content := strings.Join([]string{
		`{"ts":"2026-03-31T12:00:00Z","claw_id":"analyst","requested_model":"m1"}`,
		`{"ts":"2026-03-31T12:01:00Z","claw_id":"analyst","requested_model":"m2"}`,
	}, "\n") + "\n"
	if err := os.WriteFile(historyPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	target := memoryTarget{
		AgentID:     "analyst",
		HistoryPath: historyPath,
		Manifest: memoryManifestFile{
			Service: "team-memory",
			Retain:  &memoryOpEntry{Path: "/retain"},
		},
	}
	replayed, err := replayHistoryFileToMemory(srv.Client(), srv.URL, "", backfillRetainTimeout(target.Manifest), target, nil, nil, 1)
	if err != nil {
		t.Fatalf("replayHistoryFileToMemory: %v", err)
	}
	if replayed != 1 || calls != 1 {
		t.Fatalf("expected 1 replayed entry and call, got replayed=%d calls=%d", replayed, calls)
	}
}

func TestReplayHistoryFileToMemoryHydratesLegacyEntryIDs(t *testing.T) {
	dir := t.TempDir()
	historyPath := filepath.Join(dir, "history.jsonl")
	if err := os.WriteFile(historyPath, []byte(`{"ts":"2026-03-31T12:00:00Z","claw_id":"analyst","requested_model":"m1"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	retainCh := make(chan map[string]any, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode retain payload: %v", err)
		}
		retainCh <- payload
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	target := memoryTarget{
		AgentID:     "analyst",
		HistoryPath: historyPath,
		Manifest: memoryManifestFile{
			Service: "team-memory",
			Retain:  &memoryOpEntry{Path: "/retain"},
		},
	}
	if _, err := replayHistoryFileToMemory(srv.Client(), srv.URL, "", backfillRetainTimeout(target.Manifest), target, nil, nil, 1); err != nil {
		t.Fatalf("replayHistoryFileToMemory: %v", err)
	}

	payload := <-retainCh
	entry := payload["entry"].(map[string]any)
	if entry["id"] == "" {
		t.Fatalf("expected hydrated backfill entry ID, got %+v", entry)
	}
}

func TestReplayHistoryFileToMemoryReturnsZeroForMissingHistory(t *testing.T) {
	target := memoryTarget{
		AgentID:     "analyst",
		HistoryPath: filepath.Join(t.TempDir(), "missing.jsonl"),
		Manifest: memoryManifestFile{
			Service: "team-memory",
			Retain:  &memoryOpEntry{Path: "/retain"},
		},
	}
	replayed, err := replayHistoryFileToMemory(&http.Client{}, "http://example.invalid/retain", "", backfillRetainTimeout(target.Manifest), target, nil, nil, 0)
	if err != nil {
		t.Fatalf("replayHistoryFileToMemory: %v", err)
	}
	if replayed != 0 {
		t.Fatalf("expected 0 replayed entries, got %d", replayed)
	}
}

func TestReplayHistoryFileToMemoryFailsOnNon2xx(t *testing.T) {
	dir := t.TempDir()
	historyPath := filepath.Join(dir, "history.jsonl")
	if err := os.WriteFile(historyPath, []byte(`{"ts":"2026-03-31T12:00:00Z","claw_id":"analyst","requested_model":"m1"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusBadGateway)
	}))
	defer srv.Close()

	target := memoryTarget{
		AgentID:     "analyst",
		HistoryPath: historyPath,
		Manifest: memoryManifestFile{
			Service: "team-memory",
			Retain:  &memoryOpEntry{Path: "/retain"},
		},
	}
	_, err := replayHistoryFileToMemory(srv.Client(), srv.URL, "", backfillRetainTimeout(target.Manifest), target, nil, nil, 0)
	if err == nil || !strings.Contains(err.Error(), "retain returned status 502") {
		t.Fatalf("expected non-2xx retain error, got %v", err)
	}
}

func TestReplayMemoryBackfillUsesPerTargetAuthAndTimeout(t *testing.T) {
	dir := t.TempDir()
	historyPath := filepath.Join(dir, "history.jsonl")
	if err := os.WriteFile(historyPath, []byte(`{"ts":"2026-03-31T12:00:00Z","claw_id":"analyst","requested_model":"m1"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	auths := make(chan string, 2)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auths <- r.Header.Get("Authorization")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	targets := []memoryTarget{
		{
			AgentID:     "a1",
			HistoryPath: historyPath,
			Manifest: memoryManifestFile{
				Service: "team-memory",
				Retain:  &memoryOpEntry{Path: "/retain", TimeoutMS: 25},
				Auth:    &memoryAuthEntry{Type: "bearer", Token: "token-1"},
			},
		},
		{
			AgentID:     "a2",
			HistoryPath: historyPath,
			Manifest: memoryManifestFile{
				Service: "team-memory",
				Retain:  &memoryOpEntry{Path: "/retain", TimeoutMS: 25},
				Auth:    &memoryAuthEntry{Type: "bearer", Token: "token-2"},
			},
		},
	}

	if _, err := replayMemoryBackfill(io.Discard, srv.URL, "", targets, nil, 1); err != nil {
		t.Fatalf("replayMemoryBackfill: %v", err)
	}

	got := []string{<-auths, <-auths}
	if !(got[0] == "Bearer token-1" && got[1] == "Bearer token-2" || got[0] == "Bearer token-2" && got[1] == "Bearer token-1") {
		t.Fatalf("unexpected auth headers: %+v", got)
	}
}

func TestForgetMemoryEntriesWritesTombstonesAndBackfillSkipsThem(t *testing.T) {
	podDir := t.TempDir()
	historyDir := filepath.Join(podDir, ".claw-session-history", "analyst")
	if err := os.MkdirAll(historyDir, 0o755); err != nil {
		t.Fatal(err)
	}

	line1 := []byte(`{"ts":"2026-03-31T12:00:00Z","claw_id":"analyst","requested_model":"m1"}`)
	line2 := []byte(`{"ts":"2026-03-31T12:01:00Z","claw_id":"analyst","requested_model":"m2"}`)
	if err := os.WriteFile(filepath.Join(historyDir, "history.jsonl"), append(append(append([]byte(nil), line1...), '\n'), append(line2, '\n')...), 0o644); err != nil {
		t.Fatal(err)
	}
	_, meta1, err := ensureHistoryEntryID(line1)
	if err != nil {
		t.Fatalf("ensureHistoryEntryID(line1): %v", err)
	}
	_, meta2, err := ensureHistoryEntryID(line2)
	if err != nil {
		t.Fatalf("ensureHistoryEntryID(line2): %v", err)
	}

	var forgetPayload map[string]any
	var forgetAuth string
	forgetSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		forgetAuth = r.Header.Get("Authorization")
		if err := json.NewDecoder(r.Body).Decode(&forgetPayload); err != nil {
			t.Fatalf("decode forget payload: %v", err)
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer forgetSrv.Close()

	target := memoryTarget{
		PodDir:      podDir,
		AgentID:     "analyst",
		HistoryPath: filepath.Join(historyDir, "history.jsonl"),
		Pod:         "desk",
		Manifest: memoryManifestFile{
			Service: "team-memory",
			Retain:  &memoryOpEntry{Path: "/retain"},
			Forget:  &memoryOpEntry{Path: "/forget"},
		},
		Metadata: map[string]any{
			"service":  "analyst",
			"type":     "openclaw",
			"timezone": "America/New_York",
		},
	}

	summary, err := forgetMemoryEntries(io.Discard, forgetSrv.URL, "secret-token", []memoryTarget{target}, []string{meta1.ID}, "policy_redaction")
	if err != nil {
		t.Fatalf("forgetMemoryEntries: %v", err)
	}
	if summary.Entries != 1 || summary.Agents != 1 || summary.AlreadyForgotten != 0 {
		t.Fatalf("unexpected forget summary: %+v", summary)
	}
	if forgetAuth != "Bearer secret-token" {
		t.Fatalf("unexpected forget auth header: %q", forgetAuth)
	}
	if forgetPayload["agent_id"] != "analyst" || forgetPayload["pod"] != "desk" || forgetPayload["reason"] != "policy_redaction" {
		t.Fatalf("unexpected forget payload: %+v", forgetPayload)
	}
	entryIDs, _ := forgetPayload["entry_ids"].([]any)
	if len(entryIDs) != 1 || entryIDs[0] != meta1.ID {
		t.Fatalf("unexpected forget entry ids: %+v", forgetPayload)
	}
	metadata, _ := forgetPayload["metadata"].(map[string]any)
	if metadata["path"] != "forget" || metadata["timezone"] != "America/New_York" {
		t.Fatalf("unexpected forget metadata: %+v", metadata)
	}

	tombstones, err := loadMemoryForgetTombstones(podDir, "analyst", "team-memory")
	if err != nil {
		t.Fatalf("loadMemoryForgetTombstones: %v", err)
	}
	if _, ok := tombstones[meta1.ID]; !ok {
		t.Fatalf("expected tombstone for %s, got %+v", meta1.ID, tombstones)
	}

	var retained []string
	retainSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode retain payload: %v", err)
		}
		entry := payload["entry"].(map[string]any)
		retained = append(retained, entry["id"].(string))
		w.WriteHeader(http.StatusNoContent)
	}))
	defer retainSrv.Close()

	tombstoneIDs, err := loadMemoryTombstoneIDSet(podDir, "analyst", "team-memory")
	if err != nil {
		t.Fatalf("loadMemoryTombstoneIDSet: %v", err)
	}
	replayed, err := replayHistoryFileToMemory(retainSrv.Client(), retainSrv.URL, "", backfillRetainTimeout(target.Manifest), target, tombstoneIDs, nil, 0)
	if err != nil {
		t.Fatalf("replayHistoryFileToMemory: %v", err)
	}
	if replayed != 1 {
		t.Fatalf("expected 1 replayed entry after tombstone filtering, got %d", replayed)
	}
	if len(retained) != 1 || retained[0] != meta2.ID {
		t.Fatalf("expected only non-forgotten entry %s to replay, got %+v", meta2.ID, retained)
	}
}

func TestForgetMemoryEntriesRejectsMissingEntryID(t *testing.T) {
	podDir := t.TempDir()
	historyDir := filepath.Join(podDir, ".claw-session-history", "analyst")
	if err := os.MkdirAll(historyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(historyDir, "history.jsonl"), []byte(`{"ts":"2026-03-31T12:00:00Z","claw_id":"analyst","requested_model":"m1"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	target := memoryTarget{
		PodDir:      podDir,
		AgentID:     "analyst",
		HistoryPath: filepath.Join(historyDir, "history.jsonl"),
		Manifest: memoryManifestFile{
			Service: "team-memory",
			Forget:  &memoryOpEntry{Path: "/forget"},
		},
	}

	_, err := forgetMemoryEntries(io.Discard, srv.URL, "", []memoryTarget{target}, []string{"hist1_missing"}, "")
	if err == nil || !strings.Contains(err.Error(), "history does not contain entry id") {
		t.Fatalf("expected missing history entry error, got %v", err)
	}
	if calls != 0 {
		t.Fatalf("expected no forget HTTP calls, got %d", calls)
	}
	if _, statErr := os.Stat(memoryForgetTombstonePath(podDir, "analyst")); !os.IsNotExist(statErr) {
		t.Fatalf("expected no tombstone file, stat err=%v", statErr)
	}
}

func writeMemoryContextFixture(t *testing.T, podDir, agentID string, manifest memoryManifestFile, metadata map[string]any) {
	t.Helper()
	contextDir := filepath.Join(podDir, ".claw-runtime", "context", agentID)
	if err := os.MkdirAll(contextDir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifestRaw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(contextDir, "memory.json"), append(manifestRaw, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	metadataRaw, err := json.Marshal(metadata)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(contextDir, "metadata.json"), append(metadataRaw, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}
