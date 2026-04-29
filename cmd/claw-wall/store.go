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

const (
	defaultResponseLimit  = 20
	backgroundContextSize = 10
	defaultTailLimit      = 40
	defaultTailMaxChars   = 8 * 1024
)

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

type tailRequest struct {
	ChannelIDs []string
	Since      time.Duration
	After      map[string]string
	Limit      int
	MaxChars   int
	Now        time.Time
}

type tailResult struct {
	ChannelIDs   []string
	Messages     []wallMessage
	Available    int
	Omitted      int
	CapReason    string
	WindowStart  time.Time
	BufferOldest time.Time
	BufferNewest time.Time
	Cursor       map[string]string
	After        map[string]string
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
		// No new messages since cursor — return recent background context so
		// agents retain situational awareness during quiet periods. Cursor is
		// not advanced; background context is re-served until new delta arrives.
		bgLimit := backgroundContextSize
		if bgLimit > limit {
			bgLimit = limit
		}
		background := make([]wallMessage, 0)
		for _, channelID := range channelIDs {
			state := s.channels[channelID]
			if state == nil {
				continue
			}
			background = append(background, state.messages...)
		}
		sortWallMessages(background)
		if len(background) > bgLimit {
			background = background[len(background)-bgLimit:]
		}
		if len(background) == 0 {
			return nil
		}
		out := make([]wallMessage, len(background))
		copy(out, background)
		return out
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

func (s *conversationStore) tail(req tailRequest) tailResult {
	limit := req.Limit
	if limit <= 0 {
		limit = defaultTailLimit
	}
	if limit > s.limit {
		limit = s.limit
	}
	maxChars := req.MaxChars
	if maxChars < 0 {
		maxChars = 0
	}
	now := req.Now
	if now.IsZero() {
		now = time.Now()
	}
	var windowStart time.Time
	if req.Since > 0 {
		windowStart = now.Add(-req.Since)
	}

	channelIDs := normalizeChannelIDs(req.ChannelIDs)

	s.mu.Lock()
	defer s.mu.Unlock()

	candidates := make([]wallMessage, 0)
	for _, channelID := range channelIDs {
		state := s.channels[channelID]
		if state == nil {
			continue
		}
		for _, msg := range state.messages {
			if afterID := strings.TrimSpace(req.After[channelID]); afterID != "" {
				if compareSnowflakes(msg.ID, afterID) <= 0 {
					continue
				}
			} else if req.Since > 0 && !msg.Timestamp.IsZero() && msg.Timestamp.Before(windowStart) {
				continue
			}
			candidates = append(candidates, msg)
		}
	}

	sortWallMessages(candidates)
	selectedReversed := make([]wallMessage, 0, min(limit, len(candidates)))
	usedChars := 0
	capReason := ""
	for i := len(candidates) - 1; i >= 0; i-- {
		if len(selectedReversed) >= limit {
			capReason = "limit"
			break
		}
		lineLen := len(formatWallMessage(candidates[i]))
		if len(selectedReversed) > 0 {
			lineLen++
		}
		if maxChars > 0 && usedChars+lineLen > maxChars && len(selectedReversed) > 0 {
			capReason = "max_chars"
			break
		}
		selectedReversed = append(selectedReversed, candidates[i])
		usedChars += lineLen
	}

	messages := make([]wallMessage, 0, len(selectedReversed))
	for i := len(selectedReversed) - 1; i >= 0; i-- {
		messages = append(messages, selectedReversed[i])
	}

	if len(messages) < len(candidates) && capReason == "" {
		capReason = "limit"
	}

	result := tailResult{
		ChannelIDs:  channelIDs,
		Messages:    messages,
		Available:   len(candidates),
		Omitted:     len(candidates) - len(messages),
		CapReason:   capReason,
		WindowStart: windowStart,
		After:       cloneStringMap(req.After),
	}
	for _, msg := range messages {
		if msg.ChannelID == "" || msg.ID == "" {
			continue
		}
		if result.Cursor == nil {
			result.Cursor = make(map[string]string)
		}
		if compareSnowflakes(msg.ID, result.Cursor[msg.ChannelID]) > 0 {
			result.Cursor[msg.ChannelID] = msg.ID
		}
	}
	for _, channelID := range channelIDs {
		state := s.channels[channelID]
		if state == nil {
			continue
		}
		for _, msg := range state.messages {
			if msg.Timestamp.IsZero() {
				continue
			}
			if result.BufferOldest.IsZero() || msg.Timestamp.Before(result.BufferOldest) {
				result.BufferOldest = msg.Timestamp
			}
			if result.BufferNewest.IsZero() || msg.Timestamp.After(result.BufferNewest) {
				result.BufferNewest = msg.Timestamp
			}
		}
	}

	return result
}

func newHandler(store *conversationStore) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	mux.HandleFunc("/channel-context", func(w http.ResponseWriter, r *http.Request) {
		mode := strings.TrimSpace(r.URL.Query().Get("mode"))
		if mode == "" {
			mode = "tail"
		}
		if mode != "tail" && mode != "delta" {
			http.Error(w, "mode must be tail or delta", http.StatusBadRequest)
			return
		}

		rawChannels := strings.TrimSpace(r.URL.Query().Get("channels"))
		if rawChannels == "" {
			http.Error(w, "channels is required", http.StatusBadRequest)
			return
		}
		channelIDs := strings.Split(rawChannels, ",")

		defaultLimit := defaultTailLimit
		if mode == "delta" {
			defaultLimit = defaultResponseLimit
		}
		limit := defaultLimit
		if rawLimit := strings.TrimSpace(r.URL.Query().Get("limit")); rawLimit != "" {
			parsed, err := strconv.Atoi(rawLimit)
			if err != nil || parsed < 1 {
				http.Error(w, "limit must be a positive integer", http.StatusBadRequest)
				return
			}
			limit = parsed
		}

		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		if mode == "delta" {
			consumer := strings.TrimSpace(r.URL.Query().Get("consumer"))
			if consumer == "" {
				http.Error(w, "consumer is required", http.StatusBadRequest)
				return
			}
			messages := store.consume(consumer, channelIDs, limit)
			if len(messages) == 0 {
				w.WriteHeader(http.StatusOK)
				return
			}
			_, _ = w.Write([]byte(formatWallMessages(messages)))
			return
		}

		since, err := parseDurationQuery(r.URL.Query().Get("since"), r.URL.Query().Get("window"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		maxChars := defaultTailMaxChars
		if rawMaxChars := strings.TrimSpace(r.URL.Query().Get("max_chars")); rawMaxChars != "" {
			parsed, err := strconv.Atoi(rawMaxChars)
			if err != nil || parsed < 1 {
				http.Error(w, "max_chars must be a positive integer", http.StatusBadRequest)
				return
			}
			maxChars = parsed
		}

		after, err := parseAfterQuery(r.URL.Query().Get("after"), channelIDs)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		result := store.tail(tailRequest{
			ChannelIDs: channelIDs,
			Since:      since,
			After:      after,
			Limit:      limit,
			MaxChars:   maxChars,
			Now:        time.Now(),
		})
		_, _ = w.Write([]byte(formatTailContext(result, since)))
	})
	return mux
}

func formatWallMessages(messages []wallMessage) string {
	var b strings.Builder
	for i, msg := range messages {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(formatWallMessage(msg))
	}
	return b.String()
}

func formatWallMessage(msg wallMessage) string {
	return fmt.Sprintf("[%s] %s: %s", formatWallTimestamp(msg.Timestamp), msg.Author, msg.Content)
}

func formatTailContext(result tailResult, since time.Duration) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[channel-context] mode=tail since=%s channels=%s messages=%d available=%d omitted=%d",
		formatDurationForHeader(since), strings.Join(result.ChannelIDs, ","), len(result.Messages), result.Available, result.Omitted)
	if result.CapReason != "" {
		fmt.Fprintf(&b, " cap=%s", result.CapReason)
	}
	if len(result.After) > 0 {
		fmt.Fprintf(&b, " after=%s", formatCursorMap(result.After))
	}
	if len(result.Cursor) > 0 {
		fmt.Fprintf(&b, " cursor=%s", formatCursorMap(result.Cursor))
	}
	fmt.Fprintf(&b, " range=%s buffer_range=%s",
		formatTimeRange(messageRange(result.Messages)),
		formatTimeRange(result.BufferOldest, result.BufferNewest))
	b.WriteString("\n")
	if result.Omitted > 0 {
		fmt.Fprintf(&b, "[omitted %d older retained messages due to %s; newest retained messages follow]\n",
			result.Omitted, result.CapReason)
	}
	if len(result.Messages) > 0 {
		b.WriteByte('\n')
		b.WriteString(formatWallMessages(result.Messages))
	}
	return b.String()
}

