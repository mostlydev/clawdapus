package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mostlydev/clawdapus/internal/pod"
)

func TestPortableMemoryNeedsRepairDetectsModeDrift(t *testing.T) {
	stateDir := t.TempDir()
	memoryDir := filepath.Join(stateDir, "memory")
	notesDir := filepath.Join(memoryDir, "notes")
	if err := os.MkdirAll(notesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	notePath := filepath.Join(notesDir, "2026-04-15.md")
	if err := os.WriteFile(notePath, []byte("entry"), 0o644); err != nil {
		t.Fatal(err)
	}

	needsRepair, reason, err := portableMemoryNeedsRepair(memoryDir)
	if err != nil {
		t.Fatalf("portableMemoryNeedsRepair returned error: %v", err)
	}
	if !needsRepair {
		t.Fatal("expected mode drift to require repair")
	}
	if !strings.Contains(reason, "mode=") {
		t.Fatalf("expected mode reason, got %q", reason)
	}
}

func TestRepairPortableMemoryWithDockerUsesHelperImageAndHostOwnership(t *testing.T) {
	prev := runInfraDockerCommand
	defer func() {
		runInfraDockerCommand = prev
	}()

	var gotArgs []string
	runInfraDockerCommand = func(args ...string) error {
		gotArgs = append([]string(nil), args...)
		return nil
	}

	memoryDir := filepath.Join(t.TempDir(), "memory")
	if err := os.MkdirAll(memoryDir, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := repairPortableMemoryWithDocker(memoryDir); err != nil {
		t.Fatalf("repairPortableMemoryWithDocker returned error: %v", err)
	}
	if len(gotArgs) == 0 {
		t.Fatal("expected docker helper invocation")
	}
	joined := strings.Join(gotArgs, " ")
	for _, want := range []string{"run", "--rm", "--user 0:0", portableMemoryRepairImage, "/portable-memory:rw"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("docker helper args missing %q: %s", want, joined)
		}
	}
	if uid, gid, ok := currentPortableMemoryRepairOwner(); ok {
		if !strings.Contains(joined, "HOST_UID="+strconv.Itoa(uid)) {
			t.Fatalf("docker helper args missing HOST_UID: %s", joined)
		}
		if !strings.Contains(joined, "HOST_GID="+strconv.Itoa(gid)) {
			t.Fatalf("docker helper args missing HOST_GID: %s", joined)
		}
	}
}

func TestPreMigratePortableMemoryRepairsCrossUIDTree(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not installed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := exec.CommandContext(ctx, "docker", "info").Run(); err != nil {
		t.Skipf("docker not available: %v", err)
	}
	hostUID, hostGID, ok := currentPortableMemoryRepairOwner()
	if !ok {
		t.Skip("host ownership metadata unavailable on this platform")
	}

	podDir := t.TempDir()
	runtimeDir := filepath.Join(podDir, ".claw-runtime")
	memoryRoot := filepath.Join(podDir, ".claw-memory")
	memoryDir := filepath.Join(memoryRoot, "weston", "memory")
	if err := os.MkdirAll(memoryDir, 0o777); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(
		"docker", "run", "--rm",
		"--user", "0:0",
		"-v", memoryDir+":/portable-memory:rw",
		portableMemoryRepairImage,
		"sh", "-ceu",
		`mkdir -p /portable-memory/notes
printf 'entry' >/portable-memory/notes/2026-04-15.md
chmod 0755 /portable-memory /portable-memory/notes
chmod 0644 /portable-memory/notes/2026-04-15.md
chown 12345:12345 /portable-memory /portable-memory/notes /portable-memory/notes/2026-04-15.md`,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("docker setup for foreign-owned memory failed: %v\n%s", err, out)
	}

	notePath := filepath.Join(memoryDir, "notes", "2026-04-15.md")
	noteInfo, err := os.Stat(notePath)
	if err != nil {
		t.Fatalf("stat note: %v", err)
	}
	noteUID, noteGID, ok := portableMemoryFileOwner(noteInfo)
	if !ok {
		t.Skip("host file ownership metadata unavailable")
	}
	if noteUID == hostUID && noteGID == hostGID {
		t.Skip("docker bind mounts on this host do not preserve foreign UID ownership; skipping Linux-style repair regression")
	}

	p := &pod.Pod{
		Services: map[string]*pod.Service{
			"weston": {},
		},
	}
	if err := preMigratePortableMemory(runtimeDir, memoryRoot, p); err != nil {
		t.Fatalf("preMigratePortableMemory returned error: %v", err)
	}

	checks := map[string]os.FileMode{
		memoryDir:                             0o777,
		filepath.Join(memoryDir, "notes"):     0o777,
		notePath:                              0o666,
		filepath.Join(memoryDir, "MEMORY.md"): 0o666,
		filepath.Join(memoryDir, "USER.md"):   0o666,
	}
	for path, wantMode := range checks {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat %s: %v", path, err)
		}
		if got := info.Mode().Perm(); got != wantMode {
			t.Fatalf("%s mode=%o want %o", path, got, wantMode)
		}
		uid, gid, ok := portableMemoryFileOwner(info)
		if ok && (uid != hostUID || gid != hostGID) {
			t.Fatalf("%s owner=%d:%d want %d:%d", path, uid, gid, hostUID, hostGID)
		}
	}
}
