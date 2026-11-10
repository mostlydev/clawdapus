package runs

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSplitResultHeader(t *testing.T) {
	good := `{"outcome":"answer","session":"20261109_abc-1","truncated":false}`
	h, body, ok := splitResultHeader([]byte(good + "\nhello\nworld"))
	if !ok || h.Outcome != OutcomeAnswer || h.Session != "20261109_abc-1" || string(body) != "hello\nworld" {
		t.Fatalf("good header: %+v %q %v", h, body, ok)
	}
	if h, body, ok := splitResultHeader([]byte(`{"outcome":"question","session":"s1"}`)); !ok || h.Outcome != OutcomeQuestion || len(body) != 0 {
		t.Fatalf("header with empty body: %+v %q %v", h, body, ok)
	}
	bad := map[string]string{
		"empty":             "",
		"not json":          "answer\nbody",
		"unknown outcome":   `{"outcome":"done","session":"s1"}`,
		"unknown key":       `{"outcome":"answer","session":"s1","path":"/etc"}`,
		"missing session":   `{"outcome":"answer"}`,
		"bad session":       `{"outcome":"answer","session":"../../x"}`,
		"duplicate outcome": `{"outcome":"question","outcome":"answer","session":"s1"}`,
		"duplicate session": `{"outcome":"answer","session":"s1","session":"s2"}`,
		"trailing token":    `{"outcome":"answer","session":"s1"} }`,
		"trailing value":    `{"outcome":"answer","session":"s1"} {}`,
		"array":             `[{"outcome":"answer","session":"s1"}]`,
		"oversized":         `{"outcome":"answer","session":"s1","truncated":false` + strings.Repeat(" ", maxResultHeaderBytes) + `}`,
	}
	for name, line := range bad {
		if _, _, ok := splitResultHeader([]byte(line + "\nbody")); ok {
			t.Errorf("%s: header accepted", name)
		}
	}
}

func TestTaskHomeSpecValidation(t *testing.T) {
	h := taskHomeHarness(t)
	for _, home := range []string{"", ".hidden", "a/b", "..", strings.Repeat("a", 129), "a b"} {
		raw, _ := json.Marshal(map[string]any{"template": "worker", "task": "t", "tools": []string{}, "home": home})
		if home == "" {
			raw, _ = json.Marshal(map[string]any{"template": "worker", "task": "t", "tools": []string{}})
		}
		_, _, digest, err := CanonicalSpec(raw)
		if err != nil {
			t.Fatal(err)
		}
		_, _, err = h.sup.Launch(context.Background(), LaunchRequest{Key: Key{"inst", "v"}, Generation: 1, Spec: raw, SpecDigest: digest, AuthorityExpiresAt: h.now.Add(1e12)})
		if apiCode(err) != "spec_invalid" {
			t.Errorf("home %q: got %v, want spec_invalid", home, err)
		}
	}
	// A stateless template refuses the field.
	s := newHarness(t, false)
	if _, err := launchTaskHome(s, "x", "task-a"); apiCode(err) != "spec_invalid" {
		t.Fatalf("stateless template accepted home: %v", err)
	}
}

