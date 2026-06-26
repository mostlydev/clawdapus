package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	defaultResponseLimit   = 20
	backgroundContextSize  = 10
	defaultTailLimit       = 40
	defaultAwarenessLimit  = 60
	defaultTailMaxChars    = 32 * 1024
	defaultDigestRawLimit  = 10
	defaultDigestMaxBlocks = 32
	defaultDigestRawRecent = 6 * time.Hour
)

const (
	backfillStatusComplete    = "complete"
	backfillStatusInProgress  = "in_progress"
	backfillStatusPartial     = "partial"
	backfillStatusRateLimited = "rate_limited"
	backfillStatusUnavailable = "unavailable"
)

type wallMessage struct {
	ID           string    `json:"id"`
	ChannelID    string    `json:"channel_id"`
	SourceHandle string    `json:"source_handle,omitempty"`
	AuthorID     string    `json:"author_id,omitempty"`
	Author       string    `json:"author"`
	Content      string    `json:"content"`
	Timestamp    time.Time `json:"timestamp"`
	EditedAt     time.Time `json:"edited_at,omitempty"`
	Deleted      bool      `json:"deleted,omitempty"`
}

type channelBuffer struct {
	messages []wallMessage
	seenIDs  map[string]struct{}
}

type conversationStore struct {
	limit          int
	retention      time.Duration
	now            func() time.Time
	mu             sync.Mutex
	channels       map[string]*channelBuffer
	cursors        map[string]map[string]string
	backfillStatus map[string]string
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
	ChannelIDs     []string
	Messages       []wallMessage
	Available      int
	Omitted        int
	CapReason      string
	WindowStart    time.Time
	BufferOldest   time.Time
	BufferNewest   time.Time
	Cursor         map[string]string
	After          map[string]string
	BackfillStatus map[string]string
}

type handlerConfig struct {
	toolToken     string
	agentChannels map[string]map[string]struct{}
	channelMemory *channelMemoryClient
}

type agentChannelAllowlistFile struct {
	Version int                 `json:"version"`
	Agents  map[string][]string `json:"agents"`
}

type retrievalResult struct {
	Messages         []wallMessage              `json:"messages"`
	DerivedBlocks    []channelMemoryDigestBlock `json:"derived_blocks,omitempty"`
	RetainedCoverage coverageInfo               `json:"retained_coverage"`
	Source           string                     `json:"source,omitempty"`
	SourceCounts     *sourceCounts              `json:"source_counts,omitempty"`
	Status           string                     `json:"status"`
	Hint             string                     `json:"hint,omitempty"`
}

type sourceCounts struct {
	Retained      int `json:"retained"`
	DurableSource int `json:"durable_source"`
	SparseBlock   int `json:"sparse_block"`
}

type coverageInfo struct {
	OldestTS       string `json:"oldest_ts,omitempty"`
	NewestTS       string `json:"newest_ts,omitempty"`
	BufferSize     int    `json:"buffer_size"`
	BackfillStatus string `json:"backfill_status,omitempty"`
	oldestTime     time.Time
	newestTime     time.Time
}

type searchChannelContextRequest struct {
	Channels []string `json:"channels"`
	Query    string   `json:"query"`
	Since    string   `json:"since"`
	Author   string   `json:"author"`
	Limit    int      `json:"limit"`
}

type getChannelMessagesRequest struct {
	Channels      []string `json:"channels"`
	MessageIDs    []string `json:"message_ids"`
	SourceHandles []string `json:"source_handles,omitempty"`
	After         string   `json:"after"`
	Before        string   `json:"before"`
	Author        string   `json:"author"`
	Limit         int      `json:"limit"`
}

type channelMemoryReplayRequest struct {
	Channels []string `json:"channels"`
	Limit    int      `json:"limit"`
}

type channelMemoryReplayResponse struct {
	Status   string `json:"status"`
	Messages int    `json:"messages"`
	Pushed   int    `json:"pushed"`
}

type channelAwarenessDigest struct {
	Requested         bool
	Status            string
	GeneratedAt       string
	SourceMessages    int
	DigestMessages    int
	RawRecentMessages int
	OlderRawMessages  int
	RawRecent         string
	CoverageGaps      int
	DeterministicOnly bool
	Blocks            []channelMemoryDigestBlock
}

func newConversationStore(limit int, retentions ...time.Duration) *conversationStore {
	var retention time.Duration
	if len(retentions) > 0 {
		retention = retentions[0]
	}
	return &conversationStore{
		limit:          limit,
		retention:      retention,
		now:            time.Now,
		channels:       make(map[string]*channelBuffer),
		cursors:        make(map[string]map[string]string),
		backfillStatus: make(map[string]string),
	}
}

func (s *conversationStore) currentTime() time.Time {
	if s != nil && s.now != nil {
		if now := s.now(); !now.IsZero() {
			return now
		}
	}
	return time.Now()
}

