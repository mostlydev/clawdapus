package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
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
	if !strings.Contains(string(firstBody), "[channel-context] kind=tail mode=tail") {
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

func TestChannelAwarenessHandlerReturnsRawWindow(t *testing.T) {
	store := newConversationStore(50)
	store.merge("chan-1", []wallMessage{
		{ID: "100", Author: "alice", Content: "older signal", Timestamp: time.Unix(100, 0)},
		{ID: "101", Author: "bob", Content: "newer signal", Timestamp: time.Unix(101, 0)},
	})

	server := httptest.NewServer(newHandler(store))
	defer server.Close()

	resp, err := http.Get(server.URL + "/channel-awareness?channels=chan-1&limit=1&max_chars=4096")
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
	if !strings.Contains(text, "[channel-awareness] kind=raw_window") || !strings.Contains(text, "retained=2/since-all") || !strings.Contains(text, "digest=unavailable") {
		t.Fatalf("expected raw awareness header, got %q", text)
	}
	if strings.Contains(text, "older signal") || !strings.Contains(text, "newer signal") {
		t.Fatalf("expected newest bounded awareness body, got %q", text)
	}
	if !strings.Contains(text, "source=chan-1/101") {
		t.Fatalf("expected stable source handle in awareness body, got %q", text)
	}
}

func TestChannelAwarenessHeaderReportsBackfillStatus(t *testing.T) {
	store := newConversationStore(50)
	store.merge("chan-1", []wallMessage{
		{ID: "100", Author: "alice", Content: "signal", Timestamp: time.Unix(100, 0)},
	})
	store.setBackfillStatus("chan-1", backfillStatusPartial)

	server := httptest.NewServer(newHandler(store))
	defer server.Close()

	resp, err := http.Get(server.URL + "/channel-awareness?channels=chan-1&max_chars=4096")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	if !strings.Contains(string(body), "backfill_status=partial") {
		t.Fatalf("expected backfill status in header, got %q", string(body))
	}
}

func TestChannelAwarenessHandlerServesDigestWhenAvailable(t *testing.T) {
	store := newConversationStore(50)
	store.now = func() time.Time { return time.Unix(112, 0) }
	for i := 0; i < 12; i++ {
		store.merge("chan-1", []wallMessage{{
			ID:        fmt.Sprintf("%d", 100+i),
			Author:    "agent-status",
			Content:   fmt.Sprintf("HEARTBEAT_OK message-%03d", i),
			Timestamp: time.Unix(int64(100+i), 0),
		}})
	}

	memory := newDigestChannelMemoryServer(t, channelMemoryDigestResponse{
		Status:      "ok",
		GeneratedAt: "2026-05-21T20:00:00Z",
		Coverage: channelMemoryDigestCoverage{
			SourceMessages: 12,
			DigestMessages: 1,
		},
		Blocks: []channelMemoryDigestBlock{{
			Kind:           "telemetry_count",
			Text:           "[00:01-00:02] runtime/status noise elided: 2 messages.",
			SourceChannel:  "chan-1",
			SourceMessages: []string{"100", "101"},
			SourceWindow: channelMemorySourceWindow{
				From: "1970-01-01T00:01:40Z",
				To:   "1970-01-01T00:01:41Z",
			},
			Sparse:      true,
			Score:       0.25,
			GeneratedAt: "2026-05-21T20:00:00Z",
			Processor:   "deterministic",
		}},
		Cost: channelMemoryDigestCost{DeterministicOnly: true},
	})
	defer memory.Close()
	client, err := newChannelMemoryClientWithDigest("", memory.URL+"/digest", "memory-token", time.Second)
	if err != nil {
		t.Fatalf("newChannelMemoryClientWithDigest: %v", err)
	}

	server := httptest.NewServer(newHandler(store, handlerConfig{
		channelMemory: client,
		toolToken:     "tool-token",
		agentChannels: map[string]map[string]struct{}{
			"trader-0": {"chan-1": {}},
		},
	}))
	defer server.Close()

	resp, err := http.Get(server.URL + "/channel-awareness?channels=chan-1&since=24h&limit=12&max_chars=4096&context_kind=raw_window%2Bdigest")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	text := string(body)
	if !strings.Contains(text, "kind=raw_window+digest") || !strings.Contains(text, "digest=ok") || !strings.Contains(text, "deterministic_only=true") {
		t.Fatalf("expected digest awareness header, got %q", text)
	}
	if !strings.Contains(text, "digest_blocks=1") || !strings.Contains(text, "digest_source_messages=12") {
		t.Fatalf("expected digest counts, got %q", text)
	}
	if !strings.Contains(text, "[digest kind=telemetry_count source_channel=chan-1 source_messages=100,101") {
		t.Fatalf("expected digest block provenance, got %q", text)
	}
	if strings.Contains(text, "source=chan-1/100") || !strings.Contains(text, "source=chan-1/111") {
		t.Fatalf("expected digest mode to keep only recent raw messages, got %q", text)
	}
	gotReqs := memory.requests()
	if len(gotReqs) != 1 || gotReqs[0].SourceKind != channelMemorySourceKind || gotReqs[0].Since != "24h0m0s" || gotReqs[0].Budget.MaxBlocks != defaultDigestMaxBlocks {
		t.Fatalf("unexpected digest request: %+v", gotReqs)
	}

	req, err := http.NewRequest(http.MethodPost, server.URL+"/get_channel_messages", strings.NewReader(`{"channels":["chan-1"],"message_ids":["100","101"]}`))
	if err != nil {
		t.Fatalf("build source retrieval request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer tool-token")
	req.Header.Set("X-Claw-ID", "trader-0")
	retrievalResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("source retrieval: %v", err)
	}
	defer retrievalResp.Body.Close()
	if retrievalResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(retrievalResp.Body)
		t.Fatalf("expected source retrieval 200, got %d: %s", retrievalResp.StatusCode, string(body))
	}
	var exact retrievalResult
	if err := json.NewDecoder(retrievalResp.Body).Decode(&exact); err != nil {
		t.Fatalf("decode source retrieval: %v", err)
	}
	if exact.Status != "ok" || len(exact.Messages) != 2 || exact.Messages[0].ID != "100" || exact.Messages[1].ID != "101" {
		t.Fatalf("digest source provenance did not round-trip to exact messages: %+v", exact)
	}
}

