package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

const maxDiscordFetchLimit = 100

type tokenPair struct {
	ChannelID string
	Token     string
}

type discordPoller struct {
	client       *http.Client
	store        *conversationStore
	targets      []tokenPair
	fetchLimit   int
	latestByPair map[string]string
	baseURL      string
	cooldowns    *rateLimitTracker
	now          func() time.Time
}

type discordAPIMessage struct {
	ID          string              `json:"id"`
	Content     string              `json:"content"`
	Timestamp   string              `json:"timestamp"`
	Author      discordAPIAuthor    `json:"author"`
	Member      *discordAPIMember   `json:"member,omitempty"`
	Attachments []discordAttachment `json:"attachments,omitempty"`
	Embeds      []discordEmbed      `json:"embeds,omitempty"`
}

type discordAPIAuthor struct {
	Username   string `json:"username"`
	GlobalName string `json:"global_name"`
}

type discordAPIMember struct {
	Nick string `json:"nick"`
}

type discordAttachment struct {
	Filename string `json:"filename"`
}

type discordEmbed struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	URL         string `json:"url"`
}

func parseTokenPairs(raw string) ([]tokenPair, error) {
	parts := strings.Split(strings.TrimSpace(raw), ",")
	seen := make(map[string]struct{})
	pairs := make([]tokenPair, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		pieces := strings.SplitN(part, ":", 2)
		if len(pieces) != 2 || strings.TrimSpace(pieces[0]) == "" || strings.TrimSpace(pieces[1]) == "" {
			return nil, fmt.Errorf("invalid pair %q (want channelID:token)", part)
		}

		pair := tokenPair{
			ChannelID: strings.TrimSpace(pieces[0]),
			Token:     strings.TrimSpace(pieces[1]),
		}
		key := pair.ChannelID + "\x00" + pair.Token
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		pairs = append(pairs, pair)
	}
	if len(pairs) == 0 {
		return nil, fmt.Errorf("no valid channel/token pairs found")
	}

	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].ChannelID != pairs[j].ChannelID {
			return pairs[i].ChannelID < pairs[j].ChannelID
		}
		return pairs[i].Token < pairs[j].Token
	})
	return pairs, nil
}

func newDiscordPoller(client *http.Client, store *conversationStore, targets []tokenPair, fetchLimit int) *discordPoller {
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	if fetchLimit < 1 {
		fetchLimit = 50
	}
	if fetchLimit > maxDiscordFetchLimit {
		fetchLimit = maxDiscordFetchLimit
	}
	return &discordPoller{
		client:       client,
		store:        store,
		targets:      targets,
		fetchLimit:   fetchLimit,
		latestByPair: make(map[string]string),
		baseURL:      "https://discord.com/api/v10",
		cooldowns:    newRateLimitTracker(),
		now:          time.Now,
	}
}

func (p *discordPoller) Run(ctx context.Context, interval time.Duration, logWriter io.Writer) {
	p.pollOnce(ctx, logWriter)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.pollOnce(ctx, logWriter)
		}
	}
}

func (p *discordPoller) pollOnce(ctx context.Context, logWriter io.Writer) {
	for _, target := range p.targets {
		if p.cooldowns.blocked(target, p.now()) {
			continue
		}

		latestID := p.latestByPair[pairKey(target)]
		messages, newestID, err := p.fetchMessages(ctx, target, latestID)
		if err != nil {
			var rateLimitErr *discordRateLimitError
			if errors.As(err, &rateLimitErr) {
				if logWriter != nil && rateLimitErr.FirstOccurrence {
					fmt.Fprintf(logWriter, "claw-wall: %v\n", rateLimitErr)
				}
				continue
			}
			if logWriter != nil {
				fmt.Fprintf(logWriter, "claw-wall: poll channel %s failed: %v\n", target.ChannelID, err)
			}
			continue
		}
		if len(messages) > 0 {
			p.store.merge(target.ChannelID, messages)
		}
		if strings.TrimSpace(newestID) != "" {
			p.latestByPair[pairKey(target)] = newestID
		}
	}
}

