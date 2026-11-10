package runs

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mostlydev/clawdapus/internal/cllama"
)

// recompile rewrites the harness template with another model chain, as
// claw up would after a change, and returns the new template ID.
func (h *harness) recompile(models map[string]string) string {
	h.t.Helper()
	tpl, ctx, err := LoadTemplate(h.podDir, "worker")
	if err != nil {
		h.t.Fatal(err)
	}
	ctx.Input.Metadata = cllama.InjectCompiledModelPolicy(ctx.Input.Metadata, models)
	ctx.Input.Metadata["data_policy"] = cllama.DataPolicy{
		Version: 1, Regions: []string{"us", "eu"}, Training: "none", Retention: "none", LocalRetention: "metadata",
		Attestations: []cllama.DataPolicyAttestation{
			{Provider: "openai", Endpoint: "https://internal.example/v1", Guarantees: []string{"zdr"}},
			{Provider: "chatgpt", Guarantees: []string{"zdr"}},
			{Provider: "openai", Guarantees: []string{"no_training"}},
		},
	}
	if err := WriteTemplates(h.podDir, []*Template{tpl}, map[string]*ContextTemplate{"worker": ctx}); err != nil {
		h.t.Fatal(err)
	}
	next, _, err := LoadTemplate(h.podDir, "worker")
	if err != nil {
		h.t.Fatal(err)
	}
	return next.ID
}

func pinnedSpec(t *testing.T, task, templateID string) (json.RawMessage, string) {
	t.Helper()
	fields := map[string]any{"template": "worker", "task": task, "tools": []string{}}
	if templateID != "" {
		fields["template_id"] = templateID
	}
	raw, _ := json.Marshal(fields)
	_, _, digest, err := CanonicalSpec(raw)
	if err != nil {
		t.Fatal(err)
	}
	return raw, digest
}

func (h *harness) launchSpec(runID string, raw json.RawMessage, digest string) (*Record, error) {
	rec, _, err := h.sup.Launch(context.Background(), LaunchRequest{
		Key: Key{"inst", runID}, Generation: 1, SpecDigest: digest, Spec: raw,
		AuthorityExpiresAt: h.now.Add(30 * time.Minute),
	})
	if rec == nil {
		return nil, err
	}
	got, _ := h.sup.Get(Key{"inst", runID})
	return got, err
}

// A spec built against one compiled template is not rebound to another.
func TestLaunchPinnedToATemplateID(t *testing.T) {
	h := newHarness(t, false)
	first := h.recompile(map[string]string{"primary": "openai/gpt-5.6-terra", "fallback": "chatgpt/gpt-5.6-terra"})

	raw, digest := pinnedSpec(t, "queued task", first)
	unpinnedRaw, unpinnedDigest := pinnedSpec(t, "queued task", "")
	if digest == unpinnedDigest {
		t.Fatal("template_id is not part of the spec digest")
	}

	// Admitted while the template is unchanged, with its concrete chain kept.
	rec, err := h.launchSpec("a", raw, digest)
	if err != nil || rec == nil || rec.TemplateID != first {
		t.Fatalf("launch: %v %+v", err, rec)
	}
	wantChain := []ModelRef{{"primary", "openai/gpt-5.6-terra"}, {"fallback", "chatgpt/gpt-5.6-terra"}}
	if len(rec.Models) != 2 || rec.Models[0] != wantChain[0] || rec.Models[1] != wantChain[1] {
		t.Fatalf("admitted chain = %+v", rec.Models)
	}

	// The pod is recompiled with a successor model.
	second := h.recompile(map[string]string{"primary": "openai/gpt-6.1-terra", "fallback": "chatgpt/gpt-5.6-terra"})
	if second == first {
		t.Fatal("a changed chain did not change the template ID")
	}

	// A spec queued before the recompile is refused, and leaves nothing behind.
	for _, runID := range []string{"b", "b"} {
		_, err = h.launchSpec(runID, raw, digest)
		var apiE *APIError
		if !errors.As(err, &apiE) || apiE.Code != "template_changed" || apiE.Status != http.StatusConflict {
			t.Fatalf("stale pin: %v", err)
		}
		// The refusal names the template's current ID and nothing else.
		if apiE.CurrentTemplateID != second || strings.Contains(apiE.Message, "sha256:") {
			t.Fatalf("refusal = %+v", apiE)
		}
		if got, _ := h.sup.Get(Key{"inst", runID}); got != nil {
			t.Fatalf("a refused launch left a record: %+v", got)
		}
	}

	// A repeat of the already-admitted request is still that run, unchanged.
	again, err := h.launchSpec("a", raw, digest)
	if err != nil || again.TemplateID != first || again.Models[0].Ref != "openai/gpt-5.6-terra" {
		t.Fatalf("replay of an admitted run: %v %+v", err, again)
	}

	// A spec pinned to the new template, and one with no pin, are admitted
	// against the new chain.
	freshRaw, freshDigest := pinnedSpec(t, "queued task", second)
	for runID, spec := range map[string][2]any{"c": {freshRaw, freshDigest}, "d": {unpinnedRaw, unpinnedDigest}} {
		rec, err := h.launchSpec(runID, spec[0].(json.RawMessage), spec[1].(string))
		if err != nil || rec.TemplateID != second || rec.Models[0].Ref != "openai/gpt-6.1-terra" {
			t.Fatalf("%s: %v %+v", runID, err, rec)
		}
	}
}

