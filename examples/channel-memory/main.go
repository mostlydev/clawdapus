package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const (
	defaultDataDir       = "/data/channel-memory"
	defaultDBFile        = "channel-memory.sqlite"
	defaultListenAddress = ":8080"
	defaultSourceKind    = "discord"
	defaultSourceService = "claw-wall"
)

type ingestRequest struct {
	ChannelID string            `json:"channel_id"`
	Message   ingestMessage     `json:"message"`
	Source    ingestSource      `json:"source,omitempty"`
	Metadata  map[string]string `json:"metadata,omitempty"`
	Scope     string            `json:"scope,omitempty"`
}

type ingestMessage struct {
	ID          string `json:"id"`
	AuthorID    string `json:"author_id,omitempty"`
	AuthorName  string `json:"author_name,omitempty"`
	CreatedAt   string `json:"created_at,omitempty"`
	EditedAt    string `json:"edited_at,omitempty"`
	Deleted     bool   `json:"deleted,omitempty"`
	Content     string `json:"content,omitempty"`
	ContentHash string `json:"content_hash,omitempty"`
}

type ingestSource struct {
	Kind    string `json:"kind,omitempty"`
	Service string `json:"service,omitempty"`
	Surface string `json:"surface,omitempty"`
	GuildID string `json:"guild_id,omitempty"`
}

type ingestResponse struct {
	Inserted     bool   `json:"inserted"`
	SourceKind   string `json:"source_kind"`
	ChannelID    string `json:"channel_id"`
	MessageID    string `json:"message_id"`
	SourceHandle string `json:"source_handle"`
	ContentHash  string `json:"content_hash"`
	ObservedSeq  int64  `json:"observed_seq"`
	Current      bool   `json:"current"`
}

type sourceMessagesRequest struct {
	SourceKind     string   `json:"source_kind,omitempty"`
	ChannelID      string   `json:"channel_id"`
	MessageIDs     []string `json:"message_ids"`
	IncludeHistory bool     `json:"include_history,omitempty"`
}

type sourceMessagesResponse struct {
	Messages []sourceMessageRecord `json:"messages"`
	NotFound []sourceRef           `json:"not_found,omitempty"`
}

type sourceRef struct {
	SourceKind string `json:"source_kind"`
	ChannelID  string `json:"channel_id"`
	MessageID  string `json:"message_id"`
}

type sourceMessageRecord struct {
	SourceKind      string `json:"source_kind"`
	ChannelID       string `json:"channel_id"`
	MessageID       string `json:"message_id"`
	SourceHandle    string `json:"source_handle"`
	ContentHash     string `json:"content_hash"`
	AuthorID        string `json:"author_id,omitempty"`
	AuthorName      string `json:"author_name,omitempty"`
	CreatedAt       string `json:"created_at"`
	EditedAt        string `json:"edited_at,omitempty"`
	Deleted         bool   `json:"deleted,omitempty"`
	Content         string `json:"content,omitempty"`
	Service         string `json:"service,omitempty"`
	Surface         string `json:"surface,omitempty"`
	GuildID         string `json:"guild_id,omitempty"`
	VisibilityScope string `json:"visibility_scope,omitempty"`
	ObservedSeq     int64  `json:"observed_seq"`
	ObservedAt      string `json:"observed_at"`
	IsCurrent       bool   `json:"is_current"`
	SupersededBy    *int64 `json:"superseded_by,omitempty"`
	ForgottenAt     string `json:"forgotten_at,omitempty"`
	ForgetReason    string `json:"forget_reason,omitempty"`
}

type digestRequest struct {
	SourceKind string       `json:"source_kind,omitempty"`
	ChannelIDs []string     `json:"channel_ids,omitempty"`
	Since      string       `json:"since,omitempty"`
	Budget     digestBudget `json:"budget,omitempty"`
}

type digestBudget struct {
	MaxBlocks int `json:"max_blocks,omitempty"`
}

type digestResponse struct {
	Status      string         `json:"status"`
	GeneratedAt string         `json:"generated_at"`
	Coverage    digestCoverage `json:"coverage"`
	Blocks      []digestBlock  `json:"blocks"`
	Cost        digestCost     `json:"cost"`
}

type digestCoverage struct {
	From              string        `json:"from,omitempty"`
	To                string        `json:"to,omitempty"`
	SourceMessages    int           `json:"source_messages"`
	DigestMessages    int           `json:"digest_messages"`
	RawRecentMessages int           `json:"raw_recent_messages"`
	Gaps              []coverageGap `json:"gaps,omitempty"`
}

type digestCost struct {
	DeterministicOnly bool `json:"deterministic_only"`
	LLMCallsToday     int  `json:"llm_calls_today"`
}

type digestBlock struct {
	ID                   int64        `json:"id,omitempty"`
	Kind                 string       `json:"kind"`
	EventType            string       `json:"event_type,omitempty"`
	Text                 string       `json:"text"`
	SourceChannel        string       `json:"source_channel"`
	SourceMessages       []string     `json:"source_messages"`
	CoveredContentHashes []string     `json:"covered_content_hashes,omitempty"`
	SourceWindow         sourceWindow `json:"source_window"`
	Sparse               bool         `json:"sparse"`
	Score                float64      `json:"score"`
	GeneratedAt          string       `json:"generated_at"`
	Stale                bool         `json:"stale,omitempty"`
	Dirty                bool         `json:"dirty,omitempty"`
	Processor            string       `json:"processor"`
}

type sourceWindow struct {
	From string `json:"from"`
	To   string `json:"to"`
}

