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

func TestNormalizeLineParseChannelContextOpEvent(t *testing.T) {
	line := `{"ts":"2026-05-12T10:00:00Z","claw_id":"weston","type":"channel_context_op","kind":"raw_window+digest","channels":["chan-1","chan-2"],"retained":60,"returned":40,"omitted":20,"raw_bytes":12000,"digest_bytes":800,"digest_blocks":3,"coverage_gaps":1,"deterministic_only":true,"source":"claw-wall","status":"coverage_gap","tool_name":"search_channel_context","status_code":200}`
	event, err := NormalizeLine([]byte(line))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if event.Type != "channel_context_op" || event.ChannelKind != "raw_window+digest" || event.SourceService != "claw-wall" || event.Status != "coverage_gap" {
		t.Fatalf("unexpected event: %+v", event)
	}
	if len(event.Channels) != 2 || event.Channels[0] != "chan-1" || event.Channels[1] != "chan-2" {
		t.Fatalf("unexpected channels: %+v", event.Channels)
	}
	if event.Retained == nil || *event.Retained != 60 || event.Returned == nil || *event.Returned != 40 || event.Omitted == nil || *event.Omitted != 20 {
		t.Fatalf("unexpected counts: %+v", event)
	}
	if event.RawBytes == nil || *event.RawBytes != 12000 || event.DigestBytes == nil || *event.DigestBytes != 800 || event.DigestBlocks == nil || *event.DigestBlocks != 3 || event.CoverageGaps == nil || *event.CoverageGaps != 1 {
		t.Fatalf("unexpected digest counts: %+v", event)
	}
	if event.DeterministicOnly == nil || !*event.DeterministicOnly {
		t.Fatalf("expected deterministic_only=true: %+v", event)
	}
	if event.ToolName != "search_channel_context" {
		t.Fatalf("unexpected tool name: %+v", event)
	}
}

func TestSummarizeCountsChannelContextOps(t *testing.T) {
	events := []Event{
		{ClawID: "weston", Type: "channel_context_op", Status: "ok"},
		{ClawID: "weston", Type: "channel_context_op", Status: "not_in_buffer"},
	}
	summary := Summarize(events)
	if len(summary.Agents) != 1 {
		t.Fatalf("expected 1 agent, got %d", len(summary.Agents))
	}
	if summary.Agents[0].ChannelContextOps != 2 || summary.Agents[0].ChannelContextErrs != 1 {
		t.Fatalf("unexpected channel context summary: %+v", summary.Agents[0])
	}
}

