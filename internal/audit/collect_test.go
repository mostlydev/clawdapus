package audit

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCollectSessionHistoryEventsReadsPerAgentHistory(t *testing.T) {
	dir := t.TempDir()
	agentDir := filepath.Join(dir, "nano-bot")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	raw := []byte("{\"version\":1,\"id\":\"hist1_xyz\",\"status\":\"ok\",\"ts\":\"2026-04-03T12:00:00Z\",\"claw_id\":\"nano-bot\",\"status_code\":200,\"usage\":{\"total_rounds\":2},\"tool_trace\":[{\"round\":1,\"tool_calls\":[{\"name\":\"svc.tool\",\"service\":\"svc\",\"status_code\":200}]}]}\n")
	if err := os.WriteFile(filepath.Join(agentDir, "history.jsonl"), raw, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	events, skipped, err := CollectSessionHistoryEvents(dir, time.Time{})
	if err != nil {
		t.Fatalf("CollectSessionHistoryEvents: %v", err)
	}
	if skipped != 0 {
		t.Fatalf("expected 0 skipped lines, got %d", skipped)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %+v", events)
	}
	if events[0].ClawID != "nano-bot" || events[0].ToolName != "svc.tool" {
		t.Fatalf("unexpected event: %+v", events[0])
	}
	if events[0].SourceService != "session-history" {
		t.Fatalf("expected session-history source, got %+v", events[0])
	}
}