type coverageGapRequest struct {
	ChannelID string `json:"channel_id"`
	From      string `json:"from"`
	To        string `json:"to"`
	Reason    string `json:"reason,omitempty"`
}

type coverageGap struct {
	ID        int64  `json:"id,omitempty"`
	ChannelID string `json:"channel_id"`
	From      string `json:"from"`
	To        string `json:"to"`
	Reason    string `json:"reason,omitempty"`
	CreatedAt string `json:"created_at,omitempty"`
}

type forgetRequest struct {
	SourceKind string   `json:"source_kind,omitempty"`
	ChannelID  string   `json:"channel_id"`
	MessageIDs []string `json:"message_ids"`
	Reason     string   `json:"reason,omitempty"`
}

type forgetResponse struct {
	Forgotten int `json:"forgotten"`
}

type channelMemoryStore struct {
	db  *sql.DB
	now func() time.Time
}

type storedSourceMessage struct {
	ID              int64
	SourceKind      string
	ChannelID       string
	MessageID       string
	ContentHash     string
	AuthorID        string
	AuthorName      string
	CreatedAt       string
	EditedAt        string
	Deleted         bool
	Content         string
	Service         string
	Surface         string
	GuildID         string
	VisibilityScope string
	ObservedSeq     int64
	ObservedAt      string
	IsCurrent       bool
}

func main() {
	store, err := openStoreFromEnv()
	if err != nil {
		log.Fatalf("open channel-memory store: %v", err)
	}
	defer store.Close()

	addr := strings.TrimSpace(os.Getenv("PORT"))
	if addr == "" {
		addr = defaultListenAddress
	} else if !strings.Contains(addr, ":") {
		addr = ":" + addr
	}

	log.Printf("channel-memory listening on %s", addr)
	if err := http.ListenAndServe(addr, newHandler(store)); err != nil {
		log.Fatalf("listen: %v", err)
	}
}

func openStoreFromEnv() (*channelMemoryStore, error) {
	dbPath := strings.TrimSpace(os.Getenv("CHANNEL_MEMORY_DB"))
	if dbPath == "" {
		dataDir := strings.TrimSpace(os.Getenv("CHANNEL_MEMORY_DIR"))
		if dataDir == "" {
			dataDir = defaultDataDir
		}
		dbPath = filepath.Join(dataDir, defaultDBFile)
	}
	return openStore(dbPath)
}

func openStore(dbPath string) (*channelMemoryStore, error) {
	if strings.TrimSpace(dbPath) == "" {
		return nil, errors.New("db path is required")
	}
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)

	store := &channelMemoryStore{
		db:  db,
		now: func() time.Time { return time.Now().UTC() },
	}
	if err := store.initSchema(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (s *channelMemoryStore) Close() error {
	return s.db.Close()
}

func (s *channelMemoryStore) initSchema(ctx context.Context) error {
	statements := []string{
		`PRAGMA foreign_keys = ON`,
		`PRAGMA journal_mode = WAL`,
		`CREATE TABLE IF NOT EXISTS source_messages (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			source_kind TEXT NOT NULL,
			channel_id TEXT NOT NULL,
			message_id TEXT NOT NULL,
			content_hash TEXT NOT NULL,
			author_id TEXT NOT NULL DEFAULT '',
			author_name TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL,
			edited_at TEXT NOT NULL DEFAULT '',
			deleted INTEGER NOT NULL DEFAULT 0,
			content TEXT NOT NULL DEFAULT '',
			service TEXT NOT NULL DEFAULT '',
			surface TEXT NOT NULL DEFAULT '',
			guild_id TEXT NOT NULL DEFAULT '',
			visibility_scope TEXT NOT NULL DEFAULT '',
			observed_seq INTEGER NOT NULL,
			observed_at TEXT NOT NULL,
			is_current INTEGER NOT NULL DEFAULT 1,
			superseded_by INTEGER,
			forgotten_at TEXT NOT NULL DEFAULT '',
			forget_reason TEXT NOT NULL DEFAULT '',
			UNIQUE(source_kind, channel_id, message_id, content_hash)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_source_messages_current ON source_messages(source_kind, channel_id, message_id, is_current, deleted, forgotten_at)`,
		`CREATE INDEX IF NOT EXISTS idx_source_messages_channel_time ON source_messages(source_kind, channel_id, created_at)`,
		`CREATE TABLE IF NOT EXISTS derived_blocks (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			block_key TEXT NOT NULL UNIQUE,
			kind TEXT NOT NULL,
			event_type TEXT NOT NULL DEFAULT '',
			text TEXT NOT NULL,
			source_channel TEXT NOT NULL,
			source_window_from TEXT NOT NULL,
			source_window_to TEXT NOT NULL,
			sparse INTEGER NOT NULL,
			score REAL NOT NULL,
			generated_at TEXT NOT NULL,
			stale INTEGER NOT NULL DEFAULT 0,
			dirty INTEGER NOT NULL DEFAULT 0,
			processor TEXT NOT NULL,
			metadata_json TEXT NOT NULL DEFAULT '{}'
		)`,
		`CREATE INDEX IF NOT EXISTS idx_derived_blocks_channel_window ON derived_blocks(source_channel, source_window_to, dirty, stale)`,
		`CREATE TABLE IF NOT EXISTS derived_block_sources (
			block_id INTEGER NOT NULL REFERENCES derived_blocks(id) ON DELETE CASCADE,
			source_kind TEXT NOT NULL,
			channel_id TEXT NOT NULL,
			message_id TEXT NOT NULL,
			content_hash TEXT NOT NULL,
			PRIMARY KEY(block_id, source_kind, channel_id, message_id, content_hash)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_derived_block_sources_identity ON derived_block_sources(source_kind, channel_id, message_id)`,
		`CREATE TABLE IF NOT EXISTS coverage_gaps (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			channel_id TEXT NOT NULL,
			from_ts TEXT NOT NULL,
			to_ts TEXT NOT NULL,
			reason TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_coverage_gaps_channel_time ON coverage_gaps(channel_id, from_ts, to_ts)`,
		`CREATE TABLE IF NOT EXISTS processing_queue (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			source_kind TEXT NOT NULL,
			channel_id TEXT NOT NULL,
			message_id TEXT NOT NULL,
			content_hash TEXT NOT NULL,
			status TEXT NOT NULL,
			kind TEXT NOT NULL,
			attempts INTEGER NOT NULL DEFAULT 0,
			last_error TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_processing_queue_status ON processing_queue(status, updated_at)`,
	}
	for _, stmt := range statements {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	return nil
}

func newHandler(store *channelMemoryStore) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/ingest", func(w http.ResponseWriter, r *http.Request) {
		if !requireMethod(w, r, http.MethodPost) {
			return
		}
		var req ingestRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		resp, err := store.Ingest(r.Context(), req)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, http.StatusAccepted, resp)
	})

	mux.HandleFunc("/source-messages", func(w http.ResponseWriter, r *http.Request) {
		if !requireMethod(w, r, http.MethodPost) {
			return
		}
		var req sourceMessagesRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		resp, err := store.SourceMessages(r.Context(), req)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, http.StatusOK, resp)
	})

	mux.HandleFunc("/digest", func(w http.ResponseWriter, r *http.Request) {
		if !requireMethod(w, r, http.MethodPost) {
			return
		}
		var req digestRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		resp, err := store.Digest(r.Context(), req)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, http.StatusOK, resp)
	})

	mux.HandleFunc("/coverage-gaps", func(w http.ResponseWriter, r *http.Request) {
		if !requireMethod(w, r, http.MethodPost) {
			return
		}
		var req coverageGapRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		gap, err := store.AddCoverageGap(r.Context(), req)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, http.StatusAccepted, gap)
	})

	mux.HandleFunc("/forget", func(w http.ResponseWriter, r *http.Request) {
		if !requireMethod(w, r, http.MethodPost) {
			return
		}
		var req forgetRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		resp, err := store.Forget(r.Context(), req)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, http.StatusOK, resp)
	})

	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.WriteHeader(http.StatusOK)
	})

	return mux
}

