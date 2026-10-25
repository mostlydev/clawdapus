package pod

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseDataPolicyDefaultsAndOverride(t *testing.T) {
	const yaml = `
x-claw:
  pod: data-pod
  data-policy-defaults:
    regions: [us, eu, jp]
    attestations:
      - provider: OpenAI
        guarantees: [zero-data-retention, processing:jp, zero-data-retention]
services:
  inherits:
    image: a:latest
    x-claw:
      agent: ./AGENTS.md
  override:
    image: b:latest
    x-claw:
      agent: ./AGENTS.md
      data-policy:
        regions: [global]
        training: allowed
        retention: provider-default
        local-retention: allowed
  unset:
    image: c:latest
    x-claw:
      agent: ./AGENTS.md
      data-policy: null
`
	p, err := Parse(strings.NewReader(yaml))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	want := &DataPolicyConfig{
		Regions: []string{"eu", "jp", "us"}, Training: "none", Retention: "none", LocalRetention: "none",
		Attestations: []DataPolicyAttestation{{Provider: "openai", Guarantees: []string{"processing:jp", "zero-data-retention"}}},
	}
	if got := p.Services["inherits"].Claw.DataPolicy; !reflect.DeepEqual(got, want) {
		t.Fatalf("inherited policy = %+v, want %+v", got, want)
	}
	override := p.Services["override"].Claw.DataPolicy
	if !reflect.DeepEqual(override, &DataPolicyConfig{Regions: []string{"global"}, Training: "allowed", Retention: "provider-default", LocalRetention: "allowed"}) {
		t.Fatalf("service policy must replace the pod default entirely: %+v", override)
	}
	if p.Services["unset"].Claw.DataPolicy != nil {
		t.Fatal("explicit null suppresses the pod default, leaving the conservative default")
	}
	if got := DefaultDataPolicy(); !reflect.DeepEqual(got.Regions, []string{"eu", "us"}) || got.Training != "none" || got.Retention != "none" || got.LocalRetention != "none" || got.Attestations != nil {
		t.Fatalf("default policy is not conservative: %+v", got)
	}
}

func TestParseDataPolicyEndpoints(t *testing.T) {
	for raw, want := range map[string]string{
		"":                             "",
		"HTTPS://API.Example.com/v1/":  "https://api.example.com/v1",
		"http://ollama:11434/v1":       "http://ollama:11434/v1",
		"http://127.0.0.1:8080":        "http://127.0.0.1:8080",
		"https://eu.api.openai.com/v1": "https://eu.api.openai.com/v1",
	} {
		got, err := parseDataPolicyEndpoint(raw)
		if err != nil || got != want {
			t.Errorf("%q: got %q %v, want %q", raw, got, err, want)
		}
	}
	for _, raw := range []string{"http://api.example.com/v1", "ftp://x.example.com", "https://u:p@x.example.com", "https://x.example.com/v1?k=1", "/v1", "https://x.example.com/#f", "https://x.example.com/v1%2Fadmin", "https://x.example.com/%761"} {
		_, err := parseDataPolicyEndpoint(raw)
		if err == nil {
			t.Errorf("%q must be rejected", raw)
		} else if strings.Contains(err.Error(), "x.example") || strings.Contains(err.Error(), "u:p") {
			t.Errorf("rejection must not echo the endpoint: %v", err)
		}
	}
}

func TestParseDataPolicyRejectsInvalid(t *testing.T) {
	for name, block := range map[string]string{
		"unknown key":           "trainng: allowed",
		"empty regions":         "regions: []",
		"bad region":            "regions: [US]",
		"global with others":    "regions: [global, us]",
		"bad training":          "training: maybe",
		"bad retention":         "retention: allowed",
		"bad local retention":   "local-retention: content",
		"bad guarantee":         "attestations: [{provider: openai, guarantees: [hipaa]}]",
		"global guarantee":      "attestations: [{provider: openai, guarantees: [processing:global]}]",
		"empty guarantees":      "attestations: [{provider: openai, guarantees: []}]",
		"bad provider":          "attestations: [{provider: 'open ai', guarantees: [no-training]}]",
		"unknown attest key":    "attestations: [{provider: openai, guarantee: [no-training]}]",
		"duplicate attestation": "attestations: [{provider: openai, guarantees: [no-training]}, {provider: openai, guarantees: [zero-data-retention]}]",
		"insecure endpoint":     "attestations: [{provider: meta, endpoint: 'http://api.meta.ai/v1', guarantees: [no-training]}]",
	} {
		t.Run(name, func(t *testing.T) {
			yaml := "services:\n  a:\n    image: a:latest\n    x-claw:\n      agent: ./AGENTS.md\n      data-policy: {" + block + "}\n"
			if _, err := Parse(strings.NewReader(yaml)); err == nil || !strings.Contains(err.Error(), "data-policy") {
				t.Fatalf("expected data-policy error, got %v", err)
			}
		})
	}
}

func TestParseDataPolicyDefaultsMustBeMapping(t *testing.T) {
	for _, value := range []string{"none", "[us]", "true"} {
		yaml := "x-claw:\n  data-policy-defaults: " + value + "\nservices:\n  a:\n    image: a:latest\n    x-claw:\n      agent: ./AGENTS.md\n"
		if _, err := Parse(strings.NewReader(yaml)); err == nil || !strings.Contains(err.Error(), "data-policy-defaults must be a mapping") {
			t.Errorf("%s: expected mapping error, got %v", value, err)
		}
	}
}