func TestChannelAwarenessDigestUnavailableFallsBackToRawWindow(t *testing.T) {
	store := newConversationStore(50)
	store.now = func() time.Time { return time.Unix(212, 0) }
	for i := 0; i < 12; i++ {
		store.merge("chan-1", []wallMessage{{
			ID:        fmt.Sprintf("%d", 200+i),
			Author:    "alice",
			Content:   fmt.Sprintf("raw-message-%03d", i),
			Timestamp: time.Unix(int64(200+i), 0),
		}})
	}

	memory := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "digest unavailable", http.StatusBadGateway)
	}))
	defer memory.Close()
	client, err := newChannelMemoryClientWithDigest("", memory.URL+"/digest", "", time.Second)
	if err != nil {
		t.Fatalf("newChannelMemoryClientWithDigest: %v", err)
	}

	server := httptest.NewServer(newHandler(store, handlerConfig{channelMemory: client}))
	defer server.Close()

	resp, err := http.Get(server.URL + "/channel-awareness?channels=chan-1&since=24h&limit=12&max_chars=4096&context_kind=raw_window%2Bdigest")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	text := string(body)
	if !strings.Contains(text, "digest=unavailable") || !strings.Contains(text, "raw-message-000") || !strings.Contains(text, "raw-message-011") {
		t.Fatalf("expected full raw fallback on digest failure, got %q", text)
	}
}

func TestChannelAwarenessDigestStaleAndCoverageGapStatuses(t *testing.T) {
	store := newConversationStore(50)
	store.merge("chan-1", []wallMessage{
		{ID: "300", Author: "alice", Content: "raw fallback", Timestamp: time.Unix(300, 0)},
	})

	for _, tc := range []struct {
		name     string
		response channelMemoryDigestResponse
		want     string
	}{
		{
			name: "stale",
			response: channelMemoryDigestResponse{
				Status: "ok",
				Blocks: []channelMemoryDigestBlock{{
					Kind:           "raw_excerpt",
					Text:           "stale digest",
					SourceChannel:  "chan-1",
					SourceMessages: []string{"299"},
					Stale:          true,
					Processor:      "deterministic",
				}},
				Cost: channelMemoryDigestCost{DeterministicOnly: true},
			},
			want: "digest=stale",
		},
		{
			name: "coverage-gap",
			response: channelMemoryDigestResponse{
				Status: "coverage_gap",
				Coverage: channelMemoryDigestCoverage{
					Gaps: []channelMemoryCoverageGap{{ChannelID: "chan-1", From: "2026-05-21T19:00:00Z", To: "2026-05-21T19:15:00Z"}},
				},
				Blocks: []channelMemoryDigestBlock{{
					Kind:           "raw_excerpt",
					Text:           "partial digest",
					SourceChannel:  "chan-1",
					SourceMessages: []string{"299"},
					Processor:      "deterministic",
				}},
				Cost: channelMemoryDigestCost{DeterministicOnly: true},
			},
			want: "digest=coverage_gap",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			memory := newDigestChannelMemoryServer(t, tc.response)
			defer memory.Close()
			client, err := newChannelMemoryClientWithDigest("", memory.URL+"/digest", "", time.Second)
			if err != nil {
				t.Fatalf("newChannelMemoryClientWithDigest: %v", err)
			}
			server := httptest.NewServer(newHandler(store, handlerConfig{channelMemory: client}))
			defer server.Close()
			resp, err := http.Get(server.URL + "/channel-awareness?channels=chan-1&context_kind=raw_window%2Bdigest")
			if err != nil {
				t.Fatalf("GET: %v", err)
			}
			defer resp.Body.Close()
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatalf("read response: %v", err)
			}
			if !strings.Contains(string(body), tc.want) {
				t.Fatalf("expected %s in response, got %q", tc.want, string(body))
			}
		})
	}
}

func TestToolSearchRequiresAuthAndChannelAllowlist(t *testing.T) {
	store := newConversationStore(50)
	store.merge("chan-1", []wallMessage{
		{ID: "100", Author: "alice", Content: "alpha signal", Timestamp: time.Unix(100, 0)},
	})
	store.merge("chan-2", []wallMessage{
		{ID: "200", Author: "bob", Content: "beta signal", Timestamp: time.Unix(200, 0)},
	})

	server := httptest.NewServer(newHandler(store, handlerConfig{
		toolToken: "tool-token",
		agentChannels: map[string]map[string]struct{}{
			"trader-0": {"chan-1": {}},
		},
	}))
	defer server.Close()

	req, err := http.NewRequest(http.MethodPost, server.URL+"/search_channel_context", strings.NewReader(`{"channels":["chan-1"],"query":"alpha"}`))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer tool-token")
	req.Header.Set("X-Claw-ID", "trader-0")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, string(body))
	}
	var result retrievalResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if result.Status != "ok" || len(result.Messages) != 1 || result.Messages[0].ID != "100" {
		t.Fatalf("unexpected search result: %+v", result)
	}
	if result.Messages[0].SourceHandle != "chan-1/100" {
		t.Fatalf("expected source handle in search result, got %+v", result.Messages[0])
	}

	req, err = http.NewRequest(http.MethodPost, server.URL+"/get_channel_messages", strings.NewReader(`{"channels":["chan-1"],"message_ids":["100"]}`))
	if err != nil {
		t.Fatalf("request exact message: %v", err)
	}
	req.Header.Set("Authorization", "Bearer tool-token")
	req.Header.Set("X-Claw-ID", "trader-0")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST exact message: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200 for exact message, got %d: %s", resp.StatusCode, string(body))
	}
	var exact retrievalResult
	if err := json.NewDecoder(resp.Body).Decode(&exact); err != nil {
		t.Fatalf("decode exact response: %v", err)
	}
	if exact.Status != "ok" || len(exact.Messages) != 1 || exact.Messages[0].SourceHandle != "chan-1/100" {
		t.Fatalf("unexpected exact message result: %+v", exact)
	}

	req, err = http.NewRequest(http.MethodPost, server.URL+"/get_channel_messages", strings.NewReader(`{"channels":["chan-2"],"message_ids":["200"]}`))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer tool-token")
	req.Header.Set("X-Claw-ID", "trader-0")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST forbidden: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 403 for disallowed channel, got %d: %s", resp.StatusCode, string(body))
	}
}