func (s *conversationStore) effectiveTime(now time.Time) time.Time {
	if now.IsZero() {
		return s.currentTime()
	}
	return now
}

func (s *conversationStore) merge(channelID string, messages []wallMessage) []wallMessage {
	return s.mergeAt(channelID, messages, s.currentTime())
}

func (s *conversationStore) mergeAt(channelID string, messages []wallMessage, now time.Time) []wallMessage {
	if len(messages) == 0 {
		return nil
	}
	now = s.effectiveTime(now)
	channelID = strings.TrimSpace(channelID)
	if channelID == "" {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	state := s.channels[channelID]
	if state == nil {
		state = &channelBuffer{seenIDs: make(map[string]struct{})}
		s.channels[channelID] = state
	}

	retained := make([]wallMessage, 0, len(messages))
	for _, msg := range messages {
		if strings.TrimSpace(msg.ID) == "" {
			continue
		}
		if s.retention > 0 {
			if msg.Timestamp.IsZero() {
				continue
			}
			if msg.Timestamp.Before(now.Add(-s.retention)) {
				continue
			}
		}
		msg.ChannelID = channelID
		if _, exists := state.seenIDs[msg.ID]; exists {
			continue
		}
		msg.SourceHandle = stableSourceHandle(msg)
		state.seenIDs[msg.ID] = struct{}{}
		state.messages = append(state.messages, msg)
		retained = append(retained, msg)
	}

	sortWallMessages(state.messages)
	s.trimChannelLocked(channelID, state, now)
	return retained
}

func (s *conversationStore) setBackfillStatus(channelID, status string) {
	channelID = strings.TrimSpace(channelID)
	status = normalizeBackfillStatus(status)
	if channelID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.setBackfillStatusLocked(channelID, status)
}

func (s *conversationStore) setBackfillStatusLocked(channelID, status string) {
	if status == backfillStatusComplete && s.backfillStatus[channelID] == backfillStatusPartial {
		return
	}
	s.backfillStatus[channelID] = normalizeBackfillStatus(status)
}

func (s *conversationStore) markBackfillPartialLocked(channelID string) {
	if strings.TrimSpace(channelID) == "" {
		return
	}
	if s.backfillStatus[channelID] == backfillStatusRateLimited {
		return
	}
	s.backfillStatus[channelID] = backfillStatusPartial
}

func (s *conversationStore) trimChannelLocked(channelID string, state *channelBuffer, now time.Time) {
	if state == nil {
		return
	}
	now = s.effectiveTime(now)

	messages := state.messages
	if s.retention > 0 {
		cutoff := now.Add(-s.retention)
		filtered := make([]wallMessage, 0, len(messages))
		for _, msg := range messages {
			if msg.Timestamp.IsZero() || msg.Timestamp.Before(cutoff) {
				continue
			}
			filtered = append(filtered, msg)
		}
		messages = filtered
	}

	if s.limit > 0 && len(messages) > s.limit {
		messages = messages[len(messages)-s.limit:]
		s.markBackfillPartialLocked(channelID)
	}

	newSeen := make(map[string]struct{}, len(messages))
	for _, msg := range messages {
		newSeen[msg.ID] = struct{}{}
	}
	state.messages = append([]wallMessage(nil), messages...)
	state.seenIDs = newSeen
}

func (s *conversationStore) trimChannelsLocked(channelIDs []string, now time.Time) {
	for _, channelID := range normalizeChannelIDs(channelIDs) {
		if state := s.channels[channelID]; state != nil {
			s.trimChannelLocked(channelID, state, now)
		}
	}
}

func normalizeBackfillStatus(status string) string {
	switch strings.TrimSpace(status) {
	case backfillStatusComplete, backfillStatusInProgress, backfillStatusPartial, backfillStatusRateLimited:
		return strings.TrimSpace(status)
	default:
		return backfillStatusUnavailable
	}
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
	s.trimChannelsLocked(channelIDs, s.currentTime())

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
		now = s.currentTime()
	}
	var windowStart time.Time
	if req.Since > 0 {
		windowStart = now.Add(-req.Since)
	}

	channelIDs := normalizeChannelIDs(req.ChannelIDs)

	s.mu.Lock()
	defer s.mu.Unlock()
	s.trimChannelsLocked(channelIDs, now)

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
		BackfillStatus: cloneStringMap(
			s.backfillStatusForChannelsLocked(channelIDs),
		),
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

func (s *conversationStore) search(req searchChannelContextRequest, now time.Time) retrievalResult {
	limit := req.Limit
	if limit <= 0 {
		limit = defaultResponseLimit
	}
	if limit > s.limit {
		limit = s.limit
	}
	if now.IsZero() {
		now = s.currentTime()
	}
	var windowStart time.Time
	if strings.TrimSpace(req.Since) != "" {
		if d, err := time.ParseDuration(strings.TrimSpace(req.Since)); err == nil && d > 0 {
			windowStart = now.Add(-d)
		}
	}
	query := strings.ToLower(strings.TrimSpace(req.Query))
	author := strings.ToLower(strings.TrimSpace(req.Author))
	result := s.scan(req.Channels, func(msg wallMessage) bool {
		if !windowStart.IsZero() && !msg.Timestamp.IsZero() && msg.Timestamp.Before(windowStart) {
			return false
		}
		if author != "" && !strings.Contains(strings.ToLower(msg.Author), author) {
			return false
		}
		if query != "" && !strings.Contains(strings.ToLower(msg.Content), query) {
			return false
		}
		return true
	}, limit, now)
	if result.Status == "empty" && !windowStart.IsZero() && !result.RetainedCoverage.oldestTime.IsZero() && windowStart.Before(result.RetainedCoverage.oldestTime) {
		result.Status = "not_in_buffer"
		result.Hint = "Requested window extends before buffer's oldest_ts. Operator can widen CLAW_WALL_LIMIT or rely on v2 digest once available."
	}
	return result
}

func (s *conversationStore) getMessages(req getChannelMessagesRequest) retrievalResult {
	limit := req.Limit
	if limit <= 0 {
		limit = defaultResponseLimit
	}
	if limit > s.limit {
		limit = s.limit
	}
	wanted := make(map[string]struct{}, len(req.MessageIDs))
	for _, id := range req.MessageIDs {
		id = strings.TrimSpace(id)
		if id != "" {
			wanted[id] = struct{}{}
		}
	}
	author := strings.ToLower(strings.TrimSpace(req.Author))
	result := s.scan(req.Channels, func(msg wallMessage) bool {
		if len(wanted) > 0 {
			if _, ok := wanted[msg.ID]; !ok {
				return false
			}
		}
		if after := strings.TrimSpace(req.After); after != "" && !messageAfterBoundary(msg, after) {
			return false
		}
		if before := strings.TrimSpace(req.Before); before != "" && !messageBeforeBoundary(msg, before) {
			return false
		}
		if author != "" && !strings.Contains(strings.ToLower(msg.Author), author) {
			return false
		}
		return true
	}, limit, time.Time{})
	if len(wanted) > 0 && len(result.Messages) < len(wanted) {
		result.Status = "not_in_buffer"
		result.Hint = "One or more requested message_ids are not retained in claw-wall's current buffer."
	}
	return result
}

func (s *conversationStore) scan(channelIDs []string, match func(wallMessage) bool, limit int, now time.Time) retrievalResult {
	channelIDs = normalizeChannelIDs(channelIDs)
	if limit <= 0 {
		limit = defaultResponseLimit
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now = s.effectiveTime(now)
	s.trimChannelsLocked(channelIDs, now)

	candidates := make([]wallMessage, 0)
	var bufferOldest, bufferNewest time.Time
	bufferSize := 0
	for _, channelID := range channelIDs {
		state := s.channels[channelID]
		if state == nil {
			continue
		}
		bufferSize += len(state.messages)
		for _, msg := range state.messages {
			if !msg.Timestamp.IsZero() {
				if bufferOldest.IsZero() || msg.Timestamp.Before(bufferOldest) {
					bufferOldest = msg.Timestamp
				}
				if bufferNewest.IsZero() || msg.Timestamp.After(bufferNewest) {
					bufferNewest = msg.Timestamp
				}
			}
			if match == nil || match(msg) {
				candidates = append(candidates, msg)
			}
		}
	}
	sortWallMessages(candidates)
	if len(candidates) > limit {
		candidates = candidates[len(candidates)-limit:]
	}
	status := "ok"
	if len(candidates) == 0 {
		status = "empty"
	}
	return retrievalResult{
		Messages: candidates,
		RetainedCoverage: coverageInfo{
			OldestTS:       formatOptionalHeaderTimestamp(bufferOldest),
			NewestTS:       formatOptionalHeaderTimestamp(bufferNewest),
			BufferSize:     bufferSize,
			BackfillStatus: formatBackfillStatus(s.backfillStatusForChannelsLocked(channelIDs)),
			oldestTime:     bufferOldest,
			newestTime:     bufferNewest,
		},
		Status: status,
	}
}

func (s *conversationStore) snapshot(channelIDs []string, limit int, now time.Time) []wallMessage {
	channelIDs = normalizeChannelIDs(channelIDs)
	s.mu.Lock()
	defer s.mu.Unlock()
	now = s.effectiveTime(now)
	s.trimChannelsLocked(channelIDs, now)

	messages := make([]wallMessage, 0)
	for _, channelID := range channelIDs {
		state := s.channels[channelID]
		if state == nil {
			continue
		}
		messages = append(messages, state.messages...)
	}
	sortWallMessages(messages)
	if limit > 0 && len(messages) > limit {
		messages = messages[len(messages)-limit:]
	}
	out := make([]wallMessage, len(messages))
	copy(out, messages)
	return out
}

func mergeChannelMemorySearch(ctx context.Context, result retrievalResult, client *channelMemoryClient, req searchChannelContextRequest) retrievalResult {
	if !client.searchEnabled() {
		return result
	}
	counts := &sourceCounts{Retained: len(result.Messages)}
	result.SourceCounts = counts
	result.Source = "claw-wall"

	resp, err := client.search(ctx, channelMemorySearchRequest{
		SourceKind: channelMemorySourceKind,
		ChannelIDs: normalizeChannelIDs(req.Channels),
		Query:      req.Query,
		Since:      req.Since,
		Author:     req.Author,
		Limit:      effectiveRetrievalLimit(req.Limit),
	})
	if err != nil || resp == nil {
		result.Hint = appendHint(result.Hint, "channel-memory search unavailable; returned claw-wall raw buffer only")
		return result
	}

	counts.DurableSource = len(resp.SourceMessages)
	counts.SparseBlock = len(resp.DerivedBlocks)
	result.DerivedBlocks = append([]channelMemoryDigestBlock(nil), resp.DerivedBlocks...)

	seen := messageKeySet(result.Messages)
	for _, source := range resp.SourceMessages {
		msg := wallMessageFromSource(source)
		if strings.TrimSpace(msg.ID) == "" || strings.TrimSpace(msg.ChannelID) == "" {
			continue
		}
		key := messageKey(msg)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		result.Messages = append(result.Messages, msg)
	}
	sortWallMessages(result.Messages)
	result.Messages = newestMessages(result.Messages, effectiveRetrievalLimit(req.Limit))
	result.Source = retrievalSource(*counts)
	if counts.DurableSource > 0 || counts.SparseBlock > 0 || len(result.Messages) > 0 {
		result.Status = "ok"
		if strings.Contains(result.Hint, "v2 digest") {
			result.Hint = ""
		}
	} else if result.Status == "not_in_buffer" {
		result.Status = "empty"
		result.Hint = "No matches found in claw-wall raw buffer or channel-memory retention."
	}
	return result
}

func mergeChannelMemoryMessages(ctx context.Context, result retrievalResult, client *channelMemoryClient, req getChannelMessagesRequest) retrievalResult {
	if !client.sourceMessagesEnabled() || len(req.MessageIDs) == 0 {
		return result
	}
	counts := &sourceCounts{Retained: len(result.Messages)}
	result.SourceCounts = counts
	result.Source = "claw-wall"

	seen := messageKeySet(result.Messages)
	missingByChannel := missingMessageIDsByChannel(req, seen)
	for channelID, messageIDs := range missingByChannel {
		resp, err := client.sourceMessages(ctx, channelMemorySourceMessagesRequest{
			SourceKind: channelMemorySourceKind,
			ChannelID:  channelID,
			MessageIDs: messageIDs,
		})
		if err != nil || resp == nil {
			result.Hint = appendHint(result.Hint, "channel-memory source lookup unavailable; returned claw-wall raw buffer only")
			return result
		}
		counts.DurableSource += len(resp.Messages)
		for _, source := range resp.Messages {
			msg := wallMessageFromSource(source)
			if !messageMatchesGetRequest(msg, req) {
				continue
			}
			key := messageKey(msg)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			result.Messages = append(result.Messages, msg)
		}
	}

	sortWallMessages(result.Messages)
	result.Messages = newestMessages(result.Messages, effectiveRetrievalLimit(req.Limit))
	result.Source = retrievalSource(*counts)
	if len(result.Messages) > 0 {
		result.Status = "ok"
		if result.Hint == "One or more requested message_ids are not retained in claw-wall's current buffer." {
			result.Hint = ""
		}
	}
	if len(req.MessageIDs) > 0 && !allRequestedMessageIDsFound(req, result.Messages) {
		if len(result.Messages) == 0 {
			result.Status = "not_found"
		}
		result.Hint = appendHint(result.Hint, "One or more requested message_ids were not found in claw-wall raw buffer or channel-memory.")
	}
	return result
}

func effectiveRetrievalLimit(limit int) int {
	if limit <= 0 {
		return defaultResponseLimit
	}
	return limit
}

func messageKeySet(messages []wallMessage) map[string]struct{} {
	seen := make(map[string]struct{}, len(messages))
	for _, msg := range messages {
		seen[messageKey(msg)] = struct{}{}
	}
	return seen
}

func messageKey(msg wallMessage) string {
	return strings.TrimSpace(msg.ChannelID) + "\x00" + strings.TrimSpace(msg.ID)
}

func newestMessages(messages []wallMessage, limit int) []wallMessage {
	if limit <= 0 {
		limit = defaultResponseLimit
	}
	if len(messages) <= limit {
		return messages
	}
	return messages[len(messages)-limit:]
}

func retrievalSource(counts sourceCounts) string {
	raw := counts.Retained > 0
	memory := counts.DurableSource > 0 || counts.SparseBlock > 0
	switch {
	case raw && memory:
		return "mixed"
	case memory:
		return "channel-memory"
	default:
		return "claw-wall"
	}
}

func appendHint(existing, next string) string {
	existing = strings.TrimSpace(existing)
	next = strings.TrimSpace(next)
	if existing == "" {
		return next
	}
	if next == "" || strings.Contains(existing, next) {
		return existing
	}
	return existing + " " + next
}

func wallMessageFromSource(source channelMemorySourceMessage) wallMessage {
	createdAt, _ := time.Parse(time.RFC3339, strings.TrimSpace(source.CreatedAt))
	editedAt, _ := time.Parse(time.RFC3339, strings.TrimSpace(source.EditedAt))
	return wallMessage{
		ID:           strings.TrimSpace(source.MessageID),
		ChannelID:    strings.TrimSpace(source.ChannelID),
		SourceHandle: firstNonEmptyString(source.SourceHandle, sourceHandleFor(source.ChannelID, source.MessageID)),
		AuthorID:     strings.TrimSpace(source.AuthorID),
		Author:       firstNonEmptyString(source.AuthorName, source.AuthorID, "unknown"),
		Content:      source.Content,
		Timestamp:    createdAt,
		EditedAt:     editedAt,
		Deleted:      source.Deleted,
	}
}

func sourceHandleFor(channelID, messageID string) string {
	channelID = strings.TrimSpace(channelID)
	messageID = strings.TrimSpace(messageID)
	if channelID == "" || messageID == "" {
		return ""
	}
	return channelID + "/" + messageID
}

func missingMessageIDsByChannel(req getChannelMessagesRequest, seen map[string]struct{}) map[string][]string {
	out := make(map[string][]string)
	for _, channelID := range normalizeChannelIDs(req.Channels) {
		for _, messageID := range trimNonEmptyStrings(req.MessageIDs) {
			if _, ok := seen[channelID+"\x00"+messageID]; ok {
				continue
			}
			if !containsString(out[channelID], messageID) {
				out[channelID] = append(out[channelID], messageID)
			}
		}
	}
	return out
}

func messageMatchesGetRequest(msg wallMessage, req getChannelMessagesRequest) bool {
	author := strings.ToLower(strings.TrimSpace(req.Author))
	if author != "" && !strings.Contains(strings.ToLower(msg.Author), author) && !strings.Contains(strings.ToLower(msg.AuthorID), author) {
		return false
	}
	if after := strings.TrimSpace(req.After); after != "" && !messageAfterBoundary(msg, after) {
		return false
	}
	if before := strings.TrimSpace(req.Before); before != "" && !messageBeforeBoundary(msg, before) {
		return false
	}
	return true
}

func allRequestedMessageIDsFound(req getChannelMessagesRequest, messages []wallMessage) bool {
	if len(req.MessageIDs) == 0 {
		return true
	}
	found := make(map[string]struct{})
	for _, msg := range messages {
		found[strings.TrimSpace(msg.ID)] = struct{}{}
	}
	for _, id := range trimNonEmptyStrings(req.MessageIDs) {
		if _, ok := found[id]; !ok {
			return false
		}
	}
	return true
}

func (s *conversationStore) backfillStatusForChannelsLocked(channelIDs []string) map[string]string {
	channelIDs = normalizeChannelIDs(channelIDs)
	out := make(map[string]string, len(channelIDs))
	for _, channelID := range channelIDs {
		status := s.backfillStatus[channelID]
		if strings.TrimSpace(status) == "" {
			status = backfillStatusUnavailable
		}
		out[channelID] = normalizeBackfillStatus(status)
	}
	return out
}

func newHandler(store *conversationStore, cfgs ...handlerConfig) http.Handler {
	cfg := handlerConfig{}
	if len(cfgs) > 0 {
		cfg = cfgs[0]
	}
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
			Now:        store.currentTime(),
		})
		kind := contextKindFromRequest(r, result)
		_, _ = w.Write([]byte(formatTailContext(result, since, kind)))
	})
	mux.HandleFunc("/channel-awareness", func(w http.ResponseWriter, r *http.Request) {
		rawChannels := strings.TrimSpace(r.URL.Query().Get("channels"))
		if rawChannels == "" {
			http.Error(w, "channels is required", http.StatusBadRequest)
			return
		}
		channelIDs := strings.Split(rawChannels, ",")
		limit := defaultAwarenessLimit
		if rawLimit := strings.TrimSpace(r.URL.Query().Get("limit")); rawLimit != "" {
			parsed, err := strconv.Atoi(rawLimit)
			if err != nil || parsed < 1 {
				http.Error(w, "limit must be a positive integer", http.StatusBadRequest)
				return
			}
			limit = parsed
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
		digest := channelAwarenessDigest{}
		contextKind := channelAwarenessContextKindFromRequest(r)
		if contextKind == "raw_window+digest" {
			digest = cfg.channelMemory.fetchAwarenessDigest(r.Context(), channelIDs, since)
		}
		rawLimit := limit
		if contextKind == "raw_window+digest" && len(digest.Blocks) > 0 && defaultDigestRawLimit < rawLimit {
			rawLimit = defaultDigestRawLimit
		}
		result := store.tail(tailRequest{
			ChannelIDs: channelIDs,
			Since:      since,
			Limit:      rawLimit,
			MaxChars:   maxChars,
			Now:        store.currentTime(),
		})
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte(formatChannelAwareness(result, since, contextKind, digest)))
	})
	mux.HandleFunc("/search_channel_context", func(w http.ResponseWriter, r *http.Request) {
		if !authorizeToolRequest(w, r, cfg) {
			return
		}
		var req searchChannelContextRequest
		if !decodeJSONRequest(w, r, &req) {
			return
		}
		agentID := r.Header.Get("X-Claw-ID")
		req.Channels = effectiveToolChannels(req.Channels, agentID, cfg.agentChannels)
		if len(req.Channels) == 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "channels_required"})
			return
		}
		if disallowed := firstDisallowedToolChannel(req.Channels, agentID, cfg.agentChannels); disallowed != "" {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "channel_not_allowed", "agent": agentID, "channel": disallowed})
			return
		}
		result := store.search(req, store.currentTime())
		result = mergeChannelMemorySearch(r.Context(), result, cfg.channelMemory, req)
		writeJSON(w, http.StatusOK, result)
	})
	mux.HandleFunc("/get_channel_messages", func(w http.ResponseWriter, r *http.Request) {
		if !authorizeToolRequest(w, r, cfg) {
			return
		}
		var req getChannelMessagesRequest
		if !decodeJSONRequest(w, r, &req) {
			return
		}
		agentID := r.Header.Get("X-Claw-ID")
		req = normalizeGetChannelMessagesRequest(req)
		req.Channels = effectiveToolChannels(req.Channels, agentID, cfg.agentChannels)
		if len(req.Channels) == 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "channels_required"})
			return
		}
		if disallowed := firstDisallowedToolChannel(req.Channels, agentID, cfg.agentChannels); disallowed != "" {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "channel_not_allowed", "agent": agentID, "channel": disallowed})
			return
		}
		result := store.getMessages(req)
		result = mergeChannelMemoryMessages(r.Context(), result, cfg.channelMemory, req)
		writeJSON(w, http.StatusOK, result)
	})
	mux.HandleFunc("/channel-memory/replay", func(w http.ResponseWriter, r *http.Request) {
		if !authorizeToolRequest(w, r, cfg) {
			return
		}
		if !cfg.channelMemory.enabled() {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "channel_memory_disabled"})
			return
		}
		var req channelMemoryReplayRequest
		if !decodeJSONRequest(w, r, &req) {
			return
		}
		if len(normalizeChannelIDs(req.Channels)) == 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "channels_required"})
			return
		}
		if disallowed := firstDisallowedToolChannel(req.Channels, r.Header.Get("X-Claw-ID"), cfg.agentChannels); disallowed != "" {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "channel_not_allowed", "agent": r.Header.Get("X-Claw-ID"), "channel": disallowed})
			return
		}
		messages := store.snapshot(req.Channels, req.Limit, store.currentTime())
		pushed, err := cfg.channelMemory.ingestMessages(r.Context(), messages)
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]interface{}{"error": "channel_memory_ingest_failed", "pushed": pushed})
			return
		}
		writeJSON(w, http.StatusOK, channelMemoryReplayResponse{Status: "ok", Messages: len(messages), Pushed: pushed})
	})
	return mux
}

