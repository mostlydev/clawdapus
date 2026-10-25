package runs

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeEpoch(t *testing.T, podDir, body string) {
	t.Helper()
	dir := filepath.Join(podDir, ".claw-session-history")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, traceEpochFile), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func epochJSON(epoch string) string {
	return `{"schema":"cllama.trace.epoch/v1","capture_epoch":"` + epoch + `","started_at":"2026-10-25T12:00:00.123Z"}`
}

func TestCaptureEpochObservedOnceAtSpawn(t *testing.T) {
	h := newHarness(t, false)
	writeEpoch(t, h.podDir, epochJSON("epochA"))
	rec, err := h.launch("r1", 1, "do it")
	if err != nil {
		t.Fatal(err)
	}
	if rec.CaptureEpoch != "" || rec.CaptureEpochObservedAt != nil {
		t.Fatalf("accepted run must not carry an epoch before spawn: %+v", rec)
	}
	// A proxy restart between acceptance and spawn is what spawn reports.
	writeEpoch(t, h.podDir, epochJSON("epochB"))
	rec = h.advance("r1")
	if rec.State != StateRunning || rec.CaptureEpoch != "epochB" || rec.CaptureEpochObservedAt == nil || !rec.CaptureEpochObservedAt.Equal(h.now) {
		t.Fatalf("spawn did not record the observed epoch: %+v", rec)
	}
	view := statusView(rec)
	if view.CaptureEpoch != "epochB" || view.CaptureEpochObservedAt == nil {
		t.Fatalf("status does not carry the epoch: %+v", view)
	}

	// A later restart, replayed launch and further advancement never rewrite it.
	writeEpoch(t, h.podDir, epochJSON("epochC"))
	if rec, err = h.launch("r1", 1, "do it"); err != nil || rec.CaptureEpoch != "epochB" {
		t.Fatalf("replayed launch rewrote the epoch: %+v %v", rec, err)
	}
	if rec = h.advance("r1"); rec.CaptureEpoch != "epochB" {
		t.Fatalf("advancement rewrote the epoch: %q", rec.CaptureEpoch)
	}
}

func TestCaptureEpochAbsentOrInvalidNeverFailsLaunch(t *testing.T) {
	cases := map[string]string{
		"off":          "",
		"bad json":     "{",
		"wrong schema": `{"schema":"other","capture_epoch":"e1","started_at":"2026-10-25T12:00:00Z"}`,
		"bad epoch":    epochJSON("e1/../x"),
		"empty epoch":  epochJSON(""),
		"bad time":     `{"schema":"cllama.trace.epoch/v1","capture_epoch":"e1","started_at":"yesterday"}`,
		"symlink":      "symlink",
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t, false)
			var logs []string
			h.sup.cfg.Logf = func(format string, args ...any) { logs = append(logs, format) }
			switch body {
			case "":
			case "symlink":
				target := filepath.Join(t.TempDir(), "epoch.json")
				os.WriteFile(target, []byte(epochJSON("e1")), 0o644)
				os.MkdirAll(filepath.Join(h.podDir, ".claw-session-history"), 0o755)
				if err := os.Symlink(target, filepath.Join(h.podDir, ".claw-session-history", traceEpochFile)); err != nil {
					t.Fatal(err)
				}
			default:
				writeEpoch(t, h.podDir, body)
			}
			if _, err := h.launch("r1", 1, "do it"); err != nil {
				t.Fatal(err)
			}
			rec := h.advance("r1")
			if rec.State != StateRunning || rec.CaptureEpoch != "" || rec.CaptureEpochObservedAt != nil {
				t.Fatalf("expected a running run without an epoch: %+v", rec)
			}
			if raw, _ := json.Marshal(statusView(rec)); strings.Contains(string(raw), "capture_epoch") {
				t.Fatalf("status must omit the epoch: %s", raw)
			}
			if warned := len(logs) > 0; warned != (body != "") {
				t.Fatalf("warning logged=%v for %q", warned, name)
			}
		})
	}
}

func TestHealthReportsFreshCaptureEpoch(t *testing.T) {
	h := newHarness(t, false)
	srv := &Server{Sup: h.sup, Pod: "pod"}
	health := func() map[string]any {
		rr := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/v1/health", nil))
		var out map[string]any
		if rr.Code != http.StatusOK || json.Unmarshal(rr.Body.Bytes(), &out) != nil || out["ok"] != true {
			t.Fatalf("health = %d %s", rr.Code, rr.Body)
		}
		return out
	}
	if out := health(); out["capture_epoch"] != nil || out["capture_epoch_observed_at"] != nil {
		t.Fatalf("journal off must report null: %v", out)
	}
	for _, key := range []string{"capture_epoch", "capture_epoch_observed_at"} {
		if _, ok := health()[key]; !ok {
			t.Fatalf("%s must be explicit null, not absent", key)
		}
	}
	writeEpoch(t, h.podDir, epochJSON("epochA"))
	if out := health(); out["capture_epoch"] != "epochA" || out["capture_epoch_observed_at"] == nil {
		t.Fatalf("health did not read the epoch: %v", out)
	}
	writeEpoch(t, h.podDir, epochJSON("epochB"))
	if out := health(); out["capture_epoch"] != "epochB" {
		t.Fatalf("health must read fresh: %v", out)
	}
	writeEpoch(t, h.podDir, "{")
	if out := health(); out["capture_epoch"] != nil || out["capture_epoch_warning"] != errCaptureEpochInvalid.Error() {
		t.Fatalf("malformed record must be null with the fixed warning: %v", out)
	}
}

type delayingRevoker func()

func (f delayingRevoker) Revoke(context.Context, string, uint64) error { f(); return nil }

// The observation time is the read itself, not the start of a step whose
// container lookup and revocation self-test can be slow.
func TestCaptureEpochObservedAtFollowsPreflight(t *testing.T) {
	h := newHarness(t, false)
	writeEpoch(t, h.podDir, epochJSON("epochA"))
	if _, err := h.launch("r1", 1, "do it"); err != nil {
		t.Fatal(err)
	}
	h.sup.cfg.Revoker = delayingRevoker(func() {
		h.now = h.now.Add(30 * time.Second)
		writeEpoch(t, h.podDir, epochJSON("epochB"))
	})
	rec := h.advance("r1")
	if rec.CaptureEpoch != "epochB" || rec.CaptureEpochObservedAt == nil || !rec.CaptureEpochObservedAt.Equal(h.now) {
		t.Fatalf("observation %v of %q must match the read after preflight at %v", rec.CaptureEpochObservedAt, rec.CaptureEpoch, h.now)
	}
}