func TestToolSearchMergesChannelMemoryAndUsesAllowlistForEmptyChannels(t *testing.T) {
	store := newConversationStore(50)
	store.merge("chan-1", []wallMessage{
		{ID: "200", Author: "alice", Content: "recent alpha signal", Timestamp: time.Unix(200, 0)},
	})

	var captured channelMemorySearchRequest
	memory := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/search" {
			http.NotFound(w, r)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatalf("decode search request: %v", err)
		}
		writeJSON(w, http.StatusOK, channelMemorySearchResponse{
			Status: "ok",
			Coverage: channelMemorySearchCoverage{
				SourceMessageHits: 1,
				DerivedBlockHits:  1,
			},
			SourceMessages: []channelMemorySourceMessage{{
				SourceKind:   "discord",
				ChannelID:    "chan-1",
				MessageID:    "100",
				SourceHandle: "chan-1/100",
				AuthorName:   "bob",
				CreatedAt:    time.Unix(100, 0).UTC().Format(time.RFC3339),
				Content:      "older alpha from durable store",
				IsCurrent:    true,
			}},
			DerivedBlocks: []channelMemoryDigestBlock{{
				Kind:           "topic_rollup",
				Text:           "alpha appeared in an older sparse digest",
				SourceChannel:  "chan-1",
				SourceMessages: []string{"100", "101"},
				SourceWindow: channelMemorySourceWindow{
					From: time.Unix(100, 0).UTC().Format(time.RFC3339),
					To:   time.Unix(101, 0).UTC().Format(time.RFC3339),
				},
				Sparse:      true,
				Score:       0.9,
				GeneratedAt: time.Unix(300, 0).UTC().Format(time.RFC3339),
				Processor:   "llm",
			}},
		})
	}))
	defer memory.Close()
	client, err := newChannelMemoryClientWithEndpoints("", "", memory.URL+"/search", memory.URL+"/source-messages", "", time.Second)
	if err != nil {
		t.Fatalf("newChannelMemoryClientWithEndpoints: %v", err)
	}

	server := httptest.NewServer(newHandler(store, handlerConfig{
		toolToken: "tool-token",
		agentChannels: map[string]map[string]struct{}{
			"trader-0": {"chan-1": {}},
		},
		channelMemory: client,
	}))
	defer server.Close()

	req, err := http.NewRequest(http.MethodPost, server.URL+"/search_channel_context", strings.NewReader(`{"query":"alpha"}`))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer tool-token")
	req.Header.Set("X-Claw-ID", "trader-0")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, string(body))
	}
	var result retrievalResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !slices.Equal(captured.ChannelIDs, []string{"chan-1"}) {
		t.Fatalf("channel-memory search got channels %v, want explicit allowlist", captured.ChannelIDs)
	}
	if result.Status != "ok" || result.Source != "mixed" || len(result.Messages) != 2 || len(result.DerivedBlocks) != 1 {
		t.Fatalf("unexpected merged search result: %+v", result)
	}
	if result.SourceCounts == nil || result.SourceCounts.Retained != 1 || result.SourceCounts.DurableSource != 1 || result.SourceCounts.SparseBlock != 1 {
		t.Fatalf("unexpected source counts: %+v", result.SourceCounts)
	}
}

func TestToolSearchChannelMemoryFailureFallsBackToRaw(t *testing.T) {
	store := newConversationStore(50)
	store.merge("chan-1", []wallMessage{
		{ID: "200", Author: "alice", Content: "recent alpha signal", Timestamp: time.Unix(200, 0)},
	})
	memory := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusBadGateway)
	}))
	defer memory.Close()
	client, err := newChannelMemoryClientWithEndpoints("", "", memory.URL+"/search", "", "", time.Second)
	if err != nil {
		t.Fatalf("newChannelMemoryClientWithEndpoints: %v", err)
	}
	server := httptest.NewServer(newHandler(store, handlerConfig{
		toolToken: "tool-token",
		agentChannels: map[string]map[string]struct{}{
			"trader-0": {"chan-1": {}},
		},
		channelMemory: client,
	}))
	defer server.Close()

	req, err := http.NewRequest(http.MethodPost, server.URL+"/search_channel_context", strings.NewReader(`{"channels":["chan-1"],"query":"alpha"}`))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer tool-token")
	req.Header.Set("X-Claw-ID", "trader-0")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	var result retrievalResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if result.Status != "ok" || len(result.Messages) != 1 || !strings.Contains(result.Hint, "channel-memory search unavailable") {
		t.Fatalf("expected raw fallback with hint, got %+v", result)
	}
}

