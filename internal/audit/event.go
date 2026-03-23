package audit

import "time"

type Event struct {
	Timestamp          time.Time `json:"timestamp"`
	ClawID             string    `json:"claw_id,omitempty"`
	Type               string    `json:"type"`
	Model              string    `json:"model,omitempty"`
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
	// provider_pool event fields
	Provider      string `json:"provider,omitempty"`
	KeyID         string `json:"key_id,omitempty"`
	Action        string `json:"action,omitempty"`
	Reason        string `json:"reason,omitempty"`
	CooldownUntil string `json:"cooldown_until,omitempty"`
}

type AgentSummary struct {
	ClawID              string         `json:"claw_id"`
	Requests            int            `json:"requests"`
	Responses           int            `json:"responses"`
	Errors              int            `json:"errors"`
	Interventions       int            `json:"interventions"`
	TokensIn            int            `json:"tokens_in"`
	TokensOut           int            `json:"tokens_out"`
	CostUSD             float64        `json:"cost_usd"`
	ModelUsage          map[string]int `json:"model_usage,omitempty"`
	FeedFetches         int            `json:"feed_fetches"`
	FeedErrors          int            `json:"feed_errors"`
	ProviderPoolEvents  int            `json:"provider_pool_events,omitempty"`
	FirstTimestamp      time.Time      `json:"first_timestamp,omitempty"`
	LastTimestamp       time.Time      `json:"last_timestamp,omitempty"`
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
	ProviderPoolEvents int            `json:"provider_pool_events,omitempty"`
}
