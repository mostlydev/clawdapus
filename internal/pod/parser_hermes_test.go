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

func TestParseHermesDisableTools(t *testing.T) {
	p, err := Parse(strings.NewReader(`
services:
  analyst:
    image: ghcr.io/example/analyst:latest
    x-claw:
      agent: ./AGENTS.md
      hermes:
        disable-tools:
          - skill_manage
          - session_search
`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	hermes := p.Services["analyst"].Claw.Hermes
	if hermes == nil {
		t.Fatal("expected Hermes config")
	}
	if len(hermes.DisableTools) != 2 || hermes.DisableTools[0] != "skill_manage" || hermes.DisableTools[1] != "session_search" {
		t.Fatalf("unexpected Hermes disable-tools: %+v", hermes.DisableTools)
	}
}

func TestParseHermesAllowSilent(t *testing.T) {
	p, err := Parse(strings.NewReader(`
services:
  gerrard:
    image: ghcr.io/example/gerrard:latest
    x-claw:
      agent: ./AGENTS.md
      hermes:
        allow-silent: true
`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	hermes := p.Services["gerrard"].Claw.Hermes
	if hermes == nil {
		t.Fatal("expected Hermes config")
	}
	if !hermes.AllowSilent {
		t.Fatal("expected Hermes allow-silent to parse true")
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

func TestParseHermesDisableToolsRejectsEmptyItem(t *testing.T) {
	_, err := Parse(strings.NewReader(`
services:
  analyst:
    image: ghcr.io/example/analyst:latest
    x-claw:
      agent: ./AGENTS.md
      hermes:
        disable-tools:
          - skill_manage
          - " "
`))
	if err == nil {
		t.Fatal("expected empty disable-tools item to fail")
	}
	if !strings.Contains(err.Error(), "disable-tools[1] must not be empty") {
		t.Fatalf("unexpected error: %v", err)
	}
}