func parseDurationQuery(rawSince, rawWindow string) (time.Duration, error) {
	raw := strings.TrimSpace(rawSince)
	if raw == "" {
		raw = strings.TrimSpace(rawWindow)
	}
	if raw == "" {
		return 0, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d < 0 {
		return 0, fmt.Errorf("since must be a non-negative duration")
	}
	return d, nil
}

func parseAfterQuery(raw string, channelIDs []string) (map[string]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	allowed := make(map[string]struct{})
	for _, channelID := range normalizeChannelIDs(channelIDs) {
		allowed[channelID] = struct{}{}
	}
	out := make(map[string]string)
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		channelID, messageID, ok := strings.Cut(part, ":")
		channelID = strings.TrimSpace(channelID)
		messageID = strings.TrimSpace(messageID)
		if !ok || channelID == "" || messageID == "" {
			return nil, fmt.Errorf("after must be comma-separated channel_id:message_id pairs")
		}
		if _, ok := allowed[channelID]; !ok {
			return nil, fmt.Errorf("after references channel %q not present in channels", channelID)
		}
		out[channelID] = messageID
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

func formatDurationForHeader(d time.Duration) string {
	if d <= 0 {
		return "all"
	}
	return d.String()
}

func formatCursorMap(cursors map[string]string) string {
	if len(cursors) == 0 {
		return ""
	}
	keys := make([]string, 0, len(cursors))
	for channelID, messageID := range cursors {
		if strings.TrimSpace(channelID) != "" && strings.TrimSpace(messageID) != "" {
			keys = append(keys, channelID)
		}
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, channelID := range keys {
		parts = append(parts, channelID+":"+cursors[channelID])
	}
	return strings.Join(parts, ",")
}

func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func messageRange(messages []wallMessage) (time.Time, time.Time) {
	var oldest, newest time.Time
	for _, msg := range messages {
		if msg.Timestamp.IsZero() {
			continue
		}
		if oldest.IsZero() || msg.Timestamp.Before(oldest) {
			oldest = msg.Timestamp
		}
		if newest.IsZero() || msg.Timestamp.After(newest) {
			newest = msg.Timestamp
		}
	}
	return oldest, newest
}

func formatTimeRange(start, end time.Time) string {
	if start.IsZero() || end.IsZero() {
		return "empty"
	}
	return formatHeaderTimestamp(start) + ".." + formatHeaderTimestamp(end)
}

func formatHeaderTimestamp(ts time.Time) string {
	if ts.IsZero() {
		return "unknown-time"
	}
	return ts.UTC().Format("2006-01-02T15:04Z")
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
