package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseHistoryAfter(t *testing.T) {
	after, err := parseHistoryAfter("2026-03-31T12:00:00Z")
	if err != nil {
		t.Fatalf("parseHistoryAfter: %v", err)
	}
	if after == nil || after.UTC().Format(time.RFC3339) != "2026-03-31T12:00:00Z" {
		t.Fatalf("unexpected parsed timestamp: %v", after)
	}
	if _, err := parseHistoryAfter("not-a-time"); err == nil {
		t.Fatal("expected invalid timestamp error")
	}
}

func TestNormalizeHistoryLimit(t *testing.T) {
	if _, err := normalizeHistoryLimit(0); err == nil {
		t.Fatal("expected error for limit <= 0")
	}
	got, err := normalizeHistoryLimit(5000)
	if err != nil {
		t.Fatalf("normalizeHistoryLimit: %v", err)
	}
	if got != maxHistoryExportLimit {
		t.Fatalf("expected capped limit %d, got %d", maxHistoryExportLimit, got)
	}
}

func TestExportHistoryFileAppliesAfterAndLimit(t *testing.T) {
	dir := t.TempDir()
	historyPath := filepath.Join(dir, "history.jsonl")
	content := strings.Join([]string{
		`{"ts":"2026-03-31T12:00:00Z","claw_id":"agent-1"}`,
		`{"ts":"2026-03-31T12:01:00Z","claw_id":"agent-1"}`,
		`{"ts":"2026-03-31T12:02:00Z","claw_id":"agent-1"}`,
	}, "\n") + "\n"
	if err := os.WriteFile(historyPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	after := time.Date(2026, 3, 31, 12, 0, 0, 0, time.UTC)
	if err := exportHistoryFile(&out, historyPath, &after, 1); err != nil {
		t.Fatalf("exportHistoryFile: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("expected 1 emitted line, got %q", out.String())
	}
	if !strings.Contains(lines[0], `"2026-03-31T12:01:00Z"`) {
		t.Fatalf("unexpected exported line: %q", lines[0])
	}
}

func TestResolveHistoryPodDirUsesComposePodFile(t *testing.T) {
	dir := t.TempDir()
	podFile := filepath.Join(dir, "claw-pod.yml")
	if err := os.WriteFile(podFile, []byte("services: {}"), 0o644); err != nil {
		t.Fatal(err)
	}

	prev := composePodFile
	composePodFile = podFile
	defer func() { composePodFile = prev }()

	got, err := resolveHistoryPodDir()
	if err != nil {
		t.Fatalf("resolveHistoryPodDir: %v", err)
	}
	if got != dir {
		t.Fatalf("expected pod dir %q, got %q", dir, got)
	}
}
