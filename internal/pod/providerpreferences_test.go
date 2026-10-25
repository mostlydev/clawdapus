package pod

import (
	"strings"
	"testing"
)

func perRunPod(prefs string) string {
	return "services:\n  w:\n    image: w:latest\n    x-claw:\n      agent: ./AGENTS.md\n      lifecycle: per-run\n      models:\n        primary: openrouter/google/gemini-3.8-flash\n      provider-preferences:\n        primary: {" + prefs + "}\n"
}

func TestParseProviderPreferences(t *testing.T) {
	p, err := Parse(strings.NewReader(perRunPod(`only: [google-vertex/global, amazon-bedrock], zdr: true, allow_fallbacks: false, require_parameters: true, data_collection: deny`)))
	if err != nil {
		t.Fatal(err)
	}
	got := p.Services["w"].Claw.ProviderPreferences["primary"]
	if got == nil || len(got.Only) != 2 || got.Only[1] != "amazon-bedrock" || !*got.ZDR || *got.AllowFallbacks || !*got.RequireParameters || got.DataCollection != "deny" {
		t.Fatalf("preferences = %+v", got)
	}
}

func TestParseProviderPreferencesRejectsWideningOrUnknown(t *testing.T) {
	for name, prefs := range map[string]string{
		"zdr false":              "zdr: false",
		"fallbacks true":         "allow_fallbacks: true",
		"require false":          "require_parameters: false",
		"collection allow":       "data_collection: allow",
		"empty only":             "only: []",
		"bad slug":               "only: ['Google Vertex']",
		"too deep slug":          "only: [a/b/c/d/e]",
		"duplicate slug":         "only: [amazon-bedrock, amazon-bedrock]",
		"unknown key":            "ignore: [x]",
		"order is not narrowing": "order: [amazon-bedrock]",
		"empty":                  "",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse(strings.NewReader(perRunPod(prefs))); err == nil {
				t.Fatalf("%q must be rejected", prefs)
			}
		})
	}
	longLived := strings.Replace(perRunPod("zdr: true"), "      lifecycle: per-run\n", "", 1)
	if _, err := Parse(strings.NewReader(longLived)); err == nil || !strings.Contains(err.Error(), "requires x-claw.lifecycle") {
		t.Fatalf("long-lived service must be rejected, got %v", err)
	}
}