func TestGetChannelMessagesFallsBackToChannelMemorySourceMessages(t *testing.T) {
	store := newConversationStore(50)
	var captured channelMemorySourceMessagesRequest
	memory := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/source-messages" {
			http.NotFound(w, r)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatalf("decode source-messages request: %v", err)
		}
		writeJSON(w, http.StatusOK, channelMemorySourceMessagesResponse{
			Messages: []channelMemorySourceMessage{{
				SourceKind:   "discord",
				ChannelID:    "chan-1",
				MessageID:    "100",
				SourceHandle: "chan-1/100",
				AuthorName:   "bob",
				CreatedAt:    time.Unix(100, 0).UTC().Format(time.RFC3339),
				Content:      "older exact source message",
				IsCurrent:    true,
			}},
		})
	}))
	defer memory.Close()
	client, err := newChannelMemoryClientWithEndpoints("", "", "", memory.URL+"/source-messages", "", time.Second)
	if err != nil {
		t.Fatalf("newChannelMemoryClientWithEndpoints: %v", err)
	}
	server := httptest.NewServer(newHandler(store, handlerConfig{
		toolToken: "tool-token",
		agentChannels: map[string]map[string]struct{}{
			"trader-0": {"chan-1": {}},
		},
		channelMemory: client,
	}))
	defer server.Close()

	req, err := http.NewRequest(http.MethodPost, server.URL+"/get_channel_messages", strings.NewReader(`{"message_ids":["chan-1/100"]}`))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer tool-token")
	req.Header.Set("X-Claw-ID", "trader-0")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	var result retrievalResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if captured.ChannelID != "chan-1" || !slices.Equal(captured.MessageIDs, []string{"100"}) {
		t.Fatalf("unexpected source-messages request: %+v", captured)
	}
	if result.Status != "ok" || result.Source != "channel-memory" || len(result.Messages) != 1 || result.Messages[0].SourceHandle != "chan-1/100" {
		t.Fatalf("unexpected source fallback result: %+v", result)
	}

	forbidden, err := http.NewRequest(http.MethodPost, server.URL+"/get_channel_messages", strings.NewReader(`{"message_ids":["chan-2/200"]}`))
	if err != nil {
		t.Fatalf("forbidden request: %v", err)
	}
	forbidden.Header.Set("Authorization", "Bearer tool-token")
	forbidden.Header.Set("X-Claw-ID", "trader-0")
	resp, err = http.DefaultClient.Do(forbidden)
	if err != nil {
		t.Fatalf("POST forbidden: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected forbidden source handle, got %d: %s", resp.StatusCode, string(body))
	}
}