func TestPinnedLaunchReservesNoHomeWhenRefused(t *testing.T) {
	h := taskHomeHarness(t)
	stale := "sha256:" + strings.Repeat("0", 64)
	raw, _ := json.Marshal(map[string]any{"template": "worker", "task": "task", "tools": []string{}, "home": "task-a", "template_id": stale})
	_, _, digest, err := CanonicalSpec(raw)
	if err != nil {
		t.Fatal(err)
	}
	_, err = h.launchSpec("a", raw, digest)
	var apiE *APIError
	if !errors.As(err, &apiE) || apiE.Code != "template_changed" {
		t.Fatalf("stale pin: %v", err)
	}
	// The home is free for a correctly built spec.
	if _, err := launchTaskHome(h, "b", "task-a"); err != nil {
		t.Fatalf("home was reserved by a refused launch: %v", err)
	}
}

func TestTemplateIDMustBeWellFormed(t *testing.T) {
	for _, bad := range []any{"", "worker", "sha256:abc", "sha256:" + strings.Repeat("A", 64), "sha256:" + strings.Repeat("0", 64) + "\n", 7} {
		raw, _ := json.Marshal(map[string]any{"template": "worker", "task": "x", "tools": []string{}, "template_id": bad})
		_, _, _, err := CanonicalSpec(raw)
		var apiE *APIError
		if !errors.As(err, &apiE) || apiE.Code != "spec_invalid" || apiE.Status != http.StatusBadRequest {
			t.Errorf("template_id %q: %v", bad, err)
		}
	}
}

