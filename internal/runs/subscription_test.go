package runs

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mostlydev/clawdapus/internal/cllama"
)

// The model chain is the routing order: which credential is tried first and
// what it falls back to. It is part of the template's identity, so a run
// accepted under one chain can never spawn under another.
func TestModelChainChangesTemplateIdentity(t *testing.T) {
	digest := func(models map[string]string, token string) string {
		t.Helper()
		meta := cllama.InjectCompiledModelPolicy(map[string]any{"token": token}, models)
		ctx := &ContextTemplate{Input: cllama.AgentContextInput{AgentID: "worker", Metadata: meta}}
		d, err := ctx.digest()
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	planFirst := map[string]string{"primary": "chatgpt/gpt-5.6-terra", "fallback": "openai/gpt-5.6-terra"}
	base := digest(planFirst, "tok-1")
	if digest(planFirst, "tok-2") != base {
		t.Fatal("credential rotation must not change the context digest")
	}
	for name, models := range map[string]map[string]string{
		"key first":        {"primary": "openai/gpt-5.6-terra", "fallback": "chatgpt/gpt-5.6-terra"},
		"plan only":        {"primary": "chatgpt/gpt-5.6-terra"},
		"key only":         {"primary": "openai/gpt-5.6-terra"},
		"another fallback": {"primary": "chatgpt/gpt-5.6-terra", "fallback": "vercel/openai/gpt-5.6-luna"},
		"longer chain":     {"primary": "chatgpt/gpt-5.6-terra", "fallback": "openai/gpt-5.6-terra", "fallback-2": "vercel/openai/gpt-5.6-luna"},
	} {
		if digest(models, "tok-1") == base {
			t.Errorf("%s: a changed chain did not change the context digest", name)
		}
	}
}

// A run accepted under one chain is refused at spawn once the template has
// been recompiled with another.
func TestQueuedRunRejectsChangedChain(t *testing.T) {
	h := newHarness(t, false)
	if _, err := h.launch("r1", 1, "do it"); err != nil {
		t.Fatal(err)
	}
	tpl, ctx, err := LoadTemplate(h.podDir, "worker")
	if err != nil {
		t.Fatal(err)
	}
	before := tpl.ID
	ctx.Input.Metadata = cllama.InjectCompiledModelPolicy(ctx.Input.Metadata, map[string]string{"primary": "chatgpt/gpt-5.6-terra", "fallback": "openai/gpt-5.6-terra"})
	if err := WriteTemplates(h.podDir, []*Template{tpl}, map[string]*ContextTemplate{"worker": ctx}); err != nil {
		t.Fatal(err)
	}
	if tpl.ID == before {
		t.Fatal("the chain did not change the template ID")
	}
	rec := h.advance("r1")
	for i := 0; i < 6 && !rec.State.Terminal(); i++ {
		rec = h.advance("r1")
	}
	if rec.Failure != ReasonMismatch || rec.SpawnAttempted || h.exec.count(rec.RunKey) != 0 {
		t.Fatalf("a queued run must not spawn under a changed chain: %+v", rec)
	}
}

// reportingRevoker acknowledges like the fake and adds a proxy report.
type reportingRevoker struct {
	*fakeRevoker
	probeBoot string
	report    *RevokeReport
}

func (r *reportingRevoker) RevokeWithReport(ctx context.Context, agentID string, generation uint64) (*RevokeReport, error) {
	if err := r.fakeRevoker.Revoke(ctx, agentID, generation); err != nil {
		return nil, err
	}
	if strings.HasPrefix(agentID, "_claw-runs-probe-") {
		return &RevokeReport{BootID: r.probeBoot}, nil
	}
	return r.report, nil
}

func runToEnd(t *testing.T, h *harness, runID string) *Record {
	t.Helper()
	rec := h.advance(runID)
	h.exec.stdout = []byte("\n>> done\n")
	h.exec.exit(rec.RunKey, 0)
	for i := 0; i < 8 && !rec.State.Terminal(); i++ {
		rec = h.advance(runID)
	}
	if !rec.State.Terminal() {
		t.Fatalf("run did not finish: %+v", rec)
	}
	return rec
}

func TestServedCredentialsAreRecordedFromTheAcknowledgement(t *testing.T) {
	const boot = "0123456789abcdef0123456789abcdef"
	cases := map[string]struct {
		probeBoot string
		report    *RevokeReport
		want      *ServedInfo
	}{
		"same proxy process": {
			boot, &RevokeReport{BootID: boot, ServedBy: map[string]int{"subscription": 3, "api_key": 1}, PlanFailure: "subscription_limit_reached"},
			&ServedInfo{By: map[string]int{"subscription": 3, "api_key": 1}, Complete: true, PlanFailure: "subscription_limit_reached"},
		},
		"proxy restarted during the run": {
			boot, &RevokeReport{BootID: "ffffffffffffffffffffffffffffffff", ServedBy: map[string]int{"api_key": 1}},
			&ServedInfo{By: map[string]int{"api_key": 1}, Complete: false},
		},
		"plan failure and nothing served": {
			boot, &RevokeReport{BootID: boot, PlanFailure: "subscription_signin_required"},
			&ServedInfo{Complete: true, PlanFailure: "subscription_signin_required"},
		},
		"malformed report, same process": {
			boot, &RevokeReport{BootID: boot, ServedBy: map[string]int{"api_key": 1}, Partial: true},
			&ServedInfo{By: map[string]int{"api_key": 1}, Complete: false},
		},
		"nothing to report":       {boot, &RevokeReport{BootID: boot}, nil},
		"proxy without a boot ID": {"", &RevokeReport{ServedBy: map[string]int{"api_key": 2}}, nil},
		"no report":               {boot, nil, nil},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t, false)
			h.sup.cfg.Revoker = &reportingRevoker{fakeRevoker: h.rev, probeBoot: tc.probeBoot, report: tc.report}
			if _, err := h.launch("r1", 1, "do it"); err != nil {
				t.Fatal(err)
			}
			rec := runToEnd(t, h, "r1")
			got, _ := json.Marshal(rec.Served)
			want, _ := json.Marshal(tc.want)
			if string(got) != string(want) {
				t.Fatalf("served:\n got %s\nwant %s", got, want)
			}
			if rec.ProxyBoot != tc.probeBoot {
				t.Fatalf("proxy boot %q, want %q", rec.ProxyBoot, tc.probeBoot)
			}
			view, _ := json.Marshal(statusView(rec))
			if (tc.want != nil) != strings.Contains(string(view), `"served"`) {
				t.Fatalf("status view: %s", view)
			}
		})
	}
}

