//go:build integration
// +build integration

package doctor

import (
	"os/exec"
	"testing"
)

func TestRunAll_Integration(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker is not installed")
	}

	results := RunAll()
	if len(results) != 3 {
		t.Fatalf("expected 3 checks, got %d", len(results))
	}

	for _, result := range results {
		if !result.OK {
			t.Fatalf("expected %s check to pass, got %#v", result.Name, result)
		}
	}
}
