package pod

import (
	"strings"
	"testing"
)

const podWithInvoke = `
x-claw:
  pod: test-pod

services:
  bot:
    image: openclaw:latest
    x-claw:
      agent: ./AGENTS.md
      handles:
        discord:
          id: "123456789"
          username: "tiverton"
          guilds:
            - id: "999888777"
              name: "Trading Floor"
              channels:
                - id: "111222333"
                  name: trading-floor
      invoke:
        - schedule: "15 8 * * 1-5"
          message: "Pre-market synthesis. Write report and post to #trading-floor."
          name: "Pre-market synthesis"
          to: trading-floor
        - schedule: "*/30 * * * *"
          message: "Post a brief status update."
`

const podWithoutInvoke = `
x-claw:
  pod: test-pod

services:
  bot:
    image: openclaw:latest
    x-claw:
      agent: ./AGENTS.md
`

const podWithInvokeMissingSchedule = `
x-claw:
  pod: test-pod

services:
  bot:
    image: openclaw:latest
    x-claw:
      agent: ./AGENTS.md
      invoke:
        - message: "Missing schedule field"
`

const podWithInvokeMissingMessage = `
x-claw:
  pod: test-pod

services:
  bot:
    image: openclaw:latest
    x-claw:
      agent: ./AGENTS.md
      invoke:
        - schedule: "15 8 * * 1-5"
`

func TestParsePodInvoke(t *testing.T) {
	p, err := Parse(strings.NewReader(podWithInvoke))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	svc := p.Services["bot"]
	if svc == nil || svc.Claw == nil {
		t.Fatal("expected bot service with claw block")
	}
	if len(svc.Claw.Invoke) != 2 {
		t.Fatalf("expected 2 invoke entries, got %d", len(svc.Claw.Invoke))
	}

	entry0 := svc.Claw.Invoke[0]
	if entry0.Schedule != "15 8 * * 1-5" {
		t.Errorf("expected entry[0].schedule=%q, got %q", "15 8 * * 1-5", entry0.Schedule)
	}
	if entry0.Message != "Pre-market synthesis. Write report and post to #trading-floor." {
		t.Errorf("unexpected entry[0].message: %q", entry0.Message)
	}
	if entry0.Name != "Pre-market synthesis" {
		t.Errorf("expected entry[0].name=%q, got %q", "Pre-market synthesis", entry0.Name)
	}
	if entry0.To != "trading-floor" {
		t.Errorf("expected entry[0].to=%q, got %q", "trading-floor", entry0.To)
	}

	entry1 := svc.Claw.Invoke[1]
	if entry1.Schedule != "*/30 * * * *" {
		t.Errorf("expected entry[1].schedule=%q, got %q", "*/30 * * * *", entry1.Schedule)
	}
	if entry1.To != "" {
		t.Errorf("expected entry[1].to to be empty, got %q", entry1.To)
	}
}

func TestParsePodNoInvoke(t *testing.T) {
	p, err := Parse(strings.NewReader(podWithoutInvoke))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	svc := p.Services["bot"]
	if svc == nil || svc.Claw == nil {
		t.Fatal("expected bot service with claw block")
	}
	// Should be nil or empty — not panic
	if len(svc.Claw.Invoke) != 0 {
		t.Errorf("expected 0 invoke entries, got %d", len(svc.Claw.Invoke))
	}
}

func TestParsePodInvokeMissingScheduleErrors(t *testing.T) {
	_, err := Parse(strings.NewReader(podWithInvokeMissingSchedule))
	if err == nil {
		t.Fatal("expected error for invoke entry missing schedule")
	}
}

func TestParsePodInvokeMissingMessageErrors(t *testing.T) {
	_, err := Parse(strings.NewReader(podWithInvokeMissingMessage))
	if err == nil {
		t.Fatal("expected error for invoke entry missing message")
	}
}
