package runs

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func deliverablesHome(t *testing.T) (*harness, string) {
	t.Helper()
	h := taskHomeHarness(t)
	if _, err := launchTaskHome(h, "a", "task-a"); err != nil {
		t.Fatal(err)
	}
	h.advance("a") // creates the home
	home := h.sup.homeMountSource(HomeHash("inst", "task-a"))
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	return h, home
}

func apiCodeOf(err error) (string, int) {
	var e *APIError
	if errors.As(err, &e) {
		return e.Code, e.Status
	}
	return "", 0
}

func TestDeliverablesListAndFetch(t *testing.T) {
	h, home := deliverablesHome(t)

	// No directory yet: an empty list, not an error.
	list, truncated, err := h.sup.Deliverables("inst", "task-a")
	if err != nil || len(list) != 0 || truncated {
		t.Fatalf("no directory: %v %v %v", list, truncated, err)
	}

	dir := filepath.Join(home, DeliverablesDir)
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "secret")
	_ = os.WriteFile(outside, []byte("OUTSIDE-SECRET"), 0o600)
	_ = os.WriteFile(filepath.Join(home, "workspace-secret"), []byte("HOME-SECRET"), 0o600)
	_ = os.WriteFile(filepath.Join(dir, "report.md"), []byte("# Report\n"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "b notes.txt"), []byte("notes"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "big.bin"), make([]byte, MaxDeliverableBytes+1), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "exact.bin"), make([]byte, MaxDeliverableBytes), 0o644)
	_ = os.WriteFile(filepath.Join(dir, ".hidden"), []byte("x"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "weird;name"), []byte("x"), 0o644)
	_ = os.Symlink(outside, filepath.Join(dir, "link-out"))
	_ = os.Symlink("../workspace-secret", filepath.Join(dir, "link-home"))
	_ = os.Mkdir(filepath.Join(dir, "subdir"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, "subdir", "inner.txt"), []byte("x"), 0o644)
	if err := syscall.Mkfifo(filepath.Join(dir, "pipe"), 0o644); err != nil {
		t.Fatal(err)
	}

	list, truncated, err = h.sup.Deliverables("inst", "task-a")
	if err != nil || truncated {
		t.Fatalf("list: %v %v", truncated, err)
	}
	var names []string
	for _, d := range list {
		names = append(names, d.Name)
	}
	if got := strings.Join(names, "|"); got != "b notes.txt|exact.bin|report.md" {
		t.Fatalf("listed = %s", got)
	}
	sum := sha256.Sum256([]byte("# Report\n"))
	want := hex.EncodeToString(sum[:])
	if list[2].SHA256 != want || list[2].Bytes != 9 || list[2].ModifiedAt.IsZero() {
		t.Fatalf("report entry = %+v", list[2])
	}

	content, err := h.sup.Deliverable("inst", "task-a", "report.md", want)
	if err != nil || content.SHA256 != want || content.Bytes != 9 {
		t.Fatalf("fetch: %+v %v", content, err)
	}
	raw, _ := base64.StdEncoding.DecodeString(content.ContentBase64)
	if string(raw) != "# Report\n" {
		t.Fatalf("content = %q", raw)
	}
	// The largest served file's base64 stays well inside a 1 MiB response.
	exact, err := h.sup.Deliverable("inst", "task-a", "exact.bin", "")
	if err != nil || exact.Bytes != MaxDeliverableBytes || len(exact.ContentBase64) > 700<<10 {
		t.Fatalf("exact: %v bytes=%d b64=%d", err, exact.Bytes, len(exact.ContentBase64))
	}

	// The file changed after it was listed: the old digest is not honored.
	_ = os.WriteFile(filepath.Join(dir, "report.md"), []byte("# Changed\n"), 0o644)
	if _, err := h.sup.Deliverable("inst", "task-a", "report.md", want); func() bool { c, s := apiCodeOf(err); return c != "deliverable_changed" || s != http.StatusConflict }() {
		t.Fatalf("changed file: %v", err)
	}

	for name, wantCode := range map[string]string{
		"big.bin":             "deliverable_too_large",
		"link-out":            "deliverable_unknown",
		"link-home":           "deliverable_unknown",
		"pipe":                "deliverable_unknown",
		"subdir":              "deliverable_unknown",
		"missing.txt":         "deliverable_unknown",
		".hidden":             "deliverable_unknown",
		"weird;name":          "deliverable_unknown",
		"../workspace-secret": "deliverable_unknown",
		"subdir/inner.txt":    "deliverable_unknown",
		"..":                  "deliverable_unknown",
	} {
		got, err := h.sup.Deliverable("inst", "task-a", name, "")
		if code, _ := apiCodeOf(err); code != wantCode || got != nil {
			t.Errorf("%q: %v %+v, want %s", name, err, got, wantCode)
		}
	}
	if _, err := h.sup.Deliverable("inst", "task-a", "report.md", "ABC"); func() bool { c, _ := apiCodeOf(err); return c != "request_invalid" }() {
		t.Fatalf("bad digest: %v", err)
	}

	// Another home, and a malformed reference.
	if _, _, err := h.sup.Deliverables("inst", "no-such-home"); func() bool { c, s := apiCodeOf(err); return c != "home_unknown" || s != http.StatusNotFound }() {
		t.Fatalf("unknown home: %v", err)
	}
	if _, _, err := h.sup.Deliverables("inst", "../x"); func() bool { c, _ := apiCodeOf(err); return c != "home_invalid" }() {
		t.Fatalf("malformed home: %v", err)
	}
}