func (s *channelMemoryStore) Ingest(ctx context.Context, req ingestRequest) (ingestResponse, error) {
	normalized, err := normalizeIngestRequest(req, s.now())
	if err != nil {
		return ingestResponse{}, err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ingestResponse{}, err
	}
	defer rollbackUnlessCommitted(tx)

	var nextObservedSeq int64
	if err := tx.QueryRowContext(ctx, `
		SELECT COALESCE(MAX(observed_seq), 0) + 1
		FROM source_messages
		WHERE source_kind = ? AND channel_id = ? AND message_id = ?`,
		normalized.SourceKind, normalized.ChannelID, normalized.MessageID,
	).Scan(&nextObservedSeq); err != nil {
		return ingestResponse{}, err
	}
	normalized.ObservedSeq = nextObservedSeq

	result, err := tx.ExecContext(ctx, `
		INSERT OR IGNORE INTO source_messages (
			source_kind, channel_id, message_id, content_hash,
			author_id, author_name, created_at, edited_at, deleted, content,
			service, surface, guild_id, visibility_scope,
			observed_seq, observed_at, is_current
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1)`,
		normalized.SourceKind, normalized.ChannelID, normalized.MessageID, normalized.ContentHash,
		normalized.AuthorID, normalized.AuthorName, normalized.CreatedAt, normalized.EditedAt, boolInt(normalized.Deleted), normalized.Content,
		normalized.Service, normalized.Surface, normalized.GuildID, normalized.VisibilityScope,
		normalized.ObservedSeq, normalized.ObservedAt,
	)
	if err != nil {
		return ingestResponse{}, err
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return ingestResponse{}, err
	}

	inserted := rows > 0
	if inserted {
		sourceID, err := result.LastInsertId()
		if err != nil {
			return ingestResponse{}, err
		}
		normalized.ID = sourceID
		if _, err := tx.ExecContext(ctx, `
			UPDATE source_messages
			SET is_current = 0, superseded_by = ?
			WHERE source_kind = ? AND channel_id = ? AND message_id = ? AND id <> ? AND is_current = 1`,
			sourceID, normalized.SourceKind, normalized.ChannelID, normalized.MessageID, sourceID,
		); err != nil {
			return ingestResponse{}, err
		}
		if err := markIdentityBlocksDirtyTx(ctx, tx, normalized.SourceKind, normalized.ChannelID, normalized.MessageID, normalized.ContentHash); err != nil {
			return ingestResponse{}, err
		}
		if err := enqueueProcessedTx(ctx, tx, normalized); err != nil {
			return ingestResponse{}, err
		}
		if err := upsertDeterministicBlocksTx(ctx, tx, normalized, s.now()); err != nil {
			return ingestResponse{}, err
		}
	} else {
		loaded, err := loadSourceByIdentityAndHashTx(ctx, tx, normalized.SourceKind, normalized.ChannelID, normalized.MessageID, normalized.ContentHash)
		if err != nil {
			return ingestResponse{}, err
		}
		normalized = loaded
	}

	if err := tx.Commit(); err != nil {
		return ingestResponse{}, err
	}

	return ingestResponse{
		Inserted:     inserted,
		SourceKind:   normalized.SourceKind,
		ChannelID:    normalized.ChannelID,
		MessageID:    normalized.MessageID,
		SourceHandle: sourceHandle(normalized.ChannelID, normalized.MessageID),
		ContentHash:  normalized.ContentHash,
		ObservedSeq:  normalized.ObservedSeq,
		Current:      normalized.IsCurrent,
	}, nil
}