func TestStatelessTemplateIdentityUnchanged(t *testing.T) {
	h := newHarness(t, false)
	raw, err := os.ReadFile(filepath.Join(TemplateDir(h.podDir), "worker.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{`"home"`, `"gate"`, `"content"`, `"header"`} {
		if strings.Contains(string(raw), key) {
			t.Errorf("stateless template serializes task-home field %s", key)
		}
	}
}

func TestTaskHomeLifecycleAndVerbs(t *testing.T) {
	h := taskHomeHarness(t)
	ctx := context.Background()
	if _, err := launchTaskHome(h, "a", "task-a"); err != nil {
		t.Fatal(err)
	}
	a := h.advance("a")
	src := h.sup.homeMountSource(a.HomeHash)
	if info, err := os.Stat(src); err != nil || !info.IsDir() || info.Mode().Perm() != 0o777 {
		t.Fatalf("home not prepared: %v %v", info, err)
	}
	if info, _ := os.Stat(filepath.Dir(src)); info.Mode().Perm() != 0o700 {
		t.Fatalf("home parent is not private: %v", info.Mode())
	}
	h.exec.mu.Lock()
	rendered := string(h.exec.lastFile)
	h.exec.mu.Unlock()
	if strings.Count(rendered, "source: "+src+"\n") != 2 || strings.Count(rendered, "type: bind") != 2 {
		t.Fatalf("worker and read-only meter must mount only this home %s:\n%s", src, rendered)
	}
	if strings.Contains(rendered, "source: "+filepath.Dir(src)+"\n") || strings.Contains(rendered, "source: "+h.sup.homesDir()+"\n") {
		t.Fatalf("a parent of the home is mounted:\n%s", rendered)
	}
	if strings.Contains(src, "task-a") {
		t.Fatalf("caller ID appears in the path: %s", src)
	}
	st, err := h.sup.HomeStatus("inst", "task-a")
	if err != nil || !st.Exists || st.ReservedBy != "a" || st.LastRunID != "a" {
		t.Fatalf("status while live: %+v %v", st, err)
	}
	if err := h.sup.DeleteHome(ctx, "inst", "task-a"); apiCode(err) != "home_busy" {
		t.Fatalf("delete of a reserved home: %v", err)
	}
	// Another installation's same ID is a different, free home.
	if st, err := h.sup.HomeStatus("other", "task-a"); err != nil || st.Exists || st.ReservedBy != "" {
		t.Fatalf("cross-installation status: %+v %v", st, err)
	}

	// The worker leaves hostile entries behind; deletion must not follow them.
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	_ = os.Symlink(outside, filepath.Join(src, "link"))
	_ = os.Symlink(filepath.Dir(outside), filepath.Join(src, "dirlink"))
	_ = os.MkdirAll(filepath.Join(src, "deep", "er"), 0o755)
	_ = os.WriteFile(filepath.Join(src, "deep", "er", "f"), []byte("x"), 0o600)
	_ = os.Chmod(filepath.Join(src, "deep", "er"), 0)
	_ = os.Chmod(filepath.Join(src, "deep"), 0)

	h.exec.exit(a.RunKey, 0)
	if a = h.advance("a"); !a.State.Terminal() {
		t.Fatalf("run not terminal: %+v", a)
	}
	if err := h.sup.DeleteHome(ctx, "inst", "task-a"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := os.Lstat(h.sup.homeRoot(a.HomeHash)); !os.IsNotExist(err) {
		t.Fatalf("home still present: %v", err)
	}
	if raw, err := os.ReadFile(outside); err != nil || string(raw) != "keep" {
		t.Fatalf("deletion followed a symlink: %q %v", raw, err)
	}
	if entries, _ := os.ReadDir(h.sup.homesDir()); len(entries) != 0 {
		t.Fatalf("trash left behind: %v", entries)
	}
	if err := h.sup.DeleteHome(ctx, "inst", "task-a"); err != nil {
		t.Fatalf("delete of an absent home: %v", err)
	}
	// A reset: the next run gets a new, empty home.
	if _, err := launchTaskHome(h, "b", "task-a"); err != nil {
		t.Fatal(err)
	}
	b := h.advance("b")
	if entries, _ := os.ReadDir(h.sup.homeMountSource(b.HomeHash)); len(entries) != 0 {
		t.Fatalf("home not empty after reset: %v", entries)
	}
}

func TestTaskHomeResultHeader(t *testing.T) {
	h := taskHomeHarness(t)
	if _, err := launchTaskHome(h, "a", "task-a"); err != nil {
		t.Fatal(err)
	}
	a := h.advance("a")
	h.exec.mu.Lock()
	h.exec.stdout = []byte("noise\n" + taskHomeMarker + `{"outcome":"question","session":"sess-1","truncated":false}` + "\nWhich branch?\n")
	h.exec.mu.Unlock()
	h.exec.exit(a.RunKey, 0)
	a = h.advance("a")
	if a.Result == nil || a.Result.Outcome != OutcomeQuestion || a.Result.Session != "sess-1" {
		t.Fatalf("result: %+v failure=%q", a.Result, a.Failure)
	}
	body, _, err := h.sup.Result(Key{"inst", "a"})
	if err != nil || string(body) != "Which branch?" {
		t.Fatalf("body %q %v", body, err)
	}

	// A malformed header is a missing result, never an answer.
	if _, err := launchTaskHome(h, "b", "task-b"); err != nil {
		t.Fatal(err)
	}
	b := h.advance("b")
	h.exec.mu.Lock()
	h.exec.stdout = []byte(taskHomeMarker + `{"outcome":"answer","session":"s","x":1}` + "\nbody\n")
	h.exec.mu.Unlock()
	h.exec.exit(b.RunKey, 0)
	b = h.advance("b")
	if b.Result != nil || b.Failure != ReasonResultMissing {
		t.Fatalf("malformed header accepted: %+v %q", b.Result, b.Failure)
	}
}

func TestTaskHomeAmbiguousReservationFailsClosed(t *testing.T) {
	for name, corrupt := range map[string]func(*Record){
		"missing hash":          func(r *Record) { r.HomeHash = "" },
		"mismatched hash":       func(r *Record) { r.HomeHash = HomeHash("inst", "other") },
		"hash, no home":         func(r *Record) { r.Spec.Home = "" },
		"missing spec and hash": func(r *Record) { r.Spec = nil; r.HomeHash = "" },
	} {
		h := taskHomeHarness(t)
		if _, err := launchTaskHome(h, "a", "task-a"); err != nil {
			t.Fatal(err)
		}
		rec, err := h.sup.ledger.Get(Key{"inst", "a"})
		if err != nil {
			t.Fatal(err)
		}
		corrupt(rec)
		if err := h.sup.ledger.Put(rec); err != nil {
			t.Fatal(err)
		}
		if _, err := launchTaskHome(h, "b", "unrelated"); apiCode(err) != "ledger_unavailable" {
			t.Errorf("%s: launch admitted over an ambiguous reservation: %v", name, err)
		}
		if err := h.sup.DeleteHome(context.Background(), "inst", "task-a"); apiCode(err) != "ledger_unavailable" {
			t.Errorf("%s: delete admitted over an ambiguous reservation: %v", name, err)
		}
	}
}
