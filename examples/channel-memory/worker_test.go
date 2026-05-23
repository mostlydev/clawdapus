package main

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

// fakeLLMClient records prompts and returns scripted results so worker behavior
// is deterministic in tests.
type fakeLLMClient struct {
	mu      sync.Mutex
	prompts []llmDigestPrompt
	respond func(llmDigestPrompt) (llmDigestResult, error)
}

func (f *fakeLLMClient) Summarize(_ context.Context, prompt llmDigestPrompt) (llmDigestResult, error) {
	f.mu.Lock()
	f.prompts = append(f.prompts, prompt)
	f.mu.Unlock()
	return f.respond(prompt)
}

func (f *fakeLLMClient) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.prompts)
}

// citeAllTopicRollup is the happy-path fake: a topic_rollup citing every message
// in the window.
func citeAllTopicRollup(prompt llmDigestPrompt) (llmDigestResult, error) {
	ids := make([]string, 0, len(prompt.Messages))
	for _, m := range prompt.Messages {
		ids = append(ids, m.MessageID)
	}
	return llmDigestResult{
		Kind:           rollupKindTopic,
		Text:           fmt.Sprintf("Rollup of %d messages in %s.", len(ids), prompt.Channel),
		SourceMessages: ids,
		Score:          0.7,
		CostUSD:        0.002,
	}, nil
}

func testWorker(store *channelMemoryStore, client llmDigestClient, mutate func(*workerConfig)) *digestWorker {
	cfg := workerConfig{
		Enabled:              true,
		Provider:             "openai",
		Model:                "gpt-4o-mini",
		Version:              "gpt-4o-mini",
		WindowSize:           3,
		MinWindow:            3,
		PerChannelDailyCalls: 100,
		PerPodDailyCalls:     100,
		Interval:             time.Minute,
	}
	if mutate != nil {
		mutate(&cfg)
	}
	return newDigestWorker(store, client, cfg)
}

func ingestRaw(t *testing.T, store *channelMemoryStore, channel, id, content string, created time.Time, hash string) {
	t.Helper()
	_, err := store.Ingest(context.Background(), ingestRequest{
		ChannelID: channel,
		Message: ingestMessage{
			ID:          id,
			AuthorName:  "member-" + id,
			CreatedAt:   created.UTC().Format(time.RFC3339),
			Content:     content,
			ContentHash: hash,
		},
	})
	if err != nil {
		t.Fatalf("ingest %s: %v", id, err)
	}
}

func digestFor(t *testing.T, store *channelMemoryStore, channel string) digestResponse {
	t.Helper()
	resp, err := store.Digest(context.Background(), digestRequest{ChannelIDs: []string{channel}, Since: "24h"})
	if err != nil {
		t.Fatalf("digest: %v", err)
	}
	return resp
}

func llmBlockState(t *testing.T, store *channelMemoryStore) (found bool, kind string, sparse, dirty, stale bool) {
	t.Helper()
	var k string
	var sp, dr, st int
	// Any error (including sql.ErrNoRows) means there is no LLM block.
	if err := store.db.QueryRowContext(context.Background(),
		`SELECT kind, sparse, dirty, stale FROM derived_blocks WHERE processor = 'llm' ORDER BY id LIMIT 1`,
	).Scan(&k, &sp, &dr, &st); err != nil {
		return false, "", false, false, false
	}
	return true, k, sp != 0, dr != 0, st != 0
}

