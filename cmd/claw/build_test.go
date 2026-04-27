package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mostlydev/clawdapus/internal/build"
)

func TestResolveClawfilePathRejectsMissingInput(t *testing.T) {
	_, err := resolveClawfilePath(filepath.Join(t.TempDir(), "missing"))
	if err == nil {
		t.Fatal("expected error for missing input path")
	}
}

func TestResolveClawfilePathRejectsDirWithoutClawfile(t *testing.T) {
	dir := t.TempDir()
	_, err := resolveClawfilePath(dir)
	if err == nil {
		t.Fatal("expected error for directory without Clawfile")
	}
}

func TestResolveClawfilePathAcceptsDirWithClawfile(t *testing.T) {
	dir := t.TempDir()
	clawfile := filepath.Join(dir, "Clawfile")
	if err := os.WriteFile(clawfile, []byte("FROM alpine\nCLAW_TYPE openclaw\n"), 0o644); err != nil {
		t.Fatalf("write Clawfile: %v", err)
	}

	got, err := resolveClawfilePath(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != clawfile {
		t.Fatalf("expected %q, got %q", clawfile, got)
	}
}

func TestResolveBuildContextDefaultsToClawfileDir(t *testing.T) {
	dir := t.TempDir()
	clawfile := filepath.Join(dir, "agents", "shared", "OpenClawfile")
	if err := os.MkdirAll(filepath.Dir(clawfile), 0o755); err != nil {
		t.Fatalf("mkdir clawfile dir: %v", err)
	}
	if err := os.WriteFile(clawfile, []byte("FROM alpine\nCLAW_TYPE openclaw\n"), 0o644); err != nil {
		t.Fatalf("write Clawfile: %v", err)
	}

	got, err := resolveBuildContext("", clawfile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != filepath.Dir(clawfile) {
		t.Fatalf("expected %q, got %q", filepath.Dir(clawfile), got)
	}
}

func TestResolveBuildContextAcceptsExplicitDirectory(t *testing.T) {
	dir := t.TempDir()
	contextDir := filepath.Join(dir, "repo")
	clawfile := filepath.Join(dir, "agents", "shared", "OpenClawfile")
	if err := os.MkdirAll(contextDir, 0o755); err != nil {
		t.Fatalf("mkdir context dir: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(clawfile), 0o755); err != nil {
		t.Fatalf("mkdir clawfile dir: %v", err)
	}
	if err := os.WriteFile(clawfile, []byte("FROM alpine\nCLAW_TYPE openclaw\n"), 0o644); err != nil {
		t.Fatalf("write Clawfile: %v", err)
	}

	got, err := resolveBuildContext(contextDir, clawfile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != contextDir {
		t.Fatalf("expected %q, got %q", contextDir, got)
	}
}

func TestResolveBuildContextRejectsNonDirectory(t *testing.T) {
	dir := t.TempDir()
	contextFile := filepath.Join(dir, "context.txt")
	clawfile := filepath.Join(dir, "OpenClawfile")
	if err := os.WriteFile(contextFile, []byte("not a dir"), 0o644); err != nil {
		t.Fatalf("write context file: %v", err)
	}
	if err := os.WriteFile(clawfile, []byte("FROM alpine\nCLAW_TYPE openclaw\n"), 0o644); err != nil {
		t.Fatalf("write Clawfile: %v", err)
	}

	_, err := resolveBuildContext(contextFile, clawfile)
	if err == nil {
		t.Fatal("expected error for non-directory context")
	}
	if !strings.Contains(err.Error(), "is not a directory") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestFormatGenerateErrorUsesPullCommandForClawfile(t *testing.T) {
	err := formatGenerateError(&build.MissingRunnerBaseError{ImageRef: "openclaw:latest"}, "", "examples/quickstart/Clawfile")
	if err == nil {
		t.Fatal("expected remediation error")
	}
	if !strings.Contains(err.Error(), "run: claw pull examples/quickstart/Clawfile") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestFormatGenerateErrorUsesPullCommandForPod(t *testing.T) {
	err := formatGenerateError(&build.MissingRunnerBaseError{ImageRef: "openclaw:latest"}, "examples/quickstart/claw-pod.yml", "examples/quickstart/Clawfile")
	if err == nil {
		t.Fatal("expected remediation error")
	}
	if !strings.Contains(err.Error(), "run: claw pull -f examples/quickstart/claw-pod.yml") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestFormatGenerateErrorPassesThroughUnknownErrors(t *testing.T) {
	original := errors.New("boom")
	if got := formatGenerateError(original, "", ""); got != original {
		t.Fatalf("expected original error to pass through, got %v", got)
	}
}
