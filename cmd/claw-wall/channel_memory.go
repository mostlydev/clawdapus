package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	channelMemorySourceKind    = "discord"
	channelMemorySourceService = "claw-wall"
	channelMemorySourceSurface = "discord"
)

type channelMemoryClient struct {
	ingestURL string
	digestURL string
	token     string
	client    *http.Client
}

type channelMemoryIngestRequest struct {
	ChannelID string                     `json:"channel_id"`
	Message   channelMemoryIngestMessage `json:"message"`
	Source    channelMemoryIngestSource  `json:"source,omitempty"`
	Metadata  map[string]string          `json:"metadata,omitempty"`
	Scope     string                     `json:"scope,omitempty"`
}

type channelMemoryIngestMessage struct {
	ID          string `json:"id"`
	AuthorID    string `json:"author_id,omitempty"`
	AuthorName  string `json:"author_name,omitempty"`
	CreatedAt   string `json:"created_at,omitempty"`
	EditedAt    string `json:"edited_at,omitempty"`
	Deleted     bool   `json:"deleted,omitempty"`
	Content     string `json:"content,omitempty"`
	ContentHash string `json:"content_hash,omitempty"`
}

type channelMemoryIngestSource struct {
	Kind    string `json:"kind,omitempty"`
	Service string `json:"service,omitempty"`
	Surface string `json:"surface,omitempty"`
	GuildID string `json:"guild_id,omitempty"`
}

type channelMemoryDigestRequest struct {
	SourceKind string                    `json:"source_kind,omitempty"`
	ChannelIDs []string                  `json:"channel_ids,omitempty"`
	Since      string                    `json:"since,omitempty"`
	Budget     channelMemoryDigestBudget `json:"budget,omitempty"`
}

type channelMemoryDigestBudget struct {
	MaxBlocks int `json:"max_blocks,omitempty"`
}

type channelMemoryDigestResponse struct {
	Status      string                      `json:"status"`
	GeneratedAt string                      `json:"generated_at"`
	Coverage    channelMemoryDigestCoverage `json:"coverage"`
	Blocks      []channelMemoryDigestBlock  `json:"blocks"`
	Cost        channelMemoryDigestCost     `json:"cost"`
}

type channelMemoryDigestCoverage struct {
	From              string                     `json:"from,omitempty"`
	To                string                     `json:"to,omitempty"`
	SourceMessages    int                        `json:"source_messages"`
	DigestMessages    int                        `json:"digest_messages"`
	RawRecentMessages int                        `json:"raw_recent_messages"`
	Gaps              []channelMemoryCoverageGap `json:"gaps,omitempty"`
}

type channelMemoryCoverageGap struct {
	ID        int64  `json:"id,omitempty"`
	ChannelID string `json:"channel_id"`
	From      string `json:"from"`
	To        string `json:"to"`
	Reason    string `json:"reason,omitempty"`
	CreatedAt string `json:"created_at,omitempty"`
}

type channelMemoryDigestCost struct {
	DeterministicOnly bool `json:"deterministic_only"`
	LLMCallsToday     int  `json:"llm_calls_today"`
}

type channelMemoryDigestBlock struct {
	ID                   int64                     `json:"id,omitempty"`
	Kind                 string                    `json:"kind"`
	EventType            string                    `json:"event_type,omitempty"`
	Text                 string                    `json:"text"`
	SourceChannel        string                    `json:"source_channel"`
	SourceMessages       []string                  `json:"source_messages"`
	CoveredContentHashes []string                  `json:"covered_content_hashes,omitempty"`
	SourceWindow         channelMemorySourceWindow `json:"source_window"`
	Sparse               bool                      `json:"sparse"`
	Score                float64                   `json:"score"`
	GeneratedAt          string                    `json:"generated_at"`
	Stale                bool                      `json:"stale,omitempty"`
	Dirty                bool                      `json:"dirty,omitempty"`
	Processor            string                    `json:"processor"`
}

type channelMemorySourceWindow struct {
	From string `json:"from"`
	To   string `json:"to"`
}

func newChannelMemoryClient(rawURL, token string, timeout time.Duration) (*channelMemoryClient, error) {
	return newChannelMemoryClientWithDigest(rawURL, "", token, timeout)
}

func newChannelMemoryClientWithDigest(rawIngestURL, rawDigestURL, token string, timeout time.Duration) (*channelMemoryClient, error) {
	rawIngestURL = strings.TrimSpace(rawIngestURL)
	rawDigestURL = strings.TrimSpace(rawDigestURL)
	if rawIngestURL == "" && rawDigestURL == "" {
		return nil, nil
	}
	if rawDigestURL == "" {
		rawDigestURL = deriveChannelMemoryDigestURL(rawIngestURL)
	}
	if rawIngestURL != "" {
		if err := validateChannelMemoryURL(rawIngestURL, "ingest"); err != nil {
			return nil, err
		}
	}
	if rawDigestURL != "" {
		if err := validateChannelMemoryURL(rawDigestURL, "digest"); err != nil {
			return nil, err
		}
	}
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	return &channelMemoryClient{
		ingestURL: rawIngestURL,
		digestURL: rawDigestURL,
		token:     strings.TrimSpace(token),
		client:    &http.Client{Timeout: timeout},
	}, nil
}

func validateChannelMemoryURL(rawURL, name string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("parse channel-memory %s URL: %w", name, err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("channel-memory %s URL must use http or https", name)
	}
	if strings.TrimSpace(parsed.Host) == "" {
		return fmt.Errorf("channel-memory %s URL must include a host", name)
	}
	return nil
}

