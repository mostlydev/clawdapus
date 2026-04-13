package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestParseTokenPairsDeduplicates(t *testing.T) {
	pairs, err := parseTokenPairs("chan-2:token-b,chan-1:token-a,chan-2:token-b,chan-2:token-c")
	if err != nil {
		t.Fatalf("parseTokenPairs: %v", err)
	}

	want := []tokenPair{
		{ChannelID: "chan-1", Token: "token-a"},
		{ChannelID: "chan-2", Token: "token-b"},
		{ChannelID: "chan-2", Token: "token-c"},
	}
	if !slices.Equal(pairs, want) {
		t.Fatalf("unexpected token pairs: got %+v want %+v", pairs, want)
	}
}

func TestConversationStoreConsumeAdvancesCursorWithoutSkipping(t *testing.T) {
	store := newConversationStore(50)
	store.merge("chan-1", []wallMessage{
		{ID: "100", Author: "alice", Content: "first", Timestamp: time.Unix(100, 0)},
		{ID: "101", Author: "bob", Content: "second", Timestamp: time.Unix(101, 0)},
		{ID: "102", Author: "carol", Content: "third", Timestamp: time.Unix(102, 0)},
	})

	first := store.consume("trader-0", []string{"chan-1"}, 2)
	if len(first) != 2 || first[0].ID != "100" || first[1].ID != "101" {
		t.Fatalf("unexpected first page: %+v", first)
	}

	second := store.consume("trader-0", []string{"chan-1"}, 2)
	if len(second) != 1 || second[0].ID != "102" {
		t.Fatalf("unexpected second page: %+v", second)
	}

	quiet := store.consume("trader-0", []string{"chan-1"}, 2)
	// Quiet turn: no new delta, but background context returns last backgroundContextSize messages.
	// With only 3 messages in the buffer and bgLimit=min(10,2)=2, we get the 2 most recent.
	if len(quiet) != 2 || quiet[0].ID != "101" || quiet[1].ID != "102" {
		t.Fatalf("expected background context on quiet turn, got %+v", quiet)
	}
}

func TestChannelContextHandlerReturnsBackgroundContextOnQuietTurn(t *testing.T) {
	store := newConversationStore(50)
	store.merge("chan-1", []wallMessage{
		{
			ID:        "100",
			ChannelID: "chan-1",
			Author:    "alice",
			Content:   "Has anyone reviewed the latest signals?",
			Timestamp: time.Date(2026, 3, 23, 14, 32, 0, 0, time.UTC),
		},
	})

	server := httptest.NewServer(newHandler(store))
	defer server.Close()

	firstResp, err := http.Get(server.URL + "/channel-context?consumer=trader-0&channels=chan-1&limit=20")
	if err != nil {
		t.Fatalf("first GET: %v", err)
	}
	defer firstResp.Body.Close()
	firstBody, err := io.ReadAll(firstResp.Body)
	if err != nil {
		t.Fatalf("read first response: %v", err)
	}
	if firstResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", firstResp.StatusCode)
	}
	if !strings.Contains(string(firstBody), "latest signals") {
		t.Fatalf("expected channel context body, got %q", string(firstBody))
	}

	secondResp, err := http.Get(server.URL + "/channel-context?consumer=trader-0&channels=chan-1&limit=20")
	if err != nil {
		t.Fatalf("second GET: %v", err)
	}
	defer secondResp.Body.Close()
	secondBody, err := io.ReadAll(secondResp.Body)
	if err != nil {
		t.Fatalf("read second response: %v", err)
	}
	if secondResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", secondResp.StatusCode)
	}
	// Quiet turn: no new delta, but background context returns the last message again.
	if !strings.Contains(string(secondBody), "latest signals") {
		t.Fatalf("expected background context on quiet turn, got %q", string(secondBody))
	}
}

func TestLoadConfigDefaultsPollIntervalToThirtySeconds(t *testing.T) {
	t.Setenv("CLAW_WALL_ADDR", "")
	t.Setenv("CLAW_WALL_LIMIT", "")
	t.Setenv("CLAW_WALL_POLL_INTERVAL", "")
	t.Setenv("CLAW_WALL_TOKENS", "chan-1:token-a")

	cfg, err := loadConfig()
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if cfg.PollInterval != 30*time.Second {
		t.Fatalf("expected 30s poll interval, got %s", cfg.PollInterval)
	}
}

func TestLoadConfigUsesPollIntervalOverride(t *testing.T) {
	t.Setenv("CLAW_WALL_ADDR", "")
	t.Setenv("CLAW_WALL_LIMIT", "")
	t.Setenv("CLAW_WALL_POLL_INTERVAL", "42")
	t.Setenv("CLAW_WALL_TOKENS", "chan-1:token-a")

	cfg, err := loadConfig()
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if cfg.PollInterval != 42*time.Second {
		t.Fatalf("expected 42s poll interval, got %s", cfg.PollInterval)
	}
}

