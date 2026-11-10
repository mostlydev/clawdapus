package pod

import (
	"strings"
	"testing"
)

const familyPod = `
x-claw:
  pod: fam
  model-families:
    terra:
      provider: openai
      pattern: gpt-*-terra
      max-input: 4
      max-output: 18
    luna:
      provider: openai
      pattern: gpt-*-luna
      max-input: 0.5
      max-output: 2
services:
  worker:
    image: worker:latest
    x-claw:
      agent: ./AGENTS.md
      models:
        primary: openai/@terra
        fallback: chatgpt/gpt-6.1-terra
`

func TestParseModelFamilies(t *testing.T) {
	p, err := Parse(strings.NewReader(familyPod))
	if err != nil {
		t.Fatal(err)
	}
	if len(p.ModelFamilies) != 2 || p.ModelFamilies[0].Name != "luna" || p.ModelFamilies[1].Name != "terra" {
		t.Fatalf("families = %+v", p.ModelFamilies)
	}
	terra := p.ModelFamilies[1]
	if terra.Provider != "openai" || terra.Pattern != "gpt-*-terra" || terra.MaxInput != 4 || terra.MaxOutput != 18 {
		t.Fatalf("terra = %+v", terra)
	}
	// The slot keeps the family reference until claw up resolves it.
	if got := p.Services["worker"].Claw.Models["primary"]; got != "openai/@terra" {
		t.Fatalf("primary = %q", got)
	}

	for name, edit := range map[string][2]string{
		"unknown key":     {"      max-output: 18\n", "      max-output: 18\n      region: eu\n"},
		"no ceiling":      {"      max-input: 4\n", ""},
		"bad pattern":     {"gpt-*-terra", "gpt-terra"},
		"provider a path": {"provider: openai\n      pattern: gpt-*-terra", "provider: openai/eu\n      pattern: gpt-*-terra"},
	} {
		if _, err := Parse(strings.NewReader(strings.Replace(familyPod, edit[0], edit[1], 1))); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
