package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

// The async digest worker turns verbose channel material into compact sparse
// rollup blocks using an LLM, strictly off the /digest hot path. Hard events
// keep their verbatim deterministic blocks; only raw_excerpt windows are
// compressed. Every generated block carries exact source provenance, the
// worker caches by source ids + content hashes, enforces conservative daily
// call caps per channel and per pod, and falls back to deterministic-only
// output whenever it is disabled, over budget, or failing.

const (
	digestProcessorLLM = "llm"
	rollupKindMessage  = "message_summary"
	rollupKindTopic    = "topic_rollup"
	rollupKindSequence = "sequence_rollup"

	defaultWorkerWindowSize       = 8
	defaultWorkerMinWindow        = 3
	defaultWorkerLongMessageChars = 1200
	defaultWorkerPerChannelCalls  = 24
	defaultWorkerPerPodCalls      = 96
	defaultWorkerPerChannelCost   = 0.50
	defaultWorkerPerPodCost       = 2.00
	defaultWorkerCostPerCall      = 0.002
	defaultWorkerIntervalSeconds  = 60
	defaultWorkerLLMTimeoutSecond = 90
)

type llmPromptMessage struct {
	MessageID string
	Author    string
	CreatedAt string
	Content   string
}

type llmDigestPrompt struct {
	Channel  string
	Messages []llmPromptMessage
}

// llmDigestResult is the structured contract the worker requires from any
// model. Free-form prose without provenance is rejected.
type llmDigestResult struct {
	Kind           string   `json:"kind"`
	Text           string   `json:"text"`
	SourceMessages []string `json:"source_messages"`
	Score          float64  `json:"score"`
	CostUSD        float64  `json:"cost_usd"`
}

func (result *llmDigestResult) UnmarshalJSON(raw []byte) error {
	var aux struct {
		Kind           string       `json:"kind"`
		Text           string       `json:"text"`
		SourceMessages sourceIDList `json:"source_messages"`
		Score          float64      `json:"score"`
		CostUSD        float64      `json:"cost_usd"`
	}
	if err := json.Unmarshal(raw, &aux); err != nil {
		return err
	}
	result.Kind = aux.Kind
	result.Text = aux.Text
	result.SourceMessages = []string(aux.SourceMessages)
	result.Score = aux.Score
	result.CostUSD = aux.CostUSD
	return nil
}

// sourceIDList accepts provider output that encodes Discord snowflakes as
// either strings or bare JSON integers. Bare integers are common from models,
// but the raw JSON token is the only safe source of truth because snowflakes can
// exceed JavaScript's safe integer range.
type sourceIDList []string

func (ids *sourceIDList) UnmarshalJSON(raw []byte) error {
	var values []json.RawMessage
	if err := json.Unmarshal(raw, &values); err != nil {
		return err
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(string(value))
		if trimmed == "" || trimmed == "null" {
			continue
		}
		if strings.HasPrefix(trimmed, `"`) {
			var id string
			if err := json.Unmarshal(value, &id); err != nil {
				return err
			}
			out = append(out, id)
			continue
		}
		if !isJSONIntegerLiteral(trimmed) {
			return fmt.Errorf("source_messages entry %q must be a string or integer literal", trimmed)
		}
		out = append(out, trimmed)
	}
	*ids = out
	return nil
}

