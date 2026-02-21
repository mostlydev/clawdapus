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
