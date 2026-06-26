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

func TestChannelMemoryDeterministicRepeatedDecisionCollapse(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()

	mustIngest := func(id, author, createdAt, content string) {
		t.Helper()
		if _, err := store.Ingest(context.Background(), ingestRequest{
			ChannelID: "chan-decisions",
			Message: ingestMessage{
				ID:          id,
				AuthorName:  author,
				CreatedAt:   createdAt,
				Content:     content,
				ContentHash: "sha256:" + id,
			},
		}); err != nil {
			t.Fatalf("ingest %s: %v", id, err)
		}
	}

	mustIngest("701", "analyst-a", "2026-05-21T18:00:00Z", "Same conclusion as 09:30: no change; wait for cleaner entry.")
	mustIngest("702", "analyst-a", "2026-05-21T18:10:00Z", "Same conclusion as 10:15: no change; wait for cleaner entry.")
	mustIngest("703", "analyst-b", "2026-05-21T18:12:00Z", "Same conclusion as 10:15: no change; wait for cleaner entry.")
	mustIngest("704", "analyst-a", "2026-05-21T18:20:00Z", "[PROPOSED] signal-704 BUY ACME")

	digest, err := store.Digest(context.Background(), digestRequest{ChannelIDs: []string{"chan-decisions"}, Since: "24h"})
	if err != nil {
		t.Fatalf("digest decisions: %v", err)
	}

	var repeated *digestBlock
	foundHardEvent := false
	for i := range digest.Blocks {
		block := &digest.Blocks[i]
		if block.Kind == "decision_repeat" {
			if repeated != nil {
				t.Fatalf("expected one decision_repeat block, got %+v", digest.Blocks)
			}
			repeated = block
		}
		if block.Kind == "hard_event" && len(block.SourceMessages) == 1 && block.SourceMessages[0] == "704" {
			foundHardEvent = true
		}
		if block.Kind == "raw_excerpt" {
			for _, messageID := range block.SourceMessages {
				if messageID == "701" || messageID == "702" {
					t.Fatalf("collapsed decision source %s leaked as raw excerpt: %+v", messageID, block)
				}
			}
		}
	}
	if repeated == nil {
		t.Fatalf("expected repeated decision block, got %+v", digest.Blocks)
	}
	if !repeated.Sparse || repeated.EventType != "repeated_decision" || repeated.Processor != "deterministic" {
		t.Fatalf("unexpected repeated decision metadata: %+v", repeated)
	}
	if got := repeated.SourceMessages; len(got) != 2 || got[0] != "701" || got[1] != "702" {
		t.Fatalf("repeated decision should cover analyst-a sources only, got %+v", got)
	}
	if !strings.Contains(repeated.Text, "repeated a low-change decision x2") {
		t.Fatalf("repeated decision text should include count, got %q", repeated.Text)
	}
	if !foundHardEvent {
		t.Fatalf("hard event should remain explicit, got %+v", digest.Blocks)
	}

	search, err := store.Search(context.Background(), searchRequest{
		ChannelIDs: []string{"chan-decisions"},
		Query:      "low-change decision",
		Since:      "24h",
	})
	if err != nil {
		t.Fatalf("search repeated decision: %v", err)
	}
	if search.Status != "ok" || len(search.DerivedBlocks) != 1 || search.DerivedBlocks[0].Kind != "decision_repeat" {
		t.Fatalf("search should return the sparse repeated decision block, got %+v", search)
	}
}

