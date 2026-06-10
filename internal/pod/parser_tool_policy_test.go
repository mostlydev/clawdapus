package pod

import (
	"strings"
	"testing"
)

func TestParseServiceToolPolicy(t *testing.T) {
	const yaml = `
x-claw:
  pod: policy-pod

services:
  analyst:
    image: analyst:latest
    x-claw:
      agent: ./AGENTS.md
      tools:
        - trading-api
      tool-policy:
        max-rounds: 6
        timeout-per-tool-ms: 45000
        total-timeout-ms: 300000
`

	pod, err := Parse(strings.NewReader(yaml))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	analyst := pod.Services["analyst"]
	if analyst == nil || analyst.Claw == nil || analyst.Claw.ToolPolicy == nil {
		t.Fatal("expected analyst tool-policy")
	}
	tp := analyst.Claw.ToolPolicy
	if tp.MaxRounds == nil || *tp.MaxRounds != 6 {
		t.Fatalf("unexpected max-rounds: %+v", tp.MaxRounds)
	}
	if tp.TimeoutPerToolMS == nil || *tp.TimeoutPerToolMS != 45000 {
		t.Fatalf("unexpected timeout-per-tool-ms: %+v", tp.TimeoutPerToolMS)
	}
	if tp.TotalTimeoutMS == nil || *tp.TotalTimeoutMS != 300000 {
		t.Fatalf("unexpected total-timeout-ms: %+v", tp.TotalTimeoutMS)
	}
}

func TestParseServiceToolPolicyPartialFields(t *testing.T) {
	const yaml = `
services:
  analyst:
    image: analyst:latest
    x-claw:
      agent: ./AGENTS.md
      tool-policy:
        total-timeout-ms: 300000
`

	pod, err := Parse(strings.NewReader(yaml))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	tp := pod.Services["analyst"].Claw.ToolPolicy
	if tp == nil {
		t.Fatal("expected tool-policy")
	}
	if tp.MaxRounds != nil || tp.TimeoutPerToolMS != nil {
		t.Fatalf("unset fields must stay nil: %+v", tp)
	}
	if tp.TotalTimeoutMS == nil || *tp.TotalTimeoutMS != 300000 {
		t.Fatalf("unexpected total-timeout-ms: %+v", tp.TotalTimeoutMS)
	}
}

func TestParseServiceToolPolicyValidation(t *testing.T) {
	cases := []struct {
		name    string
		policy  string
		wantErr string
	}{
		{
			name:    "zero max-rounds",
			policy:  "max-rounds: 0",
			wantErr: "max-rounds must be > 0",
		},
		{
			name:    "negative timeout-per-tool-ms",
			policy:  "timeout-per-tool-ms: -5",
			wantErr: "timeout-per-tool-ms must be > 0",
		},
		{
			name:    "zero total-timeout-ms",
			policy:  "total-timeout-ms: 0",
			wantErr: "total-timeout-ms must be > 0",
		},
		{
			name:    "total below per-tool",
			policy:  "timeout-per-tool-ms: 60000\n        total-timeout-ms: 30000",
			wantErr: "total-timeout-ms must be >= timeout-per-tool-ms",
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
      tool-policy:
        ` + tc.policy + `
`
			_, err := Parse(strings.NewReader(yaml))
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("expected error containing %q, got %v", tc.wantErr, err)
			}
		})
	}
}

func TestParsePodToolPolicyDefaults(t *testing.T) {
	const yaml = `
x-claw:
  pod: policy-pod
  tool-policy-defaults:
    total-timeout-ms: 300000

services:
  analyst:
    image: analyst:latest
    x-claw:
      agent: ./AGENTS.md
  override:
    image: override:latest
    x-claw:
      agent: ./AGENTS.md
      tool-policy:
        max-rounds: 4
  suppressed:
    image: suppressed:latest
    x-claw:
      agent: ./AGENTS.md
      tool-policy: null
`

	pod, err := Parse(strings.NewReader(yaml))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	analyst := pod.Services["analyst"].Claw.ToolPolicy
	if analyst == nil || analyst.TotalTimeoutMS == nil || *analyst.TotalTimeoutMS != 300000 {
		t.Fatalf("expected inherited pod default, got %+v", analyst)
	}

	override := pod.Services["override"].Claw.ToolPolicy
	if override == nil || override.MaxRounds == nil || *override.MaxRounds != 4 {
		t.Fatalf("expected service override, got %+v", override)
	}
	if override.TotalTimeoutMS != nil {
		t.Fatalf("service declaration must replace pod default entirely, got %+v", override)
	}

	suppressed := pod.Services["suppressed"].Claw.ToolPolicy
	if suppressed != nil {
		t.Fatalf("explicit null must suppress pod default, got %+v", suppressed)
	}
}
