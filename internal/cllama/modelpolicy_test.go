package cllama

import "testing"

func TestCompileModelPolicyOrdersPrimaryFallbackThenSortedRemainder(t *testing.T) {
	policy := CompileModelPolicy(map[string]string{
		"experimental": "openrouter/meta-llama/llama-4-maverick",
		"primary":      "xai/grok-4.1-fast",
		"fallback":     "anthropic/claude-haiku-4-5",
		"cheap":        "openai/gpt-4o-mini",
	})
	if policy == nil {
		t.Fatal("expected non-nil policy")
	}
	if policy.Mode != ModelPolicyModeClamp {
		t.Fatalf("expected mode=%q, got %q", ModelPolicyModeClamp, policy.Mode)
	}
	if len(policy.Allowed) != 4 {
		t.Fatalf("expected 4 allowed refs, got %d", len(policy.Allowed))
	}
	want := []AllowedModel{
		{Slot: "primary", Ref: "xai/grok-4.1-fast"},
		{Slot: "fallback", Ref: "anthropic/claude-haiku-4-5"},
		{Slot: "cheap", Ref: "openai/gpt-4o-mini"},
		{Slot: "experimental", Ref: "openrouter/meta-llama/llama-4-maverick"},
	}
	for i, entry := range want {
		if policy.Allowed[i] != entry {
			t.Fatalf("allowed[%d] = %#v, want %#v", i, policy.Allowed[i], entry)
		}
	}
}

func TestCompileModelPolicyDedupsRefsAndSkipsBlanks(t *testing.T) {
	policy := CompileModelPolicy(map[string]string{
		"primary":  "xai/grok-4.1-fast",
		"fallback": "xai/grok-4.1-fast",
		"cheap":    "  ",
	})
	if policy == nil {
		t.Fatal("expected non-nil policy")
	}
	if len(policy.Allowed) != 1 {
		t.Fatalf("expected 1 allowed ref after dedup/blank filtering, got %d", len(policy.Allowed))
	}
	if policy.Allowed[0].Slot != "primary" || policy.Allowed[0].Ref != "xai/grok-4.1-fast" {
		t.Fatalf("unexpected allowed entry: %#v", policy.Allowed[0])
	}
}

func TestCompileModelPolicyReturnsNilWhenEmpty(t *testing.T) {
	if policy := CompileModelPolicy(nil); policy != nil {
		t.Fatalf("expected nil policy for nil models, got %#v", policy)
	}
	if policy := CompileModelPolicy(map[string]string{"primary": "   "}); policy != nil {
		t.Fatalf("expected nil policy for blank-only models, got %#v", policy)
	}
}

func TestInjectCompiledModelPolicyClonesMetadataAndAddsPolicy(t *testing.T) {
	meta := map[string]any{
		"service": "weston",
		"pod":     "desk",
	}
	out := InjectCompiledModelPolicy(meta, map[string]string{
		"primary": "xai/grok-4.1-fast",
	})

	if _, ok := meta["model_policy"]; ok {
		t.Fatal("expected input metadata map to remain unchanged")
	}
	policyRaw, ok := out["model_policy"]
	if !ok {
		t.Fatal("expected output metadata to contain model_policy")
	}
	policy, ok := policyRaw.(*ModelPolicy)
	if !ok {
		t.Fatalf("expected model_policy to be *ModelPolicy, got %T", policyRaw)
	}
	if len(policy.Allowed) != 1 || policy.Allowed[0].Ref != "xai/grok-4.1-fast" {
		t.Fatalf("unexpected compiled policy: %#v", policy)
	}
}

// Ordered fallback chains: normalized fallback-N slots compile into multiple
// slot=="fallback" entries in chain order, because cllama's FailoverRefs
// consumes every fallback-slot entry in declared order (cllama ADR / #28).
func TestCompileModelPolicyEmitsFallbackChainInOrderWithFallbackSlot(t *testing.T) {
	policy := CompileModelPolicy(map[string]string{
		"fallback-10": "openrouter/meta-llama/llama-4-maverick",
		"primary":     "openai/gpt-5.6",
		"fallback-2":  "anthropic/claude-sonnet-5",
		"fallback":    "openai/gpt-5.1",
		"cheap":       "anthropic/claude-haiku-4-5",
	})
	if policy == nil {
		t.Fatal("expected non-nil policy")
	}
	want := []AllowedModel{
		{Slot: "primary", Ref: "openai/gpt-5.6"},
		{Slot: "fallback", Ref: "openai/gpt-5.1"},
		{Slot: "fallback", Ref: "anthropic/claude-sonnet-5"},
		{Slot: "fallback", Ref: "openrouter/meta-llama/llama-4-maverick"},
		{Slot: "cheap", Ref: "anthropic/claude-haiku-4-5"},
	}
	if len(policy.Allowed) != len(want) {
		t.Fatalf("allowed = %#v, want %#v", policy.Allowed, want)
	}
	for i, entry := range want {
		if policy.Allowed[i] != entry {
			t.Fatalf("allowed[%d] = %#v, want %#v", i, policy.Allowed[i], entry)
		}
	}
}

func TestCompileModelPolicyFallbackChainSkipsBlankAndDuplicateLinks(t *testing.T) {
	policy := CompileModelPolicy(map[string]string{
		"primary":    "openai/gpt-5.6",
		"fallback":   "anthropic/claude-sonnet-5",
		"fallback-2": "  ",
		"fallback-3": "anthropic/claude-sonnet-5",
		"fallback-4": "anthropic/claude-haiku-4-5",
	})
	if policy == nil {
		t.Fatal("expected non-nil policy")
	}
	want := []AllowedModel{
		{Slot: "primary", Ref: "openai/gpt-5.6"},
		{Slot: "fallback", Ref: "anthropic/claude-sonnet-5"},
		{Slot: "fallback", Ref: "anthropic/claude-haiku-4-5"},
	}
	if len(policy.Allowed) != len(want) {
		t.Fatalf("allowed = %#v, want %#v", policy.Allowed, want)
	}
	for i, entry := range want {
		if policy.Allowed[i] != entry {
			t.Fatalf("allowed[%d] = %#v, want %#v", i, policy.Allowed[i], entry)
		}
	}
}