func TestChannelMemoryDeterministicLowValueAcknowledgementCollapse(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()

	mustIngest := func(id, author, createdAt, content string) {
		t.Helper()
		if _, err := store.Ingest(context.Background(), ingestRequest{
			ChannelID: "chan-acks",
			Message: ingestMessage{
				ID:          id,
				AuthorName:  author,
				CreatedAt:   createdAt,
				Content:     content,
				ContentHash: "sha256:" + id,
			},
		}); err != nil {
			t.Fatalf("ingest %s: %v", id, err)
		}
	}

	mustIngest("801", "analyst-a", "2026-05-21T18:00:00Z", "ok")
	mustIngest("802", "analyst-b", "2026-05-21T18:02:00Z", "Thanks!")
	mustIngest("803", "analyst-c", "2026-05-21T18:03:00Z", "got it.")
	mustIngest("804", "analyst-a", "2026-05-21T18:04:00Z", "ok ACME level still matters")
	mustIngest("805", "analyst-a", "2026-05-21T18:05:00Z", "[APPROVED] signal-805 BUY ACME")

	digest, err := store.Digest(context.Background(), digestRequest{ChannelIDs: []string{"chan-acks"}, Since: "24h"})
	if err != nil {
		t.Fatalf("digest acknowledgements: %v", err)
	}

	var ack *digestBlock
	foundRawContext := false
	foundHardEvent := false
	for i := range digest.Blocks {
		block := &digest.Blocks[i]
		if block.Kind == "low_value_ack" {
			if ack != nil {
				t.Fatalf("expected one low_value_ack block, got %+v", digest.Blocks)
			}
			ack = block
		}
		if block.Kind == "raw_excerpt" && len(block.SourceMessages) == 1 && block.SourceMessages[0] == "804" {
			foundRawContext = true
		}
		if block.Kind == "hard_event" && len(block.SourceMessages) == 1 && block.SourceMessages[0] == "805" {
			foundHardEvent = true
		}
		if block.Kind == "raw_excerpt" {
			for _, messageID := range block.SourceMessages {
				if messageID == "801" || messageID == "802" || messageID == "803" {
					t.Fatalf("low-value acknowledgement %s leaked as raw excerpt: %+v", messageID, block)
				}
			}
		}
	}
	if ack == nil {
		t.Fatalf("expected low_value_ack block, got %+v", digest.Blocks)
	}
	if !ack.Sparse || ack.EventType != "low_value_acknowledgement" || ack.Processor != "deterministic" {
		t.Fatalf("unexpected low-value acknowledgement metadata: %+v", ack)
	}
	if got := ack.SourceMessages; len(got) != 3 || got[0] != "801" || got[1] != "802" || got[2] != "803" {
		t.Fatalf("low-value acknowledgement should cover only exact short acks, got %+v", got)
	}
	if !strings.Contains(ack.Text, "low-value acknowledgements elided: 3 messages from 3 authors") {
		t.Fatalf("low-value acknowledgement text should include count, got %q", ack.Text)
	}
	if !foundRawContext {
		t.Fatalf("non-trivial message should remain raw, got %+v", digest.Blocks)
	}
	if !foundHardEvent {
		t.Fatalf("hard event should remain explicit, got %+v", digest.Blocks)
	}

	search, err := store.Search(context.Background(), searchRequest{
		ChannelIDs: []string{"chan-acks"},
		Query:      "low-value acknowledgements",
		Since:      "24h",
	})
	if err != nil {
		t.Fatalf("search low-value acknowledgement: %v", err)
	}
	if search.Status != "ok" || len(search.DerivedBlocks) != 1 || search.DerivedBlocks[0].Kind != "low_value_ack" {
		t.Fatalf("search should return the sparse low-value acknowledgement block, got %+v", search)
	}
}

