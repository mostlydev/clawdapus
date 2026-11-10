package main

import (
	"encoding/json"
	"testing"

	"github.com/mostlydev/clawdapus/internal/pod"
)

func TestHostedCapabilitiesAreCompiledOnlyForTheDeclaringService(t *testing.T) {
	p := &pod.Pod{Services: map[string]*pod.Service{
		"researcher": {Claw: &pod.ClawBlock{HostedCapabilities: []string{"code_interpreter", "web_search"}}},
		"writer":     {Claw: &pod.ClawBlock{}},
		"plain":      {},
	}}
	base := func() map[string]any { return map[string]any{"service": "x", "token": "t"} }

	granted := injectHostedCapabilities(base(), agentHostedCapabilities(p, "researcher"))
	raw, _ := json.Marshal(granted)
	if string(raw) != `{"hosted_capabilities":["code_interpreter","web_search"],"service":"x","token":"t"}` {
		t.Fatalf("researcher metadata = %s", raw)
	}
	// The compiled list is a copy: changing it cannot change the pod's.
	granted["hosted_capabilities"].([]string)[0] = "changed"
	if p.Services["researcher"].Claw.HostedCapabilities[0] != "code_interpreter" {
		t.Fatal("the compiled grant aliases the pod's declaration")
	}

	// A service with no grant, no x-claw block or no entry at all compiles
	// metadata byte-identical to before the field existed.
	want, _ := json.Marshal(base())
	for _, name := range []string{"writer", "plain", "missing"} {
		got, _ := json.Marshal(injectHostedCapabilities(base(), agentHostedCapabilities(p, name)))
		if string(got) != string(want) {
			t.Errorf("%s metadata = %s, want %s", name, got, want)
		}
	}
}

func TestHostedCapabilitiesNeedCllama(t *testing.T) {
	if err := hostedCapabilitiesNeedCllama("researcher", []string{"web_search"}, nil); err == nil {
		t.Fatal("a grant was accepted on a service without cllama")
	}
	for name, tc := range map[string][2][]string{
		"granted with cllama":   {{"web_search"}, {"passthrough"}},
		"no grant, no cllama":   {nil, nil},
		"no grant with cllama":  {nil, {"passthrough"}},
		"empty grant no cllama": {{}, nil},
	} {
		if err := hostedCapabilitiesNeedCllama("svc", tc[0], tc[1]); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// Every ordinal of a scaled service is compiled from the base service's
// declaration, looked up by the base name. An ordinal name is not a service
// and resolves to no grant, so one service's ordinals cannot pick up
// another's by name.
func TestHostedCapabilitiesAreLookedUpByBaseService(t *testing.T) {
	p := &pod.Pod{Services: map[string]*pod.Service{
		"researcher": {Claw: &pod.ClawBlock{Count: 2, HostedCapabilities: []string{"web_search"}}},
		"writer":     {Claw: &pod.ClawBlock{Count: 2}},
	}}
	for i := 0; i < 2; i++ {
		got := injectHostedCapabilities(map[string]any{"ordinal": i}, agentHostedCapabilities(p, "researcher"))
		if caps, _ := got["hosted_capabilities"].([]string); len(caps) != 1 || caps[0] != "web_search" {
			t.Fatalf("researcher ordinal %d: %v", i, got)
		}
		if got := injectHostedCapabilities(map[string]any{"ordinal": i}, agentHostedCapabilities(p, "writer")); got["hosted_capabilities"] != nil {
			t.Fatalf("writer ordinal %d inherited a grant: %v", i, got)
		}
	}
	for _, ordinal := range []string{"researcher-0", "writer-0"} {
		if got := agentHostedCapabilities(p, ordinal); got != nil {
			t.Fatalf("%s resolved a grant: %v", ordinal, got)
		}
	}
}
