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
