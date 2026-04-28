package describe

import "testing"

func TestBuildToolRegistryGroupsToolsByService(t *testing.T) {
	registry, err := BuildToolRegistry(map[string]*ServiceDescriptor{
		"trading-api": {
			Version: 2,
			Tools: []ToolDescriptor{
				{Name: "get_market_context", Description: "Get market context", InputSchema: map[string]interface{}{"type": "object"}},
				{Name: "get_positions", Description: "Get positions", InputSchema: map[string]interface{}{"type": "object"}},
			},
		},
		"analytics": {
			Version: 2,
			Tools: []ToolDescriptor{
				{Name: "get_report", Description: "Get report", InputSchema: map[string]interface{}{"type": "object"}},
			},
		},
	})
	if err != nil {
		t.Fatalf("BuildToolRegistry: %v", err)
	}
	if len(registry["trading-api"]) != 2 {
		t.Fatalf("expected 2 trading-api tools, got %+v", registry["trading-api"])
	}
	if registry["trading-api"][0].Service != "trading-api" {
		t.Fatalf("expected trading-api service tag, got %+v", registry["trading-api"][0])
	}
	if len(registry["analytics"]) != 1 || registry["analytics"][0].Name != "get_report" {
		t.Fatalf("unexpected analytics registry entry: %+v", registry["analytics"])
	}
}

func TestBuildToolRegistryCarriesMCPDescriptor(t *testing.T) {
	registry, err := BuildToolRegistry(map[string]*ServiceDescriptor{
		"perplexity-mcp": {
			Version: 2,
			MCP:     &MCPDescriptor{Transport: "streamable_http", Path: "/mcp"},
			Tools: []ToolDescriptor{
				{Name: "search", Description: "Search", InputSchema: map[string]interface{}{"type": "object"}},
			},
		},
	})
	if err != nil {
		t.Fatalf("BuildToolRegistry: %v", err)
	}
	specs := registry["perplexity-mcp"]
	if len(specs) != 1 {
		t.Fatalf("expected 1 MCP tool, got %+v", specs)
	}
	if specs[0].HTTP != nil {
		t.Fatalf("expected no HTTP metadata, got %+v", specs[0].HTTP)
	}
	if specs[0].MCP == nil || specs[0].MCP.Path != "/mcp" {
		t.Fatalf("expected MCP metadata, got %+v", specs[0].MCP)
	}
}

func TestBuildToolRegistryRejectsDuplicateNamesWithinService(t *testing.T) {
	_, err := BuildToolRegistry(map[string]*ServiceDescriptor{
		"trading-api": {
			Version: 2,
			Tools: []ToolDescriptor{
				{Name: "lookup", Description: "Lookup", InputSchema: map[string]interface{}{"type": "object"}},
				{Name: "lookup", Description: "Lookup again", InputSchema: map[string]interface{}{"type": "object"}},
			},
		},
	})
	if err == nil {
		t.Fatal("expected duplicate tool name error")
	}
}
