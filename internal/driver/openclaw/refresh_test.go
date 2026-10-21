package openclaw

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mostlydev/clawdapus/internal/driver"
)

func TestCurrentSchemaUsesKeyedAgentEntries(t *testing.T) {
	data, err := GenerateConfig(&driver.ResolvedClaw{ServiceName: "bot", Handles: map[string]*driver.HandleInfo{"discord": {ID: "123"}}})
	if err != nil {
		t.Fatal(err)
	}
	var c map[string]any
	if err = json.Unmarshal(data, &c); err != nil {
		t.Fatal(err)
	}
	a := c["agents"].(map[string]any)
	if _, ok := a["list"]; ok {
		t.Fatal("retired agents.list emitted")
	}
	entries, ok := a["entries"].(map[string]any)
	if !ok {
		t.Fatal("missing keyed agents.entries")
	}
	main := entries["main"].(map[string]any)
	if _, ok := main["id"]; ok {
		t.Fatal("agent ID must be the map key, not a config field")
	}
	if main["groupChat"] == nil {
		t.Fatal("mention policy lost")
	}
	if c["session"].(map[string]any)["store"] != openclawHomeDir+"/agents/main/sessions/openclaw-agent.sqlite" {
		t.Fatal("live session database must use the durable sessions mount")
	}
}

func TestManagedProviderConfigurationCannotReroute(t *testing.T) {
	for _, command := range []string{
		`openclaw config set models.providers.openai.baseUrl https://api.openai.com/v1`,
		`openclaw config set models.providers.openai.apiKey another-secret`,
		`openclaw config set models.providers.openai.agentRuntime.id codex`,
		`openclaw config set agents.entries.main.runtime.type acp`,
		`openclaw config set agents.defaults.models {"openai/gpt-test":{"agentRuntime":{"id":"codex"}}}`,
		`openclaw config set models.providers.bypass {"baseUrl":"https://api.openai.com/v1","apiKey":"direct"}`,
		`openclaw config set models.providers.openai.auth oauth`,
		`openclaw config set models.providers.openai.authHeader false`,
		`openclaw config set models.providers.openai.headers {"aUtHoRiZaTiOn":"route-secret"}`,
		`openclaw config set models.providers.openai.headers {"X-Api-Key":"route-secret"}`,
		`openclaw config set models.providers.openai.request {"auth":{"mode":"authorization-bearer","token":"route-secret"}}`,
		`openclaw config set models.providers.openai.request {"auth":{"mode":"header","headerName":"X-Key","value":"route-secret"}}`,
		`openclaw config set models.providers.openai.request {"proxy":{"mode":"explicit-proxy","url":"http://other-proxy:8080"}}`,
		`openclaw config set models.providers.openai.request {"proxy":{"mode":"env-proxy"}}`,
		`openclaw config set models.providers.openai.request {"headers":{"Host":"other-provider"}}`,
		`openclaw config set models.providers.openai.models [{"id":"gpt-test","name":"gpt-test","headers":{"Authorization":"route-secret"}}]`,
		`openclaw config set models.providers.openai.models [{"id":"gpt-test","name":"gpt-test","api":"anthropic-messages"}]`,
		`openclaw config set models.providers.openai.models {"invalid":"list"}`,
		`openclaw config set agents.defaults.model.primary anthropic/claude-other`,
		`openclaw config set agents.entries.main.model anthropic/claude-other`,
		`openclaw config set agents.defaults.imageModel anthropic/claude-other`,
		`openclaw config set agents.defaults.subagents.model anthropic/claude-other`,
		`openclaw config set agents.defaults.heartbeat.model anthropic/claude-other`,
		`openclaw config set agents.entries.main.heartbeat.model anthropic/claude-other`,
		`openclaw config set agents.defaults.models {"anthropic/claude-other":{"alias":"escape"}}`,
		`openclaw config set env.OPENAI_API_KEY route-secret`,
		`openclaw config set env.vars.ANTHROPIC_API_KEY route-secret`,
		`openclaw config set env.OPENAI_BASE_URL https://api.openai.com/v1`,
		`openclaw config set env.OPENCLAW_CONFIG_PATH /other/config.json`,
	} {
		t.Run(command, func(t *testing.T) {
			_, err := GenerateConfig(&driver.ResolvedClaw{Models: map[string]string{"primary": "openai/gpt-test"}, Cllama: []string{"passthrough"}, CllamaToken: "proxy-token", Configures: []string{command}})
			if err == nil || !strings.Contains(err.Error(), "cllama") {
				t.Fatalf("expected explicit cllama policy error, got %v", err)
			}
			if strings.Contains(err.Error(), "another-secret") || strings.Contains(err.Error(), "route-secret") {
				t.Fatal("secret echoed in error")
			}
		})
	}
}

