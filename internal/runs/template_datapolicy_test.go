package runs

import (
	"testing"

	"github.com/mostlydev/clawdapus/internal/cllama"
)

// A data policy change must change the run template's identity, so a run
// accepted under one policy can never spawn under another; credential rotation
// must not.
func TestDataPolicyChangesTemplateIdentity(t *testing.T) {
	policy := func(mut func(*cllama.DataPolicy)) *cllama.DataPolicy {
		p := &cllama.DataPolicy{Version: 1, Regions: []string{"eu", "us"}, Training: "none", Retention: "none", LocalRetention: "none", Attestations: []cllama.DataPolicyAttestation{}}
		if mut != nil {
			mut(p)
		}
		return p
	}
	digest := func(p *cllama.DataPolicy, token string) string {
		t.Helper()
		ctx := &ContextTemplate{Input: cllama.AgentContextInput{AgentID: "worker", Metadata: map[string]interface{}{"token": token, "data_policy": p}}}
		d, err := ctx.digest()
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	base := digest(policy(nil), "tok-1")
	if digest(policy(nil), "tok-2") != base {
		t.Fatal("credential rotation must not change the context digest")
	}
	for name, mut := range map[string]func(*cllama.DataPolicy){
		"regions":         func(p *cllama.DataPolicy) { p.Regions = []string{"eu", "jp", "us"} },
		"training":        func(p *cllama.DataPolicy) { p.Training = "allowed" },
		"retention":       func(p *cllama.DataPolicy) { p.Retention = "provider-default" },
		"local retention": func(p *cllama.DataPolicy) { p.LocalRetention = "allowed" },
		"attestation": func(p *cllama.DataPolicy) {
			p.Attestations = []cllama.DataPolicyAttestation{{Provider: "openai", Guarantees: []string{"zero-data-retention"}}}
		},
	} {
		if digest(policy(mut), "tok-1") == base {
			t.Errorf("%s change did not change the context digest", name)
		}
	}
}