func authorizeToolRequest(w http.ResponseWriter, r *http.Request, cfg handlerConfig) bool {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method_not_allowed"})
		return false
	}
	auth := strings.TrimSpace(r.Header.Get("Authorization"))
	want := "Bearer " + strings.TrimSpace(cfg.toolToken)
	if cfg.toolToken == "" || auth != want {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthenticated"})
		return false
	}
	agentID := strings.TrimSpace(r.Header.Get("X-Claw-ID"))
	if agentID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing_agent_id"})
		return false
	}
	if _, ok := cfg.agentChannels[agentID]; !ok {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "unknown_agent"})
		return false
	}
	return true
}

func firstDisallowedToolChannel(channels []string, agentID string, allowlists map[string]map[string]struct{}) string {
	allowed := allowlists[strings.TrimSpace(agentID)]
	for _, channelID := range normalizeChannelIDs(channels) {
		if _, ok := allowed[channelID]; !ok {
			return channelID
		}
	}
	return ""
}

func effectiveToolChannels(requested []string, agentID string, allowlists map[string]map[string]struct{}) []string {
	channels := normalizeChannelIDs(requested)
	if len(channels) > 0 {
		return channels
	}
	allowed := allowlists[strings.TrimSpace(agentID)]
	if len(allowed) == 0 {
		return nil
	}
	channels = make([]string, 0, len(allowed))
	for channelID := range allowed {
		if strings.TrimSpace(channelID) != "" {
			channels = append(channels, channelID)
		}
	}
	sort.Strings(channels)
	return channels
}

