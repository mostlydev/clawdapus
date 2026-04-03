package describe

import "testing"

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
