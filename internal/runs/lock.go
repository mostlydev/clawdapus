package runs

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// ErrPodLocked means another process (normally `claw runs serve`) holds the
// pod's run lock.
var ErrPodLocked = errors.New("pod run lock is held by another process")

// PodLock is the single-writer fence shared by `claw runs serve`, `claw up`
// and `claw down`. The supervisor holds it for its whole lifetime.
type PodLock struct {
	f *os.File
}

// AcquirePodLock takes the pod's exclusive run lock without blocking.
func AcquirePodLock(podDir string) (*PodLock, error) {
	dir := filepath.Join(podDir, StateDirName)
	if err := EnsurePrivateDir(dir); err != nil {
		return nil, fmt.Errorf("run state dir: %w", err)
	}
	f, err := os.OpenFile(filepath.Join(dir, "lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open pod run lock: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, ErrPodLocked
		}
		return nil, fmt.Errorf("lock pod runs: %w", err)
	}
	return &PodLock{f: f}, nil
}

// Release drops the lock.
func (l *PodLock) Release() error {
	if l == nil || l.f == nil {
		return nil
	}
	err := l.f.Close()
	l.f = nil
	return err
}

// File returns the locked file so lifecycle subprocesses can inherit it: the
// flock lives on the open file description, so writer authority cannot be
// reacquired while any inheriting process still runs, even if this process
// dies.
func (l *PodLock) File() *os.File {
	if l == nil {
		return nil
	}
	return l.f
}