func normalizeGetChannelMessagesRequest(req getChannelMessagesRequest) getChannelMessagesRequest {
	channels := append([]string(nil), req.Channels...)
	messageIDs := make([]string, 0, len(req.MessageIDs)+len(req.SourceHandles))
	for _, raw := range req.MessageIDs {
		if channelID, messageID, ok := splitSourceHandle(raw); ok {
			channels = append(channels, channelID)
			messageIDs = append(messageIDs, messageID)
			continue
		}
		messageIDs = append(messageIDs, raw)
	}
	for _, raw := range req.SourceHandles {
		if channelID, messageID, ok := splitSourceHandle(raw); ok {
			channels = append(channels, channelID)
			messageIDs = append(messageIDs, messageID)
		}
	}
	req.Channels = normalizeChannelIDs(channels)
	req.MessageIDs = trimNonEmptyStrings(messageIDs)
	return req
}

func splitSourceHandle(raw string) (string, string, bool) {
	left, right, ok := strings.Cut(strings.TrimSpace(raw), "/")
	left = strings.TrimSpace(left)
	right = strings.TrimSpace(right)
	if !ok || left == "" || right == "" {
		return "", "", false
	}
	return left, right, true
}

func containsString(values []string, want string) bool {
	want = strings.TrimSpace(want)
	for _, value := range values {
		if strings.TrimSpace(value) == want {
			return true
		}
	}
	return false
}

