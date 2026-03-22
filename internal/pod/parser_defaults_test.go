package pod

import (
	"strings"
	"testing"
)

const testPodWithDefaultsYAML = `
x-claw:
  pod: defaults-pod
  cllama-defaults:
    proxy: [passthrough]
    env:
      OPENROUTER_API_KEY: ${OPENROUTER_API_KEY}
      ANTHROPIC_API_KEY: ${ANTHROPIC_API_KEY}
  surfaces-defaults:
    - "service://trading-api"
  feeds-defaults:
    - name: market-context
      source: trading-api
      path: /api/v1/market_context/{claw_id}
      ttl: 180
  skills-defaults:
    - ./skills/risk-limits.md
    - ./skills/approval-workflow.md

services:
  trading-api:
    image: trading-api:latest
  worker:
    image: worker:latest
    x-claw:
      agent: ./AGENTS.md
  coordinator:
    image: coord:latest
    x-claw:
      agent: ./AGENTS.md
      skills:
        - ...
        - ./skills/escalation.md
      feeds:
        - ...
        - sentinel-alerts
      surfaces:
        - "channel://discord"
        - ...
      cllama-env:
        OPENROUTER_API_KEY: ${COORD_OPENROUTER_API_KEY}
  sentinel:
    image: sentinel:latest
    x-claw:
      agent: ./AGENTS.md
      feeds: []
      skills: []
      surfaces:
        - "service://claw-api"
  claw-api:
    image: claw-api:latest
`

func TestParsePodDefaultsInheritWhenFieldOmitted(t *testing.T) {
	p, err := Parse(strings.NewReader(testPodWithDefaultsYAML))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	worker := p.Services["worker"]
	if worker == nil || worker.Claw == nil {
		t.Fatal("expected worker claw service")
	}
	if len(worker.Claw.Cllama) != 1 || worker.Claw.Cllama[0] != "passthrough" {
		t.Fatalf("expected inherited cllama proxy, got %v", worker.Claw.Cllama)
	}
	if got := worker.Claw.CllamaEnv["OPENROUTER_API_KEY"]; got != "${OPENROUTER_API_KEY}" {
		t.Fatalf("expected inherited cllama env, got %q", got)
	}
	if len(worker.Claw.Surfaces) != 1 || worker.Claw.Surfaces[0].Target != "trading-api" {
		t.Fatalf("expected inherited service surface, got %+v", worker.Claw.Surfaces)
	}
	if len(worker.Claw.Feeds) != 1 || worker.Claw.Feeds[0].Name != "market-context" {
		t.Fatalf("expected inherited feed, got %+v", worker.Claw.Feeds)
	}
	if len(worker.Claw.Skills) != 2 {
		t.Fatalf("expected inherited skills, got %v", worker.Claw.Skills)
	}
}

func TestParsePodDefaultsSpreadExpandsAtDeclaredPosition(t *testing.T) {
	p, err := Parse(strings.NewReader(testPodWithDefaultsYAML))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	coord := p.Services["coordinator"]
	if coord == nil || coord.Claw == nil {
		t.Fatal("expected coordinator claw service")
	}
	if got := coord.Claw.CllamaEnv["OPENROUTER_API_KEY"]; got != "${COORD_OPENROUTER_API_KEY}" {
		t.Fatalf("expected service cllama env override, got %q", got)
	}
	if got := coord.Claw.CllamaEnv["ANTHROPIC_API_KEY"]; got != "${ANTHROPIC_API_KEY}" {
		t.Fatalf("expected inherited cllama env merge, got %q", got)
	}
	if len(coord.Claw.Skills) != 3 {
		t.Fatalf("expected 3 skills after spread, got %v", coord.Claw.Skills)
	}
	if coord.Claw.Skills[0] != "./skills/risk-limits.md" || coord.Claw.Skills[2] != "./skills/escalation.md" {
		t.Fatalf("unexpected skills order after spread: %v", coord.Claw.Skills)
	}
	if len(coord.Claw.Feeds) != 2 {
		t.Fatalf("expected 2 feeds after spread, got %+v", coord.Claw.Feeds)
	}
	if coord.Claw.Feeds[0].Name != "market-context" || coord.Claw.Feeds[1].Name != "sentinel-alerts" || !coord.Claw.Feeds[1].Unresolved {
		t.Fatalf("unexpected feeds after spread: %+v", coord.Claw.Feeds)
	}
	if len(coord.Claw.Surfaces) != 2 || coord.Claw.Surfaces[0].Scheme != "channel" || coord.Claw.Surfaces[1].Target != "trading-api" {
		t.Fatalf("unexpected surfaces after spread: %+v", coord.Claw.Surfaces)
	}
}

func TestParsePodDefaultsExplicitEmptyReplacesDefaults(t *testing.T) {
	p, err := Parse(strings.NewReader(testPodWithDefaultsYAML))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	sentinel := p.Services["sentinel"]
	if sentinel == nil || sentinel.Claw == nil {
		t.Fatal("expected sentinel claw service")
	}
	if len(sentinel.Claw.Feeds) != 0 {
		t.Fatalf("expected explicit empty feeds to suppress defaults, got %+v", sentinel.Claw.Feeds)
	}
	if len(sentinel.Claw.Skills) != 0 {
		t.Fatalf("expected explicit empty skills to suppress defaults, got %+v", sentinel.Claw.Skills)
	}
	if len(sentinel.Claw.Surfaces) != 1 || sentinel.Claw.Surfaces[0].Target != "claw-api" {
		t.Fatalf("expected explicit surfaces to replace defaults, got %+v", sentinel.Claw.Surfaces)
	}
}

func TestParsePodDefaultsRejectsMultipleSpreads(t *testing.T) {
	const yaml = `
x-claw:
  pod: defaults-pod
  skills-defaults:
    - ./skills/base.md

services:
  worker:
    image: worker:latest
    x-claw:
      agent: ./AGENTS.md
      skills:
        - ...
        - ./skills/local.md
        - ...
`
	_, err := Parse(strings.NewReader(yaml))
	if err == nil || !strings.Contains(err.Error(), `spread token "..." may appear at most once`) {
		t.Fatalf("expected multiple spread error, got %v", err)
	}
}

func TestParsePodDefaultsRejectsSpreadWithNoDefaults(t *testing.T) {
	const yaml = `
x-claw:
  pod: defaults-pod

services:
  worker:
    image: worker:latest
    x-claw:
      agent: ./AGENTS.md
      skills:
        - ...
        - ./skills/local.md
`
	_, err := Parse(strings.NewReader(yaml))
	if err == nil || !strings.Contains(err.Error(), `no pod-level skills-defaults declared`) {
		t.Fatalf("expected spread-with-no-defaults error, got %v", err)
	}
}
