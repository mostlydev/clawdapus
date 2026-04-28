package pod

import (
	"strings"
	"testing"
)

func TestParseMCPStdio(t *testing.T) {
	p, err := Parse(strings.NewReader(`
services:
  perplexity:
    image: ghcr.io/mostlydev/claw-mcp-stdio:v0.12.0
    x-claw:
      mcp-stdio:
        command: npx
        args: ["-y", "perplexity-mcp"]
      describe-file: ./perplexity.claw-describe.json
`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	svc := p.Services["perplexity"]
	if svc == nil || svc.Claw == nil || svc.Claw.MCPStdio == nil {
		t.Fatalf("expected mcp-stdio block, got %+v", svc)
	}
	if !svc.IsMCPStdioSidecar() {
		t.Fatal("expected service to be classified as mcp-stdio sidecar")
	}
	if svc.IsAgentManaged() {
		t.Fatal("mcp-stdio sidecar must not be classified as agent-managed")
	}
	if svc.Claw.MCPStdio.Command != "npx" {
		t.Fatalf("command = %q, want npx", svc.Claw.MCPStdio.Command)
	}
	if got := strings.Join(svc.Claw.MCPStdio.Args, " "); got != "-y perplexity-mcp" {
		t.Fatalf("args = %q", got)
	}
	if svc.Claw.DescribeFile != "./perplexity.claw-describe.json" {
		t.Fatalf("describe-file = %q", svc.Claw.DescribeFile)
	}
}

func TestParseMCPStdioDoesNotInheritPodAgentDefaults(t *testing.T) {
	p, err := Parse(strings.NewReader(`
x-claw:
  handles-defaults:
    discord:
      guilds:
        - id: "guild-1"
          name: "Trading Floor"
  cllama-defaults:
    proxy: [passthrough]
    env:
      OPENROUTER_API_KEY: ${OPENROUTER_API_KEY}
  surfaces-defaults:
    - "service://trading-api"
  feeds-defaults:
    - market-context
  tools-defaults:
    - trading-api
  skills-defaults:
    - ./skills/shared.md

services:
  perplexity:
    image: ghcr.io/mostlydev/claw-mcp-stdio:v0.12.0
    x-claw:
      mcp-stdio:
        command: npx
        args: ["-y", "perplexity-mcp"]
  agent:
    image: agent:latest
    x-claw:
      agent: ./AGENTS.md
      handles:
        discord:
          id: "agent-id"
`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	sidecar := p.Services["perplexity"]
	if sidecar == nil || sidecar.Claw == nil || sidecar.Claw.MCPStdio == nil {
		t.Fatalf("expected mcp-stdio sidecar, got %+v", sidecar)
	}
	if len(sidecar.Claw.Cllama) != 0 {
		t.Fatalf("mcp-stdio sidecar inherited cllama defaults: %v", sidecar.Claw.Cllama)
	}
	if len(sidecar.Claw.CllamaEnv) != 0 {
		t.Fatalf("mcp-stdio sidecar inherited cllama env: %v", sidecar.Claw.CllamaEnv)
	}
	if len(sidecar.Claw.Handles) != 0 {
		t.Fatalf("mcp-stdio sidecar inherited handles defaults: %+v", sidecar.Claw.Handles)
	}
	if len(sidecar.Claw.Surfaces) != 0 || len(sidecar.Claw.Feeds) != 0 || len(sidecar.Claw.Tools) != 0 || len(sidecar.Claw.Skills) != 0 {
		t.Fatalf("mcp-stdio sidecar inherited agent defaults: surfaces=%v feeds=%v tools=%v skills=%v", sidecar.Claw.Surfaces, sidecar.Claw.Feeds, sidecar.Claw.Tools, sidecar.Claw.Skills)
	}

	agent := p.Services["agent"]
	if agent == nil || agent.Claw == nil {
		t.Fatal("expected agent service")
	}
	if len(agent.Claw.Cllama) != 1 || agent.Claw.Cllama[0] != "passthrough" {
		t.Fatalf("agent did not inherit cllama defaults: %v", agent.Claw.Cllama)
	}
	if agent.Claw.Handles["discord"] == nil || len(agent.Claw.Handles["discord"].Guilds) != 1 {
		t.Fatalf("agent did not inherit handle topology: %+v", agent.Claw.Handles)
	}
}

func TestParseMCPStdioValidation(t *testing.T) {
	tests := []struct {
		name string
		yaml string
		want string
	}{
		{
			name: "empty command",
			yaml: `
services:
  sidecar:
    image: wrapper:latest
    x-claw:
      mcp-stdio:
        command: " "
`,
			want: "mcp-stdio command is required",
		},
		{
			name: "agent",
			yaml: `
services:
  sidecar:
    image: wrapper:latest
    x-claw:
      agent: ./AGENTS.md
      mcp-stdio:
        command: npx
`,
			want: "mcp-stdio cannot be combined with agent",
		},
		{
			name: "cllama",
			yaml: `
services:
  sidecar:
    image: wrapper:latest
    x-claw:
      cllama: passthrough
      mcp-stdio:
        command: npx
`,
			want: "mcp-stdio cannot be combined with cllama",
		},
		{
			name: "count",
			yaml: `
services:
  sidecar:
    image: wrapper:latest
    x-claw:
      count: 2
      mcp-stdio:
        command: npx
`,
			want: "mcp-stdio does not support count > 1",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse(strings.NewReader(tt.yaml))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Parse error = %v, want containing %q", err, tt.want)
			}
		})
	}
}
