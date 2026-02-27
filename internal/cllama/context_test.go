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
