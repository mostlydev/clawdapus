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
	Feeds       []FeedManifestEntry
	ServiceAuth []ServiceAuthEntry
}

type FeedManifestEntry struct {
	Name   string `json:"name"`
	Source string `json:"source"`
	Path   string `json:"path"`
	TTL    int    `json:"ttl"`
	URL    string `json:"url,omitempty"`
}

type ServiceAuthEntry struct {
	Service   string `json:"service"`
	AuthType  string `json:"auth_type"`
	Token     string `json:"token,omitempty"`
	Principal string `json:"principal,omitempty"`
}

// GenerateContextDir writes per-agent context files under:
//
//	<runtimeDir>/context/<agent-id>/{AGENTS.md,CLAWDAPUS.md,metadata.json,feeds.json,service-auth/...}
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

		if len(agent.Feeds) > 0 {
			feedsJSON, err := json.MarshalIndent(agent.Feeds, "", "  ")
			if err != nil {
				return fmt.Errorf("marshal feeds for %q: %w", agent.AgentID, err)
			}
			if err := os.WriteFile(filepath.Join(agentDir, "feeds.json"), append(feedsJSON, '\n'), 0644); err != nil {
				return fmt.Errorf("write feeds.json for %q: %w", agent.AgentID, err)
			}
		}

		if len(agent.ServiceAuth) > 0 {
			authDir := filepath.Join(agentDir, "service-auth")
			if err := os.MkdirAll(authDir, 0700); err != nil {
				return fmt.Errorf("create service-auth dir for %q: %w", agent.AgentID, err)
			}
			for _, entry := range agent.ServiceAuth {
				if entry.Service == "" {
					return fmt.Errorf("service-auth entry for %q: service must not be empty", agent.AgentID)
				}
				data, err := json.MarshalIndent(entry, "", "  ")
				if err != nil {
					return fmt.Errorf("marshal service-auth for %q/%q: %w", agent.AgentID, entry.Service, err)
				}
				path := filepath.Join(authDir, entry.Service+".json")
				if err := os.WriteFile(path, append(data, '\n'), 0600); err != nil {
					return fmt.Errorf("write service-auth for %q/%q: %w", agent.AgentID, entry.Service, err)
				}
			}
		}
	}

	return nil
}
