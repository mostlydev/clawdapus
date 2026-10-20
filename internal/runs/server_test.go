package runs

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func startServer(t *testing.T, h *harness) (*Client, string) {
	t.Helper()
	// Short base path: Unix socket paths are limited to ~104 bytes on darwin.
	dir, err := os.MkdirTemp("/tmp", "clawruns-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	srv := &Server{Sup: h.sup, Pod: "pod"}
	done := make(chan error, 1)
	go func() { done <- srv.Listen(ctx, dir) }()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(dir, SocketName)); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("socket never appeared")
		}
		time.Sleep(10 * time.Millisecond)
	}
	c, err := Dial(dir, uint32(os.Geteuid()))
	if err != nil {
		t.Fatal(err)
	}
	return c, dir
}

func TestServerLaunchStatusCancelOverSocket(t *testing.T) {
	h := newHarness(t, false)
	h.sup.cfg.Now = time.Now
	c, _ := startServer(t, h)
	ctx := context.Background()

	var ident map[string]any
	if err := c.Do(ctx, "GET", "/v1/identity", nil, &ident); err != nil || ident["protocol"] != float64(ProtocolVersion) {
		t.Fatalf("identity: %v %v", err, ident)
	}
	raw, digest := specJSON(t, "x")
	body := map[string]any{"installation": "inst", "run_id": "r1", "generation": 1, "spec_digest": digest, "spec": raw,
		"authority_expires_at": time.Now().Add(10 * time.Minute)}
	var view StatusView
	if err := c.Do(ctx, "POST", "/v1/runs", body, &view); err != nil || view.State != StateAccepted || view.RunAgentID == "" {
		t.Fatalf("launch: %v %+v", err, view)
	}
	k := Key{"inst", "r1"}
	h.sup.Advance(ctx, k)
	if err := c.Do(ctx, "GET", RunPath(k, ""), nil, &view); err != nil || view.State != StateRunning {
		t.Fatalf("status: %v %+v", err, view)
	}
	go func() {
		time.Sleep(50 * time.Millisecond)
		h.sup.Advance(ctx, k)
	}()
	if err := c.Do(ctx, "POST", RunPath(k, "/cancel?wait=stopped&timeout=5s"), map[string]any{"generation": 1}, &view); err != nil {
		t.Fatal(err)
	}
	if view.State != StateStopped || !view.Revoked || !view.Cancelled {
		t.Fatalf("cancel wait: %+v", view)
	}
	err := c.Do(ctx, "POST", "/v1/runs", body, nil)
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Code != "cancelled" || apiErr.Status != http.StatusConflict {
		t.Fatalf("relaunch after cancel: %v", err)
	}
	if err := c.Do(ctx, "GET", RunPath(Key{"inst", "never"}, ""), nil, nil); !errors.As(err, &apiErr) || apiErr.Status != http.StatusNotFound {
		t.Fatalf("unknown run: %v", err)
	}
}

func TestServerRejectsMalformedRequests(t *testing.T) {
	h := newHarness(t, false)
	c, _ := startServer(t, h)
	ctx := context.Background()
	var apiErr *APIError
	if err := c.Do(ctx, "POST", "/v1/runs", map[string]any{"installation": "inst", "surprise": 1}, nil); !errors.As(err, &apiErr) || apiErr.Code != "request_invalid" {
		t.Fatalf("unknown field: %v", err)
	}
	big := strings.Repeat("a", maxRequestBytes+1)
	if err := c.Do(ctx, "POST", "/v1/runs", map[string]any{"installation": big}, nil); !errors.As(err, &apiErr) || apiErr.Code != "request_invalid" {
		t.Fatalf("oversized: %v", err)
	}
	if err := c.Do(ctx, "GET", RunPath(Key{"inst", "r1"}, "?wait=bogus"), nil, nil); !errors.As(err, &apiErr) || apiErr.Code != "request_invalid" {
		t.Fatalf("bad wait: %v", err)
	}
	// Errors never echo spec content.
	raw, _ := json.Marshal(map[string]any{"template": "worker", "task": "SECRET-TASK", "tools": []string{"nope"}})
	_, _, digest, _ := CanonicalSpec(raw)
	err := c.Do(ctx, "POST", "/v1/runs", map[string]any{"installation": "inst", "run_id": "r2", "generation": 1, "spec_digest": digest, "spec": json.RawMessage(raw), "authority_expires_at": time.Now().Add(time.Minute)}, nil)
	if !errors.As(err, &apiErr) || strings.Contains(apiErr.Message, "SECRET-TASK") {
		t.Fatalf("grant error: %v", err)
	}
}