// The deliverables directory itself may be a link the worker made. It is
// not followed.
func TestDeliverablesDirectoryLinkIsNotFollowed(t *testing.T) {
	h, home := deliverablesHome(t)
	target := t.TempDir()
	_ = os.WriteFile(filepath.Join(target, "secret.txt"), []byte("OUTSIDE"), 0o644)
	if err := os.Symlink(target, filepath.Join(home, DeliverablesDir)); err != nil {
		t.Fatal(err)
	}
	list, _, err := h.sup.Deliverables("inst", "task-a")
	if err != nil || len(list) != 0 {
		t.Fatalf("linked directory was read: %v %v", list, err)
	}
	if got, err := h.sup.Deliverable("inst", "task-a", "secret.txt", ""); got != nil || err == nil {
		t.Fatalf("file fetched through a linked directory: %+v", got)
	}
}

// A directory stuffed with entries costs a bounded amount of work.
func TestDeliverablesListingIsBounded(t *testing.T) {
	h, home := deliverablesHome(t)
	dir := filepath.Join(home, DeliverablesDir)
	_ = os.Mkdir(dir, 0o755)
	for i := 0; i < MaxDeliverables+25; i++ {
		_ = os.WriteFile(filepath.Join(dir, fmt.Sprintf("f-%04d.txt", i)), []byte("x"), 0o644)
	}
	list, truncated, err := h.sup.Deliverables("inst", "task-a")
	if err != nil || len(list) != MaxDeliverables || !truncated {
		t.Fatalf("accepted bound: %d %v %v", len(list), truncated, err)
	}
	// Rejected entries count toward the scan bound too.
	for i := 0; i < maxDeliverableEntries; i++ {
		_ = os.Symlink("/nonexistent", filepath.Join(dir, fmt.Sprintf("l-%05d", i)))
	}
	list, truncated, err = h.sup.Deliverables("inst", "task-a")
	if err != nil || !truncated || len(list) > MaxDeliverables {
		t.Fatalf("scan bound: %d %v %v", len(list), truncated, err)
	}
}

func TestDeliverablesOverTheSocket(t *testing.T) {
	h, home := deliverablesHome(t)
	dir := filepath.Join(home, DeliverablesDir)
	_ = os.Mkdir(dir, 0o755)
	_ = os.WriteFile(filepath.Join(dir, "report.md"), []byte("# Report\n"), 0o644)
	c, _ := startServer(t, h)
	var listing struct {
		Deliverables []Deliverable `json:"deliverables"`
		Truncated    bool          `json:"truncated"`
	}
	if err := c.Do(context.Background(), "GET", "/v1/homes/inst/task-a/deliverables", nil, &listing); err != nil || len(listing.Deliverables) != 1 {
		t.Fatalf("list: %v %+v", err, listing)
	}
	var content DeliverableContent
	path := "/v1/homes/inst/task-a/deliverables/report.md?sha256=" + listing.Deliverables[0].SHA256
	if err := c.Do(context.Background(), "GET", path, nil, &content); err != nil || content.SHA256 != listing.Deliverables[0].SHA256 {
		t.Fatalf("fetch: %v %+v", err, content)
	}
	err := c.Do(context.Background(), "GET", "/v1/homes/inst/task-a/deliverables/report.md?sha256="+strings.Repeat("0", 64), nil, &content)
	if code, status := apiCodeOf(err); code != "deliverable_changed" || status != http.StatusConflict {
		t.Fatalf("stale digest over the socket: %v", err)
	}
}
