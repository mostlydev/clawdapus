//go:build integration

package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	schedulepkg "github.com/mostlydev/clawdapus/internal/schedule"
)

func TestScheduleFireTransportOutlivesOldFifteenSecondDefault(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake docker transport uses a POSIX shell")
	}

	binDir := t.TempDir()
	dockerPath := filepath.Join(binDir, "docker")
	shim := "#!/bin/sh\nsleep 16\nprintf '{\"ok\":true}\\n'\n"
	if err := os.WriteFile(dockerPath, []byte(shim), 0o755); err != nil {
		t.Fatalf("write fake docker transport: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	started := time.Now()
	out, err := runClawAPIComposeCommandDefault(schedulepkg.ManualFireTransportTimeout, "compose", "exec", "claw-api")
	if err != nil {
		t.Fatalf("manual fire transport failed after the old default: %v (%s)", err, strings.TrimSpace(string(out)))
	}
	if elapsed := time.Since(started); elapsed <= defaultAPIExecTimeout {
		t.Fatalf("fake response returned in %v; test must cross the old %v deadline", elapsed, defaultAPIExecTimeout)
	}
	if strings.TrimSpace(string(out)) != `{"ok":true}` {
		t.Fatalf("unexpected delayed response: %q", string(out))
	}
}