// TestDigestWorkerCompressesVerboseToSparseAndKeepsHardEvents proves the core
// #262 win: verbose raw_excerpt material collapses into one sparse, fully
// provenanced rollup while hard events stay verbatim, and the digest flips out
// of deterministic-only mode.
func TestDigestWorkerCompressesVerboseToSparseAndKeepsHardEvents(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()
	base := time.Date(2026, 5, 21, 18, 0, 0, 0, time.UTC)

	// One hard event that must be preserved verbatim.
	_, err := store.Ingest(context.Background(), ingestRequest{
		ChannelID: "chan-1",
		Message: ingestMessage{
			ID: "100", AuthorName: "lead", CreatedAt: base.Format(time.RFC3339),
			Content: "[PROPOSED] signal-100 BUY ACME", ContentHash: "sha256:hard-100",
		},
	})
	if err != nil {
		t.Fatalf("ingest hard event: %v", err)
	}
	// Three verbose chatter messages (classify as raw_excerpt).
	ingestRaw(t, store, "chan-1", "101", "anyone grabbing lunch later today", base.Add(time.Minute), "sha256:r101")
	ingestRaw(t, store, "chan-1", "102", "the office coffee machine is broken again", base.Add(2*time.Minute), "sha256:r102")
	ingestRaw(t, store, "chan-1", "103", "weather looks nice for the weekend", base.Add(3*time.Minute), "sha256:r103")

	client := &fakeLLMClient{respond: citeAllTopicRollup}
	worker := testWorker(store, client, nil)

	generated, err := worker.processOnce(context.Background())
	if err != nil {
		t.Fatalf("processOnce: %v", err)
	}
	if generated != 1 {
		t.Fatalf("expected 1 sparse block generated, got %d", generated)
	}
	if client.callCount() != 1 {
		t.Fatalf("expected exactly 1 LLM call, got %d", client.callCount())
	}

	resp := digestFor(t, store, "chan-1")
	if resp.Cost.DeterministicOnly {
		t.Fatalf("expected deterministic_only=false once an LLM block exists: %+v", resp.Cost)
	}
	if resp.Cost.LLMCallsToday != 1 {
		t.Fatalf("expected llm_calls_today=1, got %d", resp.Cost.LLMCallsToday)
	}

	var hardEvents, sparseRollups int
	var sparse digestBlock
	for _, b := range resp.Blocks {
		switch {
		case b.Kind == "hard_event":
			hardEvents++
			if b.Text != "[18:00] lead: [PROPOSED] signal-100 BUY ACME" {
				t.Fatalf("hard event text not preserved verbatim: %q", b.Text)
			}
		case b.Processor == digestProcessorLLM:
			sparseRollups++
			sparse = b
		case b.Kind == "raw_excerpt":
			t.Fatalf("verbose raw_excerpt block %v should have been compressed away", b.SourceMessages)
		}
	}
	if hardEvents != 1 {
		t.Fatalf("expected hard event preserved, got %d", hardEvents)
	}
	if sparseRollups != 1 {
		t.Fatalf("expected 1 sparse rollup, got %d", sparseRollups)
	}
	if !sparse.Sparse || sparse.Kind != rollupKindTopic {
		t.Fatalf("unexpected sparse block: %+v", sparse)
	}
	// Provenance: rollup must cite exactly the three verbose messages.
	want := map[string]bool{"101": true, "102": true, "103": true}
	if len(sparse.SourceMessages) != 3 {
		t.Fatalf("expected 3 source messages on rollup, got %v", sparse.SourceMessages)
	}
	for _, id := range sparse.SourceMessages {
		if !want[id] {
			t.Fatalf("rollup cites unexpected source %q", id)
		}
	}
	// Compression: 4 source messages -> 2 served blocks.
	if len(resp.Blocks) != 2 {
		t.Fatalf("expected 2 served blocks (hard event + rollup), got %d: %+v", len(resp.Blocks), resp.Blocks)
	}
}