func TestGetChannelMessagesSupportsTimestampRange(t *testing.T) {
	store := newConversationStore(50)
	store.merge("chan-1", []wallMessage{
		{ID: "100", Author: "alice", Content: "before", Timestamp: time.Date(2026, 5, 12, 8, 0, 0, 0, time.UTC)},
		{ID: "101", Author: "bob", Content: "inside", Timestamp: time.Date(2026, 5, 12, 9, 0, 0, 0, time.UTC)},
		{ID: "102", Author: "carol", Content: "after", Timestamp: time.Date(2026, 5, 12, 10, 0, 0, 0, time.UTC)},
	})

	result := store.getMessages(getChannelMessagesRequest{
		Channels: []string{"chan-1"},
		After:    "2026-05-12T08:30Z",
		Before:   "2026-05-12T09:30Z",
	})
	if result.Status != "ok" || len(result.Messages) != 1 || result.Messages[0].ID != "101" {
		t.Fatalf("unexpected timestamp-bounded result: %+v", result)
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

func TestChannelContextHeaderByContextKindParam(t *testing.T) {
	store := newConversationStore(50)
	store.merge("chan-1", []wallMessage{
		{ID: "100", Author: "alice", Content: "first", Timestamp: time.Unix(100, 0)},
		{ID: "101", Author: "bob", Content: "second", Timestamp: time.Unix(101, 0)},
	})

	server := httptest.NewServer(newHandler(store))
	defer server.Close()

	cases := []struct {
		name string
		kind string
		want string
	}{
		{name: "delta", kind: "delta_tail", want: "[channel-context delta] kind=delta_tail"},
		{name: "bootstrap", kind: "bootstrap_tail", want: "[channel-context bootstrap] kind=bootstrap_tail reason=epoch_changed"},
		{name: "tail", kind: "tail", want: "[channel-context] kind=tail"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := http.Get(server.URL + "/channel-context?channels=chan-1&mode=tail&context_kind=" + tc.kind)
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
			if !strings.Contains(string(body), tc.want) {
				t.Fatalf("expected %q, got %q", tc.want, string(body))
			}
		})
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
	t.Setenv("CLAW_WALL_RETENTION", "")
	t.Setenv("CLAW_WALL_BACKFILL_MAX_PAGES", "")
	t.Setenv("CLAW_WALL_DISCORD_BASE_URL", "")
	t.Setenv("CLAW_WALL_CHANNEL_MEMORY_INGEST_URL", "")
	t.Setenv("CLAW_WALL_CHANNEL_MEMORY_DIGEST_URL", "")
	t.Setenv("CLAW_WALL_CHANNEL_MEMORY_SEARCH_URL", "")
	t.Setenv("CLAW_WALL_CHANNEL_MEMORY_SOURCE_URL", "")
	t.Setenv("CLAW_WALL_CHANNEL_MEMORY_TOKEN", "")
	t.Setenv("CLAW_WALL_CHANNEL_MEMORY_TIMEOUT", "")
	t.Setenv("CLAW_WALL_TOKENS", "chan-1:token-a")

	cfg, err := loadConfig()
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if cfg.PollInterval != 30*time.Second {
		t.Fatalf("expected 30s poll interval, got %s", cfg.PollInterval)
	}
	if cfg.BufferLimit != 5000 {
		t.Fatalf("expected default buffer limit 5000, got %d", cfg.BufferLimit)
	}
	if cfg.Retention != 24*time.Hour {
		t.Fatalf("expected 24h retention, got %s", cfg.Retention)
	}
	if cfg.BackfillMaxPages != 25 {
		t.Fatalf("expected 25 backfill pages, got %d", cfg.BackfillMaxPages)
	}
	if cfg.DiscordBaseURL != "" {
		t.Fatalf("expected empty discord base url by default, got %q", cfg.DiscordBaseURL)
	}
	if cfg.ChannelMemoryIngestURL != "" || cfg.ChannelMemoryDigestURL != "" || cfg.ChannelMemorySearchURL != "" || cfg.ChannelMemorySourceURL != "" || cfg.ChannelMemoryToken != "" || cfg.ChannelMemoryTimeout != 2*time.Second {
		t.Fatalf("unexpected channel-memory defaults: %+v", cfg)
	}
}

func TestLoadConfigReadsDiscordBaseURLOverride(t *testing.T) {
	t.Setenv("CLAW_WALL_ADDR", "")
	t.Setenv("CLAW_WALL_LIMIT", "")
	t.Setenv("CLAW_WALL_POLL_INTERVAL", "")
	t.Setenv("CLAW_WALL_RETENTION", "")
	t.Setenv("CLAW_WALL_BACKFILL_MAX_PAGES", "")
	t.Setenv("CLAW_WALL_DISCORD_BASE_URL", "http://fake-discord:9000/api/v10")
	t.Setenv("CLAW_WALL_CHANNEL_MEMORY_DIGEST_URL", "")
	t.Setenv("CLAW_WALL_CHANNEL_MEMORY_SEARCH_URL", "")
	t.Setenv("CLAW_WALL_CHANNEL_MEMORY_SOURCE_URL", "")
	t.Setenv("CLAW_WALL_CHANNEL_MEMORY_TIMEOUT", "")
	t.Setenv("CLAW_WALL_TOKENS", "chan-1:token-a")

	cfg, err := loadConfig()
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if cfg.DiscordBaseURL != "http://fake-discord:9000/api/v10" {
		t.Fatalf("unexpected DiscordBaseURL: %q", cfg.DiscordBaseURL)
	}
}

func TestLoadConfigReadsChannelMemoryConfig(t *testing.T) {
	t.Setenv("CLAW_WALL_ADDR", "")
	t.Setenv("CLAW_WALL_LIMIT", "")
	t.Setenv("CLAW_WALL_POLL_INTERVAL", "")
	t.Setenv("CLAW_WALL_RETENTION", "")
	t.Setenv("CLAW_WALL_BACKFILL_MAX_PAGES", "")
	t.Setenv("CLAW_WALL_DISCORD_BASE_URL", "")
	t.Setenv("CLAW_WALL_CHANNEL_MEMORY_INGEST_URL", "http://channel-memory:8080/ingest")
	t.Setenv("CLAW_WALL_CHANNEL_MEMORY_DIGEST_URL", "http://channel-memory:8080/digest")
	t.Setenv("CLAW_WALL_CHANNEL_MEMORY_SEARCH_URL", "http://channel-memory:8080/search")
	t.Setenv("CLAW_WALL_CHANNEL_MEMORY_SOURCE_URL", "http://channel-memory:8080/source-messages")
	t.Setenv("CLAW_WALL_CHANNEL_MEMORY_TOKEN", "memory-token")
	t.Setenv("CLAW_WALL_CHANNEL_MEMORY_TIMEOUT", "750ms")
	t.Setenv("CLAW_WALL_TOKENS", "chan-1:token-a")

	cfg, err := loadConfig()
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if cfg.ChannelMemoryIngestURL != "http://channel-memory:8080/ingest" || cfg.ChannelMemoryDigestURL != "http://channel-memory:8080/digest" || cfg.ChannelMemorySearchURL != "http://channel-memory:8080/search" || cfg.ChannelMemorySourceURL != "http://channel-memory:8080/source-messages" || cfg.ChannelMemoryToken != "memory-token" || cfg.ChannelMemoryTimeout != 750*time.Millisecond {
		t.Fatalf("unexpected channel-memory config: %+v", cfg)
	}
}

func TestChannelMemoryClientDerivesSearchAndSourceURLs(t *testing.T) {
	client, err := newChannelMemoryClientWithDigest("http://channel-memory:8080/ingest", "", "", time.Second)
	if err != nil {
		t.Fatalf("newChannelMemoryClientWithDigest: %v", err)
	}
	if client.digestURL != "http://channel-memory:8080/digest" || client.searchURL != "http://channel-memory:8080/search" || client.sourceURL != "http://channel-memory:8080/source-messages" {
		t.Fatalf("unexpected derived channel-memory URLs: %+v", client)
	}
}

func TestLoadConfigUsesPollIntervalOverride(t *testing.T) {
	t.Setenv("CLAW_WALL_ADDR", "")
	t.Setenv("CLAW_WALL_LIMIT", "")
	t.Setenv("CLAW_WALL_POLL_INTERVAL", "42")
	t.Setenv("CLAW_WALL_RETENTION", "6h")
	t.Setenv("CLAW_WALL_BACKFILL_MAX_PAGES", "7")
	t.Setenv("CLAW_WALL_CHANNEL_MEMORY_INGEST_URL", "")
	t.Setenv("CLAW_WALL_CHANNEL_MEMORY_DIGEST_URL", "")
	t.Setenv("CLAW_WALL_CHANNEL_MEMORY_SEARCH_URL", "")
	t.Setenv("CLAW_WALL_CHANNEL_MEMORY_SOURCE_URL", "")
	t.Setenv("CLAW_WALL_CHANNEL_MEMORY_TIMEOUT", "")
	t.Setenv("CLAW_WALL_TOKENS", "chan-1:token-a")

	cfg, err := loadConfig()
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if cfg.PollInterval != 42*time.Second {
		t.Fatalf("expected 42s poll interval, got %s", cfg.PollInterval)
	}
	if cfg.Retention != 6*time.Hour {
		t.Fatalf("expected 6h retention, got %s", cfg.Retention)
	}
	if cfg.BackfillMaxPages != 7 {
		t.Fatalf("expected 7 backfill pages, got %d", cfg.BackfillMaxPages)
	}
}

func TestDiscordPollerClampsFetchLimitToDiscordMaximum(t *testing.T) {
	poller := newDiscordPoller(nil, newConversationStore(500), []tokenPair{{ChannelID: "chan-1", Token: "token-a"}}, 500)
	if poller.fetchLimit != maxDiscordFetchLimit {
		t.Fatalf("expected fetch limit %d, got %d", maxDiscordFetchLimit, poller.fetchLimit)
	}
}

func TestConversationStoreTrimsByRetentionHorizon(t *testing.T) {
	now := time.Date(2026, 5, 12, 18, 0, 0, 0, time.UTC)
	store := newConversationStore(50, 24*time.Hour)
	store.mergeAt("chan-1", []wallMessage{
		{ID: "100", Author: "alice", Content: "too old", Timestamp: now.Add(-30 * time.Hour)},
		{ID: "101", Author: "bob", Content: "recent", Timestamp: now.Add(-12 * time.Hour)},
		{ID: "102", Author: "carol", Content: "now", Timestamp: now},
		{ID: "103", Author: "dave", Content: "bad timestamp"},
	}, now)

	got := store.tail(tailRequest{ChannelIDs: []string{"chan-1"}, Since: 24 * time.Hour, Limit: 10, Now: now})
	if len(got.Messages) != 2 || got.Messages[0].ID != "101" || got.Messages[1].ID != "102" {
		t.Fatalf("expected only timestamp-valid in-window messages, got %+v", got.Messages)
	}
}

func TestConversationStoreSafetyCapMarksCoveragePartial(t *testing.T) {
	now := time.Date(2026, 5, 12, 18, 0, 0, 0, time.UTC)
	store := newConversationStore(2, 24*time.Hour)
	store.setBackfillStatus("chan-1", backfillStatusComplete)
	store.mergeAt("chan-1", []wallMessage{
		{ID: "100", Author: "alice", Content: "first", Timestamp: now.Add(-3 * time.Hour)},
		{ID: "101", Author: "bob", Content: "second", Timestamp: now.Add(-2 * time.Hour)},
		{ID: "102", Author: "carol", Content: "third", Timestamp: now.Add(-1 * time.Hour)},
	}, now)

	got := store.tail(tailRequest{ChannelIDs: []string{"chan-1"}, Since: 24 * time.Hour, Limit: 10, Now: now})
	if len(got.Messages) != 2 || got.Messages[0].ID != "101" || got.Messages[1].ID != "102" {
		t.Fatalf("expected newest capped messages, got %+v", got.Messages)
	}
	if formatBackfillStatus(got.BackfillStatus) != backfillStatusPartial {
		t.Fatalf("expected partial coverage after cap eviction, got %+v", got.BackfillStatus)
	}
}

func TestDiscordPollerBackfillsPastFirstPageToHorizon(t *testing.T) {
	now := time.Date(2026, 5, 12, 18, 0, 0, 0, time.UTC)
	messages := makeDiscordMessages(now.Add(-36*time.Hour), 360, 6*time.Minute)
	var beforeRequests int
	server := newDiscordMessagesServer(t, messages, func(r *http.Request) {
		if r.URL.Query().Get("before") != "" {
			beforeRequests++
		}
	})
	defer server.Close()

	store := newConversationStore(500, 24*time.Hour)
	poller := newDiscordPoller(server.Client(), store, []tokenPair{{ChannelID: "chan-1", Token: "token-a"}}, 500)
	poller.baseURL = server.URL
	poller.now = func() time.Time { return now }
	poller.backfillRetention = 24 * time.Hour
	poller.backfillMaxPages = 5

	poller.backfillAll(context.Background(), io.Discard)

	got := store.tail(tailRequest{ChannelIDs: []string{"chan-1"}, Since: 24 * time.Hour, Limit: 500, Now: now})
	if len(got.Messages) != 240 {
		t.Fatalf("expected 240 in-window messages after backfill, got %d", len(got.Messages))
	}
	if got.Messages[0].ID != "1120" || got.Messages[len(got.Messages)-1].ID != "1359" {
		t.Fatalf("unexpected retained range: first=%s last=%s", got.Messages[0].ID, got.Messages[len(got.Messages)-1].ID)
	}
	if beforeRequests < 2 {
		t.Fatalf("expected pagination with before=, got %d before requests", beforeRequests)
	}
	if formatBackfillStatus(got.BackfillStatus) != backfillStatusComplete {
		t.Fatalf("expected complete backfill, got %+v", got.BackfillStatus)
	}
}

func TestDiscordPollerBackfillStopsAtPageBudget(t *testing.T) {
	now := time.Date(2026, 5, 12, 18, 0, 0, 0, time.UTC)
	messages := makeDiscordMessages(now.Add(-10*time.Hour), 500, time.Minute)
	server := newDiscordMessagesServer(t, messages, nil)
	defer server.Close()

	store := newConversationStore(500, 24*time.Hour)
	poller := newDiscordPoller(server.Client(), store, []tokenPair{{ChannelID: "chan-1", Token: "token-a"}}, 500)
	poller.baseURL = server.URL
	poller.now = func() time.Time { return now }
	poller.backfillRetention = 24 * time.Hour
	poller.backfillMaxPages = 2

	poller.backfillAll(context.Background(), io.Discard)

	got := store.tail(tailRequest{ChannelIDs: []string{"chan-1"}, Since: 24 * time.Hour, Limit: 500, Now: now})
	if len(got.Messages) != 200 {
		t.Fatalf("expected two pages retained, got %d", len(got.Messages))
	}
	if formatBackfillStatus(got.BackfillStatus) != backfillStatusPartial {
		t.Fatalf("expected partial backfill, got %+v", got.BackfillStatus)
	}
}

func TestDiscordPollerBackfillRespectsRateLimit(t *testing.T) {
	now := time.Date(2026, 5, 12, 18, 0, 0, 0, time.UTC)
	messages := makeDiscordMessages(now.Add(-10*time.Hour), 200, time.Minute)
	hits := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if hits == 2 {
			w.Header().Set("Retry-After", "60")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = io.WriteString(w, `{"retry_after":60}`)
			return
		}
		writeDiscordMessagesPage(t, w, r, messages)
	}))
	defer server.Close()

	store := newConversationStore(500, 24*time.Hour)
	poller := newDiscordPoller(server.Client(), store, []tokenPair{{ChannelID: "chan-1", Token: "token-a"}}, 500)
	poller.baseURL = server.URL
	poller.now = func() time.Time { return now }
	poller.backfillRetention = 24 * time.Hour
	poller.backfillMaxPages = 5

	poller.backfillAll(context.Background(), io.Discard)

	got := store.tail(tailRequest{ChannelIDs: []string{"chan-1"}, Since: 24 * time.Hour, Limit: 500, Now: now})
	if len(got.Messages) != 100 {
		t.Fatalf("expected first page retained before rate limit, got %d", len(got.Messages))
	}
	if formatBackfillStatus(got.BackfillStatus) != backfillStatusRateLimited {
		t.Fatalf("expected rate_limited backfill, got %+v", got.BackfillStatus)
	}
}