func (p *discordPoller) fetchMessages(ctx context.Context, target tokenPair, afterID string) ([]wallMessage, string, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(p.baseURL), "/")
	if baseURL == "" {
		baseURL = "https://discord.com/api/v10"
	}

	url := fmt.Sprintf("%s/channels/%s/messages?limit=%d", baseURL, target.ChannelID, p.fetchLimit)
	if strings.TrimSpace(afterID) != "" {
		url += "&after=" + afterID
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Authorization", "Bot "+target.Token)
	req.Header.Set("User-Agent", "DiscordBot (https://github.com/mostlydev/clawdapus, 1.0)")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		if limit := parseDiscordRateLimit(resp, body, p.now()); limit != nil {
			return nil, "", &discordRateLimitError{
				Target:          target,
				Limit:           *limit,
				FirstOccurrence: p.cooldowns.record(target, *limit),
			}
		}
		return nil, "", fmt.Errorf("discord returned %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}

	var payload []discordAPIMessage
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, "", err
	}
	if len(payload) == 0 {
		return nil, afterID, nil
	}

	messages := make([]wallMessage, 0, len(payload))
	newestID := afterID
	for _, msg := range payload {
		converted, ok := convertDiscordMessage(target.ChannelID, msg)
		if !ok {
			continue
		}
		messages = append(messages, converted)
		if compareSnowflakes(msg.ID, newestID) > 0 {
			newestID = msg.ID
		}
	}
	sortWallMessages(messages)
	return messages, newestID, nil
}

func convertDiscordMessage(channelID string, msg discordAPIMessage) (wallMessage, bool) {
	messageID := strings.TrimSpace(msg.ID)
	if messageID == "" {
		return wallMessage{}, false
	}

	timestamp, err := time.Parse(time.RFC3339, msg.Timestamp)
	if err != nil {
		timestamp = time.Time{}
	}

	return wallMessage{
		ID:        messageID,
		ChannelID: channelID,
		Author:    discordAuthorName(msg),
		Content:   renderDiscordMessageContent(msg),
		Timestamp: timestamp,
	}, true
}

func discordAuthorName(msg discordAPIMessage) string {
	switch {
	case msg.Member != nil && strings.TrimSpace(msg.Member.Nick) != "":
		return strings.TrimSpace(msg.Member.Nick)
	case strings.TrimSpace(msg.Author.GlobalName) != "":
		return strings.TrimSpace(msg.Author.GlobalName)
	case strings.TrimSpace(msg.Author.Username) != "":
		return strings.TrimSpace(msg.Author.Username)
	default:
		return "unknown"
	}
}

func renderDiscordMessageContent(msg discordAPIMessage) string {
	parts := make([]string, 0, 3)

	if text := collapseWhitespace(msg.Content); text != "" {
		parts = append(parts, text)
	}

	if len(msg.Attachments) > 0 {
		names := make([]string, 0, len(msg.Attachments))
		for _, attachment := range msg.Attachments {
			name := collapseWhitespace(attachment.Filename)
			if name == "" {
				name = "attachment"
			}
			names = append(names, name)
		}
		parts = append(parts, "[attachments: "+strings.Join(names, ", ")+"]")
	} else if len(parts) == 0 && len(msg.Embeds) > 0 {
		parts = append(parts, "[embed]")
	}

	if len(parts) == 0 {
		return "[message with no text]"
	}
	return strings.Join(parts, " ")
}

func collapseWhitespace(value string) string {
	fields := strings.Fields(strings.TrimSpace(value))
	if len(fields) == 0 {
		return ""
	}
	return strings.Join(fields, " ")
}

func pairKey(pair tokenPair) string {
	return pair.ChannelID + "\x00" + pair.Token
}