func TestDiscordPollerChannelRateLimitSkipsOnlyBlockedPair(t *testing.T) {
	store := newConversationStore(50)
	current := time.Unix(100, 0)
	targets := []tokenPair{
		{ChannelID: "chan-1", Token: "token-a"},
		{ChannelID: "chan-2", Token: "token-a"},
	}

	var (
		mu   sync.Mutex
		hits = make(map[string]int)
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		channelID := strings.Split(strings.Trim(r.URL.Path, "/"), "/")[1]

		mu.Lock()
		hits[channelID]++
		hit := hits[channelID]
		mu.Unlock()

		if channelID == "chan-1" && hit == 1 {
			w.Header().Set("Retry-After", "60")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = io.WriteString(w, `{"message":"rate limited"}`)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `[]`)
	}))
	defer server.Close()

	poller := newDiscordPoller(server.Client(), store, targets, 50)
	poller.baseURL = server.URL
	poller.now = func() time.Time { return current }

	var logs strings.Builder
	poller.pollOnce(context.Background(), &logs)
	if !strings.Contains(logs.String(), "rate limited") {
		t.Fatalf("expected rate-limit log, got %q", logs.String())
	}

	logs.Reset()
	poller.pollOnce(context.Background(), &logs)
	if logs.Len() != 0 {
		t.Fatalf("expected no log while pair is cooled down, got %q", logs.String())
	}

	mu.Lock()
	defer mu.Unlock()
	if hits["chan-1"] != 1 {
		t.Fatalf("expected chan-1 to be skipped after cooldown, got %d hits", hits["chan-1"])
	}
	if hits["chan-2"] != 2 {
		t.Fatalf("expected chan-2 to keep polling, got %d hits", hits["chan-2"])
	}
}

func TestDiscordPollerTokenRateLimitSkipsAllChannelsForToken(t *testing.T) {
	store := newConversationStore(50)
	current := time.Unix(100, 0)
	targets := []tokenPair{
		{ChannelID: "chan-1", Token: "token-a"},
		{ChannelID: "chan-2", Token: "token-a"},
	}

	var (
		mu   sync.Mutex
		hits = make(map[string]int)
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		channelID := strings.Split(strings.Trim(r.URL.Path, "/"), "/")[1]

		mu.Lock()
		hits[channelID]++
		hit := hits[channelID]
		mu.Unlock()

		if channelID == "chan-1" && hit == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = io.WriteString(w, `{"retry_after":60,"global":true}`)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `[]`)
	}))
	defer server.Close()

	poller := newDiscordPoller(server.Client(), store, targets, 50)
	poller.baseURL = server.URL
	poller.now = func() time.Time { return current }

	var logs strings.Builder
	poller.pollOnce(context.Background(), &logs)
	if !strings.Contains(logs.String(), "rate limited") {
		t.Fatalf("expected rate-limit log, got %q", logs.String())
	}

	logs.Reset()
	poller.pollOnce(context.Background(), &logs)
	if logs.Len() != 0 {
		t.Fatalf("expected no log while token is cooled down, got %q", logs.String())
	}

	mu.Lock()
	defer mu.Unlock()
	if hits["chan-1"] != 1 {
		t.Fatalf("expected chan-1 to stop after first global 429, got %d hits", hits["chan-1"])
	}
	if hits["chan-2"] != 0 {
		t.Fatalf("expected chan-2 to be blocked by token cooldown, got %d hits", hits["chan-2"])
	}
}

func TestDiscordPollerNonRateLimitFailureDoesNotCreateCooldown(t *testing.T) {
	store := newConversationStore(50)
	current := time.Unix(100, 0)

	var (
		mu   sync.Mutex
		hits = make(map[string]int)
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		channelID := strings.Split(strings.Trim(r.URL.Path, "/"), "/")[1]

		mu.Lock()
		hits[channelID]++
		mu.Unlock()

		if channelID == "chan-1" {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `[]`)
	}))
	defer server.Close()

	poller := newDiscordPoller(server.Client(), store, []tokenPair{
		{ChannelID: "chan-1", Token: "token-a"},
		{ChannelID: "chan-2", Token: "token-a"},
	}, 50)
	poller.baseURL = server.URL
	poller.now = func() time.Time { return current }

	poller.pollOnce(context.Background(), io.Discard)
	poller.pollOnce(context.Background(), io.Discard)

	mu.Lock()
	defer mu.Unlock()
	if hits["chan-1"] != 2 {
		t.Fatalf("expected chan-1 to keep retrying non-429 errors, got %d hits", hits["chan-1"])
	}
	if hits["chan-2"] != 2 {
		t.Fatalf("expected chan-2 to keep polling, got %d hits", hits["chan-2"])
	}
}

func TestDiscordPollerResumesPollingAfterCooldownExpires(t *testing.T) {
	store := newConversationStore(50)
	current := time.Unix(100, 0)

	var (
		mu   sync.Mutex
		hits = make(map[string]int)
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		channelID := strings.Split(strings.Trim(r.URL.Path, "/"), "/")[1]

		mu.Lock()
		hits[channelID]++
		hit := hits[channelID]
		mu.Unlock()

		if channelID == "chan-1" && hit == 1 {
			w.Header().Set("Retry-After", "2")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = io.WriteString(w, `{"message":"rate limited"}`)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `[]`)
	}))
	defer server.Close()

	poller := newDiscordPoller(server.Client(), store, []tokenPair{
		{ChannelID: "chan-1", Token: "token-a"},
	}, 50)
	poller.baseURL = server.URL
	poller.now = func() time.Time { return current }

	poller.pollOnce(context.Background(), io.Discard)
	poller.pollOnce(context.Background(), io.Discard)

	current = current.Add(3 * time.Second)
	poller.pollOnce(context.Background(), io.Discard)

	mu.Lock()
	defer mu.Unlock()
	if hits["chan-1"] != 2 {
		t.Fatalf("expected polling to resume after cooldown expiry, got %d hits", hits["chan-1"])
	}
}
