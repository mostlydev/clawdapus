package audit

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestNormalizeLinePreservesRevocationFenceFields(t *testing.T) {
	dispatch := `{"ts":"2026-10-20T10:00:00Z","claw_id":"worker-run-1","type":"tool_dispatch","dispatch_id":"18f-2","dispatch_status":"admitted_before_revoke","intervention":null}`
	event, err := NormalizeLine([]byte(dispatch))
	if err != nil {
		t.Fatal(err)
	}
	if event.Type != "tool_dispatch" || event.DispatchID != "18f-2" || event.DispatchStatus != "admitted_before_revoke" {
		t.Fatalf("dispatch fields lost: %+v", event)
	}
	revocation := `{"ts":"2026-10-20T10:00:01Z","claw_id":"worker-run-1","type":"revocation","revocation_generation":3,"admitted_before_revoke":2,"refused_after_revoke":1,"intervention":null}`
	event, err = NormalizeLine([]byte(revocation))
	if err != nil {
		t.Fatal(err)
	}
	if event.RevocationGeneration == nil || *event.RevocationGeneration != 3 ||
		event.AdmittedBeforeRevoke == nil || *event.AdmittedBeforeRevoke != 2 ||
		event.RefusedAfterRevoke == nil || *event.RefusedAfterRevoke != 1 {
		t.Fatalf("revocation fields lost: %+v", event)
	}
	raw, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{`"revocation_generation":3`, `"admitted_before_revoke":2`, `"refused_after_revoke":1`} {
		if !strings.Contains(string(raw), key) {
			t.Fatalf("JSON export lost %s: %s", key, raw)
		}
	}
}

func TestSummarizeDoesNotCountDispatchRecordsAsToolCalls(t *testing.T) {
	events := []Event{
		{ClawID: "w", Type: "tool_dispatch", DispatchID: "1", DispatchStatus: "admitted"},
		{ClawID: "w", Type: "tool_dispatch", DispatchID: "1", DispatchStatus: "completed"},
		{ClawID: "w", Type: "tool_call", ToolName: "api.get"},
	}
	if s := Summarize(events); s.ToolCalls != 1 {
		t.Fatalf("tool_dispatch must not inflate tool call counts, got %d", s.ToolCalls)
	}
}

func TestRevocationGenerationIsExactForLargeValues(t *testing.T) {
	for _, gen := range []string{"9007199254740993", "18446744073709551615"} {
		line := `{"ts":"2026-10-20T10:00:01Z","claw_id":"w","type":"revocation","revocation_generation":` + gen + `,"admitted_before_revoke":0,"refused_after_revoke":0}`
		event, err := NormalizeLine([]byte(line))
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(event)
		if !strings.Contains(string(raw), `"revocation_generation":`+gen) {
			t.Fatalf("generation %s not preserved: %s", gen, raw)
		}
	}
}