// TestDigestWorkerRejectsMalformedAndProvenanceFreeOutput proves invalid model
// output never becomes a block and the adapter stays deterministic.
func TestDigestWorkerRejectsMalformedAndProvenanceFreeOutput(t *testing.T) {
	cases := []struct {
		name    string
		respond func(llmDigestPrompt) (llmDigestResult, error)
	}{
		{"bad kind", func(p llmDigestPrompt) (llmDigestResult, error) {
			return llmDigestResult{Kind: "freeform", Text: "stuff", SourceMessages: []string{p.Messages[0].MessageID}}, nil
		}},
		{"empty text", func(p llmDigestPrompt) (llmDigestResult, error) {
			return llmDigestResult{Kind: rollupKindTopic, Text: "   ", SourceMessages: []string{p.Messages[0].MessageID}}, nil
		}},
		{"provenance free", func(p llmDigestPrompt) (llmDigestResult, error) {
			return llmDigestResult{Kind: rollupKindTopic, Text: "summary", SourceMessages: nil}, nil
		}},
		{"partial provenance", func(p llmDigestPrompt) (llmDigestResult, error) {
			return llmDigestResult{Kind: rollupKindTopic, Text: "summary", SourceMessages: []string{p.Messages[0].MessageID}}, nil
		}},
		{"hallucinated source", func(p llmDigestPrompt) (llmDigestResult, error) {
			return llmDigestResult{Kind: rollupKindTopic, Text: "summary", SourceMessages: []string{"999999"}}, nil
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := newTestStore(t)
			defer store.Close()
			base := time.Date(2026, 5, 21, 18, 0, 0, 0, time.UTC)
			ingestRaw(t, store, "chan-1", "201", "chat one about nothing", base, "sha256:r201")
			ingestRaw(t, store, "chan-1", "202", "chat two about nothing", base.Add(time.Minute), "sha256:r202")
			ingestRaw(t, store, "chan-1", "203", "chat three about nothing", base.Add(2*time.Minute), "sha256:r203")

			client := &fakeLLMClient{respond: tc.respond}
			worker := testWorker(store, client, nil)
			generated, err := worker.processOnce(context.Background())
			if err != nil {
				t.Fatalf("processOnce: %v", err)
			}
			if generated != 0 {
				t.Fatalf("expected 0 blocks generated for %s, got %d", tc.name, generated)
			}
			if found, _, _, _, _ := llmBlockState(t, store); found {
				t.Fatalf("malformed output (%s) must not produce an llm block", tc.name)
			}
			resp := digestFor(t, store, "chan-1")
			if !resp.Cost.DeterministicOnly {
				t.Fatalf("expected deterministic-only fallback after rejection (%s)", tc.name)
			}
			// Deterministic raw_excerpt blocks remain served.
			rawCount := 0
			for _, b := range resp.Blocks {
				if b.Kind == "raw_excerpt" {
					rawCount++
				}
			}
			if rawCount != 3 {
				t.Fatalf("expected 3 deterministic raw_excerpt blocks to remain (%s), got %d", tc.name, rawCount)
			}
			// Failure is recorded against the queue.
			var attempts int
			if err := store.db.QueryRowContext(context.Background(),
				`SELECT COALESCE(MAX(attempts),0) FROM processing_queue WHERE channel_id = 'chan-1'`,
			).Scan(&attempts); err != nil {
				t.Fatalf("queue attempts: %v", err)
			}
			if attempts < 1 {
				t.Fatalf("expected queue failure recorded for %s", tc.name)
			}
		})
	}
}

// TestDigestWorkerCachesByContentHash proves identical windows are not
// re-summarized.
func TestDigestWorkerCachesByContentHash(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()
	base := time.Date(2026, 5, 21, 18, 0, 0, 0, time.UTC)
	ingestRaw(t, store, "chan-1", "301", "first idle message", base, "sha256:r301")
	ingestRaw(t, store, "chan-1", "302", "second idle message", base.Add(time.Minute), "sha256:r302")
	ingestRaw(t, store, "chan-1", "303", "third idle message", base.Add(2*time.Minute), "sha256:r303")

	client := &fakeLLMClient{respond: citeAllTopicRollup}
	worker := testWorker(store, client, nil)

	if _, err := worker.processOnce(context.Background()); err != nil {
		t.Fatalf("processOnce #1: %v", err)
	}
	if client.callCount() != 1 {
		t.Fatalf("expected 1 call after first pass, got %d", client.callCount())
	}
	// Second pass over identical source ids + content hashes: cache hit, no call.
	if _, err := worker.processOnce(context.Background()); err != nil {
		t.Fatalf("processOnce #2: %v", err)
	}
	if client.callCount() != 1 {
		t.Fatalf("expected cache hit (still 1 call), got %d", client.callCount())
	}
}