func TestEndCallbackServedFields(t *testing.T) {
	var last string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		last = string(raw)
	}))
	defer server.Close()
	notifier, err := NewHTTPNotifier(server.URL, "/runs/{run_id}")
	if err != nil {
		t.Fatal(err)
	}
	key := Key{"inst", "r1"}
	const home = "0123456789abcdef0123456789abcdef"
	for name, tc := range map[string]struct {
		rec                 *Record
		outcome, reason, in string
		want                string
	}{
		"stateless run with a report": {
			// The stateless body is byte-for-byte what it was.
			&Record{Key: key, Generation: 1, Served: &ServedInfo{By: map[string]int{"api_key": 4}, Complete: true, PlanFailure: "subscription_limit_reached"}}, "succeeded", "", "ok",
			`{"generation":1,"installation":"inst","outcome":"succeeded","reason":"","run_id":"r1","summary":"ok","summary_truncated":false}`,
		},
		"no report": {
			&Record{Key: key, Generation: 1}, "succeeded", "", "ok",
			`{"generation":1,"installation":"inst","outcome":"succeeded","reason":"","run_id":"r1","summary":"ok","summary_truncated":false}`,
		},
		"plan served, key fallback": {
			&Record{Key: key, Generation: 1, HomeHash: home, Served: &ServedInfo{By: map[string]int{"subscription": 3, "api_key": 1}, Complete: true, PlanFailure: "subscription_limit_reached"}}, "succeeded", "", "ok",
			`{"generation":1,"installation":"inst","last_plan_failure":"subscription_limit_reached","outcome":"succeeded","reason":"","run_id":"r1","served_by":{"api_key":1,"subscription":3},"served_by_complete":true,"summary":"ok","summary_truncated":false}`,
		},
		"partial counts": {
			&Record{Key: key, Generation: 1, HomeHash: home, Served: &ServedInfo{By: map[string]int{"api_key": 2}}}, "succeeded", "", "ok",
			`{"generation":1,"installation":"inst","outcome":"succeeded","reason":"","run_id":"r1","served_by":{"api_key":2},"served_by_complete":false,"summary":"ok","summary_truncated":false}`,
		},
		"plan only, sign-in missing": {
			&Record{Key: key, Generation: 2, HomeHash: home, Served: &ServedInfo{Complete: true, PlanFailure: "subscription_signin_required"}}, "failed", "exit_nonzero", "",
			`{"generation":2,"installation":"inst","last_plan_failure":"subscription_signin_required","outcome":"failed","reason":"exit_nonzero","run_id":"r1","summary":"","summary_truncated":false}`,
		},
	} {
		if err := notifier.Ended(context.Background(), tc.rec, "credential", tc.outcome, tc.reason, []byte(tc.in)); err != nil {
			t.Fatal(err)
		}
		if last != tc.want {
			t.Errorf("%s:\n got %s\nwant %s", name, last, tc.want)
		}
	}
}