func TestDiscordPollerPushesBackfillToChannelMemory(t *testing.T) {
	now := time.Date(2026, 5, 21, 16, 0, 0, 0, time.UTC)
	messages := makeDiscordMessages(now.Add(-3*time.Minute), 3, time.Minute)
	messages[0].Author.ID = "user-1000"
	memory := newRecordingChannelMemoryServer(t, http.StatusAccepted)
	defer memory.Close()
	client, err := newChannelMemoryClient(memory.URL+"/ingest", "", time.Second)
	if err != nil {
		t.Fatalf("newChannelMemoryClient: %v", err)
	}
	discord := newDiscordMessagesServer(t, messages, nil)
	defer discord.Close()

	store := newConversationStore(50, 24*time.Hour)
	poller := newDiscordPoller(discord.Client(), store, []tokenPair{{ChannelID: "chan-1", Token: "token-a"}}, 50)
	poller.baseURL = discord.URL
	poller.now = func() time.Time { return now }
	poller.backfillRetention = 24 * time.Hour
	poller.backfillMaxPages = 1
	poller.channelMemory = client

	poller.backfillAll(context.Background(), io.Discard)

	got := memory.requests()
	if len(got) != 3 {
		t.Fatalf("expected 3 channel-memory ingests, got %d: %+v", len(got), got)
	}
	if got[0].ChannelID != "chan-1" || got[0].Message.ID != "1000" || got[0].Message.AuthorID != "user-1000" {
		t.Fatalf("unexpected first ingest payload: %+v", got[0])
	}
	if got[0].Message.ContentHash == "" || !strings.HasPrefix(got[0].Message.ContentHash, "sha256:") {
		t.Fatalf("expected content hash in ingest payload: %+v", got[0].Message)
	}
	if got[0].Scope != "channel:chan-1" || got[0].Metadata["visibility_scope"] != "channel:chan-1" {
		t.Fatalf("expected channel visibility scope, got %+v", got[0])
	}
}

