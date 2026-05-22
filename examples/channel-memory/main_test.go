package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestChannelMemoryIngestIdempotentAndEditedVersionIsCurrent(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()

	base := ingestRequest{
		ChannelID: "chan-1",
		Message: ingestMessage{
			ID:          "101",
			AuthorName:  "analyst-a",
			CreatedAt:   "2026-05-21T16:00:00Z",
			Content:     "[PROPOSED] signal-101 SELL ACME",
			ContentHash: "sha256:first",
		},
	}

	first, err := store.Ingest(context.Background(), base)
	if err != nil {
		t.Fatalf("ingest first: %v", err)
	}
	if !first.Inserted || first.ObservedSeq != 1 || !first.Current {
		t.Fatalf("unexpected first ingest response: %+v", first)
	}

	duplicate, err := store.Ingest(context.Background(), base)
	if err != nil {
		t.Fatalf("ingest duplicate: %v", err)
	}
	if duplicate.Inserted || duplicate.ObservedSeq != 1 {
		t.Fatalf("duplicate should be idempotent, got %+v", duplicate)
	}

	edited := base
	edited.Message.EditedAt = "2026-05-21T16:02:00Z"
	edited.Message.Content = "[PROPOSED] signal-101 SELL ACME after revised momentum check"
	edited.Message.ContentHash = "sha256:second"
	editResp, err := store.Ingest(context.Background(), edited)
	if err != nil {
		t.Fatalf("ingest edit: %v", err)
	}
	if !editResp.Inserted || editResp.ObservedSeq != 2 {
		t.Fatalf("edit should be a new observed source row, got %+v", editResp)
	}

	current, err := store.SourceMessages(context.Background(), sourceMessagesRequest{
		ChannelID:  "chan-1",
		MessageIDs: []string{"101"},
	})
	if err != nil {
		t.Fatalf("current source query: %v", err)
	}
	if len(current.Messages) != 1 {
		t.Fatalf("expected one current source message, got %+v", current)
	}
	if got := current.Messages[0].ContentHash; got != "sha256:second" {
		t.Fatalf("current query returned wrong content hash %q", got)
	}
	if !strings.Contains(current.Messages[0].Content, "revised momentum") {
		t.Fatalf("current query returned old content: %+v", current.Messages[0])
	}

	history, err := store.SourceMessages(context.Background(), sourceMessagesRequest{
		ChannelID:      "chan-1",
		MessageIDs:     []string{"101"},
		IncludeHistory: true,
	})
	if err != nil {
		t.Fatalf("history source query: %v", err)
	}
	if len(history.Messages) != 2 {
		t.Fatalf("expected two content-hash versions, got %+v", history.Messages)
	}
	if history.Messages[0].IsCurrent || history.Messages[0].SupersededBy == nil {
		t.Fatalf("old source row should be superseded, got %+v", history.Messages[0])
	}
	if !history.Messages[1].IsCurrent {
		t.Fatalf("edited source row should be current, got %+v", history.Messages[1])
	}
}

func TestChannelMemoryDeleteTombstoneAndForgetSuppressDerivedBlocks(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()

	msg := ingestRequest{
		ChannelID: "chan-1",
		Message: ingestMessage{
			ID:          "201",
			AuthorName:  "analyst-a",
			CreatedAt:   "2026-05-21T17:00:00Z",
			Content:     "[APPROVED] signal-201 BUY ACME",
			ContentHash: "sha256:approved",
		},
	}
	if _, err := store.Ingest(context.Background(), msg); err != nil {
		t.Fatalf("ingest approved message: %v", err)
	}

	beforeDelete, err := store.Digest(context.Background(), digestRequest{ChannelIDs: []string{"chan-1"}, Since: "24h"})
	if err != nil {
		t.Fatalf("digest before delete: %v", err)
	}
	if len(beforeDelete.Blocks) != 1 || beforeDelete.Blocks[0].Kind != "hard_event" || beforeDelete.Blocks[0].Sparse {
		t.Fatalf("expected one faithful hard_event before delete, got %+v", beforeDelete.Blocks)
	}

	deleted := msg
	deleted.Message.Deleted = true
	deleted.Message.EditedAt = "2026-05-21T17:03:00Z"
	deleted.Message.Content = ""
	deleted.Message.ContentHash = "sha256:deleted"
	if _, err := store.Ingest(context.Background(), deleted); err != nil {
		t.Fatalf("ingest delete tombstone: %v", err)
	}

	current, err := store.SourceMessages(context.Background(), sourceMessagesRequest{ChannelID: "chan-1", MessageIDs: []string{"201"}})
	if err != nil {
		t.Fatalf("current after delete: %v", err)
	}
	if len(current.Messages) != 0 || len(current.NotFound) != 1 {
		t.Fatalf("deleted current source should not be served by default, got %+v", current)
	}

	afterDelete, err := store.Digest(context.Background(), digestRequest{ChannelIDs: []string{"chan-1"}, Since: "24h"})
	if err != nil {
		t.Fatalf("digest after delete: %v", err)
	}
	if len(afterDelete.Blocks) != 1 || afterDelete.Blocks[0].Kind != "tombstone" || !afterDelete.Blocks[0].Sparse {
		t.Fatalf("expected sparse tombstone after delete, got %+v", afterDelete.Blocks)
	}
	if strings.Contains(afterDelete.Blocks[0].Text, "BUY ACME") {
		t.Fatalf("tombstone leaked deleted source content: %+v", afterDelete.Blocks[0])
	}

	forget, err := store.Forget(context.Background(), forgetRequest{
		ChannelID:  "chan-1",
		MessageIDs: []string{"201"},
		Reason:     "operator requested removal",
	})
	if err != nil {
		t.Fatalf("forget: %v", err)
	}
	if forget.Forgotten != 2 {
		t.Fatalf("expected both content versions forgotten, got %+v", forget)
	}

	afterForget, err := store.Digest(context.Background(), digestRequest{ChannelIDs: []string{"chan-1"}, Since: "24h"})
	if err != nil {
		t.Fatalf("digest after forget: %v", err)
	}
	if len(afterForget.Blocks) != 0 || afterForget.Status != "unavailable" {
		t.Fatalf("forget should suppress derived blocks, got %+v", afterForget)
	}
}

