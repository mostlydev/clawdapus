package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mostlydev/clawdapus/internal/runs"
	"github.com/spf13/cobra"
)

// The family report remains available while serve owns the pod lock. This
// exercises the actual command path without needing Docker or provider access.
func TestRunsDoctorReportsFamiliesBeforeBusyRunLock(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "claw-pod.yml")
	body := []byte("x-claw:\n  pod: family-doctor\n  model-families:\n    terra:\n      provider: openai\n      pattern: gpt-*-terra\n      max-input: 2\n      max-output: 7.5\nservices:\n  plain:\n    image: busybox:latest\n")
	if err := os.WriteFile(path, body, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "compose.generated.yml"), []byte("services: {}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	old := composePodFile
	composePodFile = path
	defer func() { composePodFile = old }()
	lock, err := runs.AcquirePodLock(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Release()
	var output bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&output)
	err = runsDoctorCmd.RunE(cmd, nil)
	if err == nil || !strings.Contains(err.Error(), "holds this pod's run lock") {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(output.String(), "openai/@terra: declared, not resolved yet") {
		t.Fatalf("missing report: %s", &output)
	}
	if _, err := os.Stat(filepath.Join(dir, "models.lock")); !os.IsNotExist(err) {
		t.Fatal("doctor wrote a model lock")
	}
	if _, err := os.Stat(filepath.Join(dir, runs.StateDirName, "templates")); !os.IsNotExist(err) {
		t.Fatal("doctor wrote templates")
	}
}