func (s *channelMemoryStore) SourceMessages(ctx context.Context, req sourceMessagesRequest) (sourceMessagesResponse, error) {
	sourceKind := normalizeSourceKind(req.SourceKind, "")
	channelID := strings.TrimSpace(req.ChannelID)
	if channelID == "" {
		return sourceMessagesResponse{}, errors.New("channel_id is required")
	}
	if len(req.MessageIDs) == 0 {
		return sourceMessagesResponse{}, errors.New("message_ids is required")
	}

	resp := sourceMessagesResponse{}
	for _, rawMessageID := range req.MessageIDs {
		messageID := strings.TrimSpace(rawMessageID)
		if messageID == "" {
			continue
		}
		records, err := s.querySourceMessages(ctx, sourceKind, channelID, messageID, req.IncludeHistory)
		if err != nil {
			return sourceMessagesResponse{}, err
		}
		if len(records) == 0 {
			resp.NotFound = append(resp.NotFound, sourceRef{SourceKind: sourceKind, ChannelID: channelID, MessageID: messageID})
			continue
		}
		resp.Messages = append(resp.Messages, records...)
	}
	return resp, nil
}

func (s *channelMemoryStore) Digest(ctx context.Context, req digestRequest) (digestResponse, error) {
	sourceKind := normalizeSourceKind(req.SourceKind, "")
	cutoff, cutoffText, err := parseSince(req.Since, s.now())
	if err != nil {
		return digestResponse{}, err
	}
	channels := trimNonEmpty(req.ChannelIDs)
	if len(channels) == 0 {
		var err error
		channels, err = s.allChannels(ctx, sourceKind)
		if err != nil {
			return digestResponse{}, err
		}
	}
	maxBlocks := req.Budget.MaxBlocks
	if maxBlocks <= 0 {
		maxBlocks = 32
	}

	blocks := make([]digestBlock, 0)
	for _, channelID := range channels {
		channelBlocks, err := s.queryDigestBlocks(ctx, channelID, cutoff.Format(time.RFC3339), maxBlocks-len(blocks))
		if err != nil {
			return digestResponse{}, err
		}
		blocks = append(blocks, channelBlocks...)
		if len(blocks) >= maxBlocks {
			break
		}
	}

	gaps, err := s.queryCoverageGaps(ctx, channels, cutoff.Format(time.RFC3339))
	if err != nil {
		return digestResponse{}, err
	}
	sourceCount, err := s.countCurrentSources(ctx, sourceKind, channels, cutoff.Format(time.RFC3339))
	if err != nil {
		return digestResponse{}, err
	}

	status := "ok"
	if len(gaps) > 0 {
		status = "coverage_gap"
	} else if len(blocks) == 0 {
		status = "unavailable"
	}

	rawRecent := 0
	for _, block := range blocks {
		if block.Kind == "raw_excerpt" || block.Kind == "hard_event" || block.Kind == "tombstone" {
			rawRecent++
		}
	}

	return digestResponse{
		Status:      status,
		GeneratedAt: s.now().Format(time.RFC3339),
		Coverage: digestCoverage{
			From:              cutoffText,
			To:                s.now().Format(time.RFC3339),
			SourceMessages:    sourceCount,
			DigestMessages:    len(blocks),
			RawRecentMessages: rawRecent,
			Gaps:              gaps,
		},
		Blocks: blocks,
		Cost: digestCost{
			DeterministicOnly: true,
			LLMCallsToday:     0,
		},
	}, nil
}

func (s *channelMemoryStore) AddCoverageGap(ctx context.Context, req coverageGapRequest) (coverageGap, error) {
	channelID := strings.TrimSpace(req.ChannelID)
	if channelID == "" {
		return coverageGap{}, errors.New("channel_id is required")
	}
	from, err := normalizeTimestamp(req.From, s.now())
	if err != nil {
		return coverageGap{}, fmt.Errorf("from: %w", err)
	}
	to, err := normalizeTimestamp(req.To, s.now())
	if err != nil {
		return coverageGap{}, fmt.Errorf("to: %w", err)
	}
	createdAt := s.now().Format(time.RFC3339)
	result, err := s.db.ExecContext(ctx, `
		INSERT INTO coverage_gaps(channel_id, from_ts, to_ts, reason, created_at)
		VALUES (?, ?, ?, ?, ?)`,
		channelID, from, to, strings.TrimSpace(req.Reason), createdAt,
	)
	if err != nil {
		return coverageGap{}, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return coverageGap{}, err
	}
	return coverageGap{ID: id, ChannelID: channelID, From: from, To: to, Reason: strings.TrimSpace(req.Reason), CreatedAt: createdAt}, nil
}

