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

func newChannelMemoryClient(rawURL, token string, timeout time.Duration) (*channelMemoryClient, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return nil, nil
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("parse channel-memory ingest URL: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("channel-memory ingest URL must use http or https")
	}
	if strings.TrimSpace(parsed.Host) == "" {
		return nil, fmt.Errorf("channel-memory ingest URL must include a host")
	}
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	return &channelMemoryClient{
		ingestURL: rawURL,
		token:     strings.TrimSpace(token),
		client:    &http.Client{Timeout: timeout},
	}, nil
}

func (c *channelMemoryClient) enabled() bool {
	return c != nil && strings.TrimSpace(c.ingestURL) != ""
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