func deriveChannelMemoryDigestURL(rawIngestURL string) string {
	parsed, err := url.Parse(strings.TrimSpace(rawIngestURL))
	if err != nil {
		return ""
	}
	path := strings.TrimRight(parsed.Path, "/")
	if strings.HasSuffix(path, "/ingest") {
		parsed.Path = strings.TrimSuffix(path, "/ingest") + "/digest"
		return parsed.String()
	}
	return ""
}

func (c *channelMemoryClient) enabled() bool {
	return c != nil && strings.TrimSpace(c.ingestURL) != ""
}

func (c *channelMemoryClient) digestEnabled() bool {
	return c != nil && strings.TrimSpace(c.digestURL) != ""
}

func (c *channelMemoryClient) ingestMessages(ctx context.Context, messages []wallMessage) (int, error) {
	if !c.enabled() || len(messages) == 0 {
		return 0, nil
	}
	pushed := 0
	for _, msg := range messages {
		if strings.TrimSpace(msg.ChannelID) == "" || strings.TrimSpace(msg.ID) == "" {
			continue
		}
		if err := c.ingestMessage(ctx, msg); err != nil {
			return pushed, err
		}
		pushed++
	}
	return pushed, nil
}

func (c *channelMemoryClient) ingestMessage(ctx context.Context, msg wallMessage) error {
	payload := channelMemoryPayloadForMessage(msg)
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.ingestURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("channel-memory ingest returned %s: %s", resp.Status, strings.TrimSpace(string(respBody)))
	}
	io.Copy(io.Discard, resp.Body)
	return nil
}

func (c *channelMemoryClient) digest(ctx context.Context, req channelMemoryDigestRequest) (*channelMemoryDigestResponse, error) {
	if !c.digestEnabled() {
		return nil, nil
	}
	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.digestURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := c.client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("channel-memory digest returned %s: %s", resp.Status, strings.TrimSpace(string(respBody)))
	}
	var parsed channelMemoryDigestResponse
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return nil, fmt.Errorf("decode channel-memory digest: %w", err)
	}
	return &parsed, nil
}

func (c *channelMemoryClient) fetchAwarenessDigest(ctx context.Context, channelIDs []string, since time.Duration) channelAwarenessDigest {
	digest := channelAwarenessDigest{Requested: true, Status: "unavailable"}
	if !c.digestEnabled() {
		return digest
	}
	req := channelMemoryDigestRequest{
		SourceKind: channelMemorySourceKind,
		ChannelIDs: normalizeChannelIDs(channelIDs),
		Budget: channelMemoryDigestBudget{
			MaxBlocks: defaultDigestMaxBlocks,
		},
	}
	if since > 0 {
		req.Since = since.String()
	}
	resp, err := c.digest(ctx, req)
	if err != nil || resp == nil {
		return digest
	}
	status := normalizeDigestStatus(resp.Status)
	if status == "ok" && digestBlocksStale(resp.Blocks) {
		status = "stale"
	}
	digest.Status = status
	digest.GeneratedAt = strings.TrimSpace(resp.GeneratedAt)
	digest.SourceMessages = resp.Coverage.SourceMessages
	digest.DigestMessages = resp.Coverage.DigestMessages
	digest.RawRecentMessages = resp.Coverage.RawRecentMessages
	digest.CoverageGaps = len(resp.Coverage.Gaps)
	digest.DeterministicOnly = resp.Cost.DeterministicOnly
	digest.Blocks = append([]channelMemoryDigestBlock(nil), resp.Blocks...)
	return digest
}

func normalizeDigestStatus(status string) string {
	switch strings.TrimSpace(status) {
	case "ok", "stale", "unavailable", "coverage_gap":
		return strings.TrimSpace(status)
	default:
		return "unavailable"
	}
}

func digestBlocksStale(blocks []channelMemoryDigestBlock) bool {
	for _, block := range blocks {
		if block.Stale || block.Dirty {
			return true
		}
	}
	return false
}

func channelMemoryPayloadForMessage(msg wallMessage) channelMemoryIngestRequest {
	scope := "channel:" + strings.TrimSpace(msg.ChannelID)
	return channelMemoryIngestRequest{
		ChannelID: strings.TrimSpace(msg.ChannelID),
		Message: channelMemoryIngestMessage{
			ID:          strings.TrimSpace(msg.ID),
			AuthorID:    strings.TrimSpace(msg.AuthorID),
			AuthorName:  strings.TrimSpace(msg.Author),
			CreatedAt:   formatRFC3339(msg.Timestamp),
			EditedAt:    formatRFC3339(msg.EditedAt),
			Deleted:     msg.Deleted,
			Content:     msg.Content,
			ContentHash: wallMessageContentHash(msg),
		},
		Source: channelMemoryIngestSource{
			Kind:    channelMemorySourceKind,
			Service: channelMemorySourceService,
			Surface: channelMemorySourceSurface,
		},
		Metadata: map[string]string{
			"source_handle":    stableSourceHandle(msg),
			"visibility_scope": scope,
		},
		Scope: scope,
	}
}

func wallMessageContentHash(msg wallMessage) string {
	seed := strings.Join([]string{
		channelMemorySourceKind,
		strings.TrimSpace(msg.ChannelID),
		strings.TrimSpace(msg.ID),
		msg.Content,
		fmt.Sprintf("deleted=%t", msg.Deleted),
	}, "\x00")
	sum := sha256.Sum256([]byte(seed))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func formatRFC3339(ts time.Time) string {
	if ts.IsZero() {
		return ""
	}
	return ts.UTC().Format(time.RFC3339)
}