func TestChannelMemoryDeterministicTelemetryAndCoverageGap(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()

	for i := 0; i < 3; i++ {
		req := ingestRequest{
			ChannelID: "chan-ops",
			Message: ingestMessage{
				ID:          string(rune('a' + i)),
				AuthorName:  "agent-status",
				CreatedAt:   "2026-05-21T18:0" + string(rune('0'+i)) + ":00Z",
				Content:     "HEARTBEAT_OK runtime status nominal",
				ContentHash: "sha256:heartbeat-" + string(rune('0'+i)),
			},
		}
		if _, err := store.Ingest(context.Background(), req); err != nil {
			t.Fatalf("ingest heartbeat %d: %v", i, err)
		}
	}

	digest, err := store.Digest(context.Background(), digestRequest{ChannelIDs: []string{"chan-ops"}, Since: "24h"})
	if err != nil {
		t.Fatalf("digest telemetry: %v", err)
	}
	if len(digest.Blocks) != 1 {
		t.Fatalf("expected one collapsed telemetry block, got %+v", digest.Blocks)
	}
	block := digest.Blocks[0]
	if block.Kind != "telemetry_count" || !block.Sparse {
		t.Fatalf("expected sparse telemetry_count, got %+v", block)
	}
	if len(block.SourceMessages) != 3 || !strings.Contains(block.Text, "3 messages") {
		t.Fatalf("telemetry block should cover all source messages, got %+v", block)
	}

	gap, err := store.AddCoverageGap(context.Background(), coverageGapRequest{
		ChannelID: "chan-ops",
		From:      "2026-05-21T17:00:00Z",
		To:        "2026-05-21T17:30:00Z",
		Reason:    "backfill rate limited",
	})
	if err != nil {
		t.Fatalf("add coverage gap: %v", err)
	}
	if gap.ID == 0 {
		t.Fatalf("expected persisted gap id, got %+v", gap)
	}

	withGap, err := store.Digest(context.Background(), digestRequest{ChannelIDs: []string{"chan-ops"}, Since: "24h"})
	if err != nil {
		t.Fatalf("digest coverage gap: %v", err)
	}
	if withGap.Status != "coverage_gap" || len(withGap.Coverage.Gaps) != 1 {
		t.Fatalf("expected coverage_gap status, got %+v", withGap)
	}
}

func TestChannelMemoryHTTPAPI(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()

	server := httptest.NewServer(newHandler(store))
	defer server.Close()

	postJSONExpect(t, server.URL+"/ingest", ingestRequest{
		ChannelID: "chan-http",
		Message: ingestMessage{
			ID:          "301",
			AuthorName:  "analyst-b",
			CreatedAt:   "2026-05-21T19:00:00Z",
			Content:     "ACME note remains relevant",
			ContentHash: "sha256:http",
		},
	}, http.StatusAccepted)

	sources := postJSONDecode[sourceMessagesResponse](t, server.URL+"/source-messages", sourceMessagesRequest{
		ChannelID:  "chan-http",
		MessageIDs: []string{"301"},
	}, http.StatusOK)
	if len(sources.Messages) != 1 || sources.Messages[0].SourceHandle != "chan-http/301" {
		t.Fatalf("unexpected source query response: %+v", sources)
	}

	digest := postJSONDecode[digestResponse](t, server.URL+"/digest", digestRequest{
		ChannelIDs: []string{"chan-http"},
		Since:      "24h",
	}, http.StatusOK)
	if len(digest.Blocks) != 1 || digest.Blocks[0].Kind != "raw_excerpt" {
		t.Fatalf("unexpected digest response: %+v", digest)
	}
}

func newTestStore(t *testing.T) *channelMemoryStore {
	t.Helper()
	store, err := openStore(filepath.Join(t.TempDir(), "channel-memory.sqlite"))
	if err != nil {
		t.Fatalf("open test store: %v", err)
	}
	fixedNow := time.Date(2026, 5, 21, 20, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return fixedNow }
	return store
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
