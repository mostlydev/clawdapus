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
