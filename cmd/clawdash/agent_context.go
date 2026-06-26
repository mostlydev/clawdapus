package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

const maxAgentContextResponseBytes = 8 * 1024 * 1024

type agentContextSource interface {
	List(ctx context.Context) ([]agentContextIndexEntry, error)
	Contract(ctx context.Context, agentID string) (agentContractView, error)
	LiveContext(ctx context.Context, agentID string) (any, error)
}

type agentContextIndexEntry struct {
	ClawID         string `json:"claw_id"`
	Service        string `json:"service,omitempty"`
	ClawType       string `json:"claw_type,omitempty"`
	HasLiveContext bool   `json:"has_live_context"`

	DetailPath string
	LiveLabel  string
	LiveTone   string
}

type agentContractView struct {
	ClawID           string         `json:"claw_id"`
	AgentsMD         string         `json:"agents_md"`
	ClawdapusMD      string         `json:"clawdapus_md"`
	Metadata         any            `json:"metadata"`
	Feeds            any            `json:"feeds"`
	Tools            any            `json:"tools"`
	Memory           any            `json:"memory"`
	RuntimeReminders any            `json:"runtime_reminders"`
	ServiceAuth      map[string]any `json:"service_auth,omitempty"`
}

type agentContextHTTPClient struct {
	baseURL string
	token   string
	client  *http.Client
}

type agentContextListResponse struct {
	Agents []agentContextIndexEntry `json:"agents"`
}

type clawAPIError struct {
	StatusCode int
	Status     string
	Message    string
}

func (e *clawAPIError) Error() string {
	if e == nil {
		return ""
	}
	if strings.TrimSpace(e.Message) != "" {
		return e.Message
	}
	return e.Status
}

func newAgentContextHTTPClient(baseURL, token string) agentContextSource {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	token = strings.TrimSpace(token)
	if baseURL == "" || token == "" {
		return nil
	}
	return &agentContextHTTPClient{
		baseURL: baseURL,
		token:   token,
		client: &http.Client{
			Timeout: 4 * time.Second,
		},
	}
}

func (c *agentContextHTTPClient) List(ctx context.Context) ([]agentContextIndexEntry, error) {
	var payload agentContextListResponse
	if err := c.do(ctx, http.MethodGet, "/agents", &payload); err != nil {
		return nil, err
	}
	decorateAgentContextIndex(payload.Agents)
	return payload.Agents, nil
}

func (c *agentContextHTTPClient) Contract(ctx context.Context, agentID string) (agentContractView, error) {
	var payload agentContractView
	if err := c.do(ctx, http.MethodGet, agentContextAPIPath(agentID, "contract"), &payload); err != nil {
		return agentContractView{}, err
	}
	return payload, nil
}

