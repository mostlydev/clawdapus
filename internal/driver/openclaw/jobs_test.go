package openclaw

import (
	"encoding/json"
	"testing"

	"github.com/mostlydev/clawdapus/internal/driver"
)

func TestGenerateJobsJSONSingleInvocation(t *testing.T) {
	rc := &driver.ResolvedClaw{
		ServiceName: "tiverton",
		Invocations: []driver.Invocation{
			{
				Schedule: "15 8 * * 1-5",
				Message:  "Pre-market synthesis",
				To:       "111222333444",
				Name:     "Pre-market synthesis",
			},
		},
	}
	data, err := GenerateJobsJSON(rc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var jobs []map[string]interface{}
	if err := json.Unmarshal(data, &jobs); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("expected 1 job, got %d", len(jobs))
	}

	j := jobs[0]
	if j["agentId"] != "main" {
		t.Errorf("expected agentId=main, got %v", j["agentId"])
	}
	if j["enabled"] != true {
		t.Errorf("expected enabled=true, got %v", j["enabled"])
	}

	schedule := j["schedule"].(map[string]interface{})
	if schedule["expr"] != "15 8 * * 1-5" {
		t.Errorf("expected schedule.expr=%q, got %v", "15 8 * * 1-5", schedule["expr"])
	}

	payload := j["payload"].(map[string]interface{})
	if payload["kind"] != "agentTurn" {
		t.Errorf("expected payload.kind=agentTurn, got %v", payload["kind"])
	}
	if payload["message"] != "Pre-market synthesis" {
		t.Errorf("expected payload.message=%q, got %v", "Pre-market synthesis", payload["message"])
	}

	delivery := j["delivery"].(map[string]interface{})
	if delivery["to"] != "111222333444" {
		t.Errorf("expected delivery.to=111222333444, got %v", delivery["to"])
	}
	if delivery["mode"] != "announce" {
		t.Errorf("expected delivery.mode=announce, got %v", delivery["mode"])
	}
}

func TestGenerateJobsJSONNoTo(t *testing.T) {
	rc := &driver.ResolvedClaw{
		ServiceName: "westin",
		Invocations: []driver.Invocation{
			{
				Schedule: "*/30 * * * *",
				Message:  "Heartbeat update",
				To:       "", // empty → openclaw uses last channel
			},
		},
	}
	data, err := GenerateJobsJSON(rc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Use raw map to check key presence
	var raw []map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(raw) != 1 {
		t.Fatalf("expected 1 job, got %d", len(raw))
	}

	var delivery map[string]json.RawMessage
	if err := json.Unmarshal(raw[0]["delivery"], &delivery); err != nil {
		t.Fatalf("invalid delivery JSON: %v", err)
	}
	if _, haTo := delivery["to"]; haTo {
		t.Error("expected delivery object to omit 'to' key when To is empty")
	}
}

func TestGenerateJobsJSONDeterministicIDs(t *testing.T) {
	rc := &driver.ResolvedClaw{
		ServiceName: "tiverton",
		Invocations: []driver.Invocation{
			{Schedule: "15 8 * * 1-5", Message: "Pre-market synthesis"},
		},
	}

	first, err := GenerateJobsJSON(rc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	second, err := GenerateJobsJSON(rc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var firstJobs, secondJobs []map[string]interface{}
	json.Unmarshal(first, &firstJobs)
	json.Unmarshal(second, &secondJobs)

	id1 := firstJobs[0]["id"]
	id2 := secondJobs[0]["id"]
	if id1 != id2 {
		t.Errorf("expected deterministic ID, got %v and %v", id1, id2)
	}
}

func TestGenerateJobsJSONEmptyInvocations(t *testing.T) {
	rc := &driver.ResolvedClaw{
		ServiceName: "allen",
		Invocations: nil,
	}
	data, err := GenerateJobsJSON(rc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var jobs []map[string]interface{}
	if err := json.Unmarshal(data, &jobs); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(jobs) != 0 {
		t.Errorf("expected empty array, got %d jobs", len(jobs))
	}
}