func TestDiscordPollerPushesForwardPollToChannelMemory(t *testing.T) {
	now := time.Date(2026, 5, 21, 16, 0, 0, 0, time.UTC)
	messages := makeDiscordMessages(now.Add(-3*time.Minute), 3, time.Minute)
	memory := newRecordingChannelMemoryServer(t, http.StatusAccepted)
	defer memory.Close()
	client, err := newChannelMemoryClient(memory.URL+"/ingest", "", time.Second)
	if err != nil {
		t.Fatalf("newChannelMemoryClient: %v", err)
	}
	discord := newDiscordMessagesServer(t, messages, nil)
	defer discord.Close()

	store := newConversationStore(50, 24*time.Hour)
	poller := newDiscordPoller(discord.Client(), store, []tokenPair{{ChannelID: "chan-1", Token: "token-a"}}, 50)
	poller.baseURL = discord.URL
	poller.now = func() time.Time { return now }
	poller.latestByPair[pairKey(tokenPair{ChannelID: "chan-1", Token: "token-a"})] = "1000"
	poller.channelMemory = client

	poller.pollOnce(context.Background(), io.Discard)

	got := memory.requests()
	if len(got) != 2 || got[0].Message.ID != "1001" || got[1].Message.ID != "1002" {
		t.Fatalf("expected forward poll messages 1001,1002, got %+v", got)
	}
}

func TestDiscordPollerChannelMemoryFailureDoesNotBlockRawWindow(t *testing.T) {
	now := time.Date(2026, 5, 21, 16, 0, 0, 0, time.UTC)
	messages := makeDiscordMessages(now.Add(-time.Minute), 1, time.Minute)
	memory := newRecordingChannelMemoryServer(t, http.StatusInternalServerError)
	defer memory.Close()
	client, err := newChannelMemoryClient(memory.URL+"/ingest", "", time.Second)
	if err != nil {
		t.Fatalf("newChannelMemoryClient: %v", err)
	}
	discord := newDiscordMessagesServer(t, messages, nil)
	defer discord.Close()

	store := newConversationStore(50, 24*time.Hour)
	poller := newDiscordPoller(discord.Client(), store, []tokenPair{{ChannelID: "chan-1", Token: "token-a"}}, 50)
	poller.baseURL = discord.URL
	poller.now = func() time.Time { return now }
	poller.channelMemory = client

	var logs strings.Builder
	poller.pollOnce(context.Background(), &logs)

	if !strings.Contains(logs.String(), "channel-memory ingest failed") {
		t.Fatalf("expected channel-memory failure log, got %q", logs.String())
	}
	got := store.tail(tailRequest{ChannelIDs: []string{"chan-1"}, Since: 24 * time.Hour, Limit: 10, Now: now})
	if len(got.Messages) != 1 || got.Messages[0].ID != "1000" {
		t.Fatalf("expected raw window to retain message despite ingest failure, got %+v", got.Messages)
	}
}

func TestChannelMemoryReplayRequiresAllowedChannels(t *testing.T) {
	store := newConversationStore(50)
	store.merge("chan-1", []wallMessage{{ID: "100", Author: "alice", Content: "alpha", Timestamp: time.Unix(100, 0)}})
	store.merge("chan-2", []wallMessage{{ID: "200", Author: "bob", Content: "beta", Timestamp: time.Unix(200, 0)}})
	memory := newRecordingChannelMemoryServer(t, http.StatusAccepted)
	defer memory.Close()
	client, err := newChannelMemoryClient(memory.URL+"/ingest", "", time.Second)
	if err != nil {
		t.Fatalf("newChannelMemoryClient: %v", err)
	}
	server := httptest.NewServer(newHandler(store, handlerConfig{
		toolToken:     "tool-token",
		channelMemory: client,
		agentChannels: map[string]map[string]struct{}{
			"trader-0": {"chan-1": {}},
		},
	}))
	defer server.Close()

	req, err := http.NewRequest(http.MethodPost, server.URL+"/channel-memory/replay", strings.NewReader(`{"channels":["chan-2"]}`))
	if err != nil {
		t.Fatalf("request forbidden: %v", err)
	}
	req.Header.Set("Authorization", "Bearer tool-token")
	req.Header.Set("X-Claw-ID", "trader-0")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST forbidden: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 403, got %d: %s", resp.StatusCode, string(body))
	}
	if got := memory.requests(); len(got) != 0 {
		t.Fatalf("forbidden replay should not push messages, got %+v", got)
	}

	req, err = http.NewRequest(http.MethodPost, server.URL+"/channel-memory/replay", strings.NewReader(`{"channels":["chan-1"]}`))
	if err != nil {
		t.Fatalf("request replay: %v", err)
	}
	req.Header.Set("Authorization", "Bearer tool-token")
	req.Header.Set("X-Claw-ID", "trader-0")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST replay: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, string(body))
	}
	got := memory.requests()
	if len(got) != 1 || got[0].ChannelID != "chan-1" || got[0].Message.ID != "100" {
		t.Fatalf("expected allowed replay to push chan-1/100, got %+v", got)
	}
}