func TestRevokeReportIsParsedAndBounded(t *testing.T) {
	r := revokerFor(t, func(w http.ResponseWriter, req *http.Request) {
		_, _ = io.WriteString(w, `{"agent_id":"worker-run-1","generation":3,"state":"revoked","boot_id":"0123456789abcdef0123456789abcdef",
			"served_by":{"subscription":2,"api_key":0,"Bad Class":9,"x":-1},"last_plan_failure":"subscription_limit_reached"}`)
	})
	report, err := r.RevokeWithReport(context.Background(), "worker-run-1", 3)
	if err != nil {
		t.Fatal(err)
	}
	if report.BootID != "0123456789abcdef0123456789abcdef" || report.PlanFailure != "subscription_limit_reached" || len(report.ServedBy) != 1 || report.ServedBy["subscription"] != 2 {
		t.Fatalf("report: %+v", report)
	}
	// Dropped entries make what remains a subset, never a complete count.
	if !report.Partial {
		t.Fatal("a report with dropped entries must be marked partial")
	}

	// A malformed report never turns a valid acknowledgement into a failure,
	// and none of it is passed on.
	r = revokerFor(t, func(w http.ResponseWriter, req *http.Request) {
		_, _ = io.WriteString(w, `{"agent_id":"worker-run-1","generation":3,"state":"revoked","boot_id":"<script>","last_plan_failure":"rm -rf /"}`)
	})
	report, err = r.RevokeWithReport(context.Background(), "worker-run-1", 3)
	if err != nil || report.BootID != "" || report.PlanFailure != "" || report.ServedBy != nil {
		t.Fatalf("report: %+v err %v", report, err)
	}

	// An older proxy sends only the acknowledgement.
	r = revokerFor(t, func(w http.ResponseWriter, req *http.Request) {
		_, _ = io.WriteString(w, `{"agent_id":"worker-run-1","generation":3,"state":"revoked"}`)
	})
	if report, err = r.RevokeWithReport(context.Background(), "worker-run-1", 3); err != nil || report.BootID != "" || report.PlanFailure != "" || report.ServedBy != nil {
		t.Fatalf("report: %+v err %v", report, err)
	}
}

func TestSubscriptionStatusRelay(t *testing.T) {
	var gotAuth, gotPath string
	r := revokerFor(t, func(w http.ResponseWriter, req *http.Request) {
		gotAuth, gotPath = req.Header.Get("Authorization"), req.URL.Path
		_, _ = io.WriteString(w, `{"subscriptions":[
			{"provider":"chatgpt","state":"signed_in","account":"operator@example.com","limited_until":"2026-11-10T12:00:00Z","access_token":"leak-canary"},
			{"provider":"other","state":"made_up"}],
			"served_by":{"worker-run-1":{"api_key":1}},"last_failure":{"worker-run-1":"subscription_limit_reached"}}`)
	})
	statuses, err := r.Subscriptions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer ui-secret" || gotPath != "/subscriptions" {
		t.Fatalf("auth=%q path=%q", gotAuth, gotPath)
	}
	if len(statuses) != 1 || statuses[0].Provider != "chatgpt" || statuses[0].State != "signed_in" || statuses[0].LimitedUntil == "" {
		t.Fatalf("statuses: %+v", statuses)
	}

	// The supervisor relays the states and nothing else.
	h := newHarness(t, false)
	h.sup.cfg.Revoker = r
	srv := &Server{Sup: h.sup}
	w := httptest.NewRecorder()
	srv.handleSubscriptions(w, httptest.NewRequest(http.MethodGet, "/v1/subscriptions", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("relay: %d %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	for _, forbidden := range []string{"canary", "served_by", "last_failure", "worker-run-1", "made_up"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("relay passed %q on: %s", forbidden, body)
		}
	}
	if !strings.Contains(body, `"state":"signed_in"`) {
		t.Fatalf("relay: %s", body)
	}

	// A proxy without plan routes is reported as such, not as an error state.
	old := revokerFor(t, func(w http.ResponseWriter, req *http.Request) { http.NotFound(w, req) })
	h.sup.cfg.Revoker = old
	w = httptest.NewRecorder()
	srv.handleSubscriptions(w, httptest.NewRequest(http.MethodGet, "/v1/subscriptions", nil))
	if w.Code != http.StatusNotImplemented || !strings.Contains(w.Body.String(), "subscriptions_unsupported") {
		t.Fatalf("old proxy: %d %s", w.Code, w.Body.String())
	}
	// The plain fake revoker cannot report either.
	h.sup.cfg.Revoker = h.rev
	w = httptest.NewRecorder()
	srv.handleSubscriptions(w, httptest.NewRequest(http.MethodGet, "/v1/subscriptions", nil))
	if w.Code != http.StatusNotImplemented {
		t.Fatalf("no reporter: %d", w.Code)
	}
}

func TestMalformedAdvisoryReportDoesNotInvalidateRevocation(t *testing.T) {
	for _, report := range []string{
		`"served_by":{"subscription":"bad"}`,
		`"served_by":[1,2]`,
		`"boot_id":42`,
		`"last_plan_failure":true`,
	} {
		t.Run(report, func(t *testing.T) {
			r := revokerFor(t, func(w http.ResponseWriter, req *http.Request) {
				_, _ = io.WriteString(w, `{"agent_id":"worker-run-1","generation":3,"state":"revoked",`+report+`}`)
			})
			got, err := r.RevokeWithReport(context.Background(), "worker-run-1", 3)
			if err != nil {
				t.Fatalf("advisory report invalidated a durable revocation ACK: %v", err)
			}
			if got == nil || !got.Partial {
				t.Fatalf("malformed report must be marked partial: %+v", got)
			}
		})
	}
}
