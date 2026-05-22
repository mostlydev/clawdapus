package audit

import "time"

type Event struct {
	Timestamp          time.Time `json:"timestamp"`
	ClawID             string    `json:"claw_id,omitempty"`
	Type               string    `json:"type"`
	Model              string    `json:"model,omitempty"`
	ManifestPresent    *bool     `json:"manifest_present,omitempty"`
	ToolsCount         *int      `json:"tools_count,omitempty"`
	LatencyMS          *int64    `json:"latency_ms,omitempty"`
	StatusCode         *int      `json:"status_code,omitempty"`
	TokensIn           *int      `json:"tokens_in,omitempty"`
	TokensOut          *int      `json:"tokens_out,omitempty"`
	CostUSD            *float64  `json:"cost_usd,omitempty"`
	InterventionReason string    `json:"intervention_reason,omitempty"`
	Error              string    `json:"error,omitempty"`
	FeedName           string    `json:"feed_name,omitempty"`
	FeedURL            string    `json:"feed_url,omitempty"`
	SourceService      string    `json:"source_service,omitempty"`
	SessionEntryID     string    `json:"session_entry_id,omitempty"`
	FinalStatus        string    `json:"final_status,omitempty"`
	FinalStatusCode    *int      `json:"final_status_code,omitempty"`
	TotalRounds        *int      `json:"total_rounds,omitempty"`
	ToolName           string    `json:"tool_name,omitempty"`
	ToolService        string    `json:"tool_service,omitempty"`
	ToolRound          *int      `json:"tool_round,omitempty"`
	ToolDuplicate      bool      `json:"tool_duplicate,omitempty"`
	ToolDuplicateRound *int      `json:"tool_duplicate_of_round,omitempty"`
	ToolDuplicateCount *int      `json:"tool_duplicate_count,omitempty"`
	ToolStatus         string    `json:"tool_status,omitempty"`
	ChannelKind        string    `json:"kind,omitempty"`
	Channels           []string  `json:"channels,omitempty"`
	Retained           *int      `json:"retained,omitempty"`
	Returned           *int      `json:"returned,omitempty"`
	Omitted            *int      `json:"omitted,omitempty"`
	RawBytes           *int      `json:"raw_bytes,omitempty"`
	DigestBytes        *int      `json:"digest_bytes,omitempty"`
	DigestBlocks       *int      `json:"digest_blocks,omitempty"`
	CoverageGaps       *int      `json:"coverage_gaps,omitempty"`
	DeterministicOnly  *bool     `json:"deterministic_only,omitempty"`
	Status             string    `json:"status,omitempty"`
	// provider_pool event fields
	Provider      string `json:"provider,omitempty"`
	KeyID         string `json:"key_id,omitempty"`
	Action        string `json:"action,omitempty"`
	Reason        string `json:"reason,omitempty"`
	CooldownUntil string `json:"cooldown_until,omitempty"`
}

type AgentSummary struct {
	ClawID             string         `json:"claw_id"`
	Requests           int            `json:"requests"`
	Responses          int            `json:"responses"`
	Errors             int            `json:"errors"`
	Interventions      int            `json:"interventions"`
	TokensIn           int            `json:"tokens_in"`
	TokensOut          int            `json:"tokens_out"`
	CostUSD            float64        `json:"cost_usd"`
	ModelUsage         map[string]int `json:"model_usage,omitempty"`
	FeedFetches        int            `json:"feed_fetches"`
	FeedErrors         int            `json:"feed_errors"`
	ToolCalls          int            `json:"tool_calls,omitempty"`
	ToolErrors         int            `json:"tool_errors,omitempty"`
	ChannelContextOps  int            `json:"channel_context_ops,omitempty"`
	ChannelContextErrs int            `json:"channel_context_errors,omitempty"`
	ProviderPoolEvents int            `json:"provider_pool_events,omitempty"`
	FirstTimestamp     time.Time      `json:"first_timestamp,omitempty"`
	LastTimestamp      time.Time      `json:"last_timestamp,omitempty"`
}

type Summary struct {
	Agents             []AgentSummary `json:"agents"`
	Requests           int            `json:"requests"`
	Responses          int            `json:"responses"`
	Errors             int            `json:"errors"`
	Interventions      int            `json:"interventions"`
	TokensIn           int            `json:"tokens_in"`
	TokensOut          int            `json:"tokens_out"`
	CostUSD            float64        `json:"cost_usd"`
	ToolCalls          int            `json:"tool_calls,omitempty"`
	ToolErrors         int            `json:"tool_errors,omitempty"`
	ProviderPoolEvents int            `json:"provider_pool_events,omitempty"`
}