// The listing tells an orchestrator which template to pin and which models
// it resolves to, and nothing else.
func TestTemplatesListing(t *testing.T) {
	h := newHarness(t, false)
	id := h.recompile(map[string]string{"primary": "openai/gpt-6.1-terra", "fallback": "chatgpt/gpt-6.1-terra"})
	h.sup.cfg.Now = time.Now
	c, _ := startServer(t, h)

	var raw json.RawMessage
	if err := c.Do(context.Background(), "GET", "/v1/templates", nil, &raw); err != nil {
		t.Fatal(err)
	}
	var got struct {
		Templates []TemplateInfo `json:"templates"`
		Invalid   []string       `json:"invalid"`
	}
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&got); err != nil {
		t.Fatal(err)
	}
	if len(got.Templates) != 1 || len(got.Invalid) != 0 {
		t.Fatalf("listing = %s", raw)
	}
	tpl := got.Templates[0]
	if tpl.Name != "worker" || tpl.ID != id || !strings.HasPrefix(tpl.ContextDigest, "sha256:") {
		t.Fatalf("template = %+v", tpl)
	}
	if len(tpl.Models) != 2 || tpl.Models[0] != (ModelRef{"primary", "openai/gpt-6.1-terra"}) || tpl.Models[1] != (ModelRef{"fallback", "chatgpt/gpt-6.1-terra"}) {
		t.Fatalf("models = %+v", tpl.Models)
	}
	if tpl.Profile != ProfileStateless || len(tpl.ContentTargets) != 0 {
		t.Fatalf("profile = %q, content = %v", tpl.Profile, tpl.ContentTargets)
	}
	dp := tpl.DataPolicy
	if dp == nil || strings.Join(dp.Regions, ",") != "us,eu" || dp.Training != "none" || dp.Retention != "none" ||
		dp.LocalRetention != "metadata" || strings.Join(dp.AttestedProviders, ",") != "chatgpt,openai" {
		t.Fatalf("data policy = %+v", dp)
	}
	// The harness context holds a service credential, tool endpoints, the
	// contract and an attestation endpoint. None of it is listed.
	for _, private := range []string{"svc-secret", "http://api:80", "# worker", "# clawdapus", "api.get", "sha256:img", "internal.example", "zdr"} {
		if strings.Contains(string(raw), private) {
			t.Fatalf("listing leaks %q: %s", private, raw)
		}
	}

	// The status of an admitted run names the template and chain it got.
	specRaw, digest := pinnedSpec(t, "x", id)
	var view StatusView
	body := map[string]any{"installation": "inst", "run_id": "r1", "generation": 1, "spec_digest": digest, "spec": specRaw,
		"authority_expires_at": time.Now().Add(10 * time.Minute)}
	if err := c.Do(context.Background(), "POST", "/v1/runs", body, &view); err != nil {
		t.Fatal(err)
	}
	if view.TemplateID != id || len(view.Models) != 2 || view.Models[0].Ref != "openai/gpt-6.1-terra" {
		t.Fatalf("status = %+v", view)
	}

	// The stale-pin refusal survives the HTTP boundary with the current ID.
	current := h.recompile(map[string]string{"primary": "openai/gpt-6.1-sol"})
	body["run_id"] = "stale-http"
	var refusal *APIError
	if err := c.Do(context.Background(), "POST", "/v1/runs", body, nil); !errors.As(err, &refusal) || refusal.Status != http.StatusConflict || refusal.Code != "template_changed" || refusal.CurrentTemplateID != current {
		t.Fatalf("HTTP stale-pin refusal = %+v (%v)", refusal, err)
	}

	// A template whose files no longer verify is named as invalid and never
	// described from its stale parts.
	ctxPath := TemplateDir(h.podDir) + "/worker.context.json"
	data, err := os.ReadFile(ctxPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ctxPath, []byte(strings.Replace(string(data), "gpt-6.1-sol", "gpt-9.9-other", 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := c.Do(context.Background(), "GET", "/v1/templates", nil, &raw); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "gpt-9.9-other") || !strings.Contains(string(raw), `"invalid":["worker"]`) {
		t.Fatalf("tampered template was described: %s", raw)
	}
}

func TestTemplatesListingNamesTaskHomeProfile(t *testing.T) {
	h := taskHomeHarness(t)
	compiled, ctx, err := LoadTemplate(h.podDir, "worker")
	if err != nil {
		t.Fatal(err)
	}
	compiled.Content = []ContentMount{{Source: "/private/reference-host-canary", Target: "/reference/handbook"}}
	if err := WriteTemplates(h.podDir, []*Template{compiled}, map[string]*ContextTemplate{"worker": ctx}); err != nil {
		t.Fatal(err)
	}
	valid, invalid, err := DescribeTemplates(h.podDir)
	if err != nil || len(invalid) != 0 || len(valid) != 1 {
		t.Fatalf("listing: %v %v %v", valid, invalid, err)
	}
	if len(valid[0].ContentTargets) != 1 || valid[0].ContentTargets[0] != "/reference/handbook" {
		t.Fatalf("content targets = %v", valid[0].ContentTargets)
	}
	if valid[0].Profile != ProfileTaskHome {
		t.Fatalf("profile = %q", valid[0].Profile)
	}
	tpl, _, err := LoadTemplate(h.podDir, "worker")
	if err != nil {
		t.Fatal(err)
	}
	for i, mount := range tpl.Content {
		if valid[0].ContentTargets[i] != mount.Target {
			t.Fatalf("content targets = %v", valid[0].ContentTargets)
		}
	}
	raw, _ := json.Marshal(valid)
	for _, mount := range tpl.Content {
		if mount.Source != "" && strings.Contains(string(raw), mount.Source) {
			t.Fatalf("listing leaks a host path: %s", raw)
		}
	}
}