func TestDiscordPollerRecoversFullForwardPollGap(t *testing.T) {
	now := time.Date(2026, 5, 12, 18, 0, 0, 0, time.UTC)
	messages := makeDiscordMessages(now.Add(-4*time.Hour), 350, time.Minute)
	server := newDiscordMessagesServer(t, messages, nil)
	defer server.Close()

	store := newConversationStore(500, 24*time.Hour)
	poller := newDiscordPoller(server.Client(), store, []tokenPair{{ChannelID: "chan-1", Token: "token-a"}}, 500)
	poller.baseURL = server.URL
	poller.now = func() time.Time { return now }
	poller.latestByPair[pairKey(tokenPair{ChannelID: "chan-1", Token: "token-a"})] = "1100"
	poller.backfillMaxPages = 5

	poller.pollOnce(context.Background(), io.Discard)

	result := store.search(searchChannelContextRequest{Channels: []string{"chan-1"}, Query: "message-101"}, now)
	if result.Status != "ok" || len(result.Messages) != 1 || result.Messages[0].ID != "1101" {
		t.Fatalf("expected gap recovery to retain oldest missed message, got %+v", result)
	}
	got := store.tail(tailRequest{ChannelIDs: []string{"chan-1"}, Since: 24 * time.Hour, Limit: 500, Now: now})
	if len(got.Messages) != 249 {
		t.Fatalf("expected all 249 messages after previous cursor, got %d", len(got.Messages))
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

type recordingChannelMemoryServer struct {
	*httptest.Server
	mu       sync.Mutex
	received []channelMemoryIngestRequest
}

func newRecordingChannelMemoryServer(t *testing.T, status int) *recordingChannelMemoryServer {
	t.Helper()
	rec := &recordingChannelMemoryServer{}
	rec.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/ingest" {
			http.Error(w, "unexpected request", http.StatusNotFound)
			return
		}
		var payload channelMemoryIngestRequest
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			http.Error(w, "invalid json", http.StatusBadRequest)
			return
		}
		rec.mu.Lock()
		rec.received = append(rec.received, payload)
		rec.mu.Unlock()
		w.WriteHeader(status)
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	return rec
}

func (s *recordingChannelMemoryServer) requests() []channelMemoryIngestRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]channelMemoryIngestRequest, len(s.received))
	copy(out, s.received)
	return out
}

type digestChannelMemoryServer struct {
	*httptest.Server
	mu       sync.Mutex
	received []channelMemoryDigestRequest
}

func newDigestChannelMemoryServer(t *testing.T, response channelMemoryDigestResponse) *digestChannelMemoryServer {
	t.Helper()
	srv := &digestChannelMemoryServer{}
	srv.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/digest" {
			http.Error(w, "unexpected request", http.StatusNotFound)
			return
		}
		var payload channelMemoryDigestRequest
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			http.Error(w, "invalid json", http.StatusBadRequest)
			return
		}
		srv.mu.Lock()
		srv.received = append(srv.received, payload)
		srv.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(response); err != nil {
			http.Error(w, "encode digest response", http.StatusInternalServerError)
		}
	}))
	return srv
}

func (s *digestChannelMemoryServer) requests() []channelMemoryDigestRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]channelMemoryDigestRequest, len(s.received))
	copy(out, s.received)
	return out
}

func makeDiscordMessages(start time.Time, count int, step time.Duration) []discordAPIMessage {
	messages := make([]discordAPIMessage, 0, count)
	for i := 0; i < count; i++ {
		messages = append(messages, discordAPIMessage{
			ID:        fmt.Sprintf("%d", 1000+i),
			Content:   fmt.Sprintf("message-%03d", i),
			Timestamp: start.Add(time.Duration(i) * step).Format(time.RFC3339),
			Author: discordAPIAuthor{
				ID:       fmt.Sprintf("user-id-%03d", i),
				Username: fmt.Sprintf("user-%03d", i),
			},
		})
	}
	return messages
}

func newDiscordMessagesServer(t *testing.T, messages []discordAPIMessage, hook func(*http.Request)) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hook != nil {
			hook(r)
		}
		writeDiscordMessagesPage(t, w, r, messages)
	}))
}

func writeDiscordMessagesPage(t *testing.T, w http.ResponseWriter, r *http.Request, messages []discordAPIMessage) {
	t.Helper()
	if !strings.Contains(r.URL.Path, "/channels/chan-1/messages") {
		t.Fatalf("unexpected path: %s", r.URL.Path)
	}
	limit := maxDiscordFetchLimit
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			t.Fatalf("invalid limit %q: %v", raw, err)
		}
		limit = parsed
	}
	if limit > maxDiscordFetchLimit {
		limit = maxDiscordFetchLimit
	}

	after := strings.TrimSpace(r.URL.Query().Get("after"))
	before := strings.TrimSpace(r.URL.Query().Get("before"))
	filtered := make([]discordAPIMessage, 0, len(messages))
	for _, msg := range messages {
		if after != "" && compareSnowflakes(msg.ID, after) <= 0 {
			continue
		}
		if before != "" && compareSnowflakes(msg.ID, before) >= 0 {
			continue
		}
		filtered = append(filtered, msg)
	}
	if len(filtered) > limit {
		filtered = filtered[len(filtered)-limit:]
	}

	out := make([]discordAPIMessage, 0, len(filtered))
	for i := len(filtered) - 1; i >= 0; i-- {
		out = append(out, filtered[i])
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(out); err != nil {
		t.Fatalf("encode response: %v", err)
	}
}