func TestOpenClawDurableStateIsIsolatedAndRetained(t *testing.T) {
	root := t.TempDir()
	agent := filepath.Join(root, "AGENTS.md")
	if err := os.WriteFile(agent, []byte("contract"), 0644); err != nil {
		t.Fatal(err)
	}
	rc := &driver.ResolvedClaw{ServiceName: "bot", Count: 2, AgentHostPath: agent, Handles: map[string]*driver.HandleInfo{"discord": {ID: "123"}}}
	opts := driver.MaterializeOpts{RuntimeDir: filepath.Join(root, "runtime"), StateDir: filepath.Join(root, "state")}
	result, err := (&Driver{}).Materialize(rc, opts)
	if err != nil {
		t.Fatal(err)
	}
	var homes map[string]string
	for _, m := range result.Mounts {
		if m.ContainerPath == openclawHomeDir+"/state" {
			homes = m.HostPathByService
			if m.HostPath != "" {
				t.Fatal("shared home used for ordinals")
			}
		}
	}
	if len(homes) != 2 || homes["bot-0"] == "" || homes["bot-0"] == homes["bot-1"] {
		t.Fatalf("isolated homes missing: %v", homes)
	}
	for _, host := range homes {
		info, err := os.Stat(host)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0777 {
			t.Fatalf("nonroot cannot write home mode %o", info.Mode().Perm())
		}
	}
	marker := filepath.Join(homes["bot-0"], "persisted-session")
	if err := os.WriteFile(marker, []byte("retained"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = (&Driver{}).Materialize(rc, opts); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(marker); err != nil || string(b) != "retained" {
		t.Fatalf("state reset on materialization: %q %v", b, err)
	}
	if _, err := os.Stat(filepath.Join(homes["bot-1"], "persisted-session")); !os.IsNotExist(err) {
		t.Fatalf("ordinal state leaked: %v", err)
	}
}

func TestRetiredOpenClawConfigFailsBeforeStartup(t *testing.T) {
	for _, cmd := range []string{`openclaw config set agents.list []`, `openclaw config set cron.store /state/jobs.json`} {
		if _, err := GenerateConfig(&driver.ResolvedClaw{Configures: []string{cmd}}); err == nil || !strings.Contains(err.Error(), "retired") {
			t.Fatalf("retired config accepted: %v", err)
		}
	}
}

func TestLegacyOperatorCronStateIsPreservedAndRejected(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "openclaw-state", "bot", "cron", "jobs.json")
	if err := os.MkdirAll(filepath.Dir(path), 0777); err != nil {
		t.Fatal(err)
	}
	original := []byte(`{"version":1,"jobs":[{"id":"operator-job"}]}`)
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	_, err := prepareDurableState(&driver.ResolvedClaw{ServiceName: "bot"}, driver.MaterializeOpts{StateDir: root})
	if err == nil || !strings.Contains(err.Error(), "2026.9.7") {
		t.Fatalf("missing migration failure: %v", err)
	}
	b, err := os.ReadFile(path)
	if err != nil || string(b) != string(original) {
		t.Fatalf("legacy source altered: %q %v", b, err)
	}
}

func TestManagedProviderAllowsOrdinaryRequestSettings(t *testing.T) {
	_, err := GenerateConfig(&driver.ResolvedClaw{
		Models: map[string]string{"primary": "openai/gpt-test"},
		Cllama: []string{"passthrough"}, CllamaToken: "proxy-token",
		Configures: []string{
			`openclaw config set models.providers.openai.auth api-key`,
			`openclaw config set agents.defaults.models {"openai/gpt-test":{"alias":"quick","params":{"temperature":0.3}}}`,
			`openclaw config set agents.entries.main.model quick`,
			`openclaw config set agents.defaults.imageModel openai/gpt-test`,
			`openclaw config set agents.defaults.subagents.model openai/gpt-test`,
			`openclaw config set agents.defaults.heartbeat.model openai/gpt-test`,
			`openclaw config set env.vars.REPORT_LABEL report`,
			`openclaw config set models.providers.openai.timeoutSeconds 42`,
			`openclaw config set models.providers.openai.headers {"X-Trace-ID":"diagnostic"}`,
			`openclaw config set models.providers.openai.request {"auth":{"mode":"provider-default"},"headers":{"X-Request-ID":"diagnostic"},"tls":{"serverName":"cllama-passthrough"}}`,
		},
	})
	if err != nil {
		t.Fatalf("ordinary request settings rejected: %v", err)
	}
}

func TestChannelPluginsAreCompiledAndRespectOperatorTrust(t *testing.T) {
	for _, platform := range []string{"discord", "slack", "telegram"} {
		rc := &driver.ResolvedClaw{ServiceName: "bot", Handles: map[string]*driver.HandleInfo{platform: {ID: "123"}}}
		raw, err := GenerateConfig(rc)
		if err != nil {
			t.Fatal(err)
		}
		var config map[string]any
		if err := json.Unmarshal(raw, &config); err != nil {
			t.Fatal(err)
		}
		plugins := config["plugins"].(map[string]any)
		entry := plugins["entries"].(map[string]any)[platform].(map[string]any)
		if entry["enabled"] != true {
			t.Fatalf("required %s plugin not enabled", platform)
		}
		if platform != "telegram" && !strings.Contains(string(raw), "/opt/claw-openclaw-plugins/"+platform) {
			t.Fatalf("missing baked %s path", platform)
		}
	}
	for _, command := range []string{`openclaw config set plugins.allow ["other"]`, `openclaw config set plugins.deny ["discord"]`, `openclaw config set plugins.enabled false`, `openclaw config set plugins.entries.discord.enabled false`} {
		_, err := GenerateConfig(&driver.ResolvedClaw{Handles: map[string]*driver.HandleInfo{"discord": {ID: "123"}}, Configures: []string{command}})
		if err == nil || !strings.Contains(err.Error(), "plugins policy excludes discord") {
			t.Fatalf("operator plugin policy not respected for %q: %v", command, err)
		}
	}
}

func TestManagedProviderRequiresIdentityToken(t *testing.T) {
	_, err := GenerateConfig(&driver.ResolvedClaw{Models: map[string]string{"primary": "openai/gpt-test"}, Cllama: []string{"passthrough"}})
	if err == nil || !strings.Contains(err.Error(), "cllama") {
		t.Fatalf("missing identity must fail closed: %v", err)
	}
}
