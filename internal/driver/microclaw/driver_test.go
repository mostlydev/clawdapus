package microclaw

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mostlydev/clawdapus/internal/driver"
	"gopkg.in/yaml.v3"
)

func TestDriverRegistered(t *testing.T) {
	d, err := driver.Lookup("microclaw")
	if err != nil {
		t.Fatalf("microclaw driver not registered: %v", err)
	}
	if d == nil {
		t.Fatal("microclaw driver is nil")
	}
}

func TestValidateRequiresAgentPath(t *testing.T) {
	d := &Driver{}
	rc := &driver.ResolvedClaw{ServiceName: "mc", Models: map[string]string{"primary": "anthropic/claude-sonnet-4"}}
	err := d.Validate(rc)
	if err == nil {
		t.Fatal("expected error for missing agent host path")
	}
	if !strings.Contains(err.Error(), "no agent host path") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateRequiresPrimaryModel(t *testing.T) {
	rc, _ := newTestRC(t)
	rc.Models = map[string]string{}

	d := &Driver{}
	err := d.Validate(rc)
	if err == nil {
		t.Fatal("expected error for missing primary model")
	}
	if !strings.Contains(err.Error(), "MODEL primary") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateDiscordHandleRequiresToken(t *testing.T) {
	rc, _ := newTestRC(t)
	rc.Handles = map[string]*driver.HandleInfo{"discord": {ID: "123"}}
	rc.Environment = map[string]string{}

	d := &Driver{}
	err := d.Validate(rc)
	if err == nil {
		t.Fatal("expected error for missing DISCORD_BOT_TOKEN")
	}
	if !strings.Contains(err.Error(), "DISCORD_BOT_TOKEN") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateAcceptsBasicConfig(t *testing.T) {
	rc, _ := newTestRC(t)
	rc.Environment["ANTHROPIC_API_KEY"] = "sk-ant"

	d := &Driver{}
	if err := d.Validate(rc); err != nil {
		t.Fatalf("unexpected validation error: %v", err)
	}
}

func TestMaterializeWritesConfigAndSeededMemory(t *testing.T) {
	rc, tmp := newTestRC(t)
	rc.Models = map[string]string{"primary": "openrouter/anthropic/claude-sonnet-4"}
	rc.Environment["OPENROUTER_API_KEY"] = "or-key"

	runtimeDir := filepath.Join(tmp, "runtime")
	if err := os.MkdirAll(runtimeDir, 0o700); err != nil {
		t.Fatal(err)
	}

	d := &Driver{}
	result, err := d.Materialize(rc, driver.MaterializeOpts{RuntimeDir: runtimeDir, PodName: "mixed-pod"})
	if err != nil {
		t.Fatalf("Materialize failed: %v", err)
	}

	if result.ReadOnly {
		t.Fatal("microclaw should run with writable filesystem")
	}
	if result.SkillDir != "/claw-data/skills" {
		t.Fatalf("unexpected skill dir: %q", result.SkillDir)
	}
	if result.SkillLayout != "directory" {
		t.Fatalf("unexpected skill layout: %q", result.SkillLayout)
	}
	if result.Environment["MICROCLAW_CONFIG"] != "/app/config/microclaw.config.yaml" {
		t.Fatalf("expected MICROCLAW_CONFIG env, got %q", result.Environment["MICROCLAW_CONFIG"])
	}

	cfgPath := filepath.Join(runtimeDir, "config", "microclaw.config.yaml")
	cfgBytes, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read generated config: %v", err)
	}
	var cfg map[string]interface{}
	if err := yaml.Unmarshal(cfgBytes, &cfg); err != nil {
		t.Fatalf("parse generated config yaml: %v", err)
	}
	if got := cfg["llm_provider"]; got != "openrouter" {
		t.Fatalf("expected llm_provider=openrouter, got %v", got)
	}
	if got := cfg["model"]; got != "anthropic/claude-sonnet-4" {
		t.Fatalf("expected model=anthropic/claude-sonnet-4, got %v", got)
	}
	if got := cfg["api_key"]; got != "or-key" {
		t.Fatalf("expected api_key=or-key, got %v", got)
	}
	if got := cfg["data_dir"]; got != "/claw-data" {
		t.Fatalf("expected data_dir=/claw-data, got %v", got)
	}

	channels, _ := cfg["channels"].(map[string]interface{})
	web, _ := channels["web"].(map[string]interface{})
	if enabled, _ := web["enabled"].(bool); !enabled {
		t.Fatal("expected channels.web.enabled=true")
	}

	seedPath := filepath.Join(runtimeDir, "data", "runtime", "groups", "AGENTS.md")
	seeded, err := os.ReadFile(seedPath)
	if err != nil {
		t.Fatalf("read seeded AGENTS.md: %v", err)
	}
	seedStr := string(seeded)
	if !strings.Contains(seedStr, "You are micro") {
		t.Fatalf("seeded AGENTS.md missing agent contract content")
	}
	if !strings.Contains(seedStr, "mixed-pod") {
		t.Fatalf("seeded AGENTS.md missing pod context")
	}
}

func TestMaterializeCllamaOpenAIModelRewrite(t *testing.T) {
	rc, tmp := newTestRC(t)
	rc.Models = map[string]string{"primary": "openrouter/moonshotai/kimi-k2.5"}
	rc.Cllama = []string{"passthrough"}
	rc.CllamaToken = "agent:token"

	runtimeDir := filepath.Join(tmp, "runtime")
	if err := os.MkdirAll(runtimeDir, 0o700); err != nil {
		t.Fatal(err)
	}

	d := &Driver{}
	if _, err := d.Materialize(rc, driver.MaterializeOpts{RuntimeDir: runtimeDir}); err != nil {
		t.Fatalf("Materialize failed: %v", err)
	}

	cfgPath := filepath.Join(runtimeDir, "config", "microclaw.config.yaml")
	cfgBytes, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read generated config: %v", err)
	}
	var cfg map[string]interface{}
	if err := yaml.Unmarshal(cfgBytes, &cfg); err != nil {
		t.Fatalf("parse generated config yaml: %v", err)
	}

	if got := cfg["llm_provider"]; got != "openai" {
		t.Fatalf("expected cllama llm_provider=openai, got %v", got)
	}
	if got := cfg["model"]; got != "openrouter/moonshotai/kimi-k2.5" {
		t.Fatalf("expected cllama model provider-prefixed, got %v", got)
	}
	if got := cfg["llm_base_url"]; got != "http://cllama-passthrough:8080/v1" {
		t.Fatalf("expected cllama llm_base_url rewrite, got %v", got)
	}
	if got := cfg["api_key"]; got != "agent:token" {
		t.Fatalf("expected cllama token api_key, got %v", got)
	}
}

func TestMaterializeCllamaAnthropicModelRewrite(t *testing.T) {
	rc, tmp := newTestRC(t)
	rc.Models = map[string]string{"primary": "anthropic/claude-sonnet-4"}
	rc.Cllama = []string{"passthrough"}
	rc.CllamaToken = "agent:token"

	runtimeDir := filepath.Join(tmp, "runtime")
	if err := os.MkdirAll(runtimeDir, 0o700); err != nil {
		t.Fatal(err)
	}

	d := &Driver{}
	if _, err := d.Materialize(rc, driver.MaterializeOpts{RuntimeDir: runtimeDir}); err != nil {
		t.Fatalf("Materialize failed: %v", err)
	}

	cfgPath := filepath.Join(runtimeDir, "config", "microclaw.config.yaml")
	cfgBytes, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read generated config: %v", err)
	}
	var cfg map[string]interface{}
	if err := yaml.Unmarshal(cfgBytes, &cfg); err != nil {
		t.Fatalf("parse generated config yaml: %v", err)
	}

	if got := cfg["llm_provider"]; got != "anthropic" {
		t.Fatalf("expected anthropic provider for anthropic model under cllama, got %v", got)
	}
	if got := cfg["model"]; got != "claude-sonnet-4" {
		t.Fatalf("expected de-prefixed anthropic model, got %v", got)
	}
}

