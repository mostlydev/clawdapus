package pod

import (
	"strings"
	"testing"
)

const testPodYAML = `
x-claw:
  pod: test-fleet

services:
  coordinator:
    image: claw-openclaw-example
    x-claw:
      agent: ./AGENTS.md
      surfaces:
        - "channel://discord"
        - "service://fleet-master"
    environment:
      DISCORD_TOKEN: "${DISCORD_TOKEN}"

  worker:
    image: claw-openclaw-example
    x-claw:
      agent: ./AGENTS.md
      count: 2
      surfaces:
        - "volume://shared-cache read-write"
`

const testPodWithSkillsYAML = `
x-claw:
  pod: skill-pod

services:
  worker:
    image: claw-openclaw-example
    x-claw:
      agent: ./AGENTS.md
      skills:
        - ./skills/custom-workflow.md
        - ./skills/team-conventions.md
`

func TestParsePodExtractsSkills(t *testing.T) {
	pod, err := Parse(strings.NewReader(testPodWithSkillsYAML))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	worker := pod.Services["worker"]
	if worker == nil {
		t.Fatal("expected worker service")
	}
	if len(worker.Claw.Skills) != 2 {
		t.Fatalf("expected 2 skills, got %d", len(worker.Claw.Skills))
	}
	if worker.Claw.Skills[0] != "./skills/custom-workflow.md" {
		t.Errorf("expected first skill, got %q", worker.Claw.Skills[0])
	}
}

func TestParsePodDefaultsEmptySkills(t *testing.T) {
	pod, err := Parse(strings.NewReader(testPodYAML))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	coord := pod.Services["coordinator"]
	if coord.Claw.Skills == nil {
		t.Error("expected non-nil skills slice (empty, not nil)")
	}
	if len(coord.Claw.Skills) != 0 {
		t.Errorf("expected 0 skills, got %d", len(coord.Claw.Skills))
	}
}

func TestParsePodExtractsPodName(t *testing.T) {
	pod, err := Parse(strings.NewReader(testPodYAML))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pod.Name != "test-fleet" {
		t.Errorf("expected pod name %q, got %q", "test-fleet", pod.Name)
	}
}

func TestParsePodExtractsServices(t *testing.T) {
	pod, err := Parse(strings.NewReader(testPodYAML))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(pod.Services) != 2 {
		t.Fatalf("expected 2 services, got %d", len(pod.Services))
	}
}

func TestParsePodExtractsClawBlock(t *testing.T) {
	pod, err := Parse(strings.NewReader(testPodYAML))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	coord := pod.Services["coordinator"]
	if coord == nil {
		t.Fatal("expected coordinator service")
	}
	if coord.Claw == nil {
		t.Fatal("expected x-claw block on coordinator")
	}
	if coord.Claw.Agent != "./AGENTS.md" {
		t.Errorf("expected agent ./AGENTS.md, got %q", coord.Claw.Agent)
	}
	if len(coord.Claw.Surfaces) != 2 {
		t.Errorf("expected 2 surfaces, got %d", len(coord.Claw.Surfaces))
	}
}

func TestParsePodExtractsCount(t *testing.T) {
	pod, err := Parse(strings.NewReader(testPodYAML))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	worker := pod.Services["worker"]
	if worker == nil {
		t.Fatal("expected worker service")
	}
	if worker.Claw.Count != 2 {
		t.Errorf("expected count=2, got %d", worker.Claw.Count)
	}
}

func TestParsePodExtractsEnvironment(t *testing.T) {
	pod, err := Parse(strings.NewReader(testPodYAML))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	coord := pod.Services["coordinator"]
	if coord.Environment["DISCORD_TOKEN"] != "${DISCORD_TOKEN}" {
		t.Errorf("expected DISCORD_TOKEN env var")
	}
}

func TestParsePodCllamaStringCoercesToList(t *testing.T) {
	yaml := `
x-claw:
  pod: test-pod

services:
  bot:
    image: bot:latest
    x-claw:
      cllama: passthrough
`
	p, err := Parse(strings.NewReader(yaml))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := p.Services["bot"].Claw.Cllama
	if len(got) != 1 || got[0] != "passthrough" {
		t.Fatalf("expected [passthrough], got %v", got)
	}
}

func TestParsePodCllamaList(t *testing.T) {
	yaml := `
x-claw:
  pod: test-pod

services:
  bot:
    image: bot:latest
    x-claw:
      cllama:
        - passthrough
        - policy
`
	p, err := Parse(strings.NewReader(yaml))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := p.Services["bot"].Claw.Cllama
	if len(got) != 2 || got[0] != "passthrough" || got[1] != "policy" {
		t.Fatalf("expected [passthrough policy], got %v", got)
	}
}

func TestParseCllamaEnvBlock(t *testing.T) {
	yaml := `
x-claw:
  pod: test-pod

services:
  bot:
    image: bot:latest
    x-claw:
      cllama: passthrough
      cllama-env:
        OPENAI_API_KEY: sk-real-key
        ANTHROPIC_API_KEY: sk-ant-key
`
	p, err := Parse(strings.NewReader(yaml))
	if err != nil {
		t.Fatal(err)
	}
	env := p.Services["bot"].Claw.CllamaEnv
	if env["OPENAI_API_KEY"] != "sk-real-key" {
		t.Errorf("expected OPENAI_API_KEY, got %v", env)
	}
	if env["ANTHROPIC_API_KEY"] != "sk-ant-key" {
		t.Errorf("expected ANTHROPIC_API_KEY, got %v", env)
	}
}
