package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReferenceMemoryRetainDedupeForgetAndReplaySuppression(t *testing.T) {
	dir := t.TempDir()
	store, err := loadMemoryStore(dir)
	if err != nil {
		t.Fatalf("load store: %v", err)
	}

	server := httptest.NewServer(newHandler(store))
	defer server.Close()

	retainReq := memoryRetainRequest{
		AgentID: "analyst",
		Entry: retainedEntry{
			ID:             "hist1_alpha",
			TS:             "2026-04-03T12:00:00Z",
			RequestedModel: "openai/gpt-4o",
			RequestEffective: json.RawMessage(`{
				"messages":[
					{"role":"user","content":"remember the alpha launch date"}
				]
			}`),
			Response: retainedPayload{
				Format: "json",
				JSON: json.RawMessage(`{
					"choices":[
						{"message":{"content":"Alpha launches on Monday at 9am Eastern."}}
					]
				}`),
			},
		},
	}

	postJSONExpect(t, server.URL+"/retain", retainReq, http.StatusAccepted)
	postJSONExpect(t, server.URL+"/retain", retainReq, http.StatusAccepted)

	recall := postJSONDecode[memoryRecallResponse](t, server.URL+"/recall", memoryRecallRequest{
		AgentID: "analyst",
		Messages: []map[string]any{
			{"role": "user", "content": "when does alpha launch?"},
		},
	}, http.StatusOK)
	if len(recall.Memories) != 1 {
		t.Fatalf("expected 1 recalled memory after deduped retain, got %+v", recall.Memories)
	}
	if !strings.Contains(recall.Memories[0].Text, "Alpha launches on Monday") {
		t.Fatalf("unexpected recalled memory: %+v", recall.Memories[0])
	}

	entriesPath := filepath.Join(dir, "analyst", "entries.jsonl")
	entriesData, err := os.ReadFile(entriesPath)
	if err != nil {
		t.Fatalf("read entries file: %v", err)
	}
	if got := countNonEmptyLines(string(entriesData)); got != 1 {
		t.Fatalf("expected exactly 1 persisted retained entry after dedupe, got %d\n%s", got, entriesData)
	}

	postJSONExpect(t, server.URL+"/forget", memoryForgetRequest{
		AgentID:  "analyst",
		EntryIDs: []string{"hist1_alpha"},
		Reason:   "operator requested removal",
	}, http.StatusNoContent)

	recallAfterForget := postJSONDecode[memoryRecallResponse](t, server.URL+"/recall", memoryRecallRequest{
		AgentID: "analyst",
		Messages: []map[string]any{
			{"role": "user", "content": "when does alpha launch?"},
		},
	}, http.StatusOK)
	if len(recallAfterForget.Memories) != 0 {
		t.Fatalf("expected no recalled memories after forget, got %+v", recallAfterForget.Memories)
	}

	postJSONExpect(t, server.URL+"/retain", retainReq, http.StatusAccepted)
	recallAfterReplay := postJSONDecode[memoryRecallResponse](t, server.URL+"/recall", memoryRecallRequest{
		AgentID: "analyst",
		Messages: []map[string]any{
			{"role": "user", "content": "when does alpha launch?"},
		},
	}, http.StatusOK)
	if len(recallAfterReplay.Memories) != 0 {
		t.Fatalf("expected tombstoned entry to stay suppressed after replay, got %+v", recallAfterReplay.Memories)
	}

	tombstonesPath := filepath.Join(dir, "analyst", "tombstones.jsonl")
	tombstonesData, err := os.ReadFile(tombstonesPath)
	if err != nil {
		t.Fatalf("read tombstones file: %v", err)
	}
	if got := countNonEmptyLines(string(tombstonesData)); got != 1 {
		t.Fatalf("expected 1 persisted tombstone, got %d\n%s", got, tombstonesData)
	}

	reloaded, err := loadMemoryStore(dir)
	if err != nil {
		t.Fatalf("reload store: %v", err)
	}
	reloadedServer := httptest.NewServer(newHandler(reloaded))
	defer reloadedServer.Close()

	recallAfterRestart := postJSONDecode[memoryRecallResponse](t, reloadedServer.URL+"/recall", memoryRecallRequest{
		AgentID: "analyst",
		Messages: []map[string]any{
			{"role": "user", "content": "when does alpha launch?"},
		},
	}, http.StatusOK)
	if len(recallAfterRestart.Memories) != 0 {
		t.Fatalf("expected tombstone to persist across restart, got %+v", recallAfterRestart.Memories)
	}
}

func postJSONExpect(t *testing.T, url string, body any, wantStatus int) {
	t.Helper()
	reqBody, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	resp, err := http.Post(url, "application/json", bytes.NewReader(reqBody))
	if err != nil {
		t.Fatalf("post %s: %v", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != wantStatus {
		var got bytes.Buffer
		_, _ = got.ReadFrom(resp.Body)
		t.Fatalf("expected status %d from %s, got %d body=%s", wantStatus, url, resp.StatusCode, got.String())
	}
}

func postJSONDecode[T any](t *testing.T, url string, body any, wantStatus int) T {
	t.Helper()
	reqBody, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	resp, err := http.Post(url, "application/json", bytes.NewReader(reqBody))
	if err != nil {
		t.Fatalf("post %s: %v", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != wantStatus {
		var got bytes.Buffer
		_, _ = got.ReadFrom(resp.Body)
		t.Fatalf("expected status %d from %s, got %d body=%s", wantStatus, url, resp.StatusCode, got.String())
	}

	var decoded T
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		t.Fatalf("decode response from %s: %v", url, err)
	}
	return decoded
}

func countNonEmptyLines(text string) int {
	count := 0
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) != "" {
			count++
		}
	}
	return count
}
