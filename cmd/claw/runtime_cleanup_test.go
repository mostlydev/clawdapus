package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRuntimeDirStageCommitRepairsPermissionDeniedCleanupWithDocker(t *testing.T) {
	prevRemove := removePreviousRuntimeDir
	prevDocker := runInfraDockerCommand
	defer func() {
		removePreviousRuntimeDir = prevRemove
		runInfraDockerCommand = prevDocker
	}()

	root := t.TempDir()
	previousPath := filepath.Join(root, ".claw-runtime.previous-123")
	if err := os.MkdirAll(filepath.Join(previousPath, "worker", "cron", "runs"), 0o755); err != nil {
		t.Fatal(err)
	}

	removePreviousRuntimeDir = func(path string) error {
		if path != previousPath {
			return os.RemoveAll(path)
		}
		return &os.PathError{Op: "openfdat", Path: path, Err: os.ErrPermission}
	}

	var gotArgs []string
	runInfraDockerCommand = func(args ...string) error {
		gotArgs = append([]string(nil), args...)
		return os.RemoveAll(previousPath)
	}

	stage := &runtimeDirStage{PreviousPath: previousPath}
	if err := stage.Commit(); err != nil {
		t.Fatalf("Commit returned error: %v", err)
	}
	if _, err := os.Stat(previousPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected previous runtime dir to be removed, stat err=%v", err)
	}

	joined := strings.Join(gotArgs, " ")
	for _, want := range []string{
		"run",
		"--rm",
		"--user 0:0",
		runtimeCleanupImage,
		"/runtime-parent:rw",
		"RUNTIME_CLEANUP_TARGET=.claw-runtime.previous-123",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("docker helper args missing %q: %s", want, joined)
		}
	}
}
