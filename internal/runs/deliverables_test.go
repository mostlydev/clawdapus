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
	"time"
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

	// A home with nothing in it yet: an empty list, not an error.
	list, truncated, err := h.sup.Deliverables("inst", "task-a")
	if err != nil || len(list) != 0 || truncated {
		t.Fatalf("no directory: %v %v %v", list, truncated, err)
	}

	dir := filepath.Join(home, DeliverablesDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
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
	// The worker replaces the supervisor's directory with a link of its own.
	if err := os.Remove(filepath.Join(home, DeliverablesDir)); err != nil {
		t.Fatal(err)
	}
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
	_ = os.MkdirAll(dir, 0o755)
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
	_ = os.MkdirAll(dir, 0o755)
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

// Being refused is not the same as there being nothing: an unreadable
// directory or file is an error, never an empty list or a missing file.
func TestUnreadableDeliverablesAreNotReportedAsAbsent(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads everything")
	}
	unreadable := func(err error) bool {
		code, status := apiCodeOf(err)
		return code == "deliverables_unreadable" && status == http.StatusServiceUnavailable
	}
	h, home := deliverablesHome(t)
	dir := filepath.Join(home, DeliverablesDir)
	_ = os.MkdirAll(dir, 0o755)
	_ = os.WriteFile(filepath.Join(dir, "report.md"), []byte("# Report\n"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "private.md"), []byte("secret"), 0o644)

	// One file cannot be read: the list is refused, not shortened.
	_ = os.Chmod(filepath.Join(dir, "private.md"), 0)
	if list, _, err := h.sup.Deliverables("inst", "task-a"); !unreadable(err) || list != nil {
		t.Fatalf("list with an unreadable file: %v %v", list, err)
	}
	if got, err := h.sup.Deliverable("inst", "task-a", "private.md", ""); !unreadable(err) || got != nil {
		t.Fatalf("fetch of an unreadable file: %+v %v", got, err)
	}
	// A readable file beside it can still be fetched by name.
	if got, err := h.sup.Deliverable("inst", "task-a", "report.md", ""); err != nil || got.Bytes != 9 {
		t.Fatalf("fetch of the readable file: %+v %v", got, err)
	}
	_ = os.Chmod(filepath.Join(dir, "private.md"), 0o644)

	// The directory cannot be opened.
	_ = os.Chmod(dir, 0)
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	if list, _, err := h.sup.Deliverables("inst", "task-a"); !unreadable(err) || list != nil {
		t.Fatalf("list of an unreadable directory: %v %v", list, err)
	}
	if got, err := h.sup.Deliverable("inst", "task-a", "report.md", ""); !unreadable(err) || got != nil {
		t.Fatalf("fetch from an unreadable directory: %+v %v", got, err)
	}
}

// A new home has a deliverables directory the supervisor made and the
// worker can write into.
func TestHomeIsCreatedWithADeliverablesDirectory(t *testing.T) {
	h, home := deliverablesHome(t)
	info, err := os.Lstat(filepath.Join(home, DeliverablesDir))
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0o777 {
		t.Fatalf("deliverables directory: %v %v", info, err)
	}
	if list, _, err := h.sup.Deliverables("inst", "task-a"); err != nil || len(list) != 0 {
		t.Fatalf("fresh home: %v %v", list, err)
	}
}

// TestDeliverablesCrossUserFixture runs against a pod directory prepared by
// another user. It is the supervisor's half of the cross-user proof, run as
// an unprivileged user inside a Linux container. See
// internal/runs/testdata/cross_uid.sh.
func TestDeliverablesCrossUserFixture(t *testing.T) {
	podDir := os.Getenv("CLAW_DELIVERABLES_FIXTURE")
	if podDir == "" {
		t.Skip("set CLAW_DELIVERABLES_FIXTURE to a prepared pod directory")
	}
	sup := &Supervisor{cfg: Config{PodDir: podDir}}
	// published: written by the worker's user and published by the wrapper.
	list, truncated, err := sup.Deliverables("inst", "published")
	if err != nil || truncated || len(list) != 1 || list[0].Name != "report.md" {
		t.Fatalf("published home: %+v %v %v", list, truncated, err)
	}
	content, err := sup.Deliverable("inst", "published", "report.md", list[0].SHA256)
	if err != nil || content.Bytes != list[0].Bytes {
		t.Fatalf("published fetch: %+v %v", content, err)
	}
	// denied: written by the worker's user under its private umask and not
	// published. It must be an error, not an empty list.
	for _, home := range []string{"denied-file", "denied-dir"} {
		list, _, err := sup.Deliverables("inst", home)
		if code, _ := apiCodeOf(err); code != "deliverables_unreadable" || list != nil {
			t.Fatalf("%s: %+v %v", home, list, err)
		}
		if got, err := sup.Deliverable("inst", home, "report.md", ""); func() bool { c, _ := apiCodeOf(err); return c != "deliverables_unreadable" }() || got != nil {
			t.Fatalf("%s fetch: %+v %v", home, got, err)
		}
	}
	t.Logf("uid %d read a deliverable written by another user and was refused the unpublished ones by name", os.Geteuid())
}

// TestDeliverablesCrossUserPrepare lays out homes the way the supervisor
// does, as the supervisor's user, for cross_uid.sh to fill in as the
// worker's user. It prints each home's data directory.
func TestDeliverablesCrossUserPrepare(t *testing.T) {
	podDir := os.Getenv("CLAW_DELIVERABLES_PREPARE")
	if podDir == "" {
		t.Skip("set CLAW_DELIVERABLES_PREPARE to an empty pod directory")
	}
	sup := &Supervisor{cfg: Config{PodDir: podDir, Now: time.Now}}
	for _, home := range []string{"published", "denied-file", "denied-dir"} {
		rec := &Record{Key: Key{"inst", "run-" + home}, HomeHash: HomeHash("inst", home), Spec: &Spec{Home: home}}
		if err := sup.ensureHome(rec); err != nil {
			t.Fatal(err)
		}
		data := sup.homeMountSource(rec.HomeHash)
		if home != "denied-file" {
			// The worker replaces the supervisor's directory with its own,
			// as a worker that removes and recreates it would.
			if err := os.Remove(filepath.Join(data, DeliverablesDir)); err != nil {
				t.Fatal(err)
			}
		}
		fmt.Printf("HOME %s %s\n", home, data)
	}
}
