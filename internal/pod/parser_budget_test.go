package pod

import (
	"strings"
	"testing"
)

func TestParseServiceBudget(t *testing.T) {
	const yaml = `
x-claw:
  pod: budget-pod

services:
  analyst:
    image: analyst:latest
    x-claw:
      agent: ./AGENTS.md
      budget:
        limit-usd: 1.25
        max-requests: 12
        window: 1h
        behavior: hard_stop
`

	pod, err := Parse(strings.NewReader(yaml))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	budget := pod.Services["analyst"].Claw.Budget
	if budget == nil {
		t.Fatal("expected budget")
	}
	if budget.LimitUSD == nil || *budget.LimitUSD != 1.25 {
		t.Fatalf("unexpected limit-usd: %+v", budget.LimitUSD)
	}
	if budget.MaxRequests == nil || *budget.MaxRequests != 12 {
		t.Fatalf("unexpected max-requests: %+v", budget.MaxRequests)
	}
	if budget.Window != "1h" || budget.Behavior != "hard_stop" {
		t.Fatalf("unexpected budget metadata: %+v", budget)
	}
}

func TestParseServiceBudgetDefaultsBehavior(t *testing.T) {
	const yaml = `
services:
  analyst:
    image: analyst:latest
    x-claw:
      agent: ./AGENTS.md
      budget:
        limit-usd: 0.5
        window: 30m
`

	pod, err := Parse(strings.NewReader(yaml))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	budget := pod.Services["analyst"].Claw.Budget
	if budget == nil || budget.Behavior != "hard_stop" {
		t.Fatalf("expected default hard_stop behavior, got %+v", budget)
	}
}

func TestParseServiceBudgetValidation(t *testing.T) {
	cases := []struct {
		name    string
		budget  string
		wantErr string
	}{
		{
			name:    "missing cap",
			budget:  "window: 1h",
			wantErr: "at least one of limit-usd or max-requests is required",
		},
		{
			name:    "zero limit",
			budget:  "limit-usd: 0\n        window: 1h",
			wantErr: "limit-usd must be > 0",
		},
		{
			name:    "zero requests",
			budget:  "max-requests: 0\n        window: 1h",
			wantErr: "max-requests must be > 0",
		},
		{
			name:    "missing window",
			budget:  "limit-usd: 1",
			wantErr: "window is required",
		},
		{
			name:    "bad window",
			budget:  "limit-usd: 1\n        window: soon",
			wantErr: "window must be a positive duration",
		},
		{
			name:    "unknown behavior",
			budget:  "limit-usd: 1\n        window: 1h\n        behavior: magic",
			wantErr: "unknown behavior",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			yaml := `
services:
  analyst:
    image: analyst:latest
    x-claw:
      agent: ./AGENTS.md
      budget:
        ` + tc.budget + `
`
			_, err := Parse(strings.NewReader(yaml))
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("expected error containing %q, got %v", tc.wantErr, err)
			}
		})
	}
}

func TestParsePodBudgetDefaults(t *testing.T) {
	const yaml = `
x-claw:
  pod: budget-pod
  budget-defaults:
    limit-usd: 1
    window: 1h

services:
  analyst:
    image: analyst:latest
    x-claw:
      agent: ./AGENTS.md
  override:
    image: override:latest
    x-claw:
      agent: ./AGENTS.md
      budget:
        max-requests: 5
        window: 10m
  suppressed:
    image: suppressed:latest
    x-claw:
      agent: ./AGENTS.md
      budget: null
`

	pod, err := Parse(strings.NewReader(yaml))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	analyst := pod.Services["analyst"].Claw.Budget
	if analyst == nil || analyst.LimitUSD == nil || *analyst.LimitUSD != 1 || analyst.Window != "1h" {
		t.Fatalf("expected inherited pod default, got %+v", analyst)
	}

	override := pod.Services["override"].Claw.Budget
	if override == nil || override.MaxRequests == nil || *override.MaxRequests != 5 {
		t.Fatalf("expected service override, got %+v", override)
	}
	if override.LimitUSD != nil {
		t.Fatalf("service declaration must replace pod default entirely, got %+v", override)
	}

	if suppressed := pod.Services["suppressed"].Claw.Budget; suppressed != nil {
		t.Fatalf("explicit null must suppress pod default, got %+v", suppressed)
	}
}