func (s *channelMemoryStore) Forget(ctx context.Context, req forgetRequest) (forgetResponse, error) {
	sourceKind := normalizeSourceKind(req.SourceKind, "")
	channelID := strings.TrimSpace(req.ChannelID)
	if channelID == "" {
		return forgetResponse{}, errors.New("channel_id is required")
	}
	if len(req.MessageIDs) == 0 {
		return forgetResponse{}, errors.New("message_ids is required")
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return forgetResponse{}, err
	}
	defer rollbackUnlessCommitted(tx)

	forgottenAt := s.now().Format(time.RFC3339)
	total := 0
	for _, rawMessageID := range req.MessageIDs {
		messageID := strings.TrimSpace(rawMessageID)
		if messageID == "" {
			continue
		}
		result, err := tx.ExecContext(ctx, `
			UPDATE source_messages
			SET forgotten_at = ?, forget_reason = ?, is_current = 0
			WHERE source_kind = ? AND channel_id = ? AND message_id = ? AND forgotten_at = ''`,
			forgottenAt, strings.TrimSpace(req.Reason), sourceKind, channelID, messageID,
		)
		if err != nil {
			return forgetResponse{}, err
		}
		rows, err := result.RowsAffected()
		if err != nil {
			return forgetResponse{}, err
		}
		total += int(rows)
		if err := markIdentityBlocksDirtyTx(ctx, tx, sourceKind, channelID, messageID, ""); err != nil {
			return forgetResponse{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return forgetResponse{}, err
	}
	return forgetResponse{Forgotten: total}, nil
}

func normalizeIngestRequest(req ingestRequest, now time.Time) (storedSourceMessage, error) {
	sourceKind := normalizeSourceKind(req.Source.Kind, req.Source.Surface)
	channelID := strings.TrimSpace(req.ChannelID)
	messageID := strings.TrimSpace(req.Message.ID)
	if channelID == "" {
		return storedSourceMessage{}, errors.New("channel_id is required")
	}
	if messageID == "" {
		return storedSourceMessage{}, errors.New("message.id is required")
	}
	createdAt, err := normalizeTimestamp(req.Message.CreatedAt, now)
	if err != nil {
		return storedSourceMessage{}, fmt.Errorf("message.created_at: %w", err)
	}
	editedAt := ""
	if strings.TrimSpace(req.Message.EditedAt) != "" {
		editedAt, err = normalizeTimestamp(req.Message.EditedAt, now)
		if err != nil {
			return storedSourceMessage{}, fmt.Errorf("message.edited_at: %w", err)
		}
	}
	observedAt := now.UTC().Format(time.RFC3339)
	content := req.Message.Content
	contentHash := normalizeContentHash(req.Message.ContentHash, sourceKind, channelID, messageID, content, req.Message.Deleted)
	service := firstNonEmpty(req.Source.Service, defaultSourceService)
	surface := firstNonEmpty(req.Source.Surface, sourceKind)

	return storedSourceMessage{
		SourceKind:      sourceKind,
		ChannelID:       channelID,
		MessageID:       messageID,
		ContentHash:     contentHash,
		AuthorID:        strings.TrimSpace(req.Message.AuthorID),
		AuthorName:      strings.TrimSpace(req.Message.AuthorName),
		CreatedAt:       createdAt,
		EditedAt:        editedAt,
		Deleted:         req.Message.Deleted,
		Content:         content,
		Service:         strings.TrimSpace(service),
		Surface:         strings.TrimSpace(surface),
		GuildID:         strings.TrimSpace(req.Source.GuildID),
		VisibilityScope: strings.TrimSpace(firstNonEmpty(req.Scope, req.Metadata["visibility_scope"])),
		ObservedAt:      observedAt,
		IsCurrent:       true,
	}, nil
}

func loadSourceByIdentityAndHashTx(ctx context.Context, tx *sql.Tx, sourceKind, channelID, messageID, contentHash string) (storedSourceMessage, error) {
	row := tx.QueryRowContext(ctx, `
		SELECT id, source_kind, channel_id, message_id, content_hash, author_id, author_name,
		       created_at, edited_at, deleted, content, service, surface, guild_id, visibility_scope,
		       observed_seq, observed_at, is_current
		FROM source_messages
		WHERE source_kind = ? AND channel_id = ? AND message_id = ? AND content_hash = ?`,
		sourceKind, channelID, messageID, contentHash,
	)
	return scanStoredSource(row)
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanStoredSource(row rowScanner) (storedSourceMessage, error) {
	var record storedSourceMessage
	var deleted, current int
	if err := row.Scan(
		&record.ID, &record.SourceKind, &record.ChannelID, &record.MessageID, &record.ContentHash,
		&record.AuthorID, &record.AuthorName, &record.CreatedAt, &record.EditedAt, &deleted, &record.Content,
		&record.Service, &record.Surface, &record.GuildID, &record.VisibilityScope,
		&record.ObservedSeq, &record.ObservedAt, &current,
	); err != nil {
		return storedSourceMessage{}, err
	}
	record.Deleted = deleted != 0
	record.IsCurrent = current != 0
	return record, nil
}

func enqueueProcessedTx(ctx context.Context, tx *sql.Tx, source storedSourceMessage) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := tx.ExecContext(ctx, `
		INSERT INTO processing_queue(source_kind, channel_id, message_id, content_hash, status, kind, created_at, updated_at)
		VALUES (?, ?, ?, ?, 'processed', 'deterministic', ?, ?)`,
		source.SourceKind, source.ChannelID, source.MessageID, source.ContentHash, now, now,
	)
	return err
}

func markIdentityBlocksDirtyTx(ctx context.Context, tx *sql.Tx, sourceKind, channelID, messageID, exceptContentHash string) error {
	query := `
		UPDATE derived_blocks
		SET dirty = 1
		WHERE id IN (
			SELECT block_id FROM derived_block_sources
			WHERE source_kind = ? AND channel_id = ? AND message_id = ?`
	args := []any{sourceKind, channelID, messageID}
	if exceptContentHash != "" {
		query += ` AND content_hash <> ?`
		args = append(args, exceptContentHash)
	}
	query += `)`
	_, err := tx.ExecContext(ctx, query, args...)
	return err
}

func upsertDeterministicBlocksTx(ctx context.Context, tx *sql.Tx, source storedSourceMessage, now time.Time) error {
	if source.Deleted {
		return upsertSourceBlockTx(ctx, tx, source, deterministicBlock{
			Key:       sourceBlockKey("tombstone", source),
			Kind:      "tombstone",
			Text:      formatTombstone(source),
			Sparse:    true,
			Score:     1,
			Processor: "deterministic",
		}, now)
	}
	if isTelemetryNoise(source.Content) {
		return rebuildTelemetryBlockTx(ctx, tx, source, now)
	}
	kind, eventType, score := classifySourceContent(source.Content)
	return upsertSourceBlockTx(ctx, tx, source, deterministicBlock{
		Key:       sourceBlockKey(kind, source),
		Kind:      kind,
		EventType: eventType,
		Text:      formatSourceLine(source),
		Sparse:    false,
		Score:     score,
		Processor: "deterministic",
	}, now)
}

type deterministicBlock struct {
	Key       string
	Kind      string
	EventType string
	Text      string
	Sparse    bool
	Score     float64
	Processor string
}

func upsertSourceBlockTx(ctx context.Context, tx *sql.Tx, source storedSourceMessage, block deterministicBlock, now time.Time) error {
	if strings.TrimSpace(block.Text) == "" {
		return nil
	}
	generatedAt := now.UTC().Format(time.RFC3339)
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO derived_blocks(
			block_key, kind, event_type, text, source_channel,
			source_window_from, source_window_to, sparse, score, generated_at, stale, dirty, processor
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, 0, ?)
		ON CONFLICT(block_key) DO UPDATE SET
			kind = excluded.kind,
			event_type = excluded.event_type,
			text = excluded.text,
			source_channel = excluded.source_channel,
			source_window_from = excluded.source_window_from,
			source_window_to = excluded.source_window_to,
			sparse = excluded.sparse,
			score = excluded.score,
			generated_at = excluded.generated_at,
			stale = 0,
			dirty = 0,
			processor = excluded.processor`,
		block.Key, block.Kind, block.EventType, block.Text, source.ChannelID,
		source.CreatedAt, firstNonEmpty(source.EditedAt, source.CreatedAt), boolInt(block.Sparse), block.Score, generatedAt, block.Processor,
	); err != nil {
		return err
	}
	var blockID int64
	if err := tx.QueryRowContext(ctx, `SELECT id FROM derived_blocks WHERE block_key = ?`, block.Key).Scan(&blockID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM derived_block_sources WHERE block_id = ?`, blockID); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `
		INSERT OR IGNORE INTO derived_block_sources(block_id, source_kind, channel_id, message_id, content_hash)
		VALUES (?, ?, ?, ?, ?)`,
		blockID, source.SourceKind, source.ChannelID, source.MessageID, source.ContentHash,
	)
	return err
}

