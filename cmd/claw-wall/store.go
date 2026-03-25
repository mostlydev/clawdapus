package main

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const defaultResponseLimit = 20

type wallMessage struct {
	ID        string
	ChannelID string
	Author    string
	Content   string
	Timestamp time.Time
}

type channelBuffer struct {
	messages []wallMessage
	seenIDs  map[string]struct{}
}

type conversationStore struct {
	limit    int
	mu       sync.Mutex
	channels map[string]*channelBuffer
	cursors  map[string]map[string]string
}

func newConversationStore(limit int) *conversationStore {
	return &conversationStore{
		limit:    limit,
		channels: make(map[string]*channelBuffer),
		cursors:  make(map[string]map[string]string),
	}
}

func (s *conversationStore) merge(channelID string, messages []wallMessage) {
	if len(messages) == 0 {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	state := s.channels[channelID]
	if state == nil {
		state = &channelBuffer{seenIDs: make(map[string]struct{})}
		s.channels[channelID] = state
	}

	for _, msg := range messages {
		if strings.TrimSpace(msg.ID) == "" {
			continue
		}
		msg.ChannelID = channelID
		if _, exists := state.seenIDs[msg.ID]; exists {
			continue
		}
		state.seenIDs[msg.ID] = struct{}{}
		state.messages = append(state.messages, msg)
	}

	sortWallMessages(state.messages)
	if len(state.messages) <= s.limit {
		return
	}

	trimmed := state.messages[len(state.messages)-s.limit:]
	newSeen := make(map[string]struct{}, len(trimmed))
	for _, msg := range trimmed {
		newSeen[msg.ID] = struct{}{}
	}
	state.messages = append([]wallMessage(nil), trimmed...)
	state.seenIDs = newSeen
}

func (s *conversationStore) consume(consumer string, channelIDs []string, limit int) []wallMessage {
	if limit <= 0 {
		limit = defaultResponseLimit
	}
	if limit > s.limit {
		limit = s.limit
	}

	channelIDs = normalizeChannelIDs(channelIDs)

	s.mu.Lock()
	defer s.mu.Unlock()

	cursorByChannel := s.cursors[consumer]
	collected := make([]wallMessage, 0)
	for _, channelID := range channelIDs {
		state := s.channels[channelID]
		if state == nil {
			continue
		}
		lastSeen := cursorByChannel[channelID]
		for _, msg := range state.messages {
			if lastSeen != "" && compareSnowflakes(msg.ID, lastSeen) <= 0 {
				continue
			}
			collected = append(collected, msg)
		}
	}

	sortWallMessages(collected)
	if len(collected) > limit {
		collected = collected[:limit]
	}
	if len(collected) == 0 {
		return nil
	}

	if cursorByChannel == nil {
		cursorByChannel = make(map[string]string)
		s.cursors[consumer] = cursorByChannel
	}
	for _, msg := range collected {
		if compareSnowflakes(msg.ID, cursorByChannel[msg.ChannelID]) > 0 {
			cursorByChannel[msg.ChannelID] = msg.ID
		}
	}

	out := make([]wallMessage, len(collected))
	copy(out, collected)
	return out
}

func newHandler(store *conversationStore) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	mux.HandleFunc("/channel-context", func(w http.ResponseWriter, r *http.Request) {
		consumer := strings.TrimSpace(r.URL.Query().Get("consumer"))
		if consumer == "" {
			http.Error(w, "consumer is required", http.StatusBadRequest)
			return
		}

		rawChannels := strings.TrimSpace(r.URL.Query().Get("channels"))
		if rawChannels == "" {
			http.Error(w, "channels is required", http.StatusBadRequest)
			return
		}
		channelIDs := strings.Split(rawChannels, ",")

		limit := defaultResponseLimit
		if rawLimit := strings.TrimSpace(r.URL.Query().Get("limit")); rawLimit != "" {
			parsed, err := strconv.Atoi(rawLimit)
			if err != nil || parsed < 1 {
				http.Error(w, "limit must be a positive integer", http.StatusBadRequest)
				return
			}
			limit = parsed
		}

		messages := store.consume(consumer, channelIDs, limit)
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		if len(messages) == 0 {
			w.WriteHeader(http.StatusOK)
			return
		}
		_, _ = w.Write([]byte(formatWallMessages(messages)))
	})
	return mux
}

func formatWallMessages(messages []wallMessage) string {
	var b strings.Builder
	for i, msg := range messages {
		if i > 0 {
			b.WriteByte('\n')
		}
		fmt.Fprintf(&b, "[%s] %s: %s", formatWallTimestamp(msg.Timestamp), msg.Author, msg.Content)
	}
	return b.String()
}

func formatWallTimestamp(ts time.Time) string {
	if ts.IsZero() {
		return "unknown-time"
	}
	return ts.UTC().Format("2006-01-02 15:04")
}

func normalizeChannelIDs(channelIDs []string) []string {
	seen := make(map[string]struct{})
	normalized := make([]string, 0, len(channelIDs))
	for _, channelID := range channelIDs {
		channelID = strings.TrimSpace(channelID)
		if channelID == "" {
			continue
		}
		if _, exists := seen[channelID]; exists {
			continue
		}
		seen[channelID] = struct{}{}
		normalized = append(normalized, channelID)
	}
	sort.Strings(normalized)
	return normalized
}

func sortWallMessages(messages []wallMessage) {
	sort.Slice(messages, func(i, j int) bool {
		return compareSnowflakes(messages[i].ID, messages[j].ID) < 0
	})
}

func compareSnowflakes(left, right string) int {
	switch {
	case strings.TrimSpace(left) == "" && strings.TrimSpace(right) == "":
		return 0
	case strings.TrimSpace(left) == "":
		return -1
	case strings.TrimSpace(right) == "":
		return 1
	}

	leftID, leftErr := strconv.ParseUint(left, 10, 64)
	rightID, rightErr := strconv.ParseUint(right, 10, 64)
	if leftErr == nil && rightErr == nil {
		switch {
		case leftID < rightID:
			return -1
		case leftID > rightID:
			return 1
		default:
			return 0
		}
	}

	switch {
	case left < right:
		return -1
	case left > right:
		return 1
	default:
		return 0
	}
}
