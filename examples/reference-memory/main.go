package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
)

const (
	defaultDataDir    = "/data/reference-memory"
	maxRecallResults  = 4
	maxSummaryRunes   = 280
	maxSearchTextSize = 8 * 1024
)

var tokenPattern = regexp.MustCompile(`[a-z0-9][a-z0-9_-]{2,}`)

type memoryRecallRequest struct {
	AgentID  string         `json:"agent_id"`
	Messages any            `json:"messages,omitempty"`
	System   any            `json:"system,omitempty"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

type memoryRetainRequest struct {
	AgentID  string         `json:"agent_id"`
	Pod      string         `json:"pod,omitempty"`
	Metadata map[string]any `json:"metadata,omitempty"`
	Entry    retainedEntry  `json:"entry"`
}

type memoryForgetRequest struct {
	AgentID  string         `json:"agent_id"`
	Pod      string         `json:"pod,omitempty"`
	Metadata map[string]any `json:"metadata,omitempty"`
	EntryIDs []string       `json:"entry_ids"`
	Reason   string         `json:"reason,omitempty"`
}

type retainedEntry struct {
	ID               string          `json:"id"`
	TS               string          `json:"ts,omitempty"`
	RequestedModel   string          `json:"requested_model,omitempty"`
	RequestOriginal  json.RawMessage `json:"request_original,omitempty"`
	RequestEffective json.RawMessage `json:"request_effective,omitempty"`
	Response         retainedPayload `json:"response"`
}

type retainedPayload struct {
	Format string          `json:"format"`
	JSON   json.RawMessage `json:"json,omitempty"`
	Text   string          `json:"text,omitempty"`
}

type memoryRecallResponse struct {
	Memories []memoryBlock `json:"memories"`
}

type memoryBlock struct {
	Text   string  `json:"text"`
	Score  float64 `json:"score,omitempty"`
	Kind   string  `json:"kind,omitempty"`
	Source string  `json:"source,omitempty"`
	TS     string  `json:"ts,omitempty"`
}

type storedMemory struct {
	AgentID        string `json:"agent_id"`
	EntryID        string `json:"entry_id"`
	TS             string `json:"ts,omitempty"`
	RequestedModel string `json:"requested_model,omitempty"`
	Summary        string `json:"summary"`
	SearchText     string `json:"search_text"`
}

type memoryTombstone struct {
	AgentID     string `json:"agent_id"`
	EntryID     string `json:"entry_id"`
	Reason      string `json:"reason,omitempty"`
	ForgottenAt string `json:"forgotten_at"`
}

type memoryStore struct {
	mu     sync.Mutex
	dir    string
	agents map[string]*agentMemories
}

type agentMemories struct {
	entries     map[string]storedMemory
	order       []string
	tombstones  map[string]memoryTombstone
	tombstoned  map[string]struct{}
}

type scoredMemory struct {
	record storedMemory
	score  int
	index  int
}

func main() {
	dataDir := strings.TrimSpace(os.Getenv("MEMORY_REF_DIR"))
	if dataDir == "" {
		dataDir = defaultDataDir
	}

	store, err := loadMemoryStore(dataDir)
	if err != nil {
		log.Fatalf("load memory store: %v", err)
	}

	addr := strings.TrimSpace(os.Getenv("PORT"))
	if addr == "" {
		addr = "8080"
	}
	if !strings.Contains(addr, ":") {
		addr = ":" + addr
	}

	log.Printf("reference-memory listening on %s with data dir %s", addr, dataDir)
	if err := http.ListenAndServe(addr, newHandler(store)); err != nil {
		log.Fatalf("listen: %v", err)
	}
}

func newHandler(store *memoryStore) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/recall", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var req memoryRecallRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, fmt.Sprintf("decode recall request: %v", err), http.StatusBadRequest)
			return
		}

		memories, err := store.recall(req)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(memoryRecallResponse{Memories: memories})
	})

	mux.HandleFunc("/retain", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var req memoryRetainRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, fmt.Sprintf("decode retain request: %v", err), http.StatusBadRequest)
			return
		}
		if err := store.retain(req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusAccepted)
	})

	mux.HandleFunc("/forget", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var req memoryForgetRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, fmt.Sprintf("decode forget request: %v", err), http.StatusBadRequest)
			return
		}
		if err := store.forget(req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	return mux
}

func loadMemoryStore(dir string) (*memoryStore, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, errors.New("memory store dir is required")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}

	store := &memoryStore{
		dir:    dir,
		agents: make(map[string]*agentMemories),
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		agentID, err := url.PathUnescape(entry.Name())
		if err != nil {
			return nil, fmt.Errorf("decode agent dir %q: %w", entry.Name(), err)
		}
		agent, err := loadAgentMemories(filepath.Join(dir, entry.Name()))
		if err != nil {
			return nil, fmt.Errorf("load agent %q: %w", agentID, err)
		}
		store.agents[agentID] = agent
	}

	return store, nil
}

func loadAgentMemories(dir string) (*agentMemories, error) {
	agent := &agentMemories{
		entries:    make(map[string]storedMemory),
		tombstones: make(map[string]memoryTombstone),
		tombstoned: make(map[string]struct{}),
	}

	if err := scanJSONL(filepath.Join(dir, "entries.jsonl"), func(line []byte) error {
		var record storedMemory
		if err := json.Unmarshal(line, &record); err != nil {
			return err
		}
		if record.EntryID == "" {
			return nil
		}
		if _, exists := agent.entries[record.EntryID]; exists {
			return nil
		}
		agent.entries[record.EntryID] = record
		agent.order = append(agent.order, record.EntryID)
		return nil
	}); err != nil {
		return nil, err
	}

	if err := scanJSONL(filepath.Join(dir, "tombstones.jsonl"), func(line []byte) error {
		var tombstone memoryTombstone
		if err := json.Unmarshal(line, &tombstone); err != nil {
			return err
		}
		if tombstone.EntryID == "" {
			return nil
		}
		agent.tombstones[tombstone.EntryID] = tombstone
		agent.tombstoned[tombstone.EntryID] = struct{}{}
		return nil
	}); err != nil {
		return nil, err
	}

	if len(agent.tombstoned) > 0 {
		filtered := agent.order[:0]
		for _, entryID := range agent.order {
			if _, forgotten := agent.tombstoned[entryID]; forgotten {
				delete(agent.entries, entryID)
				continue
			}
			filtered = append(filtered, entryID)
		}
		agent.order = filtered
	}

	return agent, nil
}

func scanJSONL(path string, fn func([]byte) error) error {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if err := fn([]byte(line)); err != nil {
			return err
		}
	}
	return scanner.Err()
}

func (s *memoryStore) retain(req memoryRetainRequest) error {
	agentID := strings.TrimSpace(req.AgentID)
	entryID := strings.TrimSpace(req.Entry.ID)
	if agentID == "" {
		return errors.New("agent_id is required")
	}
	if entryID == "" {
		return errors.New("entry.id is required")
	}

	record := buildStoredMemory(agentID, req.Entry)

	s.mu.Lock()
	defer s.mu.Unlock()

	agent := s.ensureAgent(agentID)
	if _, forgotten := agent.tombstoned[entryID]; forgotten {
		return nil
	}
	if _, exists := agent.entries[entryID]; exists {
		return nil
	}

	agent.entries[entryID] = record
	agent.order = append(agent.order, entryID)
	if err := appendJSONL(s.entriesPath(agentID), record); err != nil {
		delete(agent.entries, entryID)
		agent.order = agent.order[:len(agent.order)-1]
		return err
	}
	return nil
}

func (s *memoryStore) recall(req memoryRecallRequest) ([]memoryBlock, error) {
	agentID := strings.TrimSpace(req.AgentID)
	if agentID == "" {
		return nil, errors.New("agent_id is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	agent, ok := s.agents[agentID]
	if !ok || len(agent.order) == 0 {
		return nil, nil
	}

	queryText := normalizeSpace(strings.Join([]string{
		flattenStructuredText(req.Messages),
		flattenStructuredText(req.System),
	}, " "))
	queryTokens := tokenize(queryText)

	scored := make([]scoredMemory, 0, len(agent.order))
	for i := len(agent.order) - 1; i >= 0; i-- {
		record, ok := agent.entries[agent.order[i]]
		if !ok {
			continue
		}
		score := matchScore(queryTokens, record.SearchText)
		scored = append(scored, scoredMemory{record: record, score: score, index: i})
	}
	if len(scored) == 0 {
		return nil, nil
	}

	if len(queryTokens) > 0 {
		slices.SortFunc(scored, func(a, b scoredMemory) int {
			if a.score != b.score {
				return b.score - a.score
			}
			return b.index - a.index
		})
	}

	memories := make([]memoryBlock, 0, maxRecallResults)
	for _, candidate := range scored {
		if candidate.score == 0 && len(queryTokens) > 0 && len(memories) > 0 {
			break
		}
		if candidate.score == 0 && len(queryTokens) == 0 && len(memories) >= maxRecallResults {
			break
		}
		if candidate.record.Summary == "" {
			continue
		}
		memories = append(memories, memoryBlock{
			Text:   candidate.record.Summary,
			Score:  recallScore(candidate.score),
			Kind:   "reference_memory",
			Source: "reference-memory",
			TS:     candidate.record.TS,
		})
		if len(memories) >= maxRecallResults {
			break
		}
	}
	return memories, nil
}

func (s *memoryStore) forget(req memoryForgetRequest) error {
	agentID := strings.TrimSpace(req.AgentID)
	if agentID == "" {
		return errors.New("agent_id is required")
	}
	if len(req.EntryIDs) == 0 {
		return errors.New("entry_ids is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	agent := s.ensureAgent(agentID)
	for _, entryID := range req.EntryIDs {
		entryID = strings.TrimSpace(entryID)
		if entryID == "" {
			continue
		}
		if _, exists := agent.tombstoned[entryID]; exists {
			delete(agent.entries, entryID)
			continue
		}

		tombstone := memoryTombstone{
			AgentID:     agentID,
			EntryID:     entryID,
			Reason:      strings.TrimSpace(req.Reason),
			ForgottenAt: time.Now().UTC().Format(time.RFC3339),
		}
		agent.tombstones[entryID] = tombstone
		agent.tombstoned[entryID] = struct{}{}
		delete(agent.entries, entryID)
		if err := appendJSONL(s.tombstonesPath(agentID), tombstone); err != nil {
			return err
		}
	}

	filtered := agent.order[:0]
	for _, entryID := range agent.order {
		if _, forgotten := agent.tombstoned[entryID]; forgotten {
			continue
		}
		filtered = append(filtered, entryID)
	}
	agent.order = filtered
	return nil
}

func (s *memoryStore) ensureAgent(agentID string) *agentMemories {
	agent, ok := s.agents[agentID]
	if ok {
		return agent
	}
	agent = &agentMemories{
		entries:    make(map[string]storedMemory),
		tombstones: make(map[string]memoryTombstone),
		tombstoned: make(map[string]struct{}),
	}
	s.agents[agentID] = agent
	return agent
}

func (s *memoryStore) entriesPath(agentID string) string {
	return filepath.Join(s.agentDir(agentID), "entries.jsonl")
}

func (s *memoryStore) tombstonesPath(agentID string) string {
	return filepath.Join(s.agentDir(agentID), "tombstones.jsonl")
}

func (s *memoryStore) agentDir(agentID string) string {
	return filepath.Join(s.dir, url.PathEscape(agentID))
}

func appendJSONL(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()

	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	_, err = f.Write(data)
	return err
}

func buildStoredMemory(agentID string, entry retainedEntry) storedMemory {
	responseText := extractEntryResponseText(entry)
	requestText := extractEntryRequestText(entry)
	summary := firstNonEmpty(responseText, requestText, "retained session history entry")
	summary = truncateRunes(summary, maxSummaryRunes)

	searchText := normalizeSpace(strings.ToLower(strings.Join([]string{responseText, requestText}, " ")))
	if len(searchText) > maxSearchTextSize {
		searchText = searchText[:maxSearchTextSize]
	}

	return storedMemory{
		AgentID:        agentID,
		EntryID:        strings.TrimSpace(entry.ID),
		TS:             strings.TrimSpace(entry.TS),
		RequestedModel: strings.TrimSpace(entry.RequestedModel),
		Summary:        summary,
		SearchText:     searchText,
	}
}

func extractEntryResponseText(entry retainedEntry) string {
	switch strings.ToLower(strings.TrimSpace(entry.Response.Format)) {
	case "sse":
		return normalizeSpace(entry.Response.Text)
	case "json":
		return flattenJSONText(entry.Response.JSON)
	default:
		return ""
	}
}

func extractEntryRequestText(entry retainedEntry) string {
	if text := flattenJSONText(entry.RequestEffective); text != "" {
		return text
	}
	return flattenJSONText(entry.RequestOriginal)
}

func flattenJSONText(raw json.RawMessage) string {
	if len(strings.TrimSpace(string(raw))) == 0 {
		return ""
	}

	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return ""
	}
	return flattenStructuredText(value)
}

func flattenStructuredText(value any) string {
	parts := make([]string, 0, 8)
	collectText(value, "", &parts)
	return normalizeSpace(strings.Join(parts, " "))
}

func collectText(value any, key string, out *[]string) {
	switch typed := value.(type) {
	case map[string]any:
		for childKey, child := range typed {
			collectText(child, strings.ToLower(childKey), out)
		}
	case []any:
		for _, child := range typed {
			collectText(child, key, out)
		}
	case string:
		if !isTextField(key) {
			return
		}
		text := normalizeSpace(typed)
		if text != "" {
			*out = append(*out, text)
		}
	}
}

func isTextField(key string) bool {
	switch key {
	case "content", "text", "output_text":
		return true
	default:
		return false
	}
}

func normalizeSpace(text string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(text)), " ")
}

func truncateRunes(text string, limit int) string {
	if limit <= 0 {
		return ""
	}
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit]) + "..."
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func tokenize(text string) []string {
	if text == "" {
		return nil
	}
	seen := make(map[string]struct{})
	tokens := make([]string, 0, 16)
	for _, token := range tokenPattern.FindAllString(strings.ToLower(text), -1) {
		if _, ok := seen[token]; ok {
			continue
		}
		seen[token] = struct{}{}
		tokens = append(tokens, token)
	}
	return tokens
}

func matchScore(tokens []string, haystack string) int {
	if len(tokens) == 0 || haystack == "" {
		return 0
	}
	score := 0
	for _, token := range tokens {
		if strings.Contains(haystack, token) {
			score++
		}
	}
	return score
}

func recallScore(score int) float64 {
	if score <= 0 {
		return 0.1
	}
	return float64(score)
}

func postJSON(url string, body any) (*http.Response, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	resp, err := http.Post(url, "application/json", strings.NewReader(string(data)))
	if err != nil {
		return nil, err
	}
	io.Copy(io.Discard, resp.Body)
	return resp, nil
}
