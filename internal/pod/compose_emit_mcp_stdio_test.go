package pod

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestEmitComposeMCPStdioEnv(t *testing.T) {
	p, err := Parse(strings.NewReader(`
x-claw:
  pod: mcp-stdio-test
services:
  search:
    image: ghcr.io/mostlydev/claw-mcp-stdio:v0.12.0
    x-claw:
      mcp-stdio:
        command: npx
        args: ["-y", "perplexity-mcp", "--flag=value with spaces"]
`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	out, err := EmitCompose(p, nil)
	if err != nil {
		t.Fatalf("EmitCompose: %v", err)
	}

	var cf struct {
		Services map[string]struct {
			Environment map[string]string `yaml:"environment"`
			Networks    []string          `yaml:"networks"`
			Restart     string            `yaml:"restart"`
			ReadOnly    *bool             `yaml:"read_only"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal([]byte(out), &cf); err != nil {
		t.Fatalf("unmarshal compose: %v", err)
	}

	search := cf.Services["search"]
	if search.Environment["CLAW_MCP_STDIO_COMMAND"] != "npx" {
		t.Fatalf("CLAW_MCP_STDIO_COMMAND = %q", search.Environment["CLAW_MCP_STDIO_COMMAND"])
	}
	if search.Environment["CLAW_MCP_STDIO_ARGS"] != `["-y","perplexity-mcp","--flag=value with spaces"]` {
		t.Fatalf("CLAW_MCP_STDIO_ARGS = %q", search.Environment["CLAW_MCP_STDIO_ARGS"])
	}
	if search.Restart != "on-failure" {
		t.Fatalf("restart = %q, want on-failure", search.Restart)
	}
	if search.ReadOnly != nil {
		t.Fatalf("mcp-stdio sidecar should preserve writable default rootfs, read_only=%v", *search.ReadOnly)
	}
	if len(search.Networks) != 1 || search.Networks[0] != "claw-internal" {
		t.Fatalf("networks = %v, want claw-internal", search.Networks)
	}
}

func TestEmitComposeMCPStdioUserEnvWins(t *testing.T) {
	p, err := Parse(strings.NewReader(`
services:
  search:
    image: wrapper:latest
    environment:
      CLAW_MCP_STDIO_COMMAND: custom
      CLAW_MCP_STDIO_ARGS: '["manual"]'
    x-claw:
      mcp-stdio:
        command: npx
        args: ["-y", "perplexity-mcp"]
`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	out, err := EmitCompose(p, nil)
	if err != nil {
		t.Fatalf("EmitCompose: %v", err)
	}
	var cf struct {
		Services map[string]struct {
			Environment map[string]string `yaml:"environment"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal([]byte(out), &cf); err != nil {
		t.Fatalf("unmarshal compose: %v", err)
	}
	env := cf.Services["search"].Environment
	if env["CLAW_MCP_STDIO_COMMAND"] != "custom" {
		t.Fatalf("CLAW_MCP_STDIO_COMMAND = %q, want custom", env["CLAW_MCP_STDIO_COMMAND"])
	}
	if env["CLAW_MCP_STDIO_ARGS"] != `["manual"]` {
		t.Fatalf("CLAW_MCP_STDIO_ARGS = %q, want manual", env["CLAW_MCP_STDIO_ARGS"])
	}
}