func TestSocketDirChecks(t *testing.T) {
	base, err := os.MkdirTemp("/tmp", "clawruns-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(base)
	uid := uint32(os.Geteuid())
	_ = os.Chmod(base, 0o755)
	if err := CheckSocketDir(base, uid); err == nil {
		t.Fatalf("0755 must be refused")
	}
	_ = os.Chmod(base, 0o700)
	if err := CheckSocketDir(base, uid); err != nil {
		t.Fatalf("0700 must pass: %v", err)
	}
	if err := CheckSocketDir(base, uid+1); err == nil {
		t.Fatalf("wrong owner must be refused")
	}
	link := base + "-link"
	_ = os.Symlink(base, link)
	defer os.Remove(link)
	if err := CheckSocketDir(link, uid); err == nil {
		t.Fatalf("symlinked dir must be refused")
	}
	child := filepath.Join(base, "open", "sock")
	_ = os.MkdirAll(child, 0o700)
	_ = os.Chmod(filepath.Join(base, "open"), 0o770)
	if err := CheckSocketDir(child, uid); err == nil {
		t.Fatalf("group-writable ancestor without sticky must be refused")
	}
	if _, err := Dial(base, uid+1); err == nil {
		t.Fatalf("client must refuse a directory not owned by the expected server")
	}
}

// A request held across shutdown keeps Listen from returning, so the pod
// lock (released by the caller after Listen) cannot be taken by claw up while
// a mutation may still run.
func TestServerShutdownWaitsForInflightHandlers(t *testing.T) {
	h := newHarness(t, false)
	dir, err := os.MkdirTemp("/tmp", "clawruns-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	_ = os.Chmod(dir, 0o700)
	lock, err := AcquirePodLock(h.podDir)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	srv := &Server{Sup: h.sup, Pod: "pod"}
	listenDone := make(chan error, 1)
	go func() {
		err := srv.Listen(ctx, dir)
		lock.Release()
		listenDone <- err
	}()
	for i := 0; i < 200; i++ {
		if _, err := os.Stat(filepath.Join(dir, SocketName)); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	c, err := Dial(dir, uint32(os.Geteuid()))
	if err != nil {
		t.Fatal(err)
	}
	// Hold a mutating handler open: Launch blocks just before its accepted
	// write, ignoring request cancellation.
	entered := make(chan struct{})
	release := make(chan struct{})
	h.sup.testHookBeforeAccept = func() {
		close(entered)
		<-release
	}
	h.sup.cfg.Now = time.Now
	raw, digest := specJSON(t, "x")
	held := make(chan error, 1)
	go func() {
		held <- c.Do(context.Background(), "POST", "/v1/runs", map[string]any{"installation": "inst", "run_id": "r1", "generation": 1, "spec_digest": digest, "spec": raw, "authority_expires_at": time.Now().Add(time.Minute)}, nil)
	}()
	<-entered
	time.Sleep(100 * time.Millisecond)
	cancel()
	select {
	case <-listenDone:
		t.Fatalf("Listen returned while a handler was still in flight")
	case <-time.After(300 * time.Millisecond):
	}
	if _, err := AcquirePodLock(h.podDir); !errors.Is(err, ErrPodLocked) {
		t.Fatalf("pod lock must stay held while handlers run, got %v", err)
	}
	close(release)
	<-held
	if err := <-listenDone; err != nil {
		t.Fatal(err)
	}
	l2, err := AcquirePodLock(h.podDir)
	if err != nil {
		t.Fatalf("lock must be free after shutdown: %v", err)
	}
	l2.Release()
}
