package hermes

import (
	"encoding/json"
	"testing"

	"github.com/mostlydev/clawdapus/internal/driver"
)

func TestGenerateJobsJSONUsesWrapperAndLocalDelivery(t *testing.T) {
	rc := &driver.ResolvedClaw{
		ServiceName: "hermes",
		Invocations: []driver.Invocation{
			{Schedule: "*/5 * * * *", Message: "Check status", Name: "status"},
		},
		Handles: map[string]*driver.HandleInfo{
			"discord": {},
		},
	}

	data, err := GenerateJobsJSON(rc)
	if err != nil {
		t.Fatalf("GenerateJobsJSON returned error: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("parse jobs json: %v", err)
	}

	jobs, ok := payload["jobs"].([]any)
	if !ok || len(jobs) != 1 {
		t.Fatalf("expected one wrapped job, got %#v", payload["jobs"])
	}
	job, _ := jobs[0].(map[string]any)
	if got := job["deliver"]; got != "local" {
		t.Fatalf("expected local deliver target, got %#v", got)
	}
	if _, ok := job["next_run_at"].(string); !ok {
		t.Fatalf("expected next_run_at string, got %#v", job["next_run_at"])
	}
}

func TestGenerateJobsJSONUsesSinglePlatformTargetWhenUnambiguous(t *testing.T) {
	rc := &driver.ResolvedClaw{
		ServiceName: "hermes",
		Invocations: []driver.Invocation{
			{Schedule: "0 9 * * 1-5", Message: "Market open", To: "C123"},
		},
		Handles: map[string]*driver.HandleInfo{
			"slack": {},
		},
	}

	data, err := GenerateJobsJSON(rc)
	if err != nil {
		t.Fatalf("GenerateJobsJSON returned error: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("parse jobs json: %v", err)
	}
	jobs := payload["jobs"].([]any)
	job := jobs[0].(map[string]any)
	if got := job["deliver"]; got != "slack:C123" {
		t.Fatalf("expected slack:C123 deliver target, got %#v", got)
	}
}

func TestGenerateJobsJSONRejectsInvalidSchedule(t *testing.T) {
	rc := &driver.ResolvedClaw{
		ServiceName: "hermes",
		Invocations: []driver.Invocation{
			{Schedule: "@hourly", Message: "Check status"},
		},
		Handles: map[string]*driver.HandleInfo{
			"discord": {},
		},
	}

	if _, err := GenerateJobsJSON(rc); err == nil {
		t.Fatal("expected GenerateJobsJSON to reject invalid cron")
	}
}

func TestGenerateJobsJSONDisablesPodOriginJobs(t *testing.T) {
	rc := &driver.ResolvedClaw{
		ServiceName: "hermes",
		Invocations: []driver.Invocation{
			{ID: "podjob01", Schedule: "*/5 * * * *", Message: "Check status", Origin: driver.OriginPod},
		},
		Handles: map[string]*driver.HandleInfo{
			"discord": {},
		},
	}

	data, err := GenerateJobsJSON(rc)
	if err != nil {
		t.Fatalf("GenerateJobsJSON returned error: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("parse jobs json: %v", err)
	}
	jobs := payload["jobs"].([]any)
	job := jobs[0].(map[string]any)
	if got := job["enabled"]; got != false {
		t.Fatalf("expected disabled pod-origin job, got %#v", got)
	}
	if got := job["id"]; got != "podjob01" {
		t.Fatalf("expected explicit id, got %#v", got)
	}
}
