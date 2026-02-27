package cllama

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type AgentContextInput struct {
	AgentID     string
	AgentsMD    string
	ClawdapusMD string
	Metadata    map[string]interface{}
}

// GenerateContextDir writes per-agent context files under:
//
//	<runtimeDir>/context/<agent-id>/{AGENTS.md,CLAWDAPUS.md,metadata.json}
func GenerateContextDir(runtimeDir string, agents []AgentContextInput) error {
	for _, agent := range agents {
		if agent.AgentID == "" {
			return fmt.Errorf("agent id must not be empty")
		}
		agentDir := filepath.Join(runtimeDir, "context", agent.AgentID)
		if err := os.MkdirAll(agentDir, 0700); err != nil {
			return fmt.Errorf("create context dir for %q: %w", agent.AgentID, err)
		}
		if err := os.WriteFile(filepath.Join(agentDir, "AGENTS.md"), []byte(agent.AgentsMD), 0644); err != nil {
			return fmt.Errorf("write AGENTS.md for %q: %w", agent.AgentID, err)
		}
		if err := os.WriteFile(filepath.Join(agentDir, "CLAWDAPUS.md"), []byte(agent.ClawdapusMD), 0644); err != nil {
			return fmt.Errorf("write CLAWDAPUS.md for %q: %w", agent.AgentID, err)
		}

		meta := agent.Metadata
		if meta == nil {
			meta = map[string]interface{}{}
		}
		metaJSON, err := json.MarshalIndent(meta, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal metadata for %q: %w", agent.AgentID, err)
		}
		if err := os.WriteFile(filepath.Join(agentDir, "metadata.json"), metaJSON, 0644); err != nil {
			return fmt.Errorf("write metadata.json for %q: %w", agent.AgentID, err)
		}
	}

	return nil
}
