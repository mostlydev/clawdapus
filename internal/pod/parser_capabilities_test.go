package pod

import (
	"strings"
	"testing"
)

func TestParsePodExtractsToolsAndMemory(t *testing.T) {
	const yaml = `
x-claw:
  pod: capabilities-pod

services:
  analyst:
    image: analyst:latest
    x-claw:
      agent: ./AGENTS.md
      tools:
        - trading-api
        - service: analytics
          allow:
            - get_summary
            - get_report
      memory:
        service: team-memory
        timeout-ms: 450
`

	pod, err := Parse(strings.NewReader(yaml))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	analyst := pod.Services["analyst"]
	if analyst == nil || analyst.Claw == nil {
		t.Fatal("expected analyst claw service")
	}
	if len(analyst.Claw.Tools) != 2 {
		t.Fatalf("expected 2 tool policies, got %+v", analyst.Claw.Tools)
	}
	if analyst.Claw.Tools[0].Service != "trading-api" {
		t.Fatalf("expected scalar shorthand service, got %+v", analyst.Claw.Tools[0])
	}
	if len(analyst.Claw.Tools[0].Allow) != 1 || analyst.Claw.Tools[0].Allow[0] != "all" {
		t.Fatalf("expected scalar shorthand to default to allow all, got %+v", analyst.Claw.Tools[0])
	}
	if analyst.Claw.Tools[1].Service != "analytics" {
		t.Fatalf("expected second tool policy service analytics, got %+v", analyst.Claw.Tools[1])
	}
	if len(analyst.Claw.Tools[1].Allow) != 2 || analyst.Claw.Tools[1].Allow[0] != "get_summary" || analyst.Claw.Tools[1].Allow[1] != "get_report" {
		t.Fatalf("unexpected allow list: %+v", analyst.Claw.Tools[1])
	}
	if analyst.Claw.Memory == nil {
		t.Fatal("expected memory entry")
	}
	if analyst.Claw.Memory.Service != "team-memory" || analyst.Claw.Memory.TimeoutMS != 450 {
		t.Fatalf("unexpected memory entry: %+v", analyst.Claw.Memory)
	}
}

func TestParsePodDefaultsInheritAndReplaceCapabilityConfig(t *testing.T) {
	const yaml = `
x-claw:
  pod: defaults-pod
  tools-defaults:
    - service: trading-api
  memory-defaults:
    service: team-memory
    timeout-ms: 275

services:
  worker:
    image: worker:latest
    x-claw:
      agent: ./AGENTS.md
  analyst:
    image: analyst:latest
    x-claw:
      agent: ./AGENTS.md
      tools:
        - ...
        - service: analytics
          allow:
            - get_summary
      memory:
        service: special-memory
  reviewer:
    image: reviewer:latest
    x-claw:
      agent: ./AGENTS.md
      tools: []
      memory: null
`

	pod, err := Parse(strings.NewReader(yaml))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	worker := pod.Services["worker"].Claw
	if len(worker.Tools) != 1 || worker.Tools[0].Service != "trading-api" || worker.Tools[0].Allow[0] != "all" {
		t.Fatalf("expected inherited tool defaults, got %+v", worker.Tools)
	}
	if worker.Memory == nil || worker.Memory.Service != "team-memory" || worker.Memory.TimeoutMS != 275 {
		t.Fatalf("expected inherited memory defaults, got %+v", worker.Memory)
	}

	analyst := pod.Services["analyst"].Claw
	if len(analyst.Tools) != 2 {
		t.Fatalf("expected spread-expanded tools, got %+v", analyst.Tools)
	}
	if analyst.Tools[0].Service != "trading-api" || analyst.Tools[1].Service != "analytics" {
		t.Fatalf("unexpected tool order after spread: %+v", analyst.Tools)
	}
	if analyst.Memory == nil || analyst.Memory.Service != "special-memory" {
		t.Fatalf("expected replaced memory service, got %+v", analyst.Memory)
	}
	if analyst.Memory.TimeoutMS != 300 {
		t.Fatalf("expected service-level memory replacement to use entry default timeout, got %+v", analyst.Memory)
	}

	reviewer := pod.Services["reviewer"].Claw
	if len(reviewer.Tools) != 0 {
		t.Fatalf("expected explicit empty tools to suppress defaults, got %+v", reviewer.Tools)
	}
	if reviewer.Memory != nil {
		t.Fatalf("expected null memory to suppress defaults, got %+v", reviewer.Memory)
	}
}

func TestParsePodRejectsInvalidCapabilityConfig(t *testing.T) {
	tests := []struct {
		name string
		yaml string
	}{
		{
			name: "tool allow all mixed with names",
			yaml: `
x-claw:
  pod: invalid-pod
services:
  analyst:
    image: analyst:latest
    x-claw:
      agent: ./AGENTS.md
      tools:
        - service: trading-api
          allow:
            - all
            - get_report
`,
		},
		{
			name: "memory timeout invalid",
			yaml: `
x-claw:
  pod: invalid-pod
services:
  analyst:
    image: analyst:latest
    x-claw:
      agent: ./AGENTS.md
      memory:
        service: team-memory
        timeout-ms: 0
`,
		},
		{
			name: "tools spread without defaults",
			yaml: `
x-claw:
  pod: invalid-pod
services:
  analyst:
    image: analyst:latest
    x-claw:
      agent: ./AGENTS.md
      tools:
        - ...
`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Parse(strings.NewReader(tc.yaml)); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}
