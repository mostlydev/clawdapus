package health

import "testing"

func TestParseHealthJSONClean(t *testing.T) {
	stdout := `{"status":"ok","version":"2026.2.9"}`
	result, err := ParseHealthJSON([]byte(stdout))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.OK {
		t.Error("expected OK=true")
	}
}

func TestParseHealthJSONWithLeadingNoise(t *testing.T) {
	stdout := "WARNING: some plugin loaded\n" + `{"status":"ok","version":"2026.2.9"}`
	result, err := ParseHealthJSON([]byte(stdout))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.OK {
		t.Error("expected OK=true despite leading noise")
	}
}

func TestParseHealthJSONUnhealthy(t *testing.T) {
	stdout := `{"status":"error","detail":"gateway unreachable"}`
	result, err := ParseHealthJSON([]byte(stdout))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.OK {
		t.Error("expected OK=false for error status")
	}
	if result.Detail != "gateway unreachable" {
		t.Errorf("expected detail, got %q", result.Detail)
	}
}

func TestParseHealthJSONGarbage(t *testing.T) {
	stdout := `not json at all`
	_, err := ParseHealthJSON([]byte(stdout))
	if err == nil {
		t.Fatal("expected error for non-JSON output")
	}
}
