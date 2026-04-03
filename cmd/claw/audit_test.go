package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mostlydev/clawdapus/internal/audit"
)

func TestWriteAuditJSONIncludesManagedToolEvents(t *testing.T) {
	toolStatus := 200
	finalStatus := 200
	totalRounds := 2
	toolRound := 1
	latencyMS := int64(87)
	events := []audit.Event{{
		ClawID:          "tiverton",
		Type:            "tool_call",
		Model:           "xai/grok-4.1-fast",
		LatencyMS:       &latencyMS,
		StatusCode:      &toolStatus,
		SessionEntryID:  "hist1_abc",
		FinalStatus:     "ok",
		FinalStatusCode: &finalStatus,
		TotalRounds:     &totalRounds,
		ToolName:        "trading-api.get_market_context",
		ToolService:     "trading-api",
		ToolRound:       &toolRound,
	}}

	var out bytes.Buffer
	if err := writeAuditJSON(&out, "rollcall", 0, audit.Summarize(events), events); err != nil {
		t.Fatalf("writeAuditJSON: %v", err)
	}

	var payload struct {
		Pod    string        `json:"pod"`
		Events []audit.Event `json:"events"`
	}
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal payload: %v\n%s", err, out.String())
	}
	if payload.Pod != "rollcall" || len(payload.Events) != 1 {
		t.Fatalf("unexpected payload: %+v", payload)
	}
	event := payload.Events[0]
	if event.Type != "tool_call" || event.ToolName != "trading-api.get_market_context" {
		t.Fatalf("unexpected tool event: %+v", event)
	}
	if event.ToolService != "trading-api" || event.FinalStatus != "ok" {
		t.Fatalf("unexpected managed tool fields: %+v", event)
	}
	if event.FinalStatusCode == nil || *event.FinalStatusCode != 200 {
		t.Fatalf("expected final_status_code=200, got %+v", event)
	}
}

func TestWriteAuditTextShowsManagedToolCounts(t *testing.T) {
	events := []audit.Event{
		{ClawID: "weston", Type: "request"},
		{ClawID: "weston", Type: "tool_call", ToolName: "svc.ok", FinalStatus: "ok"},
		{ClawID: "weston", Type: "tool_call", ToolName: "svc.fail", FinalStatus: "error"},
	}

	var out bytes.Buffer
	if err := writeAuditText(&out, "rollcall", 0, audit.Summarize(events), events); err != nil {
		t.Fatalf("writeAuditText: %v", err)
	}
	rendered := out.String()
	for _, want := range []string{"TOOLS", "TOOL_ERR", "weston", "Totals: req=1 resp=0 err=0 int=0 tools=2/1"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected %q in output, got:\n%s", want, rendered)
		}
	}
}
