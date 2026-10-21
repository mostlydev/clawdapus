package runs

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

// A lifecycle subprocess that inherits the lock keeps writer authority after
// the supervisor lets go (as when it is SIGKILLed mid compose up).
func TestInheritedLockOutlivesParentRelease(t *testing.T) {
	podDir := t.TempDir()
	lock, err := AcquirePodLock(podDir)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(context.Background(), "sleep", "1")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.ExtraFiles = []*os.File{lock.File()}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	lock.Release()
	if _, err := AcquirePodLock(podDir); !errors.Is(err, ErrPodLocked) {
		t.Fatalf("lock must stay held by the inheriting child, got %v", err)
	}
	_ = cmd.Wait()
	deadline := time.Now().Add(2 * time.Second)
	for {
		l2, err := AcquirePodLock(podDir)
		if err == nil {
			l2.Release()
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("lock not released after child exit: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
