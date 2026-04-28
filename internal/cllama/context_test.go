package cllama

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestGenerateContextDirWritesFiles(t *testing.T) {
	dir := t.TempDir()
	agents := []AgentContextInput{{
		AgentID:     "tiverton",
		AgentsMD:    "# Contract",
		ClawdapusMD: "# Infra",
		Metadata: map[string]interface{}{
			"service": "tiverton",
			"pod":     "test-pod",
		},
	}}
	if err := GenerateContextDir(dir, agents); err != nil {
		t.Fatal(err)
	}

	agentsMD, err := os.ReadFile(filepath.Join(dir, "context", "tiverton", "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(agentsMD) != "# Contract" {
		t.Errorf("wrong AGENTS.md: %q", agentsMD)
	}

	clawdapusMD, err := os.ReadFile(filepath.Join(dir, "context", "tiverton", "CLAWDAPUS.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(clawdapusMD) != "# Infra" {
		t.Errorf("wrong CLAWDAPUS.md: %q", clawdapusMD)
	}

	metaRaw, err := os.ReadFile(filepath.Join(dir, "context", "tiverton", "metadata.json"))
	if err != nil {
		t.Fatal(err)
	}
	var meta map[string]interface{}
	if err := json.Unmarshal(metaRaw, &meta); err != nil {
		t.Fatal(err)
	}
	if meta["service"] != "tiverton" {
		t.Errorf("wrong metadata: %v", meta)
	}
}

func TestGenerateContextDirMultipleAgents(t *testing.T) {
	dir := t.TempDir()
	agents := []AgentContextInput{
		{
			AgentID:     "bot-a",
			AgentsMD:    "# A",
			ClawdapusMD: "# A-infra",
			Metadata:    map[string]interface{}{},
		},
		{
			AgentID:     "bot-b",
			AgentsMD:    "# B",
			ClawdapusMD: "# B-infra",
			Metadata:    map[string]interface{}{},
		},
	}
	if err := GenerateContextDir(dir, agents); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(filepath.Join(dir, "context", "bot-a", "AGENTS.md")); err != nil {
		t.Errorf("bot-a missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "context", "bot-b", "AGENTS.md")); err != nil {
		t.Errorf("bot-b missing: %v", err)
	}
}

func TestGenerateContextDirWritesOptionalFeedsAndServiceAuth(t *testing.T) {
	dir := t.TempDir()
	agents := []AgentContextInput{{
		AgentID:     "octopus",
		AgentsMD:    "# Contract",
		ClawdapusMD: "# Infra",
		Metadata:    map[string]interface{}{"service": "octopus"},
		Feeds: []FeedManifestEntry{{
			Name:   "fleet-alerts",
			Source: "claw-api",
			Path:   "/fleet/alerts",
			TTL:    30,
			URL:    "http://claw-api:8080/fleet/alerts",
		}},
		Tools: []ToolManifestEntry{{
			Name:        "trading-api.propose_trade",
			Description: "Submit trade proposal",
			InputSchema: map[string]interface{}{"type": "object"},
			Execution: ToolExecution{
				Transport: "http",
				Service:   "trading-api",
				BaseURL:   "http://trading-api:4000",
				Method:    "POST",
				Path:      "/api/v1/trades",
				BodyKey:   "trade",
				Auth:      &AuthEntry{Type: "bearer", Token: "service-token"},
			},
		}},
		Memory: &MemoryManifestEntry{
			Version: 1,
			Service: "team-memory",
			BaseURL: "http://team-memory:8080",
			Recall: &MemoryOp{
				Path:      "/recall",
				TimeoutMS: 300,
			},
			Retain: &MemoryOp{
				Path: "/retain",
			},
			Auth: &AuthEntry{Type: "bearer", Token: "memory-token"},
		},
		ServiceAuth: []ServiceAuthEntry{{
			Service:   "claw-api",
			AuthType:  "bearer",
			Token:     "capi_deadbeef",
			Principal: "octopus",
		}},
	}}

	if err := GenerateContextDir(dir, agents); err != nil {
		t.Fatal(err)
	}

	feedsRaw, err := os.ReadFile(filepath.Join(dir, "context", "octopus", "feeds.json"))
	if err != nil {
		t.Fatal(err)
	}
	var feeds []map[string]interface{}
	if err := json.Unmarshal(feedsRaw, &feeds); err != nil {
		t.Fatal(err)
	}
	if len(feeds) != 1 || feeds[0]["name"] != "fleet-alerts" {
		t.Fatalf("unexpected feeds payload: %v", feeds)
	}

	toolsRaw, err := os.ReadFile(filepath.Join(dir, "context", "octopus", "tools.json"))
	if err != nil {
		t.Fatal(err)
	}
	var toolsManifest map[string]interface{}
	if err := json.Unmarshal(toolsRaw, &toolsManifest); err != nil {
		t.Fatal(err)
	}
	if toolsManifest["version"].(float64) != 1 {
		t.Fatalf("unexpected tools manifest version: %v", toolsManifest)
	}
	tools := toolsManifest["tools"].([]interface{})
	if len(tools) != 1 {
		t.Fatalf("unexpected tools manifest payload: %v", toolsManifest)
	}
	execution := tools[0].(map[string]interface{})["execution"].(map[string]interface{})
	if execution["body_key"] != "trade" {
		t.Fatalf("unexpected tools execution payload: %v", execution)
	}

	memoryRaw, err := os.ReadFile(filepath.Join(dir, "context", "octopus", "memory.json"))
	if err != nil {
		t.Fatal(err)
	}
	var memory map[string]interface{}
	if err := json.Unmarshal(memoryRaw, &memory); err != nil {
		t.Fatal(err)
	}
	if memory["service"] != "team-memory" {
		t.Fatalf("unexpected memory manifest payload: %v", memory)
	}

	authRaw, err := os.ReadFile(filepath.Join(dir, "context", "octopus", "service-auth", "claw-api.json"))
	if err != nil {
		t.Fatal(err)
	}
	var auth map[string]interface{}
	if err := json.Unmarshal(authRaw, &auth); err != nil {
		t.Fatal(err)
	}
	if auth["principal"] != "octopus" {
		t.Fatalf("unexpected service-auth payload: %v", auth)
	}
}

func TestGenerateContextDirWritesMCPToolExecution(t *testing.T) {
	dir := t.TempDir()
	agents := []AgentContextInput{{
		AgentID:     "octopus",
		AgentsMD:    "# Contract",
		ClawdapusMD: "# Infra",
		Metadata:    map[string]interface{}{"service": "octopus"},
		Tools: []ToolManifestEntry{{
			Name:        "perplexity-mcp.search",
			Description: "Search the web",
			InputSchema: map[string]interface{}{"type": "object"},
			Execution: ToolExecution{
				Transport: "mcp",
				Service:   "perplexity-mcp",
				BaseURL:   "http://perplexity-mcp:8080",
				Path:      "/mcp",
				ToolName:  "search",
				Auth:      &AuthEntry{Type: "bearer", Token: "service-token"},
			},
		}},
	}}

	if err := GenerateContextDir(dir, agents); err != nil {
		t.Fatal(err)
	}

	toolsRaw, err := os.ReadFile(filepath.Join(dir, "context", "octopus", "tools.json"))
	if err != nil {
		t.Fatal(err)
	}
	var toolsManifest map[string]interface{}
	if err := json.Unmarshal(toolsRaw, &toolsManifest); err != nil {
		t.Fatal(err)
	}
	tools := toolsManifest["tools"].([]interface{})
	execution := tools[0].(map[string]interface{})["execution"].(map[string]interface{})
	if execution["transport"] != "mcp" || execution["path"] != "/mcp" || execution["tool_name"] != "search" {
		t.Fatalf("unexpected MCP tool execution payload: %v", execution)
	}
	if _, exists := execution["method"]; exists {
		t.Fatalf("MCP execution should not emit HTTP method: %v", execution)
	}
}