func TestMaterializeDiscordHandleChannelConfig(t *testing.T) {
	rc, tmp := newTestRC(t)
	rc.Environment["OPENROUTER_API_KEY"] = "or-key"
	rc.Environment["DISCORD_BOT_TOKEN"] = "discord-token"
	rc.Models = map[string]string{"primary": "openrouter/anthropic/claude-sonnet-4"}
	rc.Handles = map[string]*driver.HandleInfo{
		"discord": {
			ID:       "111",
			Username: "micro-bot",
			Guilds: []driver.GuildInfo{{
				ID:       "999",
				Channels: []driver.ChannelInfo{{ID: "123456789012345678", Name: "trading-floor"}},
			}},
		},
	}

	runtimeDir := filepath.Join(tmp, "runtime")
	if err := os.MkdirAll(runtimeDir, 0o700); err != nil {
		t.Fatal(err)
	}

	d := &Driver{}
	if _, err := d.Materialize(rc, driver.MaterializeOpts{RuntimeDir: runtimeDir}); err != nil {
		t.Fatalf("Materialize failed: %v", err)
	}

	cfgPath := filepath.Join(runtimeDir, "config", "microclaw.config.yaml")
	cfgBytes, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read generated config: %v", err)
	}
	var cfg map[string]interface{}
	if err := yaml.Unmarshal(cfgBytes, &cfg); err != nil {
		t.Fatalf("parse generated config yaml: %v", err)
	}

	channels, _ := cfg["channels"].(map[string]interface{})
	discord, _ := channels["discord"].(map[string]interface{})
	if enabled, _ := discord["enabled"].(bool); !enabled {
		t.Fatal("expected channels.discord.enabled=true")
	}
	if got := discord["bot_token"]; got != "discord-token" {
		t.Fatalf("expected discord bot token in config, got %v", got)
	}
	if got := discord["bot_username"]; got != "micro-bot" {
		t.Fatalf("expected discord bot_username from HANDLE username, got %v", got)
	}
}

func newTestRC(t *testing.T) (*driver.ResolvedClaw, string) {
	t.Helper()
	tmp := t.TempDir()
	agentPath := filepath.Join(tmp, "AGENTS.md")
	if err := os.WriteFile(agentPath, []byte("# Agent\n\nYou are micro."), 0o644); err != nil {
		t.Fatal(err)
	}

	rc := &driver.ResolvedClaw{
		ServiceName:   "micro",
		ClawType:      "microclaw",
		AgentHostPath: agentPath,
		Models:        map[string]string{"primary": "anthropic/claude-sonnet-4"},
		Environment:   map[string]string{},
	}
	return rc, tmp
}
