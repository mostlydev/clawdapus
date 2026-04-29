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

func TestConversationStoreConsumeDeltaAdvancesCursorWithoutSkipping(t *testing.T) {
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

func TestConversationStoreTailReturnsLatestIdempotent(t *testing.T) {
	store := newConversationStore(50)
	store.merge("chan-1", []wallMessage{
		{ID: "100", Author: "alice", Content: "first", Timestamp: time.Unix(100, 0)},
		{ID: "101", Author: "bob", Content: "second", Timestamp: time.Unix(101, 0)},
		{ID: "102", Author: "carol", Content: "third", Timestamp: time.Unix(102, 0)},
	})

	req := tailRequest{ChannelIDs: []string{"chan-1"}, Limit: 2, Now: time.Unix(200, 0)}
	first := store.tail(req)
	second := store.tail(req)
	for label, got := range map[string]tailResult{"first": first, "second": second} {
		if len(got.Messages) != 2 || got.Messages[0].ID != "101" || got.Messages[1].ID != "102" {
			t.Fatalf("%s tail = %+v", label, got.Messages)
		}
		if got.Available != 3 || got.Omitted != 1 || got.CapReason != "limit" {
			t.Fatalf("%s metadata = %+v", label, got)
		}
	}
}

func TestConversationStoreTailFiltersBySinceWindow(t *testing.T) {
	store := newConversationStore(50)
	now := time.Date(2026, 4, 29, 12, 0, 0, 0, time.UTC)
	store.merge("chan-1", []wallMessage{
		{ID: "100", Author: "alice", Content: "old", Timestamp: now.Add(-48 * time.Hour)},
		{ID: "101", Author: "bob", Content: "recent", Timestamp: now.Add(-2 * time.Hour)},
	})

	got := store.tail(tailRequest{ChannelIDs: []string{"chan-1"}, Since: 24 * time.Hour, Limit: 10, Now: now})
	if len(got.Messages) != 1 || got.Messages[0].ID != "101" {
		t.Fatalf("expected only recent message, got %+v", got.Messages)
	}
	if got.Available != 1 || !got.WindowStart.Equal(now.Add(-24*time.Hour)) {
		t.Fatalf("unexpected tail metadata: %+v", got)
	}
}

func TestConversationStoreTailRespectsMaxCharsWithoutDroppingNewest(t *testing.T) {
	store := newConversationStore(50)
	store.merge("chan-1", []wallMessage{
		{ID: "100", Author: "alice", Content: "older", Timestamp: time.Unix(100, 0)},
		{ID: "101", Author: "bob", Content: strings.Repeat("x", 200), Timestamp: time.Unix(101, 0)},
		{ID: "102", Author: "carol", Content: "newest", Timestamp: time.Unix(102, 0)},
	})

	got := store.tail(tailRequest{ChannelIDs: []string{"chan-1"}, Limit: 10, MaxChars: 80, Now: time.Unix(200, 0)})
	if len(got.Messages) != 1 || got.Messages[0].ID != "102" {
		t.Fatalf("expected newest message to survive max_chars cap, got %+v", got.Messages)
	}
	if got.Omitted != 2 || got.CapReason != "max_chars" {
		t.Fatalf("unexpected max_chars metadata: %+v", got)
	}

	giantNewest := newConversationStore(50)
	giantNewest.merge("chan-1", []wallMessage{
		{ID: "200", Author: "alice", Content: "older", Timestamp: time.Unix(200, 0)},
		{ID: "201", Author: "bob", Content: strings.Repeat("y", 200), Timestamp: time.Unix(201, 0)},
	})
	got = giantNewest.tail(tailRequest{ChannelIDs: []string{"chan-1"}, Limit: 10, MaxChars: 80, Now: time.Unix(300, 0)})
	if len(got.Messages) != 1 || got.Messages[0].ID != "201" {
		t.Fatalf("expected giant newest message to be included, got %+v", got.Messages)
	}
}

func TestConversationStoreTailDoesNotMutateDeltaCursor(t *testing.T) {
	store := newConversationStore(50)
	store.merge("chan-1", []wallMessage{
		{ID: "100", Author: "alice", Content: "first", Timestamp: time.Unix(100, 0)},
		{ID: "101", Author: "bob", Content: "second", Timestamp: time.Unix(101, 0)},
	})

	_ = store.tail(tailRequest{ChannelIDs: []string{"chan-1"}, Limit: 1, Now: time.Unix(200, 0)})
	firstDelta := store.consume("trader-0", []string{"chan-1"}, 1)
	if len(firstDelta) != 1 || firstDelta[0].ID != "100" {
		t.Fatalf("tail moved delta cursor; first delta = %+v", firstDelta)
	}
}

func TestConversationStoreTailAfterCursor(t *testing.T) {
	store := newConversationStore(50)
	store.merge("chan-1", []wallMessage{
		{ID: "100", Author: "alice", Content: "first", Timestamp: time.Unix(100, 0)},
		{ID: "101", Author: "bob", Content: "second", Timestamp: time.Unix(101, 0)},
		{ID: "102", Author: "carol", Content: "third", Timestamp: time.Unix(102, 0)},
	})

	got := store.tail(tailRequest{
		ChannelIDs: []string{"chan-1"},
		After:      map[string]string{"chan-1": "100"},
		Limit:      10,
		Now:        time.Unix(200, 0),
	})
	if len(got.Messages) != 2 || got.Messages[0].ID != "101" || got.Messages[1].ID != "102" {
		t.Fatalf("expected messages after cursor, got %+v", got.Messages)
	}
	if got.Cursor["chan-1"] != "102" {
		t.Fatalf("expected returned cursor chan-1:102, got %+v", got.Cursor)
	}
}

func TestConversationStoreTailAfterAndSinceCompose(t *testing.T) {
	store := newConversationStore(50)
	now := time.Date(2026, 4, 29, 12, 0, 0, 0, time.UTC)
	store.merge("cursor-chan", []wallMessage{
		{ID: "100", Author: "alice", Content: "cursor old", Timestamp: now.Add(-48 * time.Hour)},
		{ID: "101", Author: "alice", Content: "cursor delta", Timestamp: now.Add(-47 * time.Hour)},
	})
	store.merge("bootstrap-chan", []wallMessage{
		{ID: "200", Author: "bob", Content: "bootstrap old", Timestamp: now.Add(-48 * time.Hour)},
		{ID: "201", Author: "bob", Content: "bootstrap recent", Timestamp: now.Add(-2 * time.Hour)},
	})

	got := store.tail(tailRequest{
		ChannelIDs: []string{"cursor-chan", "bootstrap-chan"},
		Since:      24 * time.Hour,
		After:      map[string]string{"cursor-chan": "100"},
		Limit:      10,
		Now:        now,
	})
	if len(got.Messages) != 2 || got.Messages[0].ID != "101" || got.Messages[1].ID != "201" {
		t.Fatalf("expected after-bound cursor channel plus since-bound bootstrap channel, got %+v", got.Messages)
	}
	if got.Cursor["cursor-chan"] != "101" || got.Cursor["bootstrap-chan"] != "201" {
		t.Fatalf("unexpected returned cursor map: %+v", got.Cursor)
	}
}

func TestChannelContextHandlerTailModeIsDefault(t *testing.T) {
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
	if !strings.Contains(string(firstBody), "[channel-context] mode=tail") {
		t.Fatalf("expected tail coverage header, got %q", string(firstBody))
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
	if string(firstBody) != string(secondBody) {
		t.Fatalf("expected stable tail response across repeated fetches\nfirst: %q\nsecond: %q", string(firstBody), string(secondBody))
	}
}

func TestChannelContextHandlerDeltaModePreservesCursorPaging(t *testing.T) {
	store := newConversationStore(50)
	store.merge("chan-1", []wallMessage{
		{ID: "100", Author: "alice", Content: "first", Timestamp: time.Unix(100, 0)},
		{ID: "101", Author: "bob", Content: "second", Timestamp: time.Unix(101, 0)},
		{ID: "102", Author: "carol", Content: "third", Timestamp: time.Unix(102, 0)},
	})

	server := httptest.NewServer(newHandler(store))
	defer server.Close()

	firstResp, err := http.Get(server.URL + "/channel-context?mode=delta&consumer=trader-0&channels=chan-1&limit=2")
	if err != nil {
		t.Fatalf("first GET: %v", err)
	}
	defer firstResp.Body.Close()
	firstBody, err := io.ReadAll(firstResp.Body)
	if err != nil {
		t.Fatalf("read first response: %v", err)
	}
	if strings.Contains(string(firstBody), "[channel-context]") {
		t.Fatalf("delta response should not include tail header: %q", string(firstBody))
	}
	if !strings.Contains(string(firstBody), "first") || !strings.Contains(string(firstBody), "second") || strings.Contains(string(firstBody), "third") {
		t.Fatalf("unexpected first delta body: %q", string(firstBody))
	}

	secondResp, err := http.Get(server.URL + "/channel-context?mode=delta&consumer=trader-0&channels=chan-1&limit=2")
	if err != nil {
		t.Fatalf("second GET: %v", err)
	}
	defer secondResp.Body.Close()
	secondBody, err := io.ReadAll(secondResp.Body)
	if err != nil {
		t.Fatalf("read second response: %v", err)
	}
	if !strings.Contains(string(secondBody), "third") || strings.Contains(string(secondBody), "first") {
		t.Fatalf("unexpected second delta body: %q", string(secondBody))
	}
}

func TestChannelContextHandlerTailAfterCursor(t *testing.T) {
	store := newConversationStore(50)
	store.merge("chan-1", []wallMessage{
		{ID: "100", Author: "alice", Content: "first", Timestamp: time.Unix(100, 0)},
		{ID: "101", Author: "bob", Content: "second", Timestamp: time.Unix(101, 0)},
	})

	server := httptest.NewServer(newHandler(store))
	defer server.Close()

	resp, err := http.Get(server.URL + "/channel-context?channels=chan-1&mode=tail&after=chan-1:100&limit=10")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, string(body))
	}
	text := string(body)
	if strings.Contains(text, "first") || !strings.Contains(text, "second") {
		t.Fatalf("unexpected after response: %q", text)
	}
	if !strings.Contains(text, "after=chan-1:100") || !strings.Contains(text, "cursor=chan-1:101") {
		t.Fatalf("expected after and returned cursor in header, got %q", text)
	}
}

func TestChannelContextHandlerRejectsAfterForUnknownChannel(t *testing.T) {
	server := httptest.NewServer(newHandler(newConversationStore(50)))
	defer server.Close()

	resp, err := http.Get(server.URL + "/channel-context?channels=chan-1&mode=tail&after=chan-2:100")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 400, got %d: %s", resp.StatusCode, string(body))
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
	if cfg.BufferLimit != 500 {
		t.Fatalf("expected default buffer limit 500, got %d", cfg.BufferLimit)
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

func TestDiscordPollerClampsFetchLimitToDiscordMaximum(t *testing.T) {
	poller := newDiscordPoller(nil, newConversationStore(500), []tokenPair{{ChannelID: "chan-1", Token: "token-a"}}, 500)
	if poller.fetchLimit != maxDiscordFetchLimit {
		t.Fatalf("expected fetch limit %d, got %d", maxDiscordFetchLimit, poller.fetchLimit)
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