func TestNormalizeLineParseToolManifestEvent(t *testing.T) {
	line := `{"ts":"2026-04-05T19:00:00Z","claw_id":"weston","type":"tool_manifest_loaded","model":"openai/gpt-4o","manifest_present":true,"tools_count":2}`
	event, err := NormalizeLine([]byte(line))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if event.Type != "tool_manifest_loaded" {
		t.Fatalf("expected type tool_manifest_loaded, got %q", event.Type)
	}
	if event.ManifestPresent == nil || !*event.ManifestPresent {
		t.Fatalf("expected manifest_present=true, got %+v", event)
	}
	if event.ToolsCount == nil || *event.ToolsCount != 2 {
		t.Fatalf("expected tools_count=2, got %+v", event)
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

func TestNormalizeSessionHistoryLineOpenAIManagedTool(t *testing.T) {
	line := `{"version":1,"id":"hist1_abc","status":"ok","ts":"2026-04-03T12:00:00Z","claw_id":"tiverton","path":"/v1/chat/completions","requested_model":"xai/grok-4.1-fast","effective_provider":"xai","effective_model":"xai/grok-4.1-fast","status_code":200,"usage":{"prompt_tokens":120,"completion_tokens":40,"total_rounds":2},"tool_trace":[{"round":1,"tool_calls":[{"name":"trading-api.get_market_context","service":"trading-api","status":"ok","latency_ms":87,"status_code":200,"result":{"ok":true,"data":{"balance":5000}}}]}]}`
	events, err := NormalizeSessionHistoryLine([]byte(line))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 tool event, got %+v", events)
	}
	event := events[0]
	if event.Type != "tool_call" || event.ToolName != "trading-api.get_market_context" {
		t.Fatalf("unexpected tool event: %+v", event)
	}
	if event.ToolService != "trading-api" || event.FinalStatus != "ok" {
		t.Fatalf("unexpected tool metadata: %+v", event)
	}
	if event.SessionEntryID != "hist1_abc" {
		t.Fatalf("expected session entry id hist1_abc, got %+v", event)
	}
	if event.StatusCode == nil || *event.StatusCode != 200 {
		t.Fatalf("expected tool status_code 200, got %+v", event)
	}
	if event.ToolStatus != "ok" {
		t.Fatalf("expected tool status ok, got %+v", event)
	}
	if event.FinalStatusCode == nil || *event.FinalStatusCode != 200 {
		t.Fatalf("expected final status_code 200, got %+v", event)
	}
	if event.TotalRounds == nil || *event.TotalRounds != 2 {
		t.Fatalf("expected total_rounds 2, got %+v", event)
	}
	if event.ToolRound == nil || *event.ToolRound != 1 {
		t.Fatalf("expected tool_round 1, got %+v", event)
	}
}

func TestNormalizeSessionHistoryLineManagedToolDuplicateMetadata(t *testing.T) {
	line := `{"version":1,"id":"hist1_dup","status":"ok","ts":"2026-04-03T12:00:00Z","claw_id":"agent","path":"/v1/chat/completions","requested_model":"xai/grok","effective_provider":"xai","effective_model":"grok","status_code":200,"usage":{"total_rounds":3},"tool_trace":[{"round":2,"tool_calls":[{"name":"search.query","service":"search","status_code":409,"duplicate":true,"duplicate_of_round":1,"duplicate_count":2,"result":{"ok":false,"error":{"code":"duplicate_tool_call","message":"already ran"}}}]}]}`
	events, err := NormalizeSessionHistoryLine([]byte(line))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 tool event, got %+v", events)
	}
	event := events[0]
	if !event.ToolDuplicate {
		t.Fatalf("expected duplicate flag, got %+v", event)
	}
	if event.ToolDuplicateRound == nil || *event.ToolDuplicateRound != 1 {
		t.Fatalf("expected duplicate source round 1, got %+v", event)
	}
	if event.ToolDuplicateCount == nil || *event.ToolDuplicateCount != 2 {
		t.Fatalf("expected duplicate count 2, got %+v", event)
	}
	if event.Error != "already ran" {
		t.Fatalf("expected extracted duplicate error message, got %+v", event)
	}
}

func TestNormalizeSessionHistoryLineAnthropicManagedToolFailure(t *testing.T) {
	line := `{"version":1,"id":"hist1_def","status":"error","ts":"2026-04-03T12:05:00Z","claw_id":"nano-bot","path":"/v1/messages","requested_model":"anthropic/claude-sonnet-4","effective_provider":"anthropic","effective_model":"anthropic/claude-sonnet-4","status_code":502,"response":{"format":"json","json":{"error":{"message":"tool result rejected"}}},"usage":{"prompt_tokens":140,"completion_tokens":30,"total_rounds":2},"tool_trace":[{"round":1,"tool_calls":[{"name":"trading-api.get_market_context","service":"trading-api","latency_ms":145,"status_code":504,"result":{"ok":false,"error":"backend timeout"}}]}]}`
	events, err := NormalizeSessionHistoryLine([]byte(line))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 tool event, got %+v", events)
	}
	event := events[0]
	if event.FinalStatus != "error" {
		t.Fatalf("expected final_status error, got %+v", event)
	}
	if event.Error != "backend timeout" {
		t.Fatalf("expected tool error backend timeout, got %+v", event)
	}
	if event.FinalStatusCode == nil || *event.FinalStatusCode != 502 {
		t.Fatalf("expected final status code 502, got %+v", event)
	}
	if event.StatusCode == nil || *event.StatusCode != 504 {
		t.Fatalf("expected tool status code 504, got %+v", event)
	}
}

