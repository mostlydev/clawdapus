package audit

import (
	"strings"
	"testing"
)

func TestNormalizeLineAcceptsReferenceLoggerShape(t *testing.T) {
	line := `{"ts":"2026-03-19T14:32:00Z","claw_id":"octopus","type":"response","model":"openai/gpt-4o","latency_ms":42,"status_code":200,"tokens_in":100,"tokens_out":40,"cost_usd":0.12,"intervention":null}`
	event, err := NormalizeLine([]byte(line))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if event.ClawID != "octopus" || event.Type != "response" {
		t.Fatalf("unexpected event: %+v", event)
	}
	if event.InterventionReason != "" {
		t.Fatalf("expected empty intervention reason, got %q", event.InterventionReason)
	}
}

func TestNormalizeLineAcceptsSpecShape(t *testing.T) {
	line := `{"timestamp":"2026-03-19T14:32:00Z","claw_id":"octopus","type":"intervention","intervention_reason":"policy"}`
	event, err := NormalizeLine([]byte(line))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if event.InterventionReason != "policy" {
		t.Fatalf("expected intervention reason policy, got %+v", event)
	}
}

func TestParseReaderSkipsMalformedLines(t *testing.T) {
	raw := strings.NewReader("{bad json\n" +
		"{\"ts\":\"2026-03-19T14:32:00Z\",\"claw_id\":\"octopus\",\"type\":\"request\"}\n")
	events, skipped, err := ParseReader(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if skipped != 1 {
		t.Fatalf("expected 1 skipped line, got %d", skipped)
	}
	if len(events) != 1 || events[0].Type != "request" {
		t.Fatalf("unexpected events: %+v", events)
	}
}

func TestSummarizeAggregatesByClaw(t *testing.T) {
	events := []Event{
		{ClawID: "octopus", Type: "request", Model: "openai/gpt-4o"},
		{ClawID: "octopus", Type: "response", Model: "openai/gpt-4o", TokensIn: ptrInt(100), TokensOut: ptrInt(40), CostUSD: ptrFloat64(0.12)},
		{ClawID: "octopus", Type: "error"},
	}
	summary := Summarize(events)
	if summary.Requests != 1 || summary.Responses != 1 || summary.Errors != 1 {
		t.Fatalf("unexpected summary: %+v", summary)
	}
	if len(summary.Agents) != 1 || summary.Agents[0].TokensIn != 100 {
		t.Fatalf("unexpected agent summary: %+v", summary.Agents)
	}
}

func TestNormalizeLineParseFeedFetchEvent(t *testing.T) {
	line := `{"ts":"2026-03-22T10:00:00Z","claw_id":"weston","type":"feed_fetch","feed_name":"market-context","feed_url":"http://trading-api:4000/api/v1/market_context/weston","status_code":200,"latency_ms":45}`
	event, err := NormalizeLine([]byte(line))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if event.Type != "feed_fetch" {
		t.Fatalf("expected type feed_fetch, got %q", event.Type)
	}
	if event.FeedName != "market-context" {
		t.Fatalf("expected feed_name market-context, got %q", event.FeedName)
	}
	if event.FeedURL != "http://trading-api:4000/api/v1/market_context/weston" {
		t.Fatalf("expected feed_url, got %q", event.FeedURL)
	}
	if event.StatusCode == nil || *event.StatusCode != 200 {
		t.Fatalf("expected status_code 200, got %v", event.StatusCode)
	}
}

func TestSummarizeCountsFeedFetches(t *testing.T) {
	events := []Event{
		{ClawID: "weston", Type: "request"},
		{ClawID: "weston", Type: "feed_fetch", FeedName: "market-context", StatusCode: ptrInt(200)},
		{ClawID: "weston", Type: "feed_fetch", FeedName: "market-context", StatusCode: ptrInt(500)},
	}
	summary := Summarize(events)
	if len(summary.Agents) != 1 {
		t.Fatalf("expected 1 agent, got %d", len(summary.Agents))
	}
	agent := summary.Agents[0]
	if agent.FeedFetches != 2 {
		t.Fatalf("expected 2 feed fetches, got %d", agent.FeedFetches)
	}
	if agent.FeedErrors != 1 {
		t.Fatalf("expected 1 feed error, got %d", agent.FeedErrors)
	}
}

func ptrInt(v int) *int {
	return &v
}

func ptrFloat64(v float64) *float64 {
	return &v
}
