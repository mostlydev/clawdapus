package openclaw

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mostlydev/clawdapus/internal/driver"
)

// getPath navigates a dot-separated path through nested maps.
// Returns (value, true) if found, (nil, false) otherwise.
func getPath(data []byte, path string) (interface{}, bool) {
	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, false
	}
	keys := strings.Split(path, ".")
	var current interface{} = m
	for _, key := range keys {
		cm, ok := current.(map[string]interface{})
		if !ok {
			return nil, false
		}
		current, ok = cm[key]
		if !ok {
			return nil, false
		}
	}
	return current, true
}

func TestGenerateConfigSetsModelPrimary(t *testing.T) {
	rc := &driver.ResolvedClaw{
		Models: map[string]string{
			"primary": "openrouter/anthropic/claude-sonnet-4",
		},
		Configures: make([]string, 0),
	}
	data, err := GenerateConfig(rc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var config map[string]interface{}
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	agents := config["agents"].(map[string]interface{})
	defaults := agents["defaults"].(map[string]interface{})
	model := defaults["model"].(map[string]interface{})
	if model["primary"] != "openrouter/anthropic/claude-sonnet-4" {
		t.Errorf("expected model primary, got %v", model["primary"])
	}
}

func TestGenerateConfigCllamaRewritesProviderBaseURL(t *testing.T) {
	rc := &driver.ResolvedClaw{
		Models: map[string]string{"primary": "anthropic/claude-sonnet-4"},
		Cllama: []string{"passthrough"},
	}
	data, err := GenerateConfig(rc)
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]interface{}
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	modelsCfg, ok := config["models"].(map[string]interface{})
	if !ok {
		t.Fatal("expected models config")
	}
	providers, ok := modelsCfg["providers"].(map[string]interface{})
	if !ok {
		t.Fatal("expected models.providers config")
	}
	anthropic, ok := providers["anthropic"].(map[string]interface{})
	if !ok {
		t.Fatal("expected models.providers.anthropic config")
	}
	if anthropic["baseUrl"] != "http://cllama-passthrough:8080/v1" {
		t.Errorf("expected proxy baseUrl, got %v", anthropic["baseUrl"])
	}
	modelEntries, ok := anthropic["models"].([]interface{})
	if !ok || len(modelEntries) == 0 {
		t.Fatalf("expected models.providers.anthropic.models entries, got %T %v", anthropic["models"], anthropic["models"])
	}
}

func TestGenerateConfigNoCllamaNoProviderRewrite(t *testing.T) {
	rc := &driver.ResolvedClaw{
		Models: map[string]string{"primary": "anthropic/claude-sonnet-4"},
	}
	data, err := GenerateConfig(rc)
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]interface{}
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	modelsCfg, ok := config["models"].(map[string]interface{})
	if !ok {
		return
	}
	if _, exists := modelsCfg["providers"]; exists {
		t.Error("models.providers should not be set when cllama is empty")
	}
}

func TestGenerateConfigCllamaInjectsDummyToken(t *testing.T) {
	rc := &driver.ResolvedClaw{
		Models:      map[string]string{"primary": "anthropic/claude-sonnet-4"},
		Cllama:      []string{"passthrough"},
		CllamaToken: "tiverton:abc123hex",
	}
	data, err := GenerateConfig(rc)
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]interface{}
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	modelsCfg := config["models"].(map[string]interface{})
	providers := modelsCfg["providers"].(map[string]interface{})
	anthropic := providers["anthropic"].(map[string]interface{})
	if anthropic["apiKey"] != "tiverton:abc123hex" {
		t.Errorf("expected dummy token, got %v", anthropic["apiKey"])
	}
}