// TestDigestWorkerEnforcesDailyCostCaps proves the per-pod daily cap stops LLM
// calls and the remaining windows fall back to deterministic.
func TestDigestWorkerEnforcesDailyCostCaps(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()
	base := time.Date(2026, 5, 21, 18, 0, 0, 0, time.UTC)
	// Two full windows in one channel (6 raw messages, window size 3).
	for i := 0; i < 6; i++ {
		id := fmt.Sprintf("4%02d", i)
		ingestRaw(t, store, "chan-1", id, "idle chatter "+id, base.Add(time.Duration(i)*time.Minute), "sha256:r"+id)
	}

	client := &fakeLLMClient{respond: citeAllTopicRollup}
	worker := testWorker(store, client, func(c *workerConfig) {
		c.PerPodDailyCalls = 1
		c.PerChannelDailyCalls = 100
	})

	generated, err := worker.processOnce(context.Background())
	if err != nil {
		t.Fatalf("processOnce: %v", err)
	}
	if generated != 1 {
		t.Fatalf("expected pod cap to allow exactly 1 block, got %d", generated)
	}
	if client.callCount() != 1 {
		t.Fatalf("expected pod cap to allow exactly 1 LLM call, got %d", client.callCount())
	}
	// Queue ordering: the single allowed call must summarize the OLDEST window
	// (messages 400,401,402), not a later one.
	if len(client.prompts) != 1 {
		t.Fatalf("expected 1 recorded prompt, got %d", len(client.prompts))
	}
	gotIDs := make([]string, 0, len(client.prompts[0].Messages))
	for _, m := range client.prompts[0].Messages {
		gotIDs = append(gotIDs, m.MessageID)
	}
	wantIDs := []string{"400", "401", "402"}
	if fmt.Sprint(gotIDs) != fmt.Sprint(wantIDs) {
		t.Fatalf("expected oldest window %v summarized first, got %v", wantIDs, gotIDs)
	}
	// Re-running stays capped (usage persists for the day).
	if _, err := worker.processOnce(context.Background()); err != nil {
		t.Fatalf("processOnce #2: %v", err)
	}
	if client.callCount() != 1 {
		t.Fatalf("expected still 1 call after cap, got %d", client.callCount())
	}
}

// TestDigestWorkerEnforcesDailyUSDCaps proves the USD cap is separate from the
// call cap and can stop a second window even when more calls are allowed.
func TestDigestWorkerEnforcesDailyUSDCaps(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()
	base := time.Date(2026, 5, 21, 18, 0, 0, 0, time.UTC)
	for i := 0; i < 6; i++ {
		id := fmt.Sprintf("45%d", i)
		ingestRaw(t, store, "chan-1", id, "idle chatter "+id, base.Add(time.Duration(i)*time.Minute), "sha256:r"+id)
	}

	client := &fakeLLMClient{respond: func(p llmDigestPrompt) (llmDigestResult, error) {
		result, err := citeAllTopicRollup(p)
		result.CostUSD = 0.30
		return result, err
	}}
	worker := testWorker(store, client, func(c *workerConfig) {
		c.PerPodDailyCalls = 100
		c.PerChannelDailyCalls = 100
		c.PerPodDailyCost = 0.50
		c.PerChannelDailyCost = 0.50
		c.CostPerCall = 0.30
	})

	generated, err := worker.processOnce(context.Background())
	if err != nil {
		t.Fatalf("processOnce: %v", err)
	}
	if generated != 1 {
		t.Fatalf("expected USD cap to allow exactly 1 block, got %d", generated)
	}
	if client.callCount() != 1 {
		t.Fatalf("expected USD cap to allow exactly 1 LLM call, got %d", client.callCount())
	}
	cost, err := store.dailyLLMCost(context.Background(), worker.now().UTC().Format("2006-01-02"), "pod")
	if err != nil {
		t.Fatalf("daily cost: %v", err)
	}
	if cost < 0.2999 || cost > 0.3001 {
		t.Fatalf("expected estimated cost 0.30, got %.4f", cost)
	}
}