func TestChannelMemoryDigestRawRecentBudgetOmitsOlderRawExcerpts(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()

	mustIngest := func(id, createdAt, content string) {
		t.Helper()
		if _, err := store.Ingest(context.Background(), ingestRequest{
			ChannelID: "chan-raw-tier",
			Message: ingestMessage{
				ID:          id,
				AuthorName:  "analyst-a",
				CreatedAt:   createdAt,
				Content:     content,
				ContentHash: "sha256:" + id,
			},
		}); err != nil {
			t.Fatalf("ingest %s: %v", id, err)
		}
	}

	mustIngest("901", "2026-05-21T10:00:00Z", "older chatter that should stay searchable")
	mustIngest("904", "2026-05-21T10:01:00Z", "older chatter that should not spend digest budget")
	mustIngest("905", "2026-05-21T10:02:00Z", "older chatter that also should not spend digest budget")
	mustIngest("902", "2026-05-21T10:05:00Z", "Stop remains 610 until invalidation changes.")
	mustIngest("903", "2026-05-21T19:30:00Z", "recent raw context still matters")

	digest, err := store.Digest(context.Background(), digestRequest{
		ChannelIDs: []string{"chan-raw-tier"},
		Since:      "24h",
		Budget:     digestBudget{MaxBlocks: 2, RawRecent: "2h"},
	})
	if err != nil {
		t.Fatalf("digest with raw_recent: %v", err)
	}
	if digest.Coverage.OlderRawMessages != 3 {
		t.Fatalf("expected three older raw messages omitted, got %+v", digest.Coverage)
	}

	foundOldHardEvent := false
	foundRecentRaw := false
	for _, block := range digest.Blocks {
		if block.Kind == "raw_excerpt" {
			for _, messageID := range block.SourceMessages {
				if messageID == "901" || messageID == "904" || messageID == "905" {
					t.Fatalf("older raw message leaked into digest: %+v", block)
				}
				if messageID == "903" {
					foundRecentRaw = true
				}
			}
		}
		if block.Kind == "hard_event" && len(block.SourceMessages) == 1 && block.SourceMessages[0] == "902" {
			foundOldHardEvent = true
		}
	}
	if !foundOldHardEvent {
		t.Fatalf("old hard event should stay explicit across raw_recent budget, got %+v", digest.Blocks)
	}
	if !foundRecentRaw {
		t.Fatalf("recent raw excerpt should stay explicit, got %+v", digest.Blocks)
	}

	search, err := store.Search(context.Background(), searchRequest{
		ChannelIDs: []string{"chan-raw-tier"},
		Query:      "older chatter",
		Since:      "24h",
	})
	if err != nil {
		t.Fatalf("search omitted raw source: %v", err)
	}
	foundSearchSource := false
	for _, source := range search.SourceMessages {
		if source.MessageID == "901" {
			foundSearchSource = true
			break
		}
	}
	if search.Status != "ok" || !foundSearchSource {
		t.Fatalf("omitted raw source should remain searchable, got %+v", search)
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

	search := postJSONDecode[searchResponse](t, server.URL+"/search", searchRequest{
		ChannelIDs: []string{"chan-http"},
		Query:      "relevant",
		Since:      "24h",
	}, http.StatusOK)
	if search.Status != "ok" || len(search.SourceMessages) != 1 || search.SourceMessages[0].SourceHandle != "chan-http/301" {
		t.Fatalf("unexpected search response: %+v", search)
	}

	digest := postJSONDecode[digestResponse](t, server.URL+"/digest", digestRequest{
		ChannelIDs: []string{"chan-http"},
		Since:      "24h",
	}, http.StatusOK)
	if len(digest.Blocks) != 1 || digest.Blocks[0].Kind != "raw_excerpt" {
		t.Fatalf("unexpected digest response: %+v", digest)
	}
}

func TestChannelMemorySearchSourceAndSparseBlocks(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()

	mustIngest := func(channelID, messageID, author, content string) {
		t.Helper()
		if _, err := store.Ingest(context.Background(), ingestRequest{
			ChannelID: channelID,
			Message: ingestMessage{
				ID:          messageID,
				AuthorName:  author,
				CreatedAt:   "2026-05-21T19:00:00Z",
				Content:     content,
				ContentHash: "sha256:" + channelID + ":" + messageID,
			},
		}); err != nil {
			t.Fatalf("ingest %s/%s: %v", channelID, messageID, err)
		}
	}

	mustIngest("chan-search", "501", "Analyst Alpha", "ACME durable catalyst note")
	mustIngest("chan-search", "502", "Ops Bot", "heartbeat_ok runtime status seq=502")
	mustIngest("chan-search", "503", "Ops Bot", "heartbeat_ok runtime status seq=503")
	mustIngest("chan-other", "601", "Analyst Alpha", "ACME must not leak across channels")

	sourceResp, err := store.Search(context.Background(), searchRequest{
		ChannelIDs: []string{"chan-search"},
		Query:      "acme",
		Author:     "alpha",
		Since:      "24h",
	})
	if err != nil {
		t.Fatalf("search source: %v", err)
	}
	if sourceResp.Status != "ok" || len(sourceResp.SourceMessages) != 1 || sourceResp.SourceMessages[0].SourceHandle != "chan-search/501" {
		t.Fatalf("unexpected source search response: %+v", sourceResp)
	}

	blockResp, err := store.Search(context.Background(), searchRequest{
		ChannelIDs: []string{"chan-search"},
		Query:      "noise elided",
		Since:      "24h",
	})
	if err != nil {
		t.Fatalf("search derived block: %v", err)
	}
	if blockResp.Status != "ok" || len(blockResp.DerivedBlocks) != 1 {
		t.Fatalf("unexpected derived search response: %+v", blockResp)
	}
	if got := blockResp.DerivedBlocks[0].SourceMessages; len(got) != 2 || got[0] != "502" || got[1] != "503" {
		t.Fatalf("derived block source messages = %v", got)
	}

	if _, err := store.Forget(context.Background(), forgetRequest{ChannelID: "chan-search", MessageIDs: []string{"501"}, Reason: "test"}); err != nil {
		t.Fatalf("forget: %v", err)
	}
	forgottenResp, err := store.Search(context.Background(), searchRequest{
		ChannelIDs: []string{"chan-search"},
		Query:      "acme",
		Since:      "24h",
	})
	if err != nil {
		t.Fatalf("search after forget: %v", err)
	}
	if forgottenResp.Status != "empty" || len(forgottenResp.SourceMessages) != 0 {
		t.Fatalf("forgotten source should be excluded, got %+v", forgottenResp)
	}
}

func TestChannelMemoryHTTPAuthWhenConfigured(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()
	server := httptest.NewServer(newHandler(store, handlerConfig{token: "memory-token"}))
	defer server.Close()

	resp, err := http.Post(server.URL+"/ingest", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("unauthorized post: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}

	reqBody, err := json.Marshal(ingestRequest{
		ChannelID: "chan-auth",
		Message: ingestMessage{
			ID:          "401",
			AuthorName:  "alice",
			CreatedAt:   "2026-05-21T16:00:00Z",
			Content:     "authorized content",
			ContentHash: "sha256:auth",
		},
	})
	if err != nil {
		t.Fatalf("marshal authorized request: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, server.URL+"/ingest", bytes.NewReader(reqBody))
	if err != nil {
		t.Fatalf("authorized request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer memory-token")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("authorized post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		var got bytes.Buffer
		_, _ = got.ReadFrom(resp.Body)
		t.Fatalf("expected 202, got %d body=%s", resp.StatusCode, got.String())
	}

	health, err := http.Get(server.URL + "/health")
	if err != nil {
		t.Fatalf("health: %v", err)
	}
	defer health.Body.Close()
	if health.StatusCode != http.StatusOK {
		t.Fatalf("expected health to remain unauthenticated, got %d", health.StatusCode)
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
