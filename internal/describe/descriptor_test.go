package describe

import (
	"fmt"
	"strings"
	"testing"
)

func TestParseDescriptorValidatesAndNormalizes(t *testing.T) {
	data := []byte(`{
	  "version": 1,
	  "description": " Trading API ",
	  "feeds": [{"name":"market-context","path":"/context","ttl":180}],
	  "endpoints": [{"method":"get","path":"/positions","description":"Open positions"}],
	  "auth": {"type":"bearer","env":"TRADING_API_TOKEN"},
	  "skill": "/app/skills/trading-api.md"
	}`)

	descriptor, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if descriptor.Description != "Trading API" {
		t.Fatalf("expected trimmed description, got %q", descriptor.Description)
	}
	if descriptor.Endpoints[0].Method != "GET" {
		t.Fatalf("expected normalized method, got %q", descriptor.Endpoints[0].Method)
	}
}

func TestBuildFeedRegistryRejectsCollisions(t *testing.T) {
	_, err := BuildFeedRegistry(map[string]*ServiceDescriptor{
		"trading-api": {
			Version: 1,
			Feeds:   []FeedDescriptor{{Name: "market-context", Path: "/context", TTL: 180}},
		},
		"pricing-api": {
			Version: 1,
			Feeds:   []FeedDescriptor{{Name: "market-context", Path: "/prices", TTL: 60}},
		},
	})
	if err == nil {
		t.Fatal("expected duplicate feed name error")
	}
}

func TestParseDescriptorV2SupportsToolsAndMemory(t *testing.T) {
	data := []byte(`{
	  "version": 2,
	  "description": " Capability service ",
	  "tools": [{
	    "name": " search_memory ",
	    "description": " Search memory ",
	    "inputSchema": {"type": "object", "properties": {"query": {"type": "string"}}},
	    "http": {"method": "post", "path": " /recall ", "body": "JSON", "body_key": " recall "}
	  }],
	  "memory": {
	    "recall": {"path": " /recall "},
	    "retain": {"path": "/retain"}
	  },
	  "auth": {"type": "bearer", "env": "MEMORY_TOKEN"}
	}`)

	descriptor, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if descriptor.Version != 2 {
		t.Fatalf("expected version 2, got %d", descriptor.Version)
	}
	if descriptor.Description != "Capability service" {
		t.Fatalf("expected trimmed description, got %q", descriptor.Description)
	}
	if got := descriptor.Tools[0].Name; got != "search_memory" {
		t.Fatalf("expected trimmed tool name, got %q", got)
	}
	if got := descriptor.Tools[0].HTTP.Method; got != "POST" {
		t.Fatalf("expected normalized http method, got %q", got)
	}
	if got := descriptor.Tools[0].HTTP.Path; got != "/recall" {
		t.Fatalf("expected trimmed http path, got %q", got)
	}
	if got := descriptor.Tools[0].HTTP.Body; got != "json" {
		t.Fatalf("expected normalized http body, got %q", got)
	}
	if got := descriptor.Tools[0].HTTP.BodyKey; got != "recall" {
		t.Fatalf("expected trimmed http body_key, got %q", got)
	}
	if got := descriptor.Memory.Recall.Path; got != "/recall" {
		t.Fatalf("expected trimmed recall path, got %q", got)
	}
}

func TestParseDescriptorV2SupportsMCPToolsWithoutHTTP(t *testing.T) {
	data := []byte(`{
	  "version": 2,
	  "description": "Perplexity MCP",
	  "mcp": {"transport": "http", "path": " /mcp "},
	  "x-claw-discovery": {
	    "command": " npx ",
	    "args": ["-y", "perplexity-mcp"],
	    "wrapper_image": " ghcr.io/mostlydev/claw-mcp-stdio:v0.12.0 ",
	    "mcp_protocol_version": " 2025-11-25 ",
	    "tool_count": 1
	  },
	  "tools": [{
	    "name": " search ",
	    "description": " Search the web ",
	    "inputSchema": {"type": "object", "properties": {"query": {"type": "string"}}}
	  }]
	}`)

	descriptor, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if descriptor.MCP == nil {
		t.Fatal("expected mcp descriptor")
	}
	if descriptor.MCP.Transport != "streamable_http" {
		t.Fatalf("expected normalized transport, got %q", descriptor.MCP.Transport)
	}
	if descriptor.MCP.Path != "/mcp" {
		t.Fatalf("expected normalized path, got %q", descriptor.MCP.Path)
	}
	if descriptor.Tools[0].HTTP != nil {
		t.Fatalf("expected MCP tool to omit http metadata, got %+v", descriptor.Tools[0].HTTP)
	}
	if descriptor.Tools[0].Name != "search" {
		t.Fatalf("expected trimmed tool name, got %q", descriptor.Tools[0].Name)
	}
	if descriptor.XClawDiscovery == nil {
		t.Fatal("expected discovery metadata")
	}
	if descriptor.XClawDiscovery.Command != "npx" {
		t.Fatalf("expected trimmed discovery command, got %q", descriptor.XClawDiscovery.Command)
	}
	if descriptor.XClawDiscovery.WrapperImage != "ghcr.io/mostlydev/claw-mcp-stdio:v0.12.0" {
		t.Fatalf("expected trimmed wrapper image, got %q", descriptor.XClawDiscovery.WrapperImage)
	}
	if descriptor.XClawDiscovery.MCPProtocolVersion != "2025-11-25" {
		t.Fatalf("expected trimmed MCP protocol version, got %q", descriptor.XClawDiscovery.MCPProtocolVersion)
	}
}

