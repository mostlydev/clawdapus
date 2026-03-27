package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
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