func (c *agentContextHTTPClient) LiveContext(ctx context.Context, agentID string) (any, error) {
	var payload any
	if err := c.do(ctx, http.MethodGet, agentContextAPIPath(agentID, "context"), &payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func (c *agentContextHTTPClient) do(ctx context.Context, method, path string, dst any) error {
	if c == nil {
		return fmt.Errorf("agent context client unavailable")
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode/100 != 2 {
		return decodeClawAPIError(resp)
	}
	if dst == nil {
		return nil
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxAgentContextResponseBytes)).Decode(dst); err != nil {
		return fmt.Errorf("decode agent context response: %w", err)
	}
	return nil
}

func agentContextAPIPath(agentID, action string) string {
	return "/agents/" + url.PathEscape(strings.TrimSpace(agentID)) + "/" + strings.TrimSpace(action)
}

func decodeClawAPIError(resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var payload struct {
		Error string `json:"error"`
	}
	message := ""
	if err := json.Unmarshal(body, &payload); err == nil {
		message = strings.TrimSpace(payload.Error)
	}
	if message == "" {
		message = strings.TrimSpace(string(body))
	}
	return &clawAPIError{
		StatusCode: resp.StatusCode,
		Status:     resp.Status,
		Message:    message,
	}
}

type agentsPageData struct {
	PodName         string
	ActiveTab       string
	HasSchedule     bool
	HasAgentContext bool
	Summary         []dashStat
	Agents          []agentContextIndexEntry
	HasAgents       bool
	Error           string
	HasError        bool
}

type agentContextDetailPageData struct {
	PodName                string
	ActiveTab              string
	HasSchedule            bool
	HasAgentContext        bool
	ContextTab             string
	IsContractTab          bool
	IsLiveTab              bool
	ContractPath           string
	LivePath               string
	ClawID                 string
	Service                string
	ClawType               string
	Contract               agentContractView
	HasContract            bool
	LiveContext            agentLiveContextView
	LiveContextJSON        string
	HasLiveContext         bool
	ContractError          string
	LiveError              string
	HasContractErr         bool
	HasLiveError           bool
	MetadataJSON           string
	FeedsJSON              string
	ToolsJSON              string
	MemoryJSON             string
	RuntimeReminderJSON    string
	ServiceAuthJSON        string
	HasMetadata            bool
	HasFeeds               bool
	HasTools               bool
	HasMemory              bool
	HasRuntimeReminder     bool
	HasServiceAuth         bool
	MetadataRows           []keyValueRow
	FeedRows               []feedManifestRow
	ToolRows               []toolManifestRow
	MemoryRows             []keyValueRow
	RuntimeReminderRows    []runtimeReminderRow
	ServiceAuthRows        []serviceAuthRow
	HasMetadataRows        bool
	HasFeedRows            bool
	HasToolRows            bool
	HasMemoryRows          bool
	HasRuntimeReminderRows bool
	HasServiceAuthRows     bool
}

type keyValueRow struct {
	Key   string
	Value string
}

type feedManifestRow struct {
	Name        string
	Source      string
	Path        string
	URL         string
	TTL         string
	Description string
}

type toolManifestRow struct {
	Name        string
	Description string
	Service     string
	Method      string
	Path        string
	Transport   string
	SchemaJSON  string
	HasSchema   bool
}

type serviceAuthRow struct {
	Service    string
	AuthType   string
	Principal  string
	DetailJSON string
}

type runtimeReminderRow struct {
	ID        string
	Enabled   string
	Cadence   string
	Placement string
	MaxChars  string
	Text      string
}

type candidateContextRow struct {
	Provider      string
	UpstreamModel string
}

type contextPlacementRow struct {
	Order       int
	Kind        string
	Label       string
	Carrier     string
	Position    string
	Occurrences int
	Scope       string
	Relation    string
}

type contextCaptureRow struct {
	Sequence       int
	CapturedAt     string
	Interval       string
	Format         string
	Model          string
	DynamicInputs  int
	FeedBlocks     int
	MemoryRecall   bool
	TimeContext    bool
	PlacementCount int
	TurnCount      int
	ManagedTool    bool
}

type agentLiveContextView struct {
	CapturedAt        string
	Format            string
	RequestedModel    string
	ChosenRef         string
	Candidates        []candidateContextRow
	Placements        []contextPlacementRow
	RecentCaptures    []contextCaptureRow
	FeedBlocks        []string
	MemoryRecall      string
	TimeContext       string
	Intervention      string
	ManagedTool       bool
	TurnCount         int
	SystemText        string
	SystemJSON        string
	ToolsJSON         string
	RawJSON           string
	HasCandidates     bool
	HasFeedBlocks     bool
	HasMemoryRecall   bool
	HasTimeContext    bool
	HasIntervention   bool
	HasSystem         bool
	HasSystemText     bool
	HasSystemJSON     bool
	HasTools          bool
	HasPlacements     bool
	HasRecentCaptures bool
}

func (h *handler) renderAgents(w http.ResponseWriter, r *http.Request) {
	if !h.hasAgentContext() {
		http.NotFound(w, r)
		return
	}
	agents, err := h.agentContextSource.List(r.Context())
	data := buildAgentsPageData(
		h.manifest.PodName,
		agents,
		errString(err),
		h.hasSchedule(),
		h.hasAgentContext(),
	)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = h.tpl.ExecuteTemplate(w, "agents.html", data)
}

func (h *handler) renderAgentContextDetail(w http.ResponseWriter, r *http.Request) {
	if !h.hasAgentContext() {
		http.NotFound(w, r)
		return
	}
	agentID, ok := parseAgentDashboardPath(r.URL.Path)
	if !ok {
		http.NotFound(w, r)
		return
	}
	tab := normalizeAgentContextTab(r.URL.Query().Get("tab"))

	contract, contractErr := h.agentContextSource.Contract(r.Context(), agentID)
	var apiErr *clawAPIError
	if errors.As(contractErr, &apiErr) && apiErr.StatusCode == http.StatusNotFound {
		http.NotFound(w, r)
		return
	}
	var liveContext any
	var liveErr error
	if tab == "live" {
		liveContext, liveErr = h.agentContextSource.LiveContext(r.Context(), agentID)
		if errors.As(liveErr, &apiErr) && apiErr.StatusCode == http.StatusNotFound {
			liveErr = nil
		}
	}
	data := buildAgentContextDetailPageData(
		h.manifest.PodName,
		agentID,
		tab,
		contract,
		contractErr,
		liveContext,
		liveErr,
		h.hasSchedule(),
		h.hasAgentContext(),
	)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = h.tpl.ExecuteTemplate(w, "agent_detail.html", data)
}

func buildAgentsPageData(podName string, agents []agentContextIndexEntry, errMsg string, hasSchedule, hasAgentContext bool) agentsPageData {
	agents = append([]agentContextIndexEntry(nil), agents...)
	decorateAgentContextIndex(agents)
	sort.Slice(agents, func(i, j int) bool {
		if agents[i].Service != agents[j].Service {
			return agents[i].Service < agents[j].Service
		}
		return agents[i].ClawID < agents[j].ClawID
	})

	live := 0
	serviceSet := map[string]struct{}{}
	typeSet := map[string]struct{}{}
	for _, agent := range agents {
		if agent.HasLiveContext {
			live++
		}
		if strings.TrimSpace(agent.Service) != "" {
			serviceSet[agent.Service] = struct{}{}
		}
		if strings.TrimSpace(agent.ClawType) != "" {
			typeSet[agent.ClawType] = struct{}{}
		}
	}

	return agentsPageData{
		PodName:         podName,
		ActiveTab:       "agents",
		HasSchedule:     hasSchedule,
		HasAgentContext: hasAgentContext,
		Summary: []dashStat{
			{Label: "Agents", Value: fmt.Sprintf("%d", len(agents)), Hint: "context directories in scope", Tone: "neutral"},
			{Label: "Live", Value: fmt.Sprintf("%d", live), Hint: "snapshots captured by cllama", Tone: toneForLiveContexts(live)},
			{Label: "Services", Value: fmt.Sprintf("%d", len(serviceSet)), Hint: "backing compose services", Tone: "neutral"},
			{Label: "Runtimes", Value: fmt.Sprintf("%d", len(typeSet)), Hint: "distinct claw types", Tone: "neutral"},
		},
		Agents:    agents,
		HasAgents: len(agents) > 0,
		Error:     errMsg,
		HasError:  strings.TrimSpace(errMsg) != "",
	}
}

func toneForLiveContexts(count int) string {
	if count <= 0 {
		return "neutral"
	}
	return "good"
}

func buildAgentContextDetailPageData(podName, agentID, tab string, contract agentContractView, contractErr error, liveContext any, liveErr error, hasSchedule, hasAgentContext bool) agentContextDetailPageData {
	if strings.TrimSpace(contract.ClawID) == "" {
		contract.ClawID = agentID
	}
	tab = normalizeAgentContextTab(tab)
	clawID := firstNonEmpty(contract.ClawID, agentID)
	liveView := buildAgentLiveContextView(liveContext)
	metadataRows := topLevelRows(contract.Metadata)
	feedRows := feedManifestRows(contract.Feeds)
	toolRows := toolManifestRows(contract.Tools)
	memoryRows := topLevelRows(contract.Memory)
	runtimeReminderRows := runtimeReminderRows(contract.RuntimeReminders)
	serviceAuthRows := serviceAuthRows(contract.ServiceAuth)
	data := agentContextDetailPageData{
		PodName:             podName,
		ActiveTab:           "agents",
		HasSchedule:         hasSchedule,
		HasAgentContext:     hasAgentContext,
		ContextTab:          tab,
		IsContractTab:       tab == "contract",
		IsLiveTab:           tab == "live",
		ContractPath:        "/agents/" + url.PathEscape(clawID) + "?tab=contract",
		LivePath:            "/agents/" + url.PathEscape(clawID) + "?tab=live",
		ClawID:              clawID,
		Service:             metadataString(contract.Metadata, "service"),
		ClawType:            metadataString(contract.Metadata, "type"),
		Contract:            contract,
		HasContract:         contractErr == nil,
		LiveContext:         liveView,
		ContractError:       errString(contractErr),
		LiveError:           errString(liveErr),
		HasContractErr:      contractErr != nil,
		HasLiveError:        tab == "live" && liveErr != nil,
		MetadataJSON:        prettyJSON(contract.Metadata),
		FeedsJSON:           prettyJSON(contract.Feeds),
		ToolsJSON:           prettyJSON(contract.Tools),
		MemoryJSON:          prettyJSON(contract.Memory),
		RuntimeReminderJSON: prettyJSON(contract.RuntimeReminders),
		ServiceAuthJSON:     prettyJSON(contract.ServiceAuth),
		LiveContextJSON:     liveView.RawJSON,
		MetadataRows:        metadataRows,
		FeedRows:            feedRows,
		ToolRows:            toolRows,
		MemoryRows:          memoryRows,
		RuntimeReminderRows: runtimeReminderRows,
		ServiceAuthRows:     serviceAuthRows,
	}
	data.HasMetadata = data.MetadataJSON != ""
	data.HasFeeds = data.FeedsJSON != ""
	data.HasTools = data.ToolsJSON != ""
	data.HasMemory = data.MemoryJSON != ""
	data.HasRuntimeReminder = data.RuntimeReminderJSON != ""
	data.HasServiceAuth = data.ServiceAuthJSON != ""
	data.HasLiveContext = data.LiveContextJSON != ""
	data.HasMetadataRows = len(metadataRows) > 0
	data.HasFeedRows = len(feedRows) > 0
	data.HasToolRows = len(toolRows) > 0
	data.HasMemoryRows = len(memoryRows) > 0
	data.HasRuntimeReminderRows = len(runtimeReminderRows) > 0
	data.HasServiceAuthRows = len(serviceAuthRows) > 0
	return data
}

func normalizeAgentContextTab(tab string) string {
	switch strings.ToLower(strings.TrimSpace(tab)) {
	case "live":
		return "live"
	default:
		return "contract"
	}
}

func decorateAgentContextIndex(agents []agentContextIndexEntry) {
	for i := range agents {
		agents[i].ClawID = strings.TrimSpace(agents[i].ClawID)
		agents[i].Service = strings.TrimSpace(agents[i].Service)
		agents[i].ClawType = strings.TrimSpace(agents[i].ClawType)
		agents[i].DetailPath = "/agents/" + url.PathEscape(agents[i].ClawID)
		if agents[i].HasLiveContext {
			agents[i].LiveLabel = "live snapshot"
			agents[i].LiveTone = "tone-good"
		} else {
			agents[i].LiveLabel = "contract only"
			agents[i].LiveTone = "tone-neutral"
		}
	}
}

func parseAgentDashboardPath(path string) (string, bool) {
	rest := strings.TrimPrefix(path, "/agents/")
	if rest == path || strings.TrimSpace(rest) == "" || strings.Contains(rest, "/") {
		return "", false
	}
	agentID, err := url.PathUnescape(rest)
	if err != nil || strings.TrimSpace(agentID) == "" {
		return "", false
	}
	return strings.TrimSpace(agentID), true
}

func metadataString(metadata any, key string) string {
	values, ok := metadata.(map[string]any)
	if !ok {
		return ""
	}
	return scalarString(values[key])
}

func scalarString(v any) string {
	switch typed := v.(type) {
	case string:
		return strings.TrimSpace(typed)
	case json.Number:
		return typed.String()
	case float64:
		return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.6f", typed), "0"), ".")
	case bool:
		if typed {
			return "true"
		}
		return "false"
	default:
		return ""
	}
}

func scalarBool(v any) bool {
	b, _ := v.(bool)
	return b
}

func scalarInt(v any) int {
	switch typed := v.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	case json.Number:
		n, _ := typed.Int64()
		return int(n)
	default:
		return 0
	}
}

func runtimeReminderRows(reminders any) []runtimeReminderRow {
	root, ok := reminders.(map[string]any)
	if !ok {
		return nil
	}
	rawList, ok := root["reminders"].([]any)
	if !ok {
		return nil
	}
	rows := make([]runtimeReminderRow, 0, len(rawList))
	for _, item := range rawList {
		entry, ok := item.(map[string]any)
		if !ok {
			continue
		}
		rows = append(rows, runtimeReminderRow{
			ID:        scalarString(entry["id"]),
			Enabled:   scalarString(entry["enabled"]),
			Cadence:   scalarString(entry["cadence"]),
			Placement: scalarString(entry["placement"]),
			MaxChars:  scalarString(entry["max_chars"]),
			Text:      scalarString(entry["text"]),
		})
	}
	return rows
}

func displayValue(v any) string {
	if s := scalarString(v); s != "" {
		return s
	}
	if !hasJSONValue(v) {
		return ""
	}
	return prettyJSON(v)
}

func topLevelRows(v any) []keyValueRow {
	m, ok := v.(map[string]any)
	if !ok || len(m) == 0 {
		return nil
	}
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	rows := make([]keyValueRow, 0, len(keys))
	for _, key := range keys {
		value := displayValue(m[key])
		if strings.TrimSpace(value) == "" {
			continue
		}
		rows = append(rows, keyValueRow{Key: humanizeKey(key), Value: value})
	}
	return rows
}

func humanizeKey(key string) string {
	key = strings.TrimSpace(key)
	if key == "" {
		return ""
	}
	words := strings.Fields(strings.ReplaceAll(key, "_", " "))
	for i, word := range words {
		switch strings.ToLower(word) {
		case "id":
			words[i] = "ID"
		case "url":
			words[i] = "URL"
		case "api":
			words[i] = "API"
		case "ttl":
			words[i] = "TTL"
		default:
			if len(word) > 0 {
				words[i] = strings.ToUpper(word[:1]) + word[1:]
			}
		}
	}
	return strings.Join(words, " ")
}

func asManifestEntries(v any, field string) []any {
	switch typed := v.(type) {
	case []any:
		return typed
	case map[string]any:
		if raw, ok := typed[field].([]any); ok {
			return raw
		}
	}
	return nil
}

func feedManifestRows(v any) []feedManifestRow {
	entries := asManifestEntries(v, "feeds")
	rows := make([]feedManifestRow, 0, len(entries))
	for _, raw := range entries {
		entry, _ := raw.(map[string]any)
		if entry == nil {
			continue
		}
		rows = append(rows, feedManifestRow{
			Name:        firstNonEmpty(scalarString(entry["name"]), "-"),
			Source:      firstNonEmpty(scalarString(entry["source"]), "-"),
			Path:        firstNonEmpty(scalarString(entry["path"]), "-"),
			URL:         firstNonEmpty(scalarString(entry["url"]), "-"),
			TTL:         firstNonEmpty(scalarString(entry["ttl"]), "-"),
			Description: scalarString(entry["description"]),
		})
	}
	return rows
}

func toolManifestRows(v any) []toolManifestRow {
	entries := asManifestEntries(v, "tools")
	rows := make([]toolManifestRow, 0, len(entries))
	for _, raw := range entries {
		entry, _ := raw.(map[string]any)
		if entry == nil {
			continue
		}
		execution, _ := entry["execution"].(map[string]any)
		schemaJSON := prettyJSON(entry["inputSchema"])
		rows = append(rows, toolManifestRow{
			Name:        firstNonEmpty(scalarString(entry["name"]), "-"),
			Description: scalarString(entry["description"]),
			Service:     firstNonEmpty(scalarString(execution["service"]), "-"),
			Method:      firstNonEmpty(scalarString(execution["method"]), "-"),
			Path:        firstNonEmpty(scalarString(execution["path"]), "-"),
			Transport:   firstNonEmpty(scalarString(execution["transport"]), "-"),
			SchemaJSON:  schemaJSON,
			HasSchema:   schemaJSON != "",
		})
	}
	return rows
}

func serviceAuthRows(v map[string]any) []serviceAuthRow {
	if len(v) == 0 {
		return nil
	}
	keys := make([]string, 0, len(v))
	for key := range v {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	rows := make([]serviceAuthRow, 0, len(keys))
	for _, key := range keys {
		entry, _ := v[key].(map[string]any)
		rows = append(rows, serviceAuthRow{
			Service:    key,
			AuthType:   firstNonEmpty(scalarString(entry["auth_type"]), scalarString(entry["type"]), "-"),
			Principal:  firstNonEmpty(scalarString(entry["principal"]), "-"),
			DetailJSON: prettyJSON(v[key]),
		})
	}
	return rows
}

func buildAgentLiveContextView(v any) agentLiveContextView {
	view := agentLiveContextView{RawJSON: prettyJSON(v)}
	m, ok := v.(map[string]any)
	if !ok || len(m) == 0 {
		return view
	}
	view.CapturedAt = scalarString(m["captured_at"])
	view.Format = scalarString(m["format"])
	view.RequestedModel = scalarString(m["requested_model"])
	view.ChosenRef = scalarString(m["chosen_ref"])
	view.Candidates = candidateContextRows(m["candidates"])
	view.Placements = contextPlacementRows(m["placements"])
	view.RecentCaptures = contextCaptureRows(m["recent_captures"])
	view.FeedBlocks = stringSlice(m["feed_blocks"])
	view.MemoryRecall = scalarString(m["memory_recall"])
	view.TimeContext = scalarString(m["time_context"])
	view.Intervention = scalarString(m["intervention"])
	view.ManagedTool = scalarBool(m["managed_tool"])
	view.TurnCount = scalarInt(m["turn_count"])
	view.ToolsJSON = prettyJSON(m["tools"])

	system := m["system"]
	if systemText := scalarString(system); systemText != "" {
		view.SystemText = systemText
	} else {
		view.SystemJSON = prettyJSON(system)
	}
	view.HasCandidates = len(view.Candidates) > 0
	view.HasFeedBlocks = len(view.FeedBlocks) > 0
	view.HasMemoryRecall = view.MemoryRecall != ""
	view.HasTimeContext = view.TimeContext != ""
	view.HasIntervention = view.Intervention != ""
	view.HasSystemText = view.SystemText != ""
	view.HasSystemJSON = view.SystemJSON != ""
	view.HasSystem = view.HasSystemText || view.HasSystemJSON
	view.HasTools = view.ToolsJSON != ""
	view.HasPlacements = len(view.Placements) > 0
	view.HasRecentCaptures = len(view.RecentCaptures) > 0
	return view
}

func candidateContextRows(v any) []candidateContextRow {
	entries, _ := v.([]any)
	rows := make([]candidateContextRow, 0, len(entries))
	for _, raw := range entries {
		entry, _ := raw.(map[string]any)
		if entry == nil {
			continue
		}
		rows = append(rows, candidateContextRow{
			Provider:      firstNonEmpty(scalarString(entry["provider"]), "-"),
			UpstreamModel: firstNonEmpty(scalarString(entry["upstream_model"]), "-"),
		})
	}
	return rows
}

func contextPlacementRows(v any) []contextPlacementRow {
	entries, _ := v.([]any)
	rows := make([]contextPlacementRow, 0, len(entries))
	for _, raw := range entries {
		entry, _ := raw.(map[string]any)
		if entry == nil {
			continue
		}
		rows = append(rows, contextPlacementRow{
			Order:       scalarInt(entry["order"]),
			Kind:        firstNonEmpty(humanizeKey(scalarString(entry["kind"])), "-"),
			Label:       firstNonEmpty(scalarString(entry["label"]), "-"),
			Carrier:     firstNonEmpty(scalarString(entry["carrier"]), "-"),
			Position:    placementPosition(entry),
			Occurrences: scalarInt(entry["occurrences"]),
			Scope:       firstNonEmpty(humanizeKey(scalarString(entry["persistence"])), "-"),
			Relation:    firstNonEmpty(humanizeKey(scalarString(entry["relation"])), "-"),
		})
	}
	return rows
}

func contextCaptureRows(v any) []contextCaptureRow {
	entries, _ := v.([]any)
	rows := make([]contextCaptureRow, 0, len(entries))
	var previous time.Time
	for _, raw := range entries {
		entry, _ := raw.(map[string]any)
		if entry == nil {
			continue
		}
		capturedText := scalarString(entry["captured_at"])
		capturedAt, hasCapturedAt := parseDashboardTime(capturedText)
		interval := "first in buffer"
		if hasCapturedAt && !previous.IsZero() {
			diff := capturedAt.Sub(previous)
			if diff < 0 {
				diff = -diff
			}
			interval = formatRelativeDuration(diff)
		}
		if hasCapturedAt {
			previous = capturedAt
		}
		rows = append(rows, contextCaptureRow{
			Sequence:       scalarInt(entry["sequence"]),
			CapturedAt:     firstNonEmpty(capturedText, "-"),
			Interval:       interval,
			Format:         firstNonEmpty(scalarString(entry["format"]), "-"),
			Model:          firstNonEmpty(scalarString(entry["chosen_ref"]), scalarString(entry["requested_model"]), "-"),
			DynamicInputs:  scalarInt(entry["dynamic_inputs"]),
			FeedBlocks:     scalarInt(entry["feed_blocks"]),
			MemoryRecall:   scalarBool(entry["memory_recall"]),
			TimeContext:    scalarBool(entry["time_context"]),
			PlacementCount: scalarInt(entry["placement_count"]),
			TurnCount:      scalarInt(entry["turn_count"]),
			ManagedTool:    scalarBool(entry["managed_tool"]),
		})
	}
	return rows
}

func placementPosition(entry map[string]any) string {
	messageIndex := scalarInt(entry["message_index"])
	blockIndex := scalarInt(entry["block_index"])
	start := scalarInt(entry["start_char"])
	end := scalarInt(entry["end_char"])
	parts := make([]string, 0, 3)
	if messageIndex >= 0 {
		parts = append(parts, fmt.Sprintf("message %d", messageIndex))
	}
	if blockIndex >= 0 {
		parts = append(parts, fmt.Sprintf("block %d", blockIndex))
	}
	if start >= 0 && end >= 0 {
		parts = append(parts, fmt.Sprintf("chars %d-%d", start, end))
	}
	if len(parts) == 0 {
		return "-"
	}
	return strings.Join(parts, ", ")
}

func parseDashboardTime(value string) (time.Time, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		parsed, err := time.Parse(layout, value)
		if err == nil {
			return parsed, true
		}
	}
	return time.Time{}, false
}

func stringSlice(v any) []string {
	entries, _ := v.([]any)
	out := make([]string, 0, len(entries))
	for _, entry := range entries {
		if text := scalarString(entry); text != "" {
			out = append(out, text)
		}
	}
	return out
}

func prettyJSON(v any) string {
	if !hasJSONValue(v) {
		return ""
	}
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(bytes.TrimSpace(raw))
}

func hasJSONValue(v any) bool {
	switch typed := v.(type) {
	case nil:
		return false
	case map[string]any:
		return len(typed) > 0
	case []any:
		return len(typed) > 0
	case string:
		return strings.TrimSpace(typed) != ""
	default:
		return true
	}
}
