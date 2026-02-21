package doctor

import (
	"errors"
	"strings"
	"testing"
)

func TestDefaultRunnerIgnoresStderrOnSuccess(t *testing.T) {
	out, err := defaultRunner("sh", "-c", "echo warning 1>&2; echo v1.2.3")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := strings.TrimSpace(string(out))
	if got != "v1.2.3" {
		t.Fatalf("expected stdout-only version, got %q", got)
	}
}

func TestCheckFailureUsesDetail(t *testing.T) {
	run := func(name string, args ...string) ([]byte, error) {
		return []byte("tool not found"), errors.New("failed")
	}

	result := check("docker", run, "docker", "version")
	if result.OK {
		t.Fatal("expected failed check")
	}
	if result.Detail != "tool not found" {
		t.Fatalf("expected detail to include error output, got %q", result.Detail)
	}
}
