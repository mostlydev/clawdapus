package pod

import (
	"strings"
	"testing"
)

const testPodWithExposeYAML = `
x-claw:
  pod: expose-pod

services:
  api-server:
    image: nginx:alpine
    expose:
      - "8080"
      - "9090"

  researcher:
    image: claw-openclaw-example
    x-claw:
      agent: ./AGENTS.md
      surfaces:
        - "service://api-server"
`

func TestParsePodExtractsExpose(t *testing.T) {
	pod, err := Parse(strings.NewReader(testPodWithExposeYAML))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	api := pod.Services["api-server"]
	if api == nil {
		t.Fatal("expected api-server service")
	}
	if len(api.Expose) != 2 {
		t.Fatalf("expected 2 expose entries, got %d", len(api.Expose))
	}
	if api.Expose[0] != "8080" {
		t.Errorf("expected first expose port 8080, got %q", api.Expose[0])
	}
	if api.Expose[1] != "9090" {
		t.Errorf("expected second expose port 9090, got %q", api.Expose[1])
	}
}

func TestParsePodDefaultsEmptyExpose(t *testing.T) {
	pod, err := Parse(strings.NewReader(testPodWithExposeYAML))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	researcher := pod.Services["researcher"]
	if researcher == nil {
		t.Fatal("expected researcher service")
	}
	if researcher.Expose == nil {
		t.Error("expected non-nil expose slice (empty, not nil)")
	}
	if len(researcher.Expose) != 0 {
		t.Errorf("expected 0 expose entries, got %d", len(researcher.Expose))
	}
}