// TestDigestWorkerDeterministicFallbackWhenDisabledOrFailing proves the worker
// is inert when disabled and harmless when the model errors.
func TestDigestWorkerDeterministicFallbackWhenDisabledOrFailing(t *testing.T) {
	base := time.Date(2026, 5, 21, 18, 0, 0, 0, time.UTC)

	t.Run("disabled", func(t *testing.T) {
		store := newTestStore(t)
		defer store.Close()
		ingestRaw(t, store, "chan-1", "501", "idle one", base, "sha256:r501")
		ingestRaw(t, store, "chan-1", "502", "idle two", base.Add(time.Minute), "sha256:r502")
		ingestRaw(t, store, "chan-1", "503", "idle three", base.Add(2*time.Minute), "sha256:r503")
		client := &fakeLLMClient{respond: citeAllTopicRollup}
		worker := testWorker(store, client, func(c *workerConfig) { c.Enabled = false })
		generated, err := worker.processOnce(context.Background())
		if err != nil {
			t.Fatalf("processOnce: %v", err)
		}
		if generated != 0 || client.callCount() != 0 {
			t.Fatalf("disabled worker must not call the model: generated=%d calls=%d", generated, client.callCount())
		}
		if !digestFor(t, store, "chan-1").Cost.DeterministicOnly {
			t.Fatal("disabled worker must leave deterministic-only digests")
		}
	})

	t.Run("failing", func(t *testing.T) {
		store := newTestStore(t)
		defer store.Close()
		ingestRaw(t, store, "chan-1", "511", "idle one", base, "sha256:r511")
		ingestRaw(t, store, "chan-1", "512", "idle two", base.Add(time.Minute), "sha256:r512")
		ingestRaw(t, store, "chan-1", "513", "idle three", base.Add(2*time.Minute), "sha256:r513")
		client := &fakeLLMClient{respond: func(llmDigestPrompt) (llmDigestResult, error) {
			return llmDigestResult{}, errors.New("upstream 503")
		}}
		worker := testWorker(store, client, nil)
		generated, err := worker.processOnce(context.Background())
		if err != nil {
			t.Fatalf("processOnce should swallow model errors: %v", err)
		}
		if generated != 0 {
			t.Fatalf("failing model must not produce blocks, got %d", generated)
		}
		resp := digestFor(t, store, "chan-1")
		if !resp.Cost.DeterministicOnly {
			t.Fatal("failing worker must leave deterministic-only digests")
		}
		rawCount := 0
		for _, b := range resp.Blocks {
			if b.Kind == "raw_excerpt" {
				rawCount++
			}
		}
		if rawCount != 3 {
			t.Fatalf("expected 3 deterministic blocks to keep serving, got %d", rawCount)
		}
	})
}

// TestDigestWorkerCountsRejectedCallsAgainstDailyCaps proves invalid or failing
// model calls still consume the call budget. Otherwise a bad provider response
// can be retried every worker interval without tripping the cap.
func TestDigestWorkerCountsRejectedCallsAgainstDailyCaps(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()
	base := time.Date(2026, 5, 21, 18, 0, 0, 0, time.UTC)
	ingestRaw(t, store, "chan-1", "551", "idle one", base, "sha256:r551")
	ingestRaw(t, store, "chan-1", "552", "idle two", base.Add(time.Minute), "sha256:r552")
	ingestRaw(t, store, "chan-1", "553", "idle three", base.Add(2*time.Minute), "sha256:r553")

	client := &fakeLLMClient{respond: func(p llmDigestPrompt) (llmDigestResult, error) {
		return llmDigestResult{Kind: rollupKindTopic, Text: "summary", SourceMessages: nil}, nil
	}}
	worker := testWorker(store, client, func(c *workerConfig) {
		c.PerPodDailyCalls = 1
		c.PerChannelDailyCalls = 1
	})

	if generated, err := worker.processOnce(context.Background()); err != nil || generated != 0 {
		t.Fatalf("first processOnce generated=%d err=%v", generated, err)
	}
	if client.callCount() != 1 {
		t.Fatalf("expected first rejected output to call model once, got %d", client.callCount())
	}
	resp := digestFor(t, store, "chan-1")
	if resp.Cost.LLMCallsToday != 1 {
		t.Fatalf("expected rejected call to count against daily cap, got %d", resp.Cost.LLMCallsToday)
	}
	if !resp.Cost.DeterministicOnly {
		t.Fatal("rejected output must leave digest deterministic-only")
	}

	if generated, err := worker.processOnce(context.Background()); err != nil || generated != 0 {
		t.Fatalf("second processOnce generated=%d err=%v", generated, err)
	}
	if client.callCount() != 1 {
		t.Fatalf("expected daily cap to prevent retry after rejection, got %d calls", client.callCount())
	}
}