func rebuildTelemetryBlockTx(ctx context.Context, tx *sql.Tx, source storedSourceMessage, now time.Time) error {
	from, to := telemetryBucket(source.CreatedAt)
	rows, err := tx.QueryContext(ctx, `
		SELECT id, source_kind, channel_id, message_id, content_hash, author_id, author_name,
		       created_at, edited_at, deleted, content, service, surface, guild_id, visibility_scope,
		       observed_seq, observed_at, is_current
		FROM source_messages
		WHERE source_kind = ? AND channel_id = ? AND is_current = 1 AND deleted = 0 AND forgotten_at = ''
		  AND created_at >= ? AND created_at < ?
		ORDER BY created_at, observed_seq`,
		source.SourceKind, source.ChannelID, from, to,
	)
	if err != nil {
		return err
	}
	defer rows.Close()

	sources := make([]storedSourceMessage, 0)
	for rows.Next() {
		record, err := scanStoredSource(rows)
		if err != nil {
			return err
		}
		if isTelemetryNoise(record.Content) {
			sources = append(sources, record)
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(sources) == 0 {
		return nil
	}

	blockKey := strings.Join([]string{"telemetry", source.SourceKind, source.ChannelID, from}, ":")
	generatedAt := now.UTC().Format(time.RFC3339)
	text := fmt.Sprintf("[%s-%s] runtime/status noise elided: %d messages.", clockText(from), clockText(to), len(sources))
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO derived_blocks(
			block_key, kind, text, source_channel, source_window_from, source_window_to,
			sparse, score, generated_at, stale, dirty, processor
		) VALUES (?, 'telemetry_count', ?, ?, ?, ?, 1, 0.25, ?, 0, 0, 'deterministic')
		ON CONFLICT(block_key) DO UPDATE SET
			text = excluded.text,
			source_window_from = excluded.source_window_from,
			source_window_to = excluded.source_window_to,
			generated_at = excluded.generated_at,
			stale = 0,
			dirty = 0`,
		blockKey, text, source.ChannelID, sources[0].CreatedAt, sources[len(sources)-1].CreatedAt, generatedAt,
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
	for _, record := range sources {
		if _, err := tx.ExecContext(ctx, `
			INSERT OR IGNORE INTO derived_block_sources(block_id, source_kind, channel_id, message_id, content_hash)
			VALUES (?, ?, ?, ?, ?)`,
			blockID, record.SourceKind, record.ChannelID, record.MessageID, record.ContentHash,
		); err != nil {
			return err
		}
	}
	return nil
}

func (s *channelMemoryStore) querySourceMessages(ctx context.Context, sourceKind, channelID, messageID string, includeHistory bool) ([]sourceMessageRecord, error) {
	query := `
		SELECT id, source_kind, channel_id, message_id, content_hash, author_id, author_name,
		       created_at, edited_at, deleted, content, service, surface, guild_id, visibility_scope,
		       observed_seq, observed_at, is_current, superseded_by, forgotten_at, forget_reason
		FROM source_messages
		WHERE source_kind = ? AND channel_id = ? AND message_id = ?`
	args := []any{sourceKind, channelID, messageID}
	if !includeHistory {
		query += ` AND is_current = 1 AND deleted = 0 AND forgotten_at = ''`
	}
	query += ` ORDER BY observed_seq`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	records := make([]sourceMessageRecord, 0)
	for rows.Next() {
		var record sourceMessageRecord
		var id int64
		var deleted, current int
		var supersededBy sql.NullInt64
		if err := rows.Scan(
			&id, &record.SourceKind, &record.ChannelID, &record.MessageID, &record.ContentHash,
			&record.AuthorID, &record.AuthorName, &record.CreatedAt, &record.EditedAt, &deleted, &record.Content,
			&record.Service, &record.Surface, &record.GuildID, &record.VisibilityScope,
			&record.ObservedSeq, &record.ObservedAt, &current, &supersededBy, &record.ForgottenAt, &record.ForgetReason,
		); err != nil {
			return nil, err
		}
		if supersededBy.Valid {
			value := supersededBy.Int64
			record.SupersededBy = &value
		}
		record.Deleted = deleted != 0
		record.IsCurrent = current != 0
		record.SourceHandle = sourceHandle(record.ChannelID, record.MessageID)
		records = append(records, record)
	}
	return records, rows.Err()
}

func (s *channelMemoryStore) queryDigestBlocks(ctx context.Context, channelID, cutoff string, limit int) ([]digestBlock, error) {
	if limit <= 0 {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, kind, event_type, text, source_channel, source_window_from, source_window_to,
		       sparse, score, generated_at, stale, dirty, processor
		FROM derived_blocks
		WHERE source_channel = ? AND source_window_to >= ? AND stale = 0 AND dirty = 0
		ORDER BY source_window_from ASC, id ASC
		LIMIT ?`,
		channelID, cutoff, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	blocks := make([]digestBlock, 0)
	for rows.Next() {
		var block digestBlock
		var sparse, stale, dirty int
		var from, to string
		if err := rows.Scan(
			&block.ID, &block.Kind, &block.EventType, &block.Text, &block.SourceChannel, &from, &to,
			&sparse, &block.Score, &block.GeneratedAt, &stale, &dirty, &block.Processor,
		); err != nil {
			return nil, err
		}
		block.SourceWindow = sourceWindow{From: from, To: to}
		block.Sparse = sparse != 0
		block.Stale = stale != 0
		block.Dirty = dirty != 0
		blocks = append(blocks, block)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}

	for i := range blocks {
		if err := s.loadBlockSources(ctx, &blocks[i]); err != nil {
			return nil, err
		}
	}
	return blocks, nil
}

func (s *channelMemoryStore) loadBlockSources(ctx context.Context, block *digestBlock) error {
	rows, err := s.db.QueryContext(ctx, `
		SELECT message_id, content_hash
		FROM derived_block_sources
		WHERE block_id = ?
		ORDER BY message_id, content_hash`,
		block.ID,
	)
	if err != nil {
		return err
	}
	defer rows.Close()

	seenMessages := make(map[string]struct{})
	for rows.Next() {
		var messageID, contentHash string
		if err := rows.Scan(&messageID, &contentHash); err != nil {
			return err
		}
		if _, ok := seenMessages[messageID]; !ok {
			block.SourceMessages = append(block.SourceMessages, messageID)
			seenMessages[messageID] = struct{}{}
		}
		block.CoveredContentHashes = append(block.CoveredContentHashes, contentHash)
	}
	return rows.Err()
}

func (s *channelMemoryStore) queryCoverageGaps(ctx context.Context, channels []string, cutoff string) ([]coverageGap, error) {
	gaps := make([]coverageGap, 0)
	for _, channelID := range channels {
		rows, err := s.db.QueryContext(ctx, `
			SELECT id, channel_id, from_ts, to_ts, reason, created_at
			FROM coverage_gaps
			WHERE channel_id = ? AND to_ts >= ?
			ORDER BY from_ts, id`,
			channelID, cutoff,
		)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var gap coverageGap
			if err := rows.Scan(&gap.ID, &gap.ChannelID, &gap.From, &gap.To, &gap.Reason, &gap.CreatedAt); err != nil {
				_ = rows.Close()
				return nil, err
			}
			gaps = append(gaps, gap)
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
	}
	return gaps, nil
}

func (s *channelMemoryStore) countCurrentSources(ctx context.Context, sourceKind string, channels []string, cutoff string) (int, error) {
	total := 0
	for _, channelID := range channels {
		var count int
		if err := s.db.QueryRowContext(ctx, `
			SELECT COUNT(*)
			FROM source_messages
			WHERE source_kind = ? AND channel_id = ? AND is_current = 1 AND deleted = 0 AND forgotten_at = '' AND created_at >= ?`,
			sourceKind, channelID, cutoff,
		).Scan(&count); err != nil {
			return 0, err
		}
		total += count
	}
	return total, nil
}

func (s *channelMemoryStore) allChannels(ctx context.Context, sourceKind string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT DISTINCT channel_id
		FROM source_messages
		WHERE source_kind = ?
		ORDER BY channel_id`,
		sourceKind,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	channels := make([]string, 0)
	for rows.Next() {
		var channelID string
		if err := rows.Scan(&channelID); err != nil {
			return nil, err
		}
		channels = append(channels, channelID)
	}
	return channels, rows.Err()
}

func normalizeSourceKind(kind, surface string) string {
	for _, candidate := range []string{kind, surface, defaultSourceKind} {
		candidate = strings.ToLower(strings.TrimSpace(candidate))
		if candidate != "" {
			return candidate
		}
	}
	return defaultSourceKind
}

func normalizeTimestamp(value string, fallback time.Time) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback.UTC().Format(time.RFC3339), nil
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return "", err
	}
	return parsed.UTC().Format(time.RFC3339), nil
}

func normalizeContentHash(value, sourceKind, channelID, messageID, content string, deleted bool) string {
	value = strings.TrimSpace(value)
	if value != "" {
		return value
	}
	seed := strings.Join([]string{sourceKind, channelID, messageID, content, fmt.Sprintf("deleted=%t", deleted)}, "\x00")
	sum := sha256.Sum256([]byte(seed))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func parseSince(value string, now time.Time) (time.Time, string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		value = "24h"
	}
	if duration, err := time.ParseDuration(value); err == nil {
		cutoff := now.UTC().Add(-duration)
		return cutoff, cutoff.Format(time.RFC3339), nil
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, "", fmt.Errorf("since must be a duration or RFC3339 timestamp")
	}
	return parsed.UTC(), parsed.UTC().Format(time.RFC3339), nil
}

func sourceHandle(channelID, messageID string) string {
	return channelID + "/" + messageID
}

func sourceBlockKey(kind string, source storedSourceMessage) string {
	return strings.Join([]string{kind, source.SourceKind, source.ChannelID, source.MessageID, source.ContentHash}, ":")
}

func classifySourceContent(content string) (kind, eventType string, score float64) {
	lower := strings.ToLower(content)
	switch {
	case containsAny(lower, "proposed", "proposal", "[proposed]"):
		return "hard_event", "trade_proposal", 1
	case containsAny(lower, "approved", "approval", "[approved]"):
		return "hard_event", "trade_approval", 1
	case containsAny(lower, "confirmed", "confirmation", "[confirmed]"):
		return "hard_event", "trade_confirmation", 1
	case containsAny(lower, "filled", "fill "):
		return "hard_event", "trade_fill", 1
	case containsAny(lower, "stop", "target"):
		return "hard_event", "stop_or_target_change", 0.95
	case containsAny(lower, "route", "no route"):
		return "hard_event", "route_decision", 0.95
	case containsAny(lower, "watchlist", "held position", "thesis", "risk limit"):
		return "hard_event", "thesis_update", 0.9
	default:
		return "raw_excerpt", "", 0.65
	}
}

func isTelemetryNoise(content string) bool {
	lower := strings.ToLower(content)
	return containsAny(lower,
		"heartbeat_ok",
		"heartbeat ok",
		"runtime status",
		"provider retry",
		"gateway reconnect",
		"gateway shutdown",
		"cron status",
		"upstream request failed",
		"context deadline exceeded",
	)
}

func formatSourceLine(source storedSourceMessage) string {
	author := firstNonEmpty(source.AuthorName, source.AuthorID, "unknown")
	return fmt.Sprintf("[%s] %s: %s", clockText(source.CreatedAt), author, strings.TrimSpace(source.Content))
}

func formatTombstone(source storedSourceMessage) string {
	author := firstNonEmpty(source.AuthorName, source.AuthorID, "unknown")
	ts := firstNonEmpty(source.EditedAt, source.CreatedAt)
	return fmt.Sprintf("[%s] %s: [message removed]; source=%s", clockText(ts), author, sourceHandle(source.ChannelID, source.MessageID))
}

func telemetryBucket(ts string) (from, to string) {
	parsed, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		parsed = time.Now().UTC()
	}
	start := parsed.UTC().Truncate(time.Hour)
	return start.Format(time.RFC3339), start.Add(time.Hour).Format(time.RFC3339)
}

func clockText(ts string) string {
	parsed, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return ts
	}
	return parsed.UTC().Format("15:04")
}

func containsAny(text string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(text, needle) {
			return true
		}
	}
	return false
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func trimNonEmpty(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" && !slices.Contains(out, value) {
			out = append(out, value)
		}
	}
	return out
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func rollbackUnlessCommitted(tx *sql.Tx) {
	_ = tx.Rollback()
}

func requireMethod(w http.ResponseWriter, r *http.Request, method string) bool {
	if r.Method != method {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return false
	}
	return true
}

func decodeJSON(w http.ResponseWriter, r *http.Request, value any) bool {
	if err := json.NewDecoder(r.Body).Decode(value); err != nil {
		http.Error(w, fmt.Sprintf("decode JSON: %v", err), http.StatusBadRequest)
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
