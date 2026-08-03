package pod

import (
	"strings"
	"testing"
)

// Ordered fallback chains: x-claw.models.fallback accepts a scalar (unchanged
// behavior) or an ordered list. List entries normalize to reserved slot keys
// fallback, fallback-2, fallback-3, ... in declared order. See ADR-019.

func parseOne(t *testing.T, yaml string) *Pod {
	t.Helper()
	p, err := Parse(strings.NewReader(yaml))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return p
}

const fallbackChainPodYAML = `
x-claw:
  pod: chain-test
services:
  agent:
    image: example/agent
    x-claw:
      agent: ./AGENTS.md
      models:
        primary: openai/gpt-5.6
        fallback:
          - openai/gpt-5.1
          - anthropic/claude-sonnet-5
          - openrouter/meta-llama/llama-4-maverick
`

func TestParseModelsFallbackListNormalizesToOrderedSlots(t *testing.T) {
	p := parseOne(t, fallbackChainPodYAML)
	models := p.Services["agent"].Claw.Models

	want := map[string]string{
		"primary":    "openai/gpt-5.6",
		"fallback":   "openai/gpt-5.1",
		"fallback-2": "anthropic/claude-sonnet-5",
		"fallback-3": "openrouter/meta-llama/llama-4-maverick",
	}
	if len(models) != len(want) {
		t.Fatalf("models = %v, want %v", models, want)
	}
	for slot, ref := range want {
		if models[slot] != ref {
			t.Errorf("models[%q] = %q, want %q", slot, models[slot], ref)
		}
	}
}

func TestParseModelsFallbackSingleElementListEqualsScalar(t *testing.T) {
	p := parseOne(t, `
x-claw:
  pod: chain-test
services:
  agent:
    image: example/agent
    x-claw:
      agent: ./AGENTS.md
      models:
        primary: openai/gpt-5.6
        fallback: [anthropic/claude-haiku-4-5]
`)
	models := p.Services["agent"].Claw.Models
	if got := models["fallback"]; got != "anthropic/claude-haiku-4-5" {
		t.Fatalf("fallback = %q", got)
	}
	if _, ok := models["fallback-2"]; ok {
		t.Fatal("single-element list must not create fallback-2")
	}
}

func TestParseModelsFallbackEmptyListMeansNoFallback(t *testing.T) {
	p := parseOne(t, `
x-claw:
  pod: chain-test
services:
  agent:
    image: example/agent
    x-claw:
      agent: ./AGENTS.md
      models:
        primary: openai/gpt-5.6
        fallback: []
`)
	models := p.Services["agent"].Claw.Models
	if _, ok := models["fallback"]; ok {
		t.Fatalf("empty list must clear fallback, got %v", models)
	}
}

func TestParseModelsListRejectedForNonFallbackSlots(t *testing.T) {
	_, err := Parse(strings.NewReader(`
x-claw:
  pod: chain-test
services:
  agent:
    image: example/agent
    x-claw:
      agent: ./AGENTS.md
      models:
        primary: [openai/gpt-5.6, openai/gpt-5.1]
`))
	if err == nil {
		t.Fatal("expected list-form primary to be rejected")
	}
	if !strings.Contains(err.Error(), "primary") || !strings.Contains(err.Error(), "fallback") {
		t.Fatalf("error should name the offending slot and point at fallback, got %v", err)
	}
}

func TestParseModelsRejectsReservedFallbackOrdinalKeys(t *testing.T) {
	_, err := Parse(strings.NewReader(`
x-claw:
  pod: chain-test
services:
  agent:
    image: example/agent
    x-claw:
      agent: ./AGENTS.md
      models:
        primary: openai/gpt-5.6
        fallback-2: anthropic/claude-sonnet-5
`))
	if err == nil {
		t.Fatal("expected explicit fallback-2 key to be rejected")
	}
	if !strings.Contains(err.Error(), "fallback-2") || !strings.Contains(err.Error(), "list") {
		t.Fatalf("error should name the reserved key and point at list form, got %v", err)
	}
}

func TestParseModelsFallbackListRejectsBlankEntries(t *testing.T) {
	_, err := Parse(strings.NewReader(`
x-claw:
  pod: chain-test
services:
  agent:
    image: example/agent
    x-claw:
      agent: ./AGENTS.md
      models:
        primary: openai/gpt-5.6
        fallback: ["openai/gpt-5.1", "  "]
`))
	if err == nil {
		t.Fatal("expected blank chain entry to be rejected")
	}
}

func TestParseModelsFallbackListRejectsDuplicateRefs(t *testing.T) {
	_, err := Parse(strings.NewReader(`
x-claw:
  pod: chain-test
services:
  agent:
    image: example/agent
    x-claw:
      agent: ./AGENTS.md
      models:
        primary: openai/gpt-5.6
        fallback: [openai/gpt-5.1, openai/gpt-5.1]
`))
	if err == nil {
		t.Fatal("expected duplicate chain refs to be rejected")
	}
}

// A service-level fallback declaration (scalar or list) replaces the entire
// default chain — never a positional merge.
func TestParseModelsDefaultsFallbackChainReplacedAtomically(t *testing.T) {
	p := parseOne(t, `
x-claw:
  pod: chain-test
  models-defaults:
    primary: openai/gpt-5.6
    fallback:
      - openai/gpt-5.1
      - anthropic/claude-sonnet-5
services:
  inheritor:
    image: example/agent
    x-claw:
      agent: ./AGENTS.md
  scalar-override:
    image: example/agent
    x-claw:
      agent: ./AGENTS.md
      models:
        fallback: anthropic/claude-haiku-4-5
  list-override:
    image: example/agent
    x-claw:
      agent: ./AGENTS.md
      models:
        fallback: [openrouter/meta-llama/llama-4-maverick]
`)

	inherited := p.Services["inheritor"].Claw.Models
	if inherited["fallback"] != "openai/gpt-5.1" || inherited["fallback-2"] != "anthropic/claude-sonnet-5" {
		t.Fatalf("inheritor should get the full default chain, got %v", inherited)
	}

	scalar := p.Services["scalar-override"].Claw.Models
	if scalar["fallback"] != "anthropic/claude-haiku-4-5" {
		t.Fatalf("scalar override fallback = %q", scalar["fallback"])
	}
	if _, ok := scalar["fallback-2"]; ok {
		t.Fatalf("scalar override must replace the whole default chain, got %v", scalar)
	}
	if scalar["primary"] != "openai/gpt-5.6" {
		t.Fatalf("primary should still inherit, got %q", scalar["primary"])
	}

	list := p.Services["list-override"].Claw.Models
	if list["fallback"] != "openrouter/meta-llama/llama-4-maverick" {
		t.Fatalf("list override fallback = %q", list["fallback"])
	}
	if _, ok := list["fallback-2"]; ok {
		t.Fatalf("list override must replace the whole default chain, got %v", list)
	}
}