func TestParseSessionHistoryReaderSkipsNonMediatedEntries(t *testing.T) {
	raw := strings.NewReader(
		"{\"version\":1,\"ts\":\"2026-04-03T12:00:00Z\",\"claw_id\":\"plain\",\"status\":\"ok\"}\n" +
			"{\"version\":1,\"id\":\"hist1_ghi\",\"status\":\"ok\",\"ts\":\"2026-04-03T12:01:00Z\",\"claw_id\":\"plain\",\"status_code\":200,\"usage\":{\"total_rounds\":2},\"tool_trace\":[{\"round\":1,\"tool_calls\":[{\"name\":\"svc.tool\",\"service\":\"svc\",\"status_code\":200}]}]}\n",
	)
	events, skipped, err := ParseSessionHistoryReader(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if skipped != 0 {
		t.Fatalf("expected 0 skipped lines, got %d", skipped)
	}
	if len(events) != 1 || events[0].ToolName != "svc.tool" {
		t.Fatalf("unexpected tool events: %+v", events)
	}
}

func TestParseSessionHistoryReaderHandlesLargeLines(t *testing.T) {
	filler := strings.Repeat("x", 2*1024*1024)
	line := `{"version":1,"id":"hist1_big","status":"ok","ts":"2026-04-17T12:00:00Z","claw_id":"weston","status_code":200,"usage":{"total_rounds":1},"filler":"` + filler + `","tool_trace":[{"round":1,"tool_calls":[{"name":"svc.tool","service":"svc","status_code":200}]}]}` + "\n"
	events, skipped, err := ParseSessionHistoryReader(strings.NewReader(line))
	if err != nil {
		t.Fatalf("unexpected error on oversized line: %v", err)
	}
	if skipped != 0 {
		t.Fatalf("expected 0 skipped lines, got %d", skipped)
	}
	if len(events) != 1 || events[0].ToolName != "svc.tool" {
		t.Fatalf("unexpected events from oversized line: %+v", events)
	}
}

func TestParseReaderHandlesLargeLines(t *testing.T) {
	filler := strings.Repeat("x", 2*1024*1024)
	line := `{"ts":"2026-04-17T12:00:00Z","claw_id":"weston","type":"request","filler":"` + filler + `"}` + "\n"
	events, skipped, err := ParseReader(strings.NewReader(line))
	if err != nil {
		t.Fatalf("unexpected error on oversized line: %v", err)
	}
	if skipped != 0 {
		t.Fatalf("expected 0 skipped lines, got %d", skipped)
	}
	if len(events) != 1 || events[0].Type != "request" {
		t.Fatalf("unexpected events from oversized line: %+v", events)
	}
}

func TestSummarizeCountsManagedToolCalls(t *testing.T) {
	events := []Event{
		{ClawID: "weston", Type: "tool_call", ToolName: "svc.ok", FinalStatus: "ok"},
		{ClawID: "weston", Type: "tool_call", ToolName: "svc.fail", FinalStatus: "error"},
	}
	summary := Summarize(events)
	if len(summary.Agents) != 1 {
		t.Fatalf("expected 1 agent, got %d", len(summary.Agents))
	}
	agent := summary.Agents[0]
	if agent.ToolCalls != 2 {
		t.Fatalf("expected 2 tool calls, got %+v", agent)
	}
	if agent.ToolErrors != 1 {
		t.Fatalf("expected 1 tool error, got %+v", agent)
	}
	if summary.ToolCalls != 2 || summary.ToolErrors != 1 {
		t.Fatalf("unexpected tool totals: %+v", summary)
	}
}

func ptrInt(v int) *int {
	return &v
}

func ptrFloat64(v float64) *float64 {
	return &v
}