func TestGenerateConfigCllamaRewritesAllModelProviders(t *testing.T) {
	rc := &driver.ResolvedClaw{
		Models: map[string]string{
			"primary":  "openrouter/moonshotai/kimi-k2.5",
			"fallback": "anthropic/claude-sonnet-4-6",
		},
		Cllama:      []string{"passthrough"},
		CllamaToken: "westin:abc123hex",
	}

	data, err := GenerateConfig(rc)
	if err != nil {
		t.Fatal(err)
	}

	var config map[string]interface{}
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}

	modelsCfg := config["models"].(map[string]interface{})
	providers := modelsCfg["providers"].(map[string]interface{})
	for _, provider := range []string{"openrouter", "anthropic"} {
		entry, ok := providers[provider].(map[string]interface{})
		if !ok {
			t.Fatalf("expected models.providers.%s", provider)
		}
		if entry["baseUrl"] != "http://cllama-passthrough:8080/v1" {
			t.Fatalf("provider %s baseUrl mismatch: %v", provider, entry["baseUrl"])
		}
		if entry["apiKey"] != "westin:abc123hex" {
			t.Fatalf("provider %s apiKey mismatch: %v", provider, entry["apiKey"])
		}
	}
}

func TestGenerateConfigAppliesConfigureDirectives(t *testing.T) {
	rc := &driver.ResolvedClaw{
		Models: make(map[string]string),
		Configures: []string{
			"openclaw config set agents.defaults.heartbeat.every 30m",
			"openclaw config set agents.defaults.heartbeat.target none",
		},
	}
	data, err := GenerateConfig(rc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var config map[string]interface{}
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	agents := config["agents"].(map[string]interface{})
	defaults := agents["defaults"].(map[string]interface{})
	heartbeat := defaults["heartbeat"].(map[string]interface{})
	if heartbeat["every"] != "30m" {
		t.Errorf("expected heartbeat.every=30m, got %v", heartbeat["every"])
	}
	if heartbeat["target"] != "none" {
		t.Errorf("expected heartbeat.target=none, got %v", heartbeat["target"])
	}
}

func TestGenerateConfigIsDeterministic(t *testing.T) {
	rc := &driver.ResolvedClaw{
		Models: map[string]string{
			"primary":  "anthropic/claude-sonnet-4",
			"fallback": "openai/gpt-4o",
		},
		Configures: []string{
			"openclaw config set agents.defaults.heartbeat.every 30m",
		},
	}
	first, _ := GenerateConfig(rc)
	second, _ := GenerateConfig(rc)
	if string(first) != string(second) {
		t.Error("config generation is not deterministic")
	}
}

func TestGenerateConfigSetsGatewayModeLocal(t *testing.T) {
	rc := &driver.ResolvedClaw{
		Models:     make(map[string]string),
		Configures: []string{},
	}

	data, err := GenerateConfig(rc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var config map[string]interface{}
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	gateway, ok := config["gateway"].(map[string]interface{})
	if !ok {
		t.Fatal("expected gateway key in config")
	}
	if gateway["mode"] != "local" {
		t.Errorf("expected gateway.mode=local, got %v", gateway["mode"])
	}
}

func TestGenerateConfigSetsWorkspace(t *testing.T) {
	rc := &driver.ResolvedClaw{
		Models:     map[string]string{"primary": "test/model"},
		Configures: []string{},
	}

	data, err := GenerateConfig(rc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var config map[string]interface{}
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	agents, ok := config["agents"].(map[string]interface{})
	if !ok {
		t.Fatal("expected agents key in config")
	}
	defaults, ok := agents["defaults"].(map[string]interface{})
	if !ok {
		t.Fatal("expected agents.defaults in config")
	}
	if defaults["workspace"] != "/claw" {
		t.Errorf("expected agents.defaults.workspace=/claw, got %v", defaults["workspace"])
	}
}

func TestGenerateConfigModelFallbacksIsArray(t *testing.T) {
	rc := &driver.ResolvedClaw{
		Models:     map[string]string{"primary": "anthropic/claude-sonnet-4-6", "fallback": "openrouter/some/model"},
		Configures: []string{},
	}

	data, err := GenerateConfig(rc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var config map[string]interface{}
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	agents := config["agents"].(map[string]interface{})
	defaults := agents["defaults"].(map[string]interface{})
	model := defaults["model"].(map[string]interface{})

	if model["primary"] != "anthropic/claude-sonnet-4-6" {
		t.Errorf("expected primary=anthropic/claude-sonnet-4-6, got %v", model["primary"])
	}
	// "fallback" slot must be emitted as "fallbacks" array
	fallbacks, ok := model["fallbacks"].([]interface{})
	if !ok {
		t.Fatalf("expected agents.defaults.model.fallbacks to be array, got %T: %v", model["fallbacks"], model["fallbacks"])
	}
	if len(fallbacks) != 1 || fallbacks[0] != "openrouter/some/model" {
		t.Errorf("expected fallbacks=[openrouter/some/model], got %v", fallbacks)
	}
	if _, exists := model["fallback"]; exists {
		t.Error("agents.defaults.model.fallback must not be present (wrong key name)")
	}
}

func TestGenerateConfigRejectsUnknownCommand(t *testing.T) {
	rc := &driver.ResolvedClaw{
		Models: make(map[string]string),
		Configures: []string{
			"some random command",
		},
	}
	_, err := GenerateConfig(rc)
	if err == nil {
		t.Fatal("expected error for unrecognized CONFIGURE command")
	}
}

func TestGenerateConfigDiscordPreEnablesPlugin(t *testing.T) {
	rc := &driver.ResolvedClaw{
		Models:     make(map[string]string),
		Configures: []string{},
		Handles: map[string]*driver.HandleInfo{
			"discord": {ID: "123456789"},
		},
	}
	data, err := GenerateConfig(rc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var config map[string]interface{}
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	plugins, ok := config["plugins"].(map[string]interface{})
	if !ok {
		t.Fatal("expected plugins key in config")
	}
	entries, ok := plugins["entries"].(map[string]interface{})
	if !ok {
		t.Fatal("expected plugins.entries in config")
	}
	discord, ok := entries["discord"].(map[string]interface{})
	if !ok {
		t.Fatal("expected plugins.entries.discord in config")
	}
	if discord["enabled"] != true {
		t.Errorf("expected plugins.entries.discord.enabled=true, got %v", discord["enabled"])
	}
}

func TestGenerateConfigHandleEnablesDiscord(t *testing.T) {
	rc := &driver.ResolvedClaw{
		Models:     make(map[string]string),
		Configures: []string{},
		Handles: map[string]*driver.HandleInfo{
			"discord": {ID: "123456789"},
		},
	}
	data, err := GenerateConfig(rc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var config map[string]interface{}
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	channels, ok := config["channels"].(map[string]interface{})
	if !ok {
		t.Fatal("expected channels key in config")
	}
	discord, ok := channels["discord"].(map[string]interface{})
	if !ok {
		t.Fatal("expected channels.discord in config")
	}
	if discord["enabled"] != true {
		t.Errorf("expected channels.discord.enabled=true, got %v", discord["enabled"])
	}
}

func TestGenerateConfigHandleEnablesSlack(t *testing.T) {
	rc := &driver.ResolvedClaw{
		Models:     make(map[string]string),
		Configures: []string{},
		Handles: map[string]*driver.HandleInfo{
			"slack": {ID: "U123456"},
		},
	}
	data, err := GenerateConfig(rc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var config map[string]interface{}
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	channels := config["channels"].(map[string]interface{})
	slack := channels["slack"].(map[string]interface{})
	if slack["enabled"] != true {
		t.Errorf("expected channels.slack.enabled=true, got %v", slack["enabled"])
	}
}

func TestGenerateConfigHandleEnablesTelegram(t *testing.T) {
	rc := &driver.ResolvedClaw{
		Models:     make(map[string]string),
		Configures: []string{},
		Handles: map[string]*driver.HandleInfo{
			"telegram": {ID: "987654321"},
		},
	}
	data, err := GenerateConfig(rc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var config map[string]interface{}
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	channels := config["channels"].(map[string]interface{})
	telegram := channels["telegram"].(map[string]interface{})
	if telegram["enabled"] != true {
		t.Errorf("expected channels.telegram.enabled=true, got %v", telegram["enabled"])
	}
}

func TestGenerateConfigHandleMultiplePlatforms(t *testing.T) {
	rc := &driver.ResolvedClaw{
		Models:     make(map[string]string),
		Configures: []string{},
		Handles: map[string]*driver.HandleInfo{
			"discord": {ID: "111"},
			"slack":   {ID: "U222"},
		},
	}
	data, err := GenerateConfig(rc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var config map[string]interface{}
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	channels := config["channels"].(map[string]interface{})
	if channels["discord"].(map[string]interface{})["enabled"] != true {
		t.Error("expected channels.discord.enabled=true")
	}
	if channels["slack"].(map[string]interface{})["enabled"] != true {
		t.Error("expected channels.slack.enabled=true")
	}
}

func TestGenerateConfigHandleUnknownPlatformNoError(t *testing.T) {
	rc := &driver.ResolvedClaw{
		Models:     make(map[string]string),
		Configures: []string{},
		Handles: map[string]*driver.HandleInfo{
			"mastodon": {ID: "@bot@example.social"},
		},
	}
	data, err := GenerateConfig(rc)
	if err != nil {
		t.Fatalf("unexpected error for unknown platform: %v", err)
	}

	var config map[string]interface{}
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	// Unknown platform should not create a channels entry
	if channels, ok := config["channels"]; ok {
		if channelMap, ok := channels.(map[string]interface{}); ok {
			if _, hasMastodon := channelMap["mastodon"]; hasMastodon {
				t.Error("expected no channels.mastodon entry for unknown platform")
			}
		}
	}
}

func TestGenerateConfigDiscordFullConfig(t *testing.T) {
	rc := &driver.ResolvedClaw{
		Models:     make(map[string]string),
		Configures: []string{},
		Handles: map[string]*driver.HandleInfo{
			"discord": {
				ID:       "123456789",
				Username: "tiverton",
				Guilds: []driver.GuildInfo{
					{
						ID:   "999888777",
						Name: "Trading Floor",
						Channels: []driver.ChannelInfo{
							{ID: "111222333", Name: "trading-floor"},
						},
					},
				},
			},
		},
	}
	data, err := GenerateConfig(rc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var config map[string]interface{}
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	channels := config["channels"].(map[string]interface{})
	discord := channels["discord"].(map[string]interface{})

	if discord["enabled"] != true {
		t.Errorf("expected channels.discord.enabled=true, got %v", discord["enabled"])
	}
	if discord["token"] != "${DISCORD_BOT_TOKEN}" {
		t.Errorf("expected channels.discord.token=${DISCORD_BOT_TOKEN}, got %v", discord["token"])
	}
	if discord["groupPolicy"] != "allowlist" {
		t.Errorf("expected channels.discord.groupPolicy=allowlist, got %v", discord["groupPolicy"])
	}
	if discord["dmPolicy"] != "allowlist" {
		t.Errorf("expected channels.discord.dmPolicy=allowlist, got %v", discord["dmPolicy"])
	}

	guilds, ok := discord["guilds"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected channels.discord.guilds to be a map, got %T", discord["guilds"])
	}
	guild, ok := guilds["999888777"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected guilds[999888777] to be a map, got %T", guilds["999888777"])
	}
	if guild["requireMention"] != true {
		t.Errorf("expected guilds[999888777].requireMention=true, got %v", guild["requireMention"])
	}
}

func TestGenerateConfigDiscordNoGuilds(t *testing.T) {
	rc := &driver.ResolvedClaw{
		Models:     make(map[string]string),
		Configures: []string{},
		Handles: map[string]*driver.HandleInfo{
			"discord": {
				ID:       "123456789",
				Username: "tiverton",
				Guilds:   nil,
			},
		},
	}
	data, err := GenerateConfig(rc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var config map[string]interface{}
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	channels := config["channels"].(map[string]interface{})
	discord := channels["discord"].(map[string]interface{})

	if _, hasGuilds := discord["guilds"]; hasGuilds {
		t.Error("expected no guilds key when Guilds slice is empty")
	}
	// Other discord fields should still be set
	if discord["token"] != "${DISCORD_BOT_TOKEN}" {
		t.Errorf("expected token to be set even with no guilds, got %v", discord["token"])
	}
}

func TestGenerateConfigDiscordAllowBots(t *testing.T) {
	rc := &driver.ResolvedClaw{
		Models:     make(map[string]string),
		Configures: []string{},
		Handles:    map[string]*driver.HandleInfo{"discord": {ID: "111"}},
	}
	data, err := GenerateConfig(rc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var config map[string]interface{}
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	discord := config["channels"].(map[string]interface{})["discord"].(map[string]interface{})
	if discord["allowBots"] != true {
		t.Errorf("expected channels.discord.allowBots=true, got %v", discord["allowBots"])
	}
}

func TestGenerateConfigDiscordMentionPatterns(t *testing.T) {
	rc := &driver.ResolvedClaw{
		Models:      make(map[string]string),
		Configures:  []string{},
		ServiceName: "tiverton",
		Handles: map[string]*driver.HandleInfo{
			"discord": {ID: "123456789", Username: "tiverton"},
		},
	}
	data, err := GenerateConfig(rc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var config map[string]interface{}
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	agents := config["agents"].(map[string]interface{})
	list, ok := agents["list"].([]interface{})
	if !ok || len(list) == 0 {
		t.Fatal("expected agents.list with at least one entry")
	}
	entry := list[0].(map[string]interface{})
	if entry["id"] != "main" {
		t.Errorf("expected agents.list[0].id=main, got %v", entry["id"])
	}
	if entry["name"] != "Tiverton" {
		t.Errorf("expected agents.list[0].name=Tiverton, got %v", entry["name"])
	}
	gc, ok := entry["groupChat"].(map[string]interface{})
	if !ok {
		t.Fatal("expected agents.list[0].groupChat")
	}
	patterns, ok := gc["mentionPatterns"].([]interface{})
	if !ok || len(patterns) == 0 {
		t.Fatal("expected mentionPatterns to be a non-empty array")
	}
	// Must contain the text pattern and the Discord native mention pattern
	patternStrs := make([]string, len(patterns))
	for i, p := range patterns {
		patternStrs[i] = p.(string)
	}
	hasText, hasMention := false, false
	for _, p := range patternStrs {
		if p == `(?i)\b@?tiverton\b` {
			hasText = true
		}
		if p == `<@!?123456789>` {
			hasMention = true
		}
	}
	if !hasText {
		t.Errorf("expected text mention pattern, got %v", patternStrs)
	}
	if !hasMention {
		t.Errorf("expected Discord native mention pattern, got %v", patternStrs)
	}
}

func TestGenerateConfigDiscordGuildUsersAndChannels(t *testing.T) {
	rc := &driver.ResolvedClaw{
		Models:     make(map[string]string),
		Configures: []string{},
		Handles: map[string]*driver.HandleInfo{
			"discord": {
				ID:       "AAA",
				Username: "tiverton",
				Guilds: []driver.GuildInfo{{
					ID: "GUILD1",
					Channels: []driver.ChannelInfo{
						{ID: "CHAN1", Name: "trading-floor"},
					},
				}},
			},
		},
	}
	data, err := GenerateConfig(rc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var config map[string]interface{}
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	discord := config["channels"].(map[string]interface{})["discord"].(map[string]interface{})
	guild := discord["guilds"].(map[string]interface{})["GUILD1"].(map[string]interface{})

	// Own ID in users list
	users, ok := guild["users"].([]interface{})
	if !ok {
		t.Fatal("expected guild.users to be an array")
	}
	found := false
	for _, u := range users {
		if u.(string) == "AAA" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected own ID %q in guild users, got %v", "AAA", users)
	}

	// Per-channel entries
	channels, ok := guild["channels"].(map[string]interface{})
	if !ok {
		t.Fatal("expected guild.channels to be a map")
	}
	ch, ok := channels["CHAN1"].(map[string]interface{})
	if !ok {
		t.Fatal("expected channels.CHAN1 entry")
	}
	if ch["allow"] != true {
		t.Error("expected channel.allow=true")
	}
	if ch["requireMention"] != true {
		t.Error("expected channel.requireMention=true")
	}
}

func TestGenerateConfigDiscordPeerHandlesInUsers(t *testing.T) {
	rc := &driver.ResolvedClaw{
		Models:     make(map[string]string),
		Configures: []string{},
		Handles: map[string]*driver.HandleInfo{
			"discord": {
				ID:     "OWN",
				Guilds: []driver.GuildInfo{{ID: "G1"}},
			},
		},
		PeerHandles: map[string]map[string]*driver.HandleInfo{
			"westin": {"discord": {ID: "PEER1"}},
			"logan":  {"discord": {ID: "PEER2"}},
		},
	}
	data, err := GenerateConfig(rc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var config map[string]interface{}
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	discord := config["channels"].(map[string]interface{})["discord"].(map[string]interface{})
	guild := discord["guilds"].(map[string]interface{})["G1"].(map[string]interface{})
	users, ok := guild["users"].([]interface{})
	if !ok {
		t.Fatal("expected guild.users array")
	}
	got := make(map[string]bool)
	for _, u := range users {
		got[u.(string)] = true
	}
	for _, expected := range []string{"OWN", "PEER1", "PEER2"} {
		if !got[expected] {
			t.Errorf("expected ID %q in guild users, got %v", expected, users)
		}
	}
}

func TestGenerateConfigMultiPlatformMentionPatterns(t *testing.T) {
	rc := &driver.ResolvedClaw{
		ServiceName: "multi-bot",
		ClawType:    "openclaw",
		Handles: map[string]*driver.HandleInfo{
			"discord":  {ID: "111", Username: "multibot"},
			"telegram": {ID: "222", Username: "multibot"},
		},
		PeerHandles: map[string]map[string]*driver.HandleInfo{},
	}

	config, err := GenerateConfig(rc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	agentsList, _ := getPath(config, "agents.list")
	agents, ok := agentsList.([]interface{})
	if !ok || len(agents) == 0 {
		t.Fatal("expected agents.list")
	}
	agent := agents[0].(map[string]interface{})
	gc, ok := agent["groupChat"].(map[string]interface{})
	if !ok {
		t.Fatal("expected groupChat")
	}
	patterns := gc["mentionPatterns"].([]interface{})
	// Should have patterns from BOTH platforms, not just whichever ran last.
	// Discord contributes: (?i)\b@?multibot\b and <@!?111>
	// Telegram contributes: (?i)\b@?multibot\b (deduped) — but no native mention
	// Minimum: 2 unique patterns (text + discord native)
	if len(patterns) < 2 {
		t.Errorf("expected at least 2 mention patterns from multi-platform, got %d: %v", len(patterns), patterns)
	}
}

func TestGenerateConfigHandleNilMeansNoChannels(t *testing.T) {
	rc := &driver.ResolvedClaw{
		Models:     make(map[string]string),
		Configures: []string{},
		Handles:    nil,
	}
	data, err := GenerateConfig(rc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var config map[string]interface{}
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	if _, ok := config["channels"]; ok {
		t.Error("expected no channels key when Handles is nil")
	}
}