func isJSONIntegerLiteral(value string) bool {
	if value == "" {
		return false
	}
	for i, r := range value {
		if i == 0 && r == '-' {
			return len(value) > 1
		}
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

type llmDigestClient interface {
	Summarize(ctx context.Context, prompt llmDigestPrompt) (llmDigestResult, error)
}

type workerConfig struct {
	Enabled              bool
	Provider             string
	Model                string
	Version              string
	WindowSize           int
	MinWindow            int
	LongMessageChars     int
	PerChannelDailyCalls int
	PerPodDailyCalls     int
	PerChannelDailyCost  float64
	PerPodDailyCost      float64
	CostPerCall          float64
	Interval             time.Duration
}

type digestWorker struct {
	store  *channelMemoryStore
	client llmDigestClient
	cfg    workerConfig
	now    func() time.Time
	logf   func(format string, args ...any)
}

func workerConfigFromEnv() workerConfig {
	cfg := workerConfig{
		Enabled:              boolEnv("CHANNEL_MEMORY_LLM_ENABLED", false),
		Provider:             strings.TrimSpace(os.Getenv("CHANNEL_MEMORY_LLM_PROVIDER")),
		Model:                strings.TrimSpace(os.Getenv("CHANNEL_MEMORY_LLM_MODEL")),
		Version:              strings.TrimSpace(os.Getenv("CHANNEL_MEMORY_LLM_VERSION")),
		WindowSize:           intEnv("CHANNEL_MEMORY_LLM_WINDOW", defaultWorkerWindowSize),
		MinWindow:            intEnv("CHANNEL_MEMORY_LLM_MIN_WINDOW", defaultWorkerMinWindow),
		LongMessageChars:     intEnv("CHANNEL_MEMORY_LLM_LONG_MESSAGE_CHARS", defaultWorkerLongMessageChars),
		PerChannelDailyCalls: intEnv("CHANNEL_MEMORY_LLM_PER_CHANNEL_DAILY", defaultWorkerPerChannelCalls),
		PerPodDailyCalls:     intEnv("CHANNEL_MEMORY_LLM_PER_POD_DAILY", defaultWorkerPerPodCalls),
		PerChannelDailyCost:  floatEnv("CHANNEL_MEMORY_LLM_PER_CHANNEL_DAILY_USD", defaultWorkerPerChannelCost),
		PerPodDailyCost:      floatEnv("CHANNEL_MEMORY_LLM_PER_POD_DAILY_USD", defaultWorkerPerPodCost),
		CostPerCall:          floatEnv("CHANNEL_MEMORY_LLM_COST_PER_CALL_USD", defaultWorkerCostPerCall),
		Interval:             time.Duration(intEnv("CHANNEL_MEMORY_LLM_INTERVAL_SECONDS", defaultWorkerIntervalSeconds)) * time.Second,
	}
	if cfg.Provider == "" {
		cfg.Provider = "openai"
	}
	if cfg.Version == "" {
		cfg.Version = cfg.Model
	}
	return cfg
}

func newDigestWorker(store *channelMemoryStore, client llmDigestClient, cfg workerConfig) *digestWorker {
	if cfg.WindowSize <= 0 {
		cfg.WindowSize = defaultWorkerWindowSize
	}
	if cfg.MinWindow <= 0 {
		cfg.MinWindow = defaultWorkerMinWindow
	}
	if cfg.MinWindow > cfg.WindowSize {
		cfg.MinWindow = cfg.WindowSize
	}
	if cfg.LongMessageChars < 0 {
		cfg.LongMessageChars = 0
	}
	if cfg.Interval <= 0 {
		cfg.Interval = defaultWorkerIntervalSeconds * time.Second
	}
	if cfg.CostPerCall < 0 {
		cfg.CostPerCall = 0
	}
	return &digestWorker{
		store:  store,
		client: client,
		cfg:    cfg,
		now:    store.now,
		logf:   log.Printf,
	}
}

// Run drives the worker on a ticker until the context is cancelled. It is a
// no-op when the worker is disabled or has no client, leaving the adapter in
// deterministic-only mode.
func (w *digestWorker) Run(ctx context.Context) {
	if !w.enabled() {
		w.logf("channel-memory digest worker disabled; deterministic-only digests")
		return
	}
	ticker := time.NewTicker(w.cfg.Interval)
	defer ticker.Stop()
	for {
		if _, err := w.processOnce(ctx); err != nil {
			w.logf("channel-memory digest worker: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (w *digestWorker) enabled() bool {
	return w != nil && w.cfg.Enabled && w.client != nil
}

// processOnce scans every channel for long single messages and raw_excerpt
// windows that lack a fresh sparse summary/rollup, then compresses them oldest
// first. It returns the number of blocks generated. It never blocks the
// /digest path and degrades to a no-op on any failure so deterministic blocks
// keep serving.
func (w *digestWorker) processOnce(ctx context.Context) (int, error) {
	if !w.enabled() {
		return 0, nil
	}
	sourceKind := normalizeSourceKind("", "")
	channels, err := w.store.allChannels(ctx, sourceKind)
	if err != nil {
		return 0, err
	}
	day := w.now().UTC().Format("2006-01-02")
	generated := 0
	for _, channelID := range channels {
		podOK, err := w.podBudgetAvailable(ctx, day)
		if err != nil {
			return generated, err
		}
		if !podOK {
			break
		}
		channelOK, err := w.channelBudgetAvailable(ctx, day, channelID)
		if err != nil {
			return generated, err
		}
		if !channelOK {
			continue
		}

		podExhausted := false
		channelExhausted := false
		messageWindows, err := w.store.candidateLongMessageWindows(ctx, sourceKind, channelID, w.cfg.LongMessageChars)
		if err != nil {
			return generated, err
		}
		for _, window := range messageWindows {
			podOK, err = w.podBudgetAvailable(ctx, day)
			if err != nil {
				return generated, err
			}
			if !podOK {
				podExhausted = true
				break
			}
			channelOK, err = w.channelBudgetAvailable(ctx, day, channelID)
			if err != nil {
				return generated, err
			}
			if !channelOK {
				channelExhausted = true
				break
			}

			meta := blockMetadata{
				Provider: w.cfg.Provider,
				Model:    w.cfg.Model,
				Version:  w.cfg.Version,
			}
			key := llmMessageSummaryBlockKey(sourceKind, channelID, window, meta)
			fresh, err := w.store.freshBlockExists(ctx, key)
			if err != nil {
				return generated, err
			}
			if fresh {
				// Cache hit: identical message id + content hash + compactor
				// identity already summarized. No LLM call.
				continue
			}

			if err := w.store.recordLLMUsage(ctx, day, channelID, 0); err != nil {
				return generated, err
			}
			result, err := w.client.Summarize(ctx, promptForWindow(channelID, window))
			if err != nil {
				w.logf("channel-memory digest worker: summarize long message channel %s: %v", channelID, err)
				_ = w.store.recordQueueFailure(ctx, sourceKind, channelID, window, err.Error())
				continue
			}
			costUSD := w.costForResult(result)
			result.CostUSD = costUSD
			if costUSD != 0 {
				if err := w.store.recordLLMCost(ctx, day, channelID, costUSD); err != nil {
					return generated, err
				}
			}
			if err := validateRollup(result, window); err != nil {
				w.logf("channel-memory digest worker: rejected long-message summary for channel %s: %v", channelID, err)
				_ = w.store.recordQueueFailure(ctx, sourceKind, channelID, window, err.Error())
				continue
			}
			meta.CostUSD = result.CostUSD
			if err := w.store.writeSparseRollup(ctx, key, sourceKind, channelID, window, result, meta, w.now()); err != nil {
				return generated, err
			}
			generated++
		}
		if podExhausted {
			break
		}
		if channelExhausted {
			continue
		}

		windows, err := w.store.candidateRollupWindows(ctx, sourceKind, channelID, w.cfg.WindowSize, w.cfg.MinWindow, w.cfg.LongMessageChars)
		if err != nil {
			return generated, err
		}
		for _, window := range windows {
			podOK, err = w.podBudgetAvailable(ctx, day)
			if err != nil {
				return generated, err
			}
			if !podOK {
				break
			}
			channelOK, err = w.channelBudgetAvailable(ctx, day, channelID)
			if err != nil {
				return generated, err
			}
			if !channelOK {
				break
			}

			key := llmRollupBlockKey(sourceKind, channelID, window)
			fresh, err := w.store.freshBlockExists(ctx, key)
			if err != nil {
				return generated, err
			}
			if fresh {
				// Cache hit: identical source ids + content hashes already
				// summarized. No LLM call.
				continue
			}

			if err := w.store.recordLLMUsage(ctx, day, channelID, 0); err != nil {
				return generated, err
			}
			result, err := w.client.Summarize(ctx, promptForWindow(channelID, window))
			if err != nil {
				w.logf("channel-memory digest worker: summarize channel %s: %v", channelID, err)
				_ = w.store.recordQueueFailure(ctx, sourceKind, channelID, window, err.Error())
				continue
			}
			costUSD := w.costForResult(result)
			result.CostUSD = costUSD
			if costUSD != 0 {
				if err := w.store.recordLLMCost(ctx, day, channelID, costUSD); err != nil {
					return generated, err
				}
			}
			if err := validateRollup(result, window); err != nil {
				w.logf("channel-memory digest worker: rejected rollup for channel %s: %v", channelID, err)
				_ = w.store.recordQueueFailure(ctx, sourceKind, channelID, window, err.Error())
				continue
			}
			meta := blockMetadata{
				Provider: w.cfg.Provider,
				Model:    w.cfg.Model,
				Version:  w.cfg.Version,
				CostUSD:  result.CostUSD,
			}
			if err := w.store.writeSparseRollup(ctx, key, sourceKind, channelID, window, result, meta, w.now()); err != nil {
				return generated, err
			}
			generated++
		}
	}
	return generated, nil
}

func (w *digestWorker) podBudgetAvailable(ctx context.Context, day string) (bool, error) {
	calls, err := w.store.dailyLLMCalls(ctx, day, "pod")
	if err != nil {
		return false, err
	}
	if calls >= w.cfg.PerPodDailyCalls {
		w.logf("channel-memory digest worker: per-pod daily call cap %d reached", w.cfg.PerPodDailyCalls)
		return false, nil
	}
	cost, err := w.store.dailyLLMCost(ctx, day, "pod")
	if err != nil {
		return false, err
	}
	if w.costWouldExceed(cost, w.cfg.PerPodDailyCost) {
		w.logf("channel-memory digest worker: per-pod daily cost cap %.4f reached", w.cfg.PerPodDailyCost)
		return false, nil
	}
	return true, nil
}

func (w *digestWorker) channelBudgetAvailable(ctx context.Context, day, channelID string) (bool, error) {
	scope := "channel:" + channelID
	calls, err := w.store.dailyLLMCalls(ctx, day, scope)
	if err != nil {
		return false, err
	}
	if calls >= w.cfg.PerChannelDailyCalls {
		return false, nil
	}
	cost, err := w.store.dailyLLMCost(ctx, day, scope)
	if err != nil {
		return false, err
	}
	if w.costWouldExceed(cost, w.cfg.PerChannelDailyCost) {
		return false, nil
	}
	return true, nil
}

func (w *digestWorker) costWouldExceed(current, cap float64) bool {
	if cap <= 0 {
		return false
	}
	projected := current
	if w.cfg.CostPerCall > 0 {
		projected += w.cfg.CostPerCall
	}
	return projected > cap
}

func (w *digestWorker) costForResult(result llmDigestResult) float64 {
	if result.CostUSD > 0 {
		return result.CostUSD
	}
	return w.cfg.CostPerCall
}

type rollupWindow struct {
	Sources []storedSourceMessage
}

func (win rollupWindow) ids() []string {
	out := make([]string, 0, len(win.Sources))
	for _, s := range win.Sources {
		out = append(out, s.MessageID)
	}
	return out
}

func (win rollupWindow) idSet() map[string]string {
	out := make(map[string]string, len(win.Sources))
	for _, s := range win.Sources {
		out[s.MessageID] = s.ContentHash
	}
	return out
}

func promptForWindow(channelID string, window rollupWindow) llmDigestPrompt {
	msgs := make([]llmPromptMessage, 0, len(window.Sources))
	for _, s := range window.Sources {
		msgs = append(msgs, llmPromptMessage{
			MessageID: s.MessageID,
			Author:    firstNonEmpty(s.AuthorName, s.AuthorID, "unknown"),
			CreatedAt: s.CreatedAt,
			Content:   s.Content,
		})
	}
	return llmDigestPrompt{Channel: channelID, Messages: msgs}
}

func validateRollup(result llmDigestResult, window rollupWindow) error {
	switch result.Kind {
	case rollupKindMessage:
		if len(window.Sources) != 1 {
			return fmt.Errorf("%s requires exactly one source, got %d", rollupKindMessage, len(window.Sources))
		}
	case rollupKindTopic, rollupKindSequence:
		if len(window.Sources) == 1 {
			return fmt.Errorf("single-message summaries must use %s", rollupKindMessage)
		}
	default:
		return fmt.Errorf("unexpected rollup kind %q", result.Kind)
	}
	if strings.TrimSpace(result.Text) == "" {
		return errors.New("empty rollup text")
	}
	cited := trimNonEmpty(result.SourceMessages)
	if len(cited) == 0 {
		return errors.New("rollup is provenance-free (no source_messages)")
	}
	allowed := window.idSet()
	seen := make(map[string]struct{}, len(cited))
	for _, id := range cited {
		if _, ok := allowed[id]; !ok {
			return fmt.Errorf("rollup cites source %q outside its window", id)
		}
		if _, ok := seen[id]; ok {
			return fmt.Errorf("rollup cites source %q more than once", id)
		}
		seen[id] = struct{}{}
	}
	if len(seen) != len(allowed) {
		for id := range allowed {
			if _, ok := seen[id]; !ok {
				return fmt.Errorf("rollup omits source %q from its window", id)
			}
		}
		return fmt.Errorf("rollup cites %d sources, expected %d", len(seen), len(allowed))
	}
	return nil
}

func llmRollupBlockKey(sourceKind, channelID string, window rollupWindow) string {
	parts := make([]string, 0, len(window.Sources))
	for _, s := range window.Sources {
		parts = append(parts, s.MessageID+"@"+s.ContentHash)
	}
	sort.Strings(parts)
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return strings.Join([]string{"llm", sourceKind, channelID, hex.EncodeToString(sum[:])}, ":")
}

func llmMessageSummaryBlockKey(sourceKind, channelID string, window rollupWindow, meta blockMetadata) string {
	if len(window.Sources) != 1 {
		return llmRollupBlockKey(sourceKind, channelID, window)
	}
	source := window.Sources[0]
	sum := sha256.Sum256([]byte(strings.Join([]string{
		sourceKind,
		channelID,
		source.MessageID,
		source.ContentHash,
		meta.Provider,
		meta.Model,
		meta.Version,
	}, "\x00")))
	return strings.Join([]string{"llm_message", sourceKind, channelID, source.MessageID, hex.EncodeToString(sum[:])}, ":")
}

type blockMetadata struct {
	Provider string  `json:"provider"`
	Model    string  `json:"model"`
	Version  string  `json:"version"`
	CostUSD  float64 `json:"cost_usd"`
}

// candidateRollupWindows groups the channel's current, non-deleted,
// non-forgotten raw_excerpt messages (the verbose, low-signal material) into
// ordered windows. Hard events and telemetry noise are excluded so they keep
// their faithful deterministic blocks.
func (s *channelMemoryStore) candidateLongMessageWindows(ctx context.Context, sourceKind, channelID string, minChars int) ([]rollupWindow, error) {
	if minChars <= 0 {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, source_kind, channel_id, message_id, content_hash, author_id, author_name,
		       created_at, edited_at, deleted, content, service, surface, guild_id, visibility_scope,
		       observed_seq, observed_at, is_current
		FROM source_messages
		WHERE source_kind = ? AND channel_id = ? AND is_current = 1 AND deleted = 0 AND forgotten_at = ''
		ORDER BY created_at, observed_seq`,
		sourceKind, channelID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	windows := make([]rollupWindow, 0)
	for rows.Next() {
		record, err := scanStoredSource(rows)
		if err != nil {
			return nil, err
		}
		if !isLongMessageSummaryCandidate(record.Content, minChars) {
			continue
		}
		windows = append(windows, rollupWindow{Sources: []storedSourceMessage{record}})
	}
	return windows, rows.Err()
}

func (s *channelMemoryStore) candidateRollupWindows(ctx context.Context, sourceKind, channelID string, windowSize, minWindow, longMessageChars int) ([]rollupWindow, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, source_kind, channel_id, message_id, content_hash, author_id, author_name,
		       created_at, edited_at, deleted, content, service, surface, guild_id, visibility_scope,
		       observed_seq, observed_at, is_current
		FROM source_messages
		WHERE source_kind = ? AND channel_id = ? AND is_current = 1 AND deleted = 0 AND forgotten_at = ''
		ORDER BY created_at, observed_seq`,
		sourceKind, channelID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	eligible := make([]storedSourceMessage, 0)
	for rows.Next() {
		record, err := scanStoredSource(rows)
		if err != nil {
			return nil, err
		}
		if isTelemetryNoise(record.Content) {
			continue
		}
		if kind, _, _ := classifySourceContent(record.Content); kind != "raw_excerpt" {
			continue
		}
		if isLongMessageSummaryCandidate(record.Content, longMessageChars) {
			continue
		}
		eligible = append(eligible, record)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	windows := make([]rollupWindow, 0)
	for start := 0; start < len(eligible); start += windowSize {
		end := start + windowSize
		if end > len(eligible) {
			end = len(eligible)
		}
		chunk := eligible[start:end]
		if len(chunk) < minWindow {
			break
		}
		windows = append(windows, rollupWindow{Sources: append([]storedSourceMessage(nil), chunk...)})
	}
	return windows, nil
}

func isLongMessageSummaryCandidate(content string, minChars int) bool {
	if minChars <= 0 {
		return false
	}
	trimmed := strings.TrimSpace(content)
	if len([]rune(trimmed)) <= minChars {
		return false
	}
	if isTelemetryNoise(trimmed) || isLowValueAcknowledgement(trimmed) {
		return false
	}
	kind, _, _ := classifySourceContent(trimmed)
	return kind == "raw_excerpt"
}

func (s *channelMemoryStore) freshBlockExists(ctx context.Context, blockKey string) (bool, error) {
	var one int
	err := s.db.QueryRowContext(ctx,
		`SELECT 1 FROM derived_blocks WHERE block_key = ? AND stale = 0 AND dirty = 0`,
		blockKey,
	).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// writeSparseRollup persists the LLM rollup block with full source provenance
// for /digest to prefer over the verbose per-message raw_excerpt blocks while
// it remains fresh. The deterministic raw blocks are left intact so they become
// the immediate fallback if the rollup is later dirtied or staled.
func (s *channelMemoryStore) writeSparseRollup(ctx context.Context, blockKey, sourceKind, channelID string, window rollupWindow, result llmDigestResult, meta blockMetadata, now time.Time) error {
	if len(window.Sources) == 0 {
		return errors.New("empty rollup window")
	}
	metaJSON, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollbackUnlessCommitted(tx)

	from := window.Sources[0].CreatedAt
	to := window.Sources[len(window.Sources)-1].CreatedAt
	generatedAt := now.UTC().Format(time.RFC3339)
	score := result.Score
	if score <= 0 {
		score = 0.5
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO derived_blocks(
			block_key, kind, event_type, text, source_channel,
			source_window_from, source_window_to, sparse, score, generated_at, stale, dirty, processor, metadata_json
		) VALUES (?, ?, '', ?, ?, ?, ?, 1, ?, ?, 0, 0, ?, ?)
		ON CONFLICT(block_key) DO UPDATE SET
			kind = excluded.kind,
			text = excluded.text,
			source_window_from = excluded.source_window_from,
			source_window_to = excluded.source_window_to,
			score = excluded.score,
			generated_at = excluded.generated_at,
			stale = 0,
			dirty = 0,
			processor = excluded.processor,
			metadata_json = excluded.metadata_json`,
		blockKey, result.Kind, strings.TrimSpace(result.Text), channelID,
		from, to, score, generatedAt, digestProcessorLLM, string(metaJSON),
	); err != nil {
		return err
	}
	var blockID int64
	if err := tx.QueryRowContext(ctx, `SELECT id FROM derived_blocks WHERE block_key = ?`, blockKey).Scan(&blockID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM derived_block_sources WHERE block_id = ?`, blockID); err != nil {
		return err
	}
	for _, src := range window.Sources {
		if _, err := tx.ExecContext(ctx, `
			INSERT OR IGNORE INTO derived_block_sources(block_id, source_kind, channel_id, message_id, content_hash)
			VALUES (?, ?, ?, ?, ?)`,
			blockID, src.SourceKind, src.ChannelID, src.MessageID, src.ContentHash,
		); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *channelMemoryStore) dailyLLMCalls(ctx context.Context, day, scope string) (int, error) {
	var calls int
	err := s.db.QueryRowContext(ctx,
		`SELECT calls FROM llm_usage WHERE day = ? AND scope = ?`,
		day, scope,
	).Scan(&calls)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return calls, nil
}

func (s *channelMemoryStore) dailyLLMCost(ctx context.Context, day, scope string) (float64, error) {
	var cost float64
	err := s.db.QueryRowContext(ctx,
		`SELECT cost_usd FROM llm_usage WHERE day = ? AND scope = ?`,
		day, scope,
	).Scan(&cost)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return cost, nil
}

func (s *channelMemoryStore) recordLLMUsage(ctx context.Context, day, channelID string, costUSD float64) error {
	for _, scope := range []string{"pod", "channel:" + channelID} {
		if _, err := s.db.ExecContext(ctx, `
			INSERT INTO llm_usage(day, scope, calls, cost_usd)
			VALUES (?, ?, 1, ?)
			ON CONFLICT(day, scope) DO UPDATE SET
				calls = calls + 1,
				cost_usd = cost_usd + excluded.cost_usd`,
			day, scope, costUSD,
		); err != nil {
			return err
		}
	}
	return nil
}

func (s *channelMemoryStore) recordLLMCost(ctx context.Context, day, channelID string, costUSD float64) error {
	for _, scope := range []string{"pod", "channel:" + channelID} {
		if _, err := s.db.ExecContext(ctx, `
			INSERT INTO llm_usage(day, scope, calls, cost_usd)
			VALUES (?, ?, 0, ?)
			ON CONFLICT(day, scope) DO UPDATE SET
				cost_usd = cost_usd + excluded.cost_usd`,
			day, scope, costUSD,
		); err != nil {
			return err
		}
	}
	return nil
}

func (s *channelMemoryStore) recordQueueFailure(ctx context.Context, sourceKind, channelID string, window rollupWindow, message string) error {
	now := s.now().UTC().Format(time.RFC3339)
	for _, src := range window.Sources {
		if _, err := s.db.ExecContext(ctx, `
			UPDATE processing_queue
			SET attempts = attempts + 1, last_error = ?, updated_at = ?
			WHERE source_kind = ? AND channel_id = ? AND message_id = ? AND content_hash = ?`,
			truncateError(message), now, src.SourceKind, channelID, src.MessageID, src.ContentHash,
		); err != nil {
			return err
		}
	}
	return nil
}

func truncateError(message string) string {
	message = strings.TrimSpace(message)
	if len(message) > 500 {
		return message[:500]
	}
	return message
}

// httpLLMClient calls an OpenAI-compatible chat completion endpoint and expects
// a JSON object matching llmDigestResult. It is intentionally minimal; richer
// providers can implement llmDigestClient directly.
type httpLLMClient struct {
	baseURL string
	apiKey  string
	model   string
	client  *http.Client
}

func httpLLMClientFromEnv(cfg workerConfig) llmDigestClient {
	baseURL := strings.TrimSpace(os.Getenv("CHANNEL_MEMORY_LLM_BASE_URL"))
	if baseURL == "" || strings.TrimSpace(cfg.Model) == "" {
		return nil
	}
	return &httpLLMClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  strings.TrimSpace(os.Getenv("CHANNEL_MEMORY_LLM_API_KEY")),
		model:   cfg.Model,
		client:  &http.Client{Timeout: time.Duration(intEnv("CHANNEL_MEMORY_LLM_TIMEOUT_SECONDS", defaultWorkerLLMTimeoutSecond)) * time.Second},
	}
}

func (c *httpLLMClient) Summarize(ctx context.Context, prompt llmDigestPrompt) (llmDigestResult, error) {
	var transcript strings.Builder
	for _, m := range prompt.Messages {
		fmt.Fprintf(&transcript, "[%s] (id=%s) %s: %s\n", m.CreatedAt, m.MessageID, m.Author, m.Content)
	}
	system := "You compress Discord channel transcripts into compact digest blocks. " +
		"For one long source message, use kind \"message_summary\". For multi-message windows, use \"topic_rollup\" or \"sequence_rollup\". " +
		"Respond ONLY with a JSON object: {\"kind\":\"message_summary\"|\"topic_rollup\"|\"sequence_rollup\",\"text\":string,\"source_messages\":[message id strings you summarized],\"score\":0..1}. " +
		"source_messages MUST list the exact message ids you used as JSON strings, because Discord ids can be too large for safe JSON numbers. Do not invent ids."
	payload := map[string]any{
		"model": c.model,
		"messages": []map[string]string{
			{"role": "system", "content": system},
			{"role": "user", "content": "Channel " + prompt.Channel + " transcript:\n" + transcript.String()},
		},
		"response_format": map[string]string{"type": "json_object"},
		"temperature":     0,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return llmDigestResult{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return llmDigestResult{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return llmDigestResult{}, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return llmDigestResult{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return llmDigestResult{}, fmt.Errorf("llm endpoint returned %s: %s", resp.Status, strings.TrimSpace(string(raw)))
	}
	var completion struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &completion); err != nil {
		return llmDigestResult{}, fmt.Errorf("decode completion: %w", err)
	}
	if len(completion.Choices) == 0 {
		return llmDigestResult{}, errors.New("llm endpoint returned no choices")
	}
	return decodeLLMDigestContent(completion.Choices[0].Message.Content)
}

func decodeLLMDigestContent(content string) (llmDigestResult, error) {
	var result llmDigestResult
	trimmed := strings.TrimSpace(content)
	if err := json.Unmarshal([]byte(trimmed), &result); err == nil {
		return result, nil
	} else if candidate, ok := firstJSONObject(trimmed); ok {
		if err := json.Unmarshal([]byte(candidate), &result); err == nil {
			return result, nil
		}
		return llmDigestResult{}, fmt.Errorf("decode rollup json: %w", err)
	} else {
		return llmDigestResult{}, fmt.Errorf("decode rollup json: %w", err)
	}
}

func firstJSONObject(content string) (string, bool) {
	start := -1
	depth := 0
	inString := false
	escaped := false
	for i := 0; i < len(content); i++ {
		b := content[i]
		if start == -1 {
			if b == '{' {
				start = i
				depth = 1
			}
			continue
		}
		if inString {
			switch {
			case escaped:
				escaped = false
			case b == '\\':
				escaped = true
			case b == '"':
				inString = false
			}
			continue
		}
		switch b {
		case '"':
			inString = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return content[start : i+1], true
			}
		}
	}
	return "", false
}

func boolEnv(name string, fallback bool) bool {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	value, err := strconv.ParseBool(raw)
	if err != nil {
		return fallback
	}
	return value
}

func intEnv(name string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}

func floatEnv(name string, fallback float64) float64 {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil || value < 0 {
		return fallback
	}
	return value
}
