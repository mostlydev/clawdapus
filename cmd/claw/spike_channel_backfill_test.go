//go:build spike

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestSpikeChannelBackfill builds the claw-wall image, runs it against a fake
// Discord HTTP server on the host, and asserts that startup backfill paginates
// backward to cover the configured retention horizon. This is the end-to-end
// proof for issue #238.
//
// Requires: Docker. Does NOT require Discord credentials.
// Run with: go test -tags spike -v -run TestSpikeChannelBackfill -timeout 5m ./cmd/claw/...
func TestSpikeChannelBackfill(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not on PATH — skipping")
	}

	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	repoRoot, err := filepath.Abs(filepath.Join(filepath.Dir(thisFile), "..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}

	// ── Build claw-wall image from the worktree under test ──────────────
	imageTag := "claw-wall:spike-channel-backfill"
	build := exec.Command("docker", "build", "-f", "dockerfiles/claw-wall/Dockerfile", "-t", imageTag, ".")
	build.Dir = repoRoot
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("docker build claw-wall: %v\n%s", err, out)
	}

	// ── Fake Discord on host, bound to 0.0.0.0 so containers can reach it
	channelID := "1234567890123456789"
	now := time.Now().UTC()
	// 360 msgs at 6min step, offset by -3min so the -24h cutoff lands
	// exactly midway between msg 120 and msg 121. Msg 120 is 3min OUTSIDE
	// the window; msg 121 is 3min INSIDE. This is robust to ≲3min of
	// startup-time drift between the test process and the wall container,
	// and pins the expected count deterministically at 239 (msgs 121..359).
	messages := makeFakeDiscordMessages(channelID, now.Add(-36*time.Hour-3*time.Minute), 360, 6*time.Minute)
	const expectedInWindow = 239

	var beforeRequests int64
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/channels/"+channelID+"/messages") {
			http.Error(w, "unexpected path", http.StatusNotFound)
			return
		}
		if r.URL.Query().Get("before") != "" {
			atomic.AddInt64(&beforeRequests, 1)
		}
		writeFakeDiscordPage(t, w, r, messages)
	})
	ts := httptest.NewUnstartedServer(handler)
	ts.Listener.Close()
	ln, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatalf("listen 0.0.0.0: %v", err)
	}
	ts.Listener = ln
	ts.Start()
	defer ts.Close()

	_, fakePort, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatalf("split host port: %v", err)
	}

	// ── Allocate a host port for the wall container ─────────────────────
	wallPortL, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("alloc wall port: %v", err)
	}
	_, wallPort, _ := net.SplitHostPort(wallPortL.Addr().String())
	wallPortL.Close()

	containerName := "claw-wall-spike-238-" + strconv.FormatInt(time.Now().UnixNano(), 36)

	// ── Run claw-wall ───────────────────────────────────────────────────
	runArgs := []string{
		"run", "--rm", "-d",
		"--name", containerName,
		"--add-host", "host.docker.internal:host-gateway",
		"-p", wallPort + ":8080",
		"-e", "CLAW_WALL_TOKENS=" + channelID + ":fake-token-zzz",
		"-e", "CLAW_WALL_DISCORD_BASE_URL=http://host.docker.internal:" + fakePort,
		"-e", "CLAW_WALL_RETENTION=24h",
		"-e", "CLAW_WALL_BACKFILL_MAX_PAGES=5",
		"-e", "CLAW_WALL_POLL_INTERVAL=3600", // suppress forward poll noise during the test
		"-e", "CLAW_WALL_LIMIT=5000",
		"-e", "CLAW_WALL_ADDR=:8080",
		imageTag,
	}
	if out, err := exec.Command("docker", runArgs...).CombinedOutput(); err != nil {
		t.Fatalf("docker run: %v\n%s", err, out)
	}
	t.Cleanup(func() {
		if logs, err := exec.Command("docker", "logs", "--tail", "200", containerName).CombinedOutput(); err == nil && t.Failed() {
			t.Logf("claw-wall logs:\n%s", logs)
		}
		_ = exec.Command("docker", "rm", "-f", containerName).Run()
	})

	// ── Wait for HTTP /health ──────────────────────────────────────────
	wallBase := "http://127.0.0.1:" + wallPort
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := waitForHealth(ctx, wallBase+"/health"); err != nil {
		t.Fatalf("wall never became healthy: %v", err)
	}

	// ── Wait for backfill to declare complete ──────────────────────────
	awareURL := wallBase + "/channel-awareness?channels=" + channelID + "&since=24h&limit=500&max_chars=200000"
	body, err := waitForBackfillComplete(ctx, awareURL)
	if err != nil {
		t.Fatalf("backfill never completed: %v\nlast body:\n%s", err, body)
	}

	// ── Assert observable behavior ─────────────────────────────────────
	if got := atomic.LoadInt64(&beforeRequests); got < 2 {
		t.Fatalf("expected ≥2 before= pagination requests, got %d", got)
	}
	if !strings.Contains(body, "backfill_status=complete") {
		t.Fatalf("expected backfill_status=complete in header, got body:\n%s", body)
	}
	wantMessages := "messages=" + strconv.Itoa(expectedInWindow)
	if !strings.Contains(body, wantMessages) {
		t.Fatalf("expected %q in header, got body:\n%s", wantMessages, body)
	}
	wantAvailable := "available=" + strconv.Itoa(expectedInWindow)
	if !strings.Contains(body, wantAvailable) {
		t.Fatalf("expected %q in header, got body:\n%s", wantAvailable, body)
	}
	wantSource := "source=" + channelID + "/1000000000000359"
	if !strings.Contains(body, wantSource) {
		t.Fatalf("expected newest message source handle %q in body, got body:\n%s", wantSource, body)
	}
	// Buffer range should start near 24h ago (the oldest in-window message
	// is at now-24h, since messages are 6min apart and index 120 = -24h
	// exactly). Allow some clock skew.
	if !strings.Contains(body, "buffer_range=") {
		t.Fatalf("missing buffer_range in header:\n%s", body)
	}
}