func TestParseDescriptorRejectsVersionOneToolsAndMemory(t *testing.T) {
	tests := []struct {
		name string
		data string
	}{
		{
			name: "tools",
			data: `{"version":1,"tools":[{"name":"lookup","description":"Lookup","inputSchema":{"type":"object"}}]}`,
		},
		{
			name: "memory",
			data: `{"version":1,"memory":{"recall":{"path":"/recall"}}}`,
		},
		{
			name: "mcp",
			data: `{"version":1,"mcp":{"path":"/mcp"}}`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Parse([]byte(tc.data)); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestParseDescriptorRejectsInvalidV2CapabilityShape(t *testing.T) {
	tests := []struct {
		name string
		data string
	}{
		{
			name: "tool schema type",
			data: `{"version":2,"tools":[{"name":"lookup","description":"Lookup","inputSchema":{"type":"array"}}]}`,
		},
		{
			name: "duplicate tool names",
			data: `{"version":2,"tools":[{"name":"lookup","description":"Lookup","inputSchema":{"type":"object"}},{"name":"lookup","description":"Lookup again","inputSchema":{"type":"object"}}]}`,
		},
		{
			name: "invalid http method",
			data: `{"version":2,"tools":[{"name":"lookup","description":"Lookup","inputSchema":{"type":"object"},"http":{"method":"trace","path":"/lookup"}}]}`,
		},
		{
			name: "missing tool http",
			data: `{"version":2,"tools":[{"name":"lookup","description":"Lookup","inputSchema":{"type":"object"}}]}`,
		},
		{
			name: "mcp and http both present",
			data: `{"version":2,"mcp":{"path":"/mcp"},"tools":[{"name":"lookup","description":"Lookup","inputSchema":{"type":"object"},"http":{"method":"post","path":"/lookup"}}]}`,
		},
		{
			name: "unsupported mcp transport",
			data: `{"version":2,"mcp":{"transport":"stdio","path":"/mcp"},"tools":[{"name":"lookup","description":"Lookup","inputSchema":{"type":"object"}}]}`,
		},
		{
			name: "invalid mcp path",
			data: `{"version":2,"mcp":{"path":"mcp"},"tools":[{"name":"lookup","description":"Lookup","inputSchema":{"type":"object"}}]}`,
		},
		{
			name: "body key on get",
			data: `{"version":2,"tools":[{"name":"lookup","description":"Lookup","inputSchema":{"type":"object"},"http":{"method":"get","path":"/lookup","body_key":"lookup"}}]}`,
		},
		{
			name: "body key on delete",
			data: `{"version":2,"tools":[{"name":"lookup","description":"Lookup","inputSchema":{"type":"object"},"http":{"method":"delete","path":"/lookup","body_key":"lookup"}}]}`,
		},
		{
			name: "memory forget only",
			data: `{"version":2,"memory":{"forget":{"path":"/forget"}}}`,
		},
		{
			name: "memory empty path",
			data: `{"version":2,"memory":{"recall":{"path":"   "}}}`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Parse([]byte(tc.data)); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

// x-claw.terminalOnSuccess is a known annotation key consumed by cllama's
// managed-tool mediation; cllama silently ignores non-boolean values, so claw
// up fails closed at compile time instead. Unknown annotation keys pass
// through untouched.
func TestParseDescriptorValidatesTerminalOnSuccessAnnotation(t *testing.T) {
	template := `{
	  "version": 2,
	  "description": "svc",
	  "tools": [{
	    "name": "hand_off",
	    "description": "Hand off",
	    "inputSchema": {"type": "object"},
	    "http": {"method": "post", "path": "/hand-off"},
	    "annotations": %s
	  }]
	}`

	valid := []string{
		`{"x-claw.terminalOnSuccess": true}`,
		`{"x-claw.terminalOnSuccess": false}`,
		`{"x-claw.terminalOnSuccess": true, "custom.key": {"nested": 1}}`,
		`{"unknown.annotation": "any-shape"}`,
	}
	for _, annotations := range valid {
		if _, err := Parse([]byte(fmt.Sprintf(template, annotations))); err != nil {
			t.Errorf("annotations %s should parse, got %v", annotations, err)
		}
	}

	invalid := []string{
		`{"x-claw.terminalOnSuccess": "true"}`,
		`{"x-claw.terminalOnSuccess": 1}`,
		`{"x-claw.terminalOnSuccess": null}`,
		`{"x-claw.terminalOnSuccess": {"enabled": true}}`,
	}
	for _, annotations := range invalid {
		_, err := Parse([]byte(fmt.Sprintf(template, annotations)))
		if err == nil {
			t.Errorf("annotations %s should be rejected", annotations)
			continue
		}
		if !strings.Contains(err.Error(), "x-claw.terminalOnSuccess") || !strings.Contains(err.Error(), "boolean") {
			t.Errorf("error should name the key and require a boolean, got %v", err)
		}
	}
}
