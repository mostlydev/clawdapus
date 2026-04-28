package mcpdiscover

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mostlydev/clawdapus/internal/describe"
)

func TestClientDiscoversToolsList(t *testing.T) {
	var initialized bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.URL.Path != "/mcp" || r.Method != http.MethodPost {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		var req struct {
			ID     interface{} `json:"id"`
			Method string      `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		switch req.Method {
		case "initialize":
			w.Header().Set("MCP-Session-Id", "session-1")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"result": map[string]interface{}{
					"protocolVersion": "2025-11-25",
					"serverInfo":      map[string]interface{}{"name": "fake"},
					"capabilities":    map[string]interface{}{"tools": map[string]interface{}{}},
				},
			})
		case "notifications/initialized":
			if r.Header.Get("MCP-Session-Id") != "session-1" {
				t.Fatalf("missing MCP session header")
			}
			initialized = true
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			if !initialized {
				t.Fatal("tools/list before initialized notification")
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"result": map[string]interface{}{
					"tools": []map[string]interface{}{{
						"name":        "echo",
						"description": "Echo text.",
						"inputSchema": map[string]interface{}{
							"type":       "object",
							"properties": map[string]interface{}{"message": map[string]interface{}{"type": "string"}},
						},
						"annotations": map[string]interface{}{"readOnly": true},
					}},
				},
			})
		default:
			t.Fatalf("unexpected method %q", req.Method)
		}
	}))
	defer server.Close()

	result, err := Client{BaseURL: server.URL, Path: "/mcp"}.Discover(context.Background())
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if result.ProtocolVersion != "2025-11-25" {
		t.Fatalf("unexpected protocol version %q", result.ProtocolVersion)
	}
	if len(result.Tools) != 1 || result.Tools[0].Name != "echo" {
		t.Fatalf("unexpected tools: %+v", result.Tools)
	}
}

func TestDescriptorConvertsToolsList(t *testing.T) {
	result := &Result{
		ProtocolVersion: "2025-11-25",
		Tools: []Tool{{
			Name: "echo",
			InputSchema: map[string]interface{}{
				"properties": map[string]interface{}{"message": map[string]interface{}{"type": "string"}},
			},
			Annotations: map[string]interface{}{"readOnly": true},
		}},
	}
	meta := &describe.DiscoveryMetadata{
		Command:      "node",
		Args:         []string{"/srv/server.js"},
		WrapperImage: "wrapper:test",
	}

	descriptor, err := Descriptor("Echo server", "/mcp", result, meta)
	if err != nil {
		t.Fatalf("Descriptor: %v", err)
	}
	if descriptor.MCP == nil || descriptor.MCP.Path != "/mcp" || descriptor.MCP.Transport != "streamable_http" {
		t.Fatalf("unexpected MCP descriptor: %+v", descriptor.MCP)
	}
	if len(descriptor.Tools) != 1 {
		t.Fatalf("tools count = %d", len(descriptor.Tools))
	}
	if descriptor.Tools[0].Description == "" {
		t.Fatal("expected fallback tool description")
	}
	if descriptor.Tools[0].InputSchema["type"] != "object" {
		t.Fatalf("expected normalized object schema, got %+v", descriptor.Tools[0].InputSchema)
	}
	if descriptor.XClawDiscovery == nil || descriptor.XClawDiscovery.ToolCount != 1 {
		t.Fatalf("unexpected metadata: %+v", descriptor.XClawDiscovery)
	}
}
