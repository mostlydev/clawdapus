package audit

import (
	"maps"
	"slices"
	"strings"
	"time"
)

type Filter struct {
	ClawID string
	Type   string
	Since  time.Time
}

func FilterEvents(events []Event, filter Filter) []Event {
	out := make([]Event, 0, len(events))
	wantClaw := strings.TrimSpace(filter.ClawID)
	wantType := strings.TrimSpace(filter.Type)
	for _, event := range events {
		if wantClaw != "" && event.ClawID != wantClaw {
			continue
		}
		if wantType != "" && event.Type != wantType {
			continue
		}
		if !filter.Since.IsZero() && event.Timestamp.Before(filter.Since) {
			continue
		}
		out = append(out, event)
	}
	return out
}

func Summarize(events []Event) Summary {
	byClaw := make(map[string]*AgentSummary)
	summary := Summary{}

	for _, event := range events {
		id := strings.TrimSpace(event.ClawID)
		if id == "" {
			id = "(unknown)"
		}
		agent := byClaw[id]
		if agent == nil {
			agent = &AgentSummary{ClawID: id, ModelUsage: make(map[string]int)}
			byClaw[id] = agent
		}
		if agent.FirstTimestamp.IsZero() || event.Timestamp.Before(agent.FirstTimestamp) {
			agent.FirstTimestamp = event.Timestamp
		}
		if event.Timestamp.After(agent.LastTimestamp) {
			agent.LastTimestamp = event.Timestamp
		}
		if event.Model != "" {
			agent.ModelUsage[event.Model]++
		}
		if event.TokensIn != nil {
			agent.TokensIn += *event.TokensIn
			summary.TokensIn += *event.TokensIn
		}
		if event.TokensOut != nil {
			agent.TokensOut += *event.TokensOut
			summary.TokensOut += *event.TokensOut
		}
		if event.CostUSD != nil {
			agent.CostUSD += *event.CostUSD
			summary.CostUSD += *event.CostUSD
		}

		switch event.Type {
		case "request":
			agent.Requests++
			summary.Requests++
		case "response":
			agent.Responses++
			summary.Responses++
		case "error":
			agent.Errors++
			summary.Errors++
		case "intervention":
			agent.Interventions++
			summary.Interventions++
		case "feed_fetch":
			agent.FeedFetches++
			if event.StatusCode != nil && *event.StatusCode >= 400 {
				agent.FeedErrors++
			} else if event.Error != "" {
				agent.FeedErrors++
			}
		case "tool_call":
			agent.ToolCalls++
			summary.ToolCalls++
			if toolEventFailed(event) {
				agent.ToolErrors++
				summary.ToolErrors++
			}
		case "channel_context_op":
			agent.ChannelContextOps++
			if channelContextEventFailed(event) {
				agent.ChannelContextErrs++
			}
		case "provider_pool":
			agent.ProviderPoolEvents++
			summary.ProviderPoolEvents++
		}
	}

	agents := make([]AgentSummary, 0, len(byClaw))
	for _, item := range byClaw {
		if len(item.ModelUsage) == 0 {
			item.ModelUsage = nil
		} else {
			item.ModelUsage = maps.Clone(item.ModelUsage)
		}
		agents = append(agents, *item)
	}
	slices.SortFunc(agents, func(left, right AgentSummary) int {
		switch {
		case left.ClawID < right.ClawID:
			return -1
		case left.ClawID > right.ClawID:
			return 1
		default:
			return 0
		}
	})
	summary.Agents = agents
	return summary
}

func channelContextEventFailed(event Event) bool {
	if event.Error != "" {
		return true
	}
	if event.StatusCode != nil && *event.StatusCode >= 400 {
		return true
	}
	switch event.Status {
	case "error", "not_in_buffer":
		return true
	default:
		return false
	}
}

func toolEventFailed(event Event) bool {
	if event.FinalStatus == "error" || event.Error != "" {
		return true
	}
	if event.StatusCode != nil && *event.StatusCode >= 400 {
		return true
	}
	if event.FinalStatusCode != nil && *event.FinalStatusCode >= 400 {
		return true
	}
	return false
}