// TestDigestWorkerEditAndForgetMarkRollupStaleOrDirty proves provenance-driven
// invalidation: editing or forgetting a covered source dirties the rollup so it
// stops serving until regenerated. Uses synthetic source events because
// claw-wall only observes first sightings.
func TestDigestWorkerEditAndForgetMarkRollupStaleOrDirty(t *testing.T) {
	t.Run("edit dirties rollup", func(t *testing.T) {
		store := newTestStore(t)
		defer store.Close()
		base := time.Date(2026, 5, 21, 18, 0, 0, 0, time.UTC)
		ingestRaw(t, store, "chan-1", "601", "idle one", base, "sha256:r601-v1")
		ingestRaw(t, store, "chan-1", "602", "idle two", base.Add(time.Minute), "sha256:r602")
		ingestRaw(t, store, "chan-1", "603", "idle three", base.Add(2*time.Minute), "sha256:r603")
		worker := testWorker(store, &fakeLLMClient{respond: citeAllTopicRollup}, nil)
		if _, err := worker.processOnce(context.Background()); err != nil {
			t.Fatalf("processOnce: %v", err)
		}
		if found, _, _, dirty, _ := llmBlockState(t, store); !found || dirty {
			t.Fatalf("expected a fresh non-dirty rollup, found=%v dirty=%v", found, dirty)
		}
		// Synthetic edit of message 601: new content hash for same id.
		ingestRaw(t, store, "chan-1", "601", "idle one (edited)", base, "sha256:r601-v2")
		found, _, _, dirty, _ := llmBlockState(t, store)
		if !found || !dirty {
			t.Fatalf("expected rollup dirtied after edit, found=%v dirty=%v", found, dirty)
		}
		// Dirty blocks are not served.
		rawCount := 0
		for _, b := range digestFor(t, store, "chan-1").Blocks {
			if b.Processor == digestProcessorLLM {
				t.Fatal("dirty rollup must not be served")
			}
			if b.Kind == "raw_excerpt" {
				rawCount++
			}
		}
		if rawCount != 3 {
			t.Fatalf("expected deterministic raw_excerpt fallback after edit, got %d", rawCount)
		}
	})

	t.Run("forget dirties rollup", func(t *testing.T) {
		store := newTestStore(t)
		defer store.Close()
		base := time.Date(2026, 5, 21, 18, 0, 0, 0, time.UTC)
		ingestRaw(t, store, "chan-1", "701", "idle one", base, "sha256:r701")
		ingestRaw(t, store, "chan-1", "702", "idle two", base.Add(time.Minute), "sha256:r702")
		ingestRaw(t, store, "chan-1", "703", "idle three", base.Add(2*time.Minute), "sha256:r703")
		worker := testWorker(store, &fakeLLMClient{respond: citeAllTopicRollup}, nil)
		if _, err := worker.processOnce(context.Background()); err != nil {
			t.Fatalf("processOnce: %v", err)
		}
		if _, err := store.Forget(context.Background(), forgetRequest{ChannelID: "chan-1", MessageIDs: []string{"702"}, Reason: "pii"}); err != nil {
			t.Fatalf("forget: %v", err)
		}
		found, _, _, dirty, _ := llmBlockState(t, store)
		if !found || !dirty {
			t.Fatalf("expected rollup dirtied after forget, found=%v dirty=%v", found, dirty)
		}
		got := make(map[string]bool)
		for _, b := range digestFor(t, store, "chan-1").Blocks {
			if b.Processor == digestProcessorLLM {
				t.Fatal("dirty rollup must not be served after forget")
			}
			if b.Kind == "raw_excerpt" && len(b.SourceMessages) == 1 {
				got[b.SourceMessages[0]] = true
			}
		}
		if got["702"] {
			t.Fatalf("forgotten source 702 must not return in fallback: %v", got)
		}
		if !got["701"] || !got["703"] || len(got) != 2 {
			t.Fatalf("expected current raw fallback for 701 and 703 after forget, got %v", got)
		}
	})
}
