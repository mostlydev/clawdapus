package runs

import (
	"context"
	"encoding/json"
	"golang.org/x/sys/unix"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCanonicalContentSelection(t *testing.T) {
	a, _, d1, err := CanonicalSpec(json.RawMessage(`{"template":"w","task":"x","content":{"/z":"two","/a":"one"}}`))
	if err != nil {
		t.Fatal(err)
	}
	b, _, d2, err := CanonicalSpec(json.RawMessage(`{"content":{"/a":"one","/z":"two"},"task":"x","template":"w"}`))
	if err != nil || string(a) != string(b) || d1 != d2 {
		t.Fatalf("canonical mismatch %s %s %v", a, b, err)
	}
	for _, raw := range []string{`{"content":{}}`, `{"content":{"/a":1}}`, `{"content":{"/a":"../escape"}}`, `{"content":null}`} {
		if _, _, _, err := CanonicalSpec(json.RawMessage(raw)); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
}

func sealedSnapshot(t *testing.T, root, id, text string) string {
	t.Helper()
	dir := filepath.Join(root, id)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ref.txt"), []byte(text), 0444); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0755) })
	return dir
}

func TestSnapshotValidation(t *testing.T) {
	root := t.TempDir()
	dir := sealedSnapshot(t, root, "v1", "reference")
	first, err := snapshotDigest(root, "v1")
	if err != nil {
		t.Fatal(err)
	}
	again, err := snapshotDigest(root, "v1")
	if err != nil || first != again {
		t.Fatalf("unstable digest %v", err)
	}
	if err := os.Chmod(filepath.Join(dir, "ref.txt"), 0445); err != nil {
		t.Fatal(err)
	}
	modeChanged, err := snapshotDigest(root, "v1")
	if err != nil || first == modeChanged {
		t.Fatal("mode not bound")
	}
	os.Chmod(filepath.Join(dir, "ref.txt"), 0644)
	if _, err := snapshotDigest(root, "v1"); err == nil {
		t.Fatal("accepted writable file")
	}
	os.WriteFile(filepath.Join(dir, "ref.txt"), []byte("changed"), 0644)
	os.Chmod(filepath.Join(dir, "ref.txt"), 0444)
	changed, err := snapshotDigest(root, "v1")
	if err != nil || first == changed {
		t.Fatal("bytes not bound")
	}
	os.Chmod(dir, 0755)
	if _, err := snapshotDigest(root, "v1"); err == nil {
		t.Fatal("accepted writable directory")
	}
	os.Symlink(dir, filepath.Join(root, "alias"))
	if _, err := snapshotDigest(root, "alias"); err == nil {
		t.Fatal("followed snapshot symlink")
	}
	for _, kind := range []string{"link", "fifo", "oversize", "depth"} {
		t.Run(kind, func(t *testing.T) {
			r := t.TempDir()
			d := sealedSnapshot(t, r, "v", "x")
			os.Chmod(d, 0755)
			switch kind {
			case "link":
				os.Symlink("ref.txt", filepath.Join(d, "bad"))
			case "fifo":
				if err := unix.Mkfifo(filepath.Join(d, "bad"), 0444); err != nil {
					t.Fatal(err)
				}
			case "oversize":
				f, err := os.Create(filepath.Join(d, "bad"))
				if err != nil {
					t.Fatal(err)
				}
				f.Truncate(maxSnapshotBytes + 1)
				f.Chmod(0444)
				f.Close()
			case "depth":
				deep := d
				for i := 0; i < maxSnapshotDepth+1; i++ {
					deep = filepath.Join(deep, "d")
					os.Mkdir(deep, 0755)
				}
				filepath.Walk(d, func(p string, info os.FileInfo, e error) error {
					if e == nil && info.IsDir() {
						os.Chmod(p, 0555)
					}
					return e
				})
				t.Cleanup(func() {
					filepath.Walk(d, func(p string, i os.FileInfo, e error) error {
						if e == nil && i.IsDir() {
							os.Chmod(p, 0755)
						}
						return nil
					})
				})
			}
			os.Chmod(d, 0555)
			if _, err := snapshotDigest(r, "v"); err == nil {
				t.Fatalf("accepted %s", kind)
			}
		})
	}
}

func TestVersionedContentAdmissionAndSpawn(t *testing.T) {
	for _, mutate := range []bool{false, true} {
		t.Run(fmtBool(mutate), func(t *testing.T) {
			h := taskHomeHarness(t)
			root := t.TempDir()
			dir := sealedSnapshot(t, root, "v1", "first")
			tpl, ctx, err := LoadTemplate(h.podDir, "worker")
			if err != nil {
				t.Fatal(err)
			}
			tpl.Content = []ContentMount{{Source: root, Target: "/ref", Versioned: true}}
			if err := WriteTemplates(h.podDir, []*Template{tpl}, map[string]*ContextTemplate{"worker": ctx}); err != nil {
				t.Fatal(err)
			}
			raw := json.RawMessage(`{"template":"worker","task":"read","home":"task","content":{"/ref":"v1"}}`)
			_, _, digest, _ := CanonicalSpec(raw)
			rec, _, err := h.sup.Launch(context.Background(), LaunchRequest{Key: Key{"inst", "a"}, Generation: 1, Spec: raw, SpecDigest: digest, AuthorityExpiresAt: h.now.Add(time.Minute)})
			if err != nil {
				t.Fatal(err)
			}
			if len(rec.Content) != 1 || rec.Content[0].Snapshot != "v1" || rec.Content[0].ManifestDigest == "" {
				t.Fatalf("missing provenance %+v", rec.Content)
			}

			if got := statusView(rec); len(got.Content) != 1 || got.Content[0] != rec.Content[0] {
				t.Fatal("status lost provenance")
			}
			if mutate {
				os.Chmod(filepath.Join(dir, "ref.txt"), 0644)
				os.WriteFile(filepath.Join(dir, "ref.txt"), []byte("replaced"), 0644)
				os.Chmod(filepath.Join(dir, "ref.txt"), 0444)
			}
			h.advance("a")
			got, err := h.sup.Get(Key{"inst", "a"})
			if err != nil {
				t.Fatal(err)
			}
			if mutate {
				if got.Failure != "content_snapshot_changed" {
					t.Fatalf("failure %s", got.Failure)
				}
			} else {
				h.exec.mu.Lock()
				render := string(h.exec.lastFile)
				h.exec.mu.Unlock()
				if !strings.Contains(render, dir) {
					t.Fatalf("selected mount absent: %s", render)
				}
			}
		})
	}
}
func fmtBool(v bool) string {
	if v {
		return "changed"
	}
	return "unchanged"
}

func TestContentGrant(t *testing.T) {
	h := taskHomeHarness(t)
	tpl, _, err := LoadTemplate(h.podDir, "worker")
	if err != nil {
		t.Fatal(err)
	}
	tpl.Content = []ContentMount{{Target: "/ref", Versioned: true}, {Target: "/fixed"}}
	for _, tc := range []struct {
		selection map[string]string
		want      string
	}{
		{nil, "content_selection_missing"},
		{map[string]string{"/other": "v"}, "spec_invalid"},
		{map[string]string{"/fixed": "v"}, "spec_invalid"},
		{map[string]string{"/ref": "../v"}, "spec_invalid"},
	} {
		err := checkContentGrant(tpl, &Spec{Content: tc.selection})
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%v: %v", tc.selection, err)
		}
	}
	spec := &Spec{Content: map[string]string{"/ref": "v"}}
	if err := checkContentGrant(tpl, spec); err != nil {
		t.Fatal(err)
	}
	tpl.Worker.Home = nil
	if err := checkContentGrant(tpl, spec); err == nil {
		t.Fatal("stateless selector accepted")
	}
}

func TestSnapshotEntryBound(t *testing.T) {
	root := t.TempDir()
	os.Chmod(root, 0555)
	t.Cleanup(func() { os.Chmod(root, 0755) })
	f, err := os.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	w := snapshotWalk{entries: make([]snapshotEntry, maxSnapshotEntries)}
	if err := w.walk(f, "", 0); err == nil {
		t.Fatal("entry bound not enforced")
	}
}

func TestContentCanonicalUTF16KeyOrder(t *testing.T) {
	raw, _, _, err := CanonicalSpec(json.RawMessage(`{"content":{"/\ue000":"a","/\ud800\udc00":"b"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `{"content":{"/𐀀":"b","/":"a"}}` {
		t.Fatalf("not JCS UTF16 order: %s", raw)
	}
}

func TestSnapshotWorkerReadModes(t *testing.T) {
	for _, tc := range []struct{ dir, file os.FileMode }{{0500, 0444}, {0555, 0400}} {
		root := t.TempDir()
		d := sealedSnapshot(t, root, "v", "private")
		os.Chmod(filepath.Join(d, "ref.txt"), tc.file)
		os.Chmod(d, tc.dir)
		if _, err := snapshotDigest(root, "v"); err == nil {
			t.Fatalf("accepted cross-UID unreadable modes %o %o", tc.dir, tc.file)
		}
	}
}
