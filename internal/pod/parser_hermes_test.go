package pod

import (
	"strings"
	"testing"
)

func TestParseHermesAllowTools(t *testing.T) {
	p, err := Parse(strings.NewReader(`
services:
  weston:
    image: ghcr.io/example/weston:latest
    x-claw:
      agent: ./AGENTS.md
      hermes:
        allow-tools:
          - text_to_speech
          - other_tool
`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	hermes := p.Services["weston"].Claw.Hermes
	if hermes == nil {
		t.Fatal("expected Hermes config")
	}
	if len(hermes.AllowTools) != 2 || hermes.AllowTools[0] != "text_to_speech" || hermes.AllowTools[1] != "other_tool" {
		t.Fatalf("unexpected Hermes allow-tools: %+v", hermes.AllowTools)
	}
}

func TestParseHermesConfigMissingOrEmpty(t *testing.T) {
	p, err := Parse(strings.NewReader(`
services:
  plain:
    image: ghcr.io/example/plain:latest
    x-claw:
      agent: ./AGENTS.md
  empty:
    image: ghcr.io/example/empty:latest
    x-claw:
      agent: ./AGENTS.md
      hermes: {}
`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Services["plain"].Claw.Hermes != nil {
		t.Fatalf("expected missing Hermes config to parse nil, got %+v", p.Services["plain"].Claw.Hermes)
	}
	if p.Services["empty"].Claw.Hermes != nil {
		t.Fatalf("expected empty Hermes config to parse nil, got %+v", p.Services["empty"].Claw.Hermes)
	}
}

func TestParseHermesAllowToolsRejectsEmptyItem(t *testing.T) {
	_, err := Parse(strings.NewReader(`
services:
  weston:
    image: ghcr.io/example/weston:latest
    x-claw:
      agent: ./AGENTS.md
      hermes:
        allow-tools:
          - text_to_speech
          - " "
`))
	if err == nil {
		t.Fatal("expected empty allow-tools item to fail")
	}
	if !strings.Contains(err.Error(), "allow-tools[1] must not be empty") {
		t.Fatalf("unexpected error: %v", err)
	}
}