func decodeJSONRequest(w http.ResponseWriter, r *http.Request, out any) bool {
	defer r.Body.Close()
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "read_failed"})
		return false
	}
	if err := json.Unmarshal(body, out); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
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
	if handle := stableSourceHandle(msg); handle != "" {
		return fmt.Sprintf("[%s source=%s] %s: %s", formatWallTimestamp(msg.Timestamp), handle, msg.Author, msg.Content)
	}
	return fmt.Sprintf("[%s] %s: %s", formatWallTimestamp(msg.Timestamp), msg.Author, msg.Content)
}

func stableSourceHandle(msg wallMessage) string {
	channelID := strings.TrimSpace(msg.ChannelID)
	messageID := strings.TrimSpace(msg.ID)
	if channelID == "" || messageID == "" {
		return ""
	}
	return channelID + "/" + messageID
}

func contextKindFromRequest(r *http.Request, result tailResult) string {
	kind := strings.TrimSpace(r.URL.Query().Get("context_kind"))
	switch kind {
	case "delta_tail", "bootstrap_tail", "tail":
		return kind
	}
	if len(result.After) > 0 {
		return "delta_tail"
	}
	return "tail"
}

func formatTailContext(result tailResult, since time.Duration, kind string) string {
	var b strings.Builder
	switch kind {
	case "delta_tail":
		fmt.Fprintf(&b, "[channel-context delta] kind=delta_tail")
	case "bootstrap_tail":
		fmt.Fprintf(&b, "[channel-context bootstrap] kind=bootstrap_tail reason=epoch_changed")
	default:
		fmt.Fprintf(&b, "[channel-context] kind=tail")
	}
	fmt.Fprintf(&b, " mode=tail since=%s channels=%s messages=%d available=%d omitted=%d",
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
	fmt.Fprintf(&b, " backfill_status=%s", formatBackfillStatus(result.BackfillStatus))
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

func channelAwarenessContextKindFromRequest(r *http.Request) string {
	switch strings.TrimSpace(r.URL.Query().Get("context_kind")) {
	case "raw_window+digest", "raw_window digest":
		return "raw_window+digest"
	default:
		return "raw_window"
	}
}

func formatChannelAwareness(result tailResult, since time.Duration, contextKind string, digest channelAwarenessDigest) string {
	if contextKind != "raw_window+digest" {
		contextKind = "raw_window"
	}
	var b strings.Builder
	rawBody := formatWallMessages(result.Messages)
	digestBody := formatDigestBlocks(digest.Blocks)
	digestStatus := "unavailable"
	if digest.Requested {
		digestStatus = normalizeDigestStatus(digest.Status)
	}
	fmt.Fprintf(&b, "[channel-awareness] kind=%s since=%s channels=%s messages=%d available=%d omitted=%d retained=%d/since-%s digest=%s",
		contextKind,
		formatDurationForHeader(since),
		strings.Join(result.ChannelIDs, ","),
		len(result.Messages),
		result.Available,
		result.Omitted,
		result.Available,
		formatDurationForHeader(since),
		digestStatus,
	)
	fmt.Fprintf(&b, " raw_bytes=%d digest_bytes=%d", len(rawBody), len(digestBody))
	if contextKind == "raw_window+digest" {
		fmt.Fprintf(&b, " digest_blocks=%d digest_source_messages=%d coverage_gaps=%d deterministic_only=%t",
			len(digest.Blocks),
			digest.SourceMessages,
			digest.CoverageGaps,
			digest.DeterministicOnly,
		)
		if digest.RawRecent != "" {
			fmt.Fprintf(&b, " digest_raw_recent=%s digest_older_raw_omitted=%d",
				headerToken(digest.RawRecent),
				digest.OlderRawMessages,
			)
		}
		if digest.GeneratedAt != "" {
			fmt.Fprintf(&b, " digest_generated_at=%s", digest.GeneratedAt)
		}
	}
	if result.CapReason != "" {
		fmt.Fprintf(&b, " cap=%s", result.CapReason)
	}
	fmt.Fprintf(&b, " range=%s buffer_range=%s",
		formatTimeRange(messageRange(result.Messages)),
		formatTimeRange(result.BufferOldest, result.BufferNewest))
	fmt.Fprintf(&b, " backfill_status=%s", formatBackfillStatus(result.BackfillStatus))
	b.WriteString("\n")
	if result.Omitted > 0 {
		fmt.Fprintf(&b, "[omitted %d older retained messages due to %s; newest retained messages follow]\n",
			result.Omitted, result.CapReason)
	}
	if digestBody != "" {
		b.WriteByte('\n')
		b.WriteString("[digest]\n")
		b.WriteString(digestBody)
		b.WriteByte('\n')
	}
	if rawBody != "" {
		b.WriteByte('\n')
		b.WriteString("[raw recent]\n")
		b.WriteString(rawBody)
	}
	return b.String()
}

func formatDigestBlocks(blocks []channelMemoryDigestBlock) string {
	var b strings.Builder
	for _, block := range blocks {
		text := strings.TrimSpace(block.Text)
		if text == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		fmt.Fprintf(&b, "[digest kind=%s source_channel=%s source_messages=%s window=%s..%s sparse=%t score=%.2f processor=%s] %s",
			headerToken(block.Kind),
			headerToken(block.SourceChannel),
			headerToken(strings.Join(trimNonEmptyStrings(block.SourceMessages), ",")),
			headerToken(block.SourceWindow.From),
			headerToken(block.SourceWindow.To),
			block.Sparse,
			block.Score,
			headerToken(block.Processor),
			text,
		)
	}
	return b.String()
}

func trimNonEmptyStrings(values []string) []string {
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func headerToken(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "none"
	}
	replacer := strings.NewReplacer(" ", "_", "\t", "_", "\n", "_", "\r", "_")
	return replacer.Replace(value)
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

func formatBackfillStatus(statuses map[string]string) string {
	if len(statuses) == 0 {
		return backfillStatusUnavailable
	}
	keys := make([]string, 0, len(statuses))
	var first string
	allSame := true
	for channelID, status := range statuses {
		channelID = strings.TrimSpace(channelID)
		if channelID == "" {
			continue
		}
		status = normalizeBackfillStatus(status)
		if first == "" {
			first = status
		} else if status != first {
			allSame = false
		}
		keys = append(keys, channelID)
	}
	if len(keys) == 0 {
		return backfillStatusUnavailable
	}
	if allSame {
		return first
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, channelID := range keys {
		parts = append(parts, channelID+":"+normalizeBackfillStatus(statuses[channelID]))
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

func formatOptionalHeaderTimestamp(ts time.Time) string {
	if ts.IsZero() {
		return ""
	}
	return formatHeaderTimestamp(ts)
}

func messageAfterBoundary(msg wallMessage, raw string) bool {
	if boundary, ok := parseMessageBoundaryTime(raw); ok {
		return !msg.Timestamp.IsZero() && msg.Timestamp.After(boundary)
	}
	return compareSnowflakes(msg.ID, raw) > 0
}

func messageBeforeBoundary(msg wallMessage, raw string) bool {
	if boundary, ok := parseMessageBoundaryTime(raw); ok {
		return !msg.Timestamp.IsZero() && msg.Timestamp.Before(boundary)
	}
	return compareSnowflakes(msg.ID, raw) < 0
}

func parseMessageBoundaryTime(raw string) (time.Time, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04Z", "2006-01-02 15:04"} {
		ts, err := time.Parse(layout, raw)
		if err == nil {
			return ts, true
		}
	}
	return time.Time{}, false
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
