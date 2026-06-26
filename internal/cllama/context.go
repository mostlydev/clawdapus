package cllama

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type AgentContextInput struct {
	AgentID           string
	AgentsMD          string
	EffectiveAgentsMD string
	ClawdapusMD       string
	Metadata          map[string]interface{}
	Feeds             []FeedManifestEntry
	Tools             []ToolManifestEntry
	ToolPolicy        *ToolPolicy // nil means DefaultToolPolicy
	Memory            *MemoryManifestEntry
	RuntimeReminders  []RuntimeReminderManifestEntry
	ServiceAuth       []ServiceAuthEntry
	ChannelAllowlist  []string
}

type FeedManifestEntry struct {
	Name   string `json:"name"`
	Source string `json:"source"`
	Path   string `json:"path"`
	TTL    int    `json:"ttl"`
	URL    string `json:"url,omitempty"`
	Auth   string `json:"auth,omitempty"` // bearer token for authenticated feeds (e.g. claw-api)
}

type ServiceAuthEntry struct {
	Service   string `json:"service"`
	AuthType  string `json:"auth_type"`
	Token     string `json:"token,omitempty"`
	Principal string `json:"principal,omitempty"`
}

type AuthEntry struct {
	Type  string `json:"type"`
	Token string `json:"token,omitempty"`
}

type ToolManifestEntry struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	InputSchema map[string]interface{} `json:"inputSchema"`
	Annotations map[string]interface{} `json:"annotations,omitempty"`
	Execution   ToolExecution          `json:"execution"`
}

type ToolExecution struct {
	Transport string     `json:"transport"`
	Service   string     `json:"service"`
	BaseURL   string     `json:"base_url"`
	Method    string     `json:"method,omitempty"`
	Path      string     `json:"path"`
	ToolName  string     `json:"tool_name,omitempty"`
	BodyKey   string     `json:"body_key,omitempty"`
	Auth      *AuthEntry `json:"auth,omitempty"`
}

type ToolManifest struct {
	Version int                 `json:"version"`
	Tools   []ToolManifestEntry `json:"tools"`
	Policy  ToolPolicy          `json:"policy"`
}

type ToolPolicy struct {
	MaxRounds        int `json:"max_rounds"`
	TimeoutPerToolMS int `json:"timeout_per_tool_ms"`
	TotalTimeoutMS   int `json:"total_timeout_ms"`
}

type BudgetPolicy struct {
	LimitUSD    *float64 `json:"limit_usd,omitempty"`
	MaxRequests *int     `json:"max_requests,omitempty"`
	Window      string   `json:"window"`
	Behavior    string   `json:"behavior"`
}

type ChannelAllowlistManifest struct {
	Version  int      `json:"version"`
	Channels []string `json:"channels"`
}

type MemoryManifestEntry struct {
	Version int        `json:"version"`
	Service string     `json:"service"`
	BaseURL string     `json:"base_url"`
	Recall  *MemoryOp  `json:"recall,omitempty"`
	Retain  *MemoryOp  `json:"retain,omitempty"`
	Forget  *MemoryOp  `json:"forget,omitempty"`
	Auth    *AuthEntry `json:"auth,omitempty"`
}

type MemoryOp struct {
	Path      string `json:"path"`
	TimeoutMS int    `json:"timeout_ms,omitempty"`
}

type RuntimeReminderManifest struct {
	Version   int                            `json:"version"`
	Reminders []RuntimeReminderManifestEntry `json:"reminders"`
}

type RuntimeReminderManifestEntry struct {
	ID        string `json:"id"`
	Text      string `json:"text"`
	Enabled   bool   `json:"enabled"`
	Placement string `json:"placement"`
	MaxChars  int    `json:"max_chars"`
	Cadence   string `json:"cadence"`
}

var DefaultToolPolicy = ToolPolicy{
	MaxRounds:        8,
	TimeoutPerToolMS: 30000,
	TotalTimeoutMS:   120000,
}

// EffectiveToolPolicy merges per-field overrides onto DefaultToolPolicy.
// Nil fields keep the default value.
func EffectiveToolPolicy(maxRounds, timeoutPerToolMS, totalTimeoutMS *int) ToolPolicy {
	policy := DefaultToolPolicy
	if maxRounds != nil {
		policy.MaxRounds = *maxRounds
	}
	if timeoutPerToolMS != nil {
		policy.TimeoutPerToolMS = *timeoutPerToolMS
	}
	if totalTimeoutMS != nil {
		policy.TotalTimeoutMS = *totalTimeoutMS
	}
	return policy
}

// GenerateContextDir writes per-agent context files under:
//
//	<runtimeDir>/context/<agent-id>/{AGENTS.md,AGENTS.effective.md,CLAWDAPUS.md,metadata.json,feeds.json,tools.json,memory.json,runtime-reminders.json,service-auth/...}
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
		if agent.EffectiveAgentsMD != "" {
			if err := os.WriteFile(filepath.Join(agentDir, "AGENTS.effective.md"), []byte(agent.EffectiveAgentsMD), 0644); err != nil {
				return fmt.Errorf("write AGENTS.effective.md for %q: %w", agent.AgentID, err)
			}
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

		if len(agent.Tools) > 0 {
			policy := DefaultToolPolicy
			if agent.ToolPolicy != nil {
				policy = *agent.ToolPolicy
			}
			toolsJSON, err := json.MarshalIndent(ToolManifest{
				Version: 1,
				Tools:   agent.Tools,
				Policy:  policy,
			}, "", "  ")
			if err != nil {
				return fmt.Errorf("marshal tools for %q: %w", agent.AgentID, err)
			}
			if err := os.WriteFile(filepath.Join(agentDir, "tools.json"), append(toolsJSON, '\n'), 0644); err != nil {
				return fmt.Errorf("write tools.json for %q: %w", agent.AgentID, err)
			}
		}

		if agent.Memory != nil {
			memoryJSON, err := json.MarshalIndent(agent.Memory, "", "  ")
			if err != nil {
				return fmt.Errorf("marshal memory for %q: %w", agent.AgentID, err)
			}
			if err := os.WriteFile(filepath.Join(agentDir, "memory.json"), append(memoryJSON, '\n'), 0644); err != nil {
				return fmt.Errorf("write memory.json for %q: %w", agent.AgentID, err)
			}
		}

		if len(agent.RuntimeReminders) > 0 {
			remindersJSON, err := json.MarshalIndent(RuntimeReminderManifest{
				Version:   1,
				Reminders: append([]RuntimeReminderManifestEntry(nil), agent.RuntimeReminders...),
			}, "", "  ")
			if err != nil {
				return fmt.Errorf("marshal runtime reminders for %q: %w", agent.AgentID, err)
			}
			if err := os.WriteFile(filepath.Join(agentDir, "runtime-reminders.json"), append(remindersJSON, '\n'), 0644); err != nil {
				return fmt.Errorf("write runtime-reminders.json for %q: %w", agent.AgentID, err)
			}
		}

		if len(agent.ChannelAllowlist) > 0 {
			allowlistJSON, err := json.MarshalIndent(ChannelAllowlistManifest{
				Version:  1,
				Channels: append([]string(nil), agent.ChannelAllowlist...),
			}, "", "  ")
			if err != nil {
				return fmt.Errorf("marshal channels allowlist for %q: %w", agent.AgentID, err)
			}
			if err := os.WriteFile(filepath.Join(agentDir, "channels-allowlist.json"), append(allowlistJSON, '\n'), 0644); err != nil {
				return fmt.Errorf("write channels-allowlist.json for %q: %w", agent.AgentID, err)
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
