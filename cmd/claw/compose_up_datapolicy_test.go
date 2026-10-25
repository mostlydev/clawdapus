package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mostlydev/clawdapus/internal/driver"
	"github.com/mostlydev/clawdapus/internal/pod"
)

func TestAgentDataPolicyIsExplicitAndConservativeByDefault(t *testing.T) {
	p := &pod.Pod{Services: map[string]*pod.Service{
		"plain": {Claw: &pod.ClawBlock{}},
		"open": {Claw: &pod.ClawBlock{DataPolicy: &pod.DataPolicyConfig{
			Regions: []string{"jp", "us"}, Training: "none", Retention: "none", LocalRetention: "allowed",
			Attestations: []pod.DataPolicyAttestation{{Provider: "openai", Guarantees: []string{"processing:jp", "zero-data-retention"}}},
		}}},
	}}
	for name, want := range map[string]string{
		"plain":   `{"data_policy":{"version":1,"regions":["eu","us"],"training":"none","retention":"none","local_retention":"none","attestations":[]}}`,
		"missing": `{"data_policy":{"version":1,"regions":["eu","us"],"training":"none","retention":"none","local_retention":"none","attestations":[]}}`,
		"open":    `{"data_policy":{"version":1,"regions":["jp","us"],"training":"none","retention":"none","local_retention":"allowed","attestations":[{"provider":"openai","endpoint":"","guarantees":["processing:jp","zero-data-retention"]}]}}`,
	} {
		raw, err := json.Marshal(injectAgentDataPolicy(map[string]any{}, agentDataPolicy(p, name)))
		if err != nil {
			t.Fatal(err)
		}
		if string(raw) != want {
			t.Errorf("%s:\n got %s\nwant %s", name, raw, want)
		}
	}
	// The compiled policy must not alias the pod's slices.
	compiled := agentDataPolicy(p, "open")
	compiled.Regions[0] = "xx"
	if p.Services["open"].Claw.DataPolicy.Regions[0] != "jp" {
		t.Fatal("compiled policy aliases the pod declaration")
	}
}

func TestDataPolicyCoverageWarnsForClawsOutsideCllama(t *testing.T) {
	warnings := dataPolicyCoverageWarnings(map[string]*driver.ResolvedClaw{
		"governed": {Cllama: []string{"passthrough"}},
		"direct":   {},
	})
	if len(warnings) != 1 || !strings.Contains(warnings[0], `"direct"`) {
		t.Fatalf("warnings = %v", warnings)
	}
}