type fakeDiscordMessage struct {
	ID        string            `json:"id"`
	Content   string            `json:"content"`
	Timestamp string            `json:"timestamp"`
	Author    fakeDiscordAuthor `json:"author"`
	ChannelID string            `json:"channel_id,omitempty"`
}

type fakeDiscordAuthor struct {
	Username   string `json:"username"`
	GlobalName string `json:"global_name,omitempty"`
}

func makeFakeDiscordMessages(channelID string, start time.Time, count int, step time.Duration) []fakeDiscordMessage {
	out := make([]fakeDiscordMessage, 0, count)
	for i := 0; i < count; i++ {
		out = append(out, fakeDiscordMessage{
			ID:        fmt.Sprintf("%d", 1_000_000_000_000_000+int64(i)),
			Content:   fmt.Sprintf("synthetic-msg-%03d", i),
			Timestamp: start.Add(time.Duration(i) * step).Format(time.RFC3339),
			Author:    fakeDiscordAuthor{Username: fmt.Sprintf("user-%03d", i)},
			ChannelID: channelID,
		})
	}
	return out
}

// writeFakeDiscordPage mimics Discord's GET /channels/{id}/messages with
// optional limit/before/after filtering. Discord returns newest-first.
func writeFakeDiscordPage(t *testing.T, w http.ResponseWriter, r *http.Request, all []fakeDiscordMessage) {
	t.Helper()
	limit := 100
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err == nil && n > 0 && n <= 100 {
			limit = n
		}
	}
	after := strings.TrimSpace(r.URL.Query().Get("after"))
	before := strings.TrimSpace(r.URL.Query().Get("before"))

	filtered := make([]fakeDiscordMessage, 0, len(all))
	for _, m := range all {
		if after != "" && compareNumericStrings(m.ID, after) <= 0 {
			continue
		}
		if before != "" && compareNumericStrings(m.ID, before) >= 0 {
			continue
		}
		filtered = append(filtered, m)
	}
	// Take the newest `limit` from filtered.
	if len(filtered) > limit {
		filtered = filtered[len(filtered)-limit:]
	}
	// Discord returns newest-first.
	reversed := make([]fakeDiscordMessage, 0, len(filtered))
	for i := len(filtered) - 1; i >= 0; i-- {
		reversed = append(reversed, filtered[i])
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(reversed); err != nil {
		t.Fatalf("encode fake discord response: %v", err)
	}
}

func compareNumericStrings(a, b string) int {
	ai, errA := strconv.ParseUint(strings.TrimSpace(a), 10, 64)
	bi, errB := strconv.ParseUint(strings.TrimSpace(b), 10, 64)
	if errA == nil && errB == nil {
		switch {
		case ai < bi:
			return -1
		case ai > bi:
			return 1
		default:
			return 0
		}
	}
	return strings.Compare(a, b)
}

func waitForHealth(ctx context.Context, url string) error {
	deadline, _ := ctx.Deadline()
	var lastErr error
	for {
		select {
		case <-ctx.Done():
			if lastErr != nil {
				return fmt.Errorf("deadline: %w", lastErr)
			}
			return ctx.Err()
		default:
		}
		resp, err := http.Get(url)
		if err == nil {
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
			lastErr = fmt.Errorf("status %d", resp.StatusCode)
		} else {
			lastErr = err
		}
		if time.Now().After(deadline.Add(-200 * time.Millisecond)) {
			return lastErr
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func waitForBackfillComplete(ctx context.Context, url string) (string, error) {
	deadline, _ := ctx.Deadline()
	var (
		lastBody string
		lastErr  error
		mu       sync.Mutex
	)
	for {
		select {
		case <-ctx.Done():
			return lastBody, fmt.Errorf("deadline: %w", lastErr)
		default:
		}
		resp, err := http.Get(url)
		if err != nil {
			mu.Lock()
			lastErr = err
			mu.Unlock()
		} else {
			b, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			body := string(b)
			mu.Lock()
			lastBody = body
			mu.Unlock()
			if strings.Contains(body, "backfill_status=complete") {
				return body, nil
			}
			lastErr = fmt.Errorf("not yet complete")
		}
		if time.Now().After(deadline.Add(-200 * time.Millisecond)) {
			return lastBody, lastErr
		}
		time.Sleep(250 * time.Millisecond)
	}
}
