package openclaw

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mostlydev/clawdapus/internal/cllama"
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

func decodeConfig(t *testing.T, data []byte) map[string]interface{} {
	t.Helper()

	var config map[string]interface{}
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	return config
}

func providerConfigEntry(t *testing.T, config map[string]interface{}, provider string) map[string]interface{} {
	t.Helper()

	modelsCfg, ok := config["models"].(map[string]interface{})
	if !ok {
		t.Fatal("expected models config")
	}
	providers, ok := modelsCfg["providers"].(map[string]interface{})
	if !ok {
		t.Fatal("expected models.providers config")
	}
	entry, ok := providers[provider].(map[string]interface{})
	if !ok {
		t.Fatalf("expected models.providers.%s config", provider)
	}
	return entry
}

func firstProviderModelID(t *testing.T, providerConfig map[string]interface{}) string {
	t.Helper()

	modelEntries, ok := providerConfig["models"].([]interface{})
	if !ok || len(modelEntries) != 1 {
		t.Fatalf("expected exactly one provider model entry, got %T %v", providerConfig["models"], providerConfig["models"])
	}
	entry, ok := modelEntries[0].(map[string]interface{})
	if !ok {
		t.Fatalf("expected provider model entry object, got %T", modelEntries[0])
	}
	modelID, ok := entry["id"].(string)
	if !ok {
		t.Fatalf("expected provider model id string, got %T", entry["id"])
	}
	return modelID
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
	if anthropic["baseUrl"] != "http://cllama:8080/v1" {
		t.Errorf("expected proxy baseUrl, got %v", anthropic["baseUrl"])
	}
	if anthropic["api"] != "anthropic-messages" {
		t.Fatalf("expected anthropic provider behind cllama to use anthropic-messages, got %v", anthropic["api"])
	}
	modelEntries, ok := anthropic["models"].([]interface{})
	if !ok || len(modelEntries) == 0 {
		t.Fatalf("expected models.providers.anthropic.models entries, got %T %v", anthropic["models"], anthropic["models"])
	}
	entry, ok := modelEntries[0].(map[string]interface{})
	if !ok {
		t.Fatalf("expected first anthropic model entry object, got %T", modelEntries[0])
	}
	if entry["id"] != "anthropic/claude-sonnet-4" {
		t.Fatalf("expected anthropic model id to stay provider-prefixed for cllama, got %v", entry["id"])
	}
}

func TestGenerateConfigCllamaGoogleUsesOpenAICompletions(t *testing.T) {
	rc := &driver.ResolvedClaw{
		Models:      map[string]string{"primary": "google/gemini-3-flash-preview"},
		Cllama:      []string{"passthrough"},
		CllamaToken: "weston:abc123hex",
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
	google, ok := providers["google"].(map[string]interface{})
	if !ok {
		t.Fatal("expected models.providers.google config")
	}
	if google["baseUrl"] != "http://cllama:8080/v1" {
		t.Fatalf("expected proxy baseUrl, got %v", google["baseUrl"])
	}
	if google["apiKey"] != "weston:abc123hex" {
		t.Fatalf("expected cllama bearer token, got %v", google["apiKey"])
	}
	if google["api"] != "openai-completions" {
		t.Fatalf("expected google provider behind cllama to use openai-completions, got %v", google["api"])
	}
	modelEntries, ok := google["models"].([]interface{})
	if !ok || len(modelEntries) != 1 {
		t.Fatalf("expected one google model entry, got %T %v", google["models"], google["models"])
	}
	entry, ok := modelEntries[0].(map[string]interface{})
	if !ok {
		t.Fatalf("expected google model entry object, got %T", modelEntries[0])
	}
	if entry["id"] != "google/gemini-3-flash-preview" {
		t.Fatalf("expected google model id to stay provider-prefixed for cllama, got %v", entry["id"])
	}
}

func TestGenerateConfigCllamaRejectsSyntheticIngressProviderPrefix(t *testing.T) {
	rc := &driver.ResolvedClaw{
		Models: map[string]string{
			"primary": "cllama/google/gemini-3-flash-preview",
		},
		Cllama: []string{"passthrough"},
	}

	_, err := GenerateConfig(rc)
	if err == nil {
		t.Fatal("expected synthetic ingress provider prefix to fail")
	}
	if !strings.Contains(err.Error(), `invalid cllama provider/model ref for slot "primary": "cllama/google/gemini-3-flash-preview"`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestGenerateConfigDirectGoogleKeepsNativeAPI(t *testing.T) {
	rc := &driver.ResolvedClaw{
		Models: map[string]string{"primary": "google/gemini-3-flash-preview"},
	}
	data, err := GenerateConfig(rc)
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := getPath(data, "models.providers.google.api"); ok {
		t.Fatalf("expected no models.providers.google config without cllama, got %v", got)
	}
	if got, ok := getPath(data, "agents.defaults.model.primary"); !ok || got != "google/gemini-3-flash-preview" {
		t.Fatalf("expected direct google model to remain on agents.defaults.model.primary, got %v (present=%v)", got, ok)
	}
}

func TestGenerateConfigDirectProviderMatrixLeavesModelsUnrewritten(t *testing.T) {
	tests := []struct {
		name  string
		model string
	}{
		{name: "google", model: "google/gemini-3-flash-preview"},
		{name: "github copilot", model: "github-copilot/gpt-4o"},
		{name: "amazon bedrock", model: "amazon-bedrock/anthropic.claude-3-7-sonnet"},
		{name: "ollama", model: "ollama/llama3.2"},
		{name: "anthropic", model: "anthropic/claude-sonnet-4"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rc := &driver.ResolvedClaw{
				Models: map[string]string{"primary": tc.model},
			}

			data, err := GenerateConfig(rc)
			if err != nil {
				t.Fatal(err)
			}
			if got, ok := getPath(data, "models.providers"); ok {
				t.Fatalf("expected no models.providers config without cllama, got %v", got)
			}
			if got, ok := getPath(data, "agents.defaults.model.primary"); !ok || got != tc.model {
				t.Fatalf("expected direct model ref %q to remain on agents.defaults.model.primary, got %v (present=%v)", tc.model, got, ok)
			}
		})
	}
}

func TestGenerateConfigCllamaProviderAPIMatrix(t *testing.T) {
	tests := []struct {
		name         string
		model        string
		wantProvider string
		wantAPI      string
		wantModelID  string
	}{
		{
			name:         "google uses openai completions",
			model:        "google/gemini-3-flash-preview",
			wantProvider: "google",
			wantAPI:      "openai-completions",
			wantModelID:  "google/gemini-3-flash-preview",
		},
		{
			name:         "github copilot uses openai completions",
			model:        "github-copilot/gpt-4o",
			wantProvider: "github-copilot",
			wantAPI:      "openai-completions",
			wantModelID:  "github-copilot/gpt-4o",
		},
		{
			name:         "amazon bedrock uses openai completions",
			model:        "amazon-bedrock/anthropic.claude-3-7-sonnet",
			wantProvider: "amazon-bedrock",
			wantAPI:      "openai-completions",
			wantModelID:  "amazon-bedrock/anthropic.claude-3-7-sonnet",
		},
		{
			name:         "ollama uses openai completions",
			model:        "ollama/llama3.2",
			wantProvider: "ollama",
			wantAPI:      "openai-completions",
			wantModelID:  "ollama/llama3.2",
		},
		{
			name:         "anthropic uses anthropic messages",
			model:        "anthropic/claude-sonnet-4",
			wantProvider: "anthropic",
			wantAPI:      "anthropic-messages",
			wantModelID:  "anthropic/claude-sonnet-4",
		},
		{
			name:         "synthetic uses anthropic messages",
			model:        "synthetic/openai/gpt-4o-mini",
			wantProvider: "synthetic",
			wantAPI:      "anthropic-messages",
			wantModelID:  "synthetic/openai/gpt-4o-mini",
		},
		{
			name:         "minimax portal uses anthropic messages",
			model:        "minimax-portal/minimax-text-01",
			wantProvider: "minimax-portal",
			wantAPI:      "anthropic-messages",
			wantModelID:  "minimax-portal/minimax-text-01",
		},
		{
			name:         "kimi alias normalizes and uses anthropic messages",
			model:        "kimi-code/kimi-k2.5",
			wantProvider: "kimi-coding",
			wantAPI:      "anthropic-messages",
			wantModelID:  "kimi-coding/kimi-k2.5",
		},
		{
			name:         "cloudflare ai gateway uses anthropic messages",
			model:        "cloudflare-ai-gateway/compat-model",
			wantProvider: "cloudflare-ai-gateway",
			wantAPI:      "anthropic-messages",
			wantModelID:  "cloudflare-ai-gateway/compat-model",
		},
		{
			name:         "xiaomi uses anthropic messages",
			model:        "xiaomi/mi-llm",
			wantProvider: "xiaomi",
			wantAPI:      "anthropic-messages",
			wantModelID:  "xiaomi/mi-llm",
		},
		{
			name:         "z ai alias normalizes and uses openai completions",
			model:        "z.ai/glm-4.5",
			wantProvider: "zai",
			wantAPI:      "openai-completions",
			wantModelID:  "zai/glm-4.5",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rc := &driver.ResolvedClaw{
				Models:      map[string]string{"primary": tc.model},
				Cllama:      []string{"passthrough"},
				CllamaToken: "weston:abc123hex",
			}

			data, err := GenerateConfig(rc)
			if err != nil {
				t.Fatal(err)
			}

			entry := providerConfigEntry(t, decodeConfig(t, data), tc.wantProvider)
			if entry["baseUrl"] != "http://cllama:8080/v1" {
				t.Fatalf("expected proxy baseUrl, got %v", entry["baseUrl"])
			}
			if entry["apiKey"] != "weston:abc123hex" {
				t.Fatalf("expected cllama bearer token, got %v", entry["apiKey"])
			}
			if entry["api"] != tc.wantAPI {
				t.Fatalf("expected provider %q behind cllama to use %q, got %v", tc.wantProvider, tc.wantAPI, entry["api"])
			}
			if got := firstProviderModelID(t, entry); got != tc.wantModelID {
				t.Fatalf("expected provider model id %q, got %q", tc.wantModelID, got)
			}
		})
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
		if entry["baseUrl"] != "http://cllama:8080/v1" {
			t.Fatalf("provider %s baseUrl mismatch: %v", provider, entry["baseUrl"])
		}
		if entry["apiKey"] != "westin:abc123hex" {
			t.Fatalf("provider %s apiKey mismatch: %v", provider, entry["apiKey"])
		}
	}

	openrouterModels, ok := providers["openrouter"].(map[string]interface{})["models"].([]interface{})
	if !ok || len(openrouterModels) != 1 {
		t.Fatalf("expected one openrouter model entry, got %T %v", providers["openrouter"].(map[string]interface{})["models"], providers["openrouter"].(map[string]interface{})["models"])
	}
	openrouterEntry, ok := openrouterModels[0].(map[string]interface{})
	if !ok {
		t.Fatalf("expected openrouter model entry object, got %T", openrouterModels[0])
	}
	if openrouterEntry["id"] != "openrouter/moonshotai/kimi-k2.5" {
		t.Fatalf("expected openrouter model id to stay provider-prefixed for cllama, got %v", openrouterEntry["id"])
	}

	anthropicModels, ok := providers["anthropic"].(map[string]interface{})["models"].([]interface{})
	if !ok || len(anthropicModels) != 1 {
		t.Fatalf("expected one anthropic model entry, got %T %v", providers["anthropic"].(map[string]interface{})["models"], providers["anthropic"].(map[string]interface{})["models"])
	}
	anthropicEntry, ok := anthropicModels[0].(map[string]interface{})
	if !ok {
		t.Fatalf("expected anthropic model entry object, got %T", anthropicModels[0])
	}
	if anthropicEntry["id"] != "anthropic/claude-sonnet-4-6" {
		t.Fatalf("expected anthropic model id to stay provider-prefixed for cllama, got %v", anthropicEntry["id"])
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

func TestOpenclawModelAPIForIngressRejectsUnknownSurface(t *testing.T) {
	_, err := openclawModelAPIForIngress(cllama.IngressSurface("vendor-native"))
	if err == nil {
		t.Fatal("expected unknown ingress surface to fail")
	}
	if err.Error() != `unsupported cllama ingress surface "vendor-native"` {
		t.Fatalf("unexpected error: %v", err)
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
	if discord["dmPolicy"] != "pairing" {
		t.Errorf("expected channels.discord.dmPolicy=pairing, got %v", discord["dmPolicy"])
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

func TestGenerateConfigPerplexityAPIKeyIsNotAutoInjectedFromEnv(t *testing.T) {
	rc := &driver.ResolvedClaw{
		Models: make(map[string]string),
		Configures: []string{
			`openclaw config set tools.web.search.provider perplexity`,
			`openclaw config set tools.web.search.perplexity.model sonar-pro`,
		},
		Environment: map[string]string{
			"PERPLEXITY_KEY":     "${PERPLEXITY_KEY}",
			"PERPLEXITY_API_KEY": "${PERPLEXITY_API_KEY}",
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

	search := config["tools"].(map[string]interface{})["web"].(map[string]interface{})["search"].(map[string]interface{})
	if search["provider"] != "perplexity" {
		t.Fatalf("expected search provider to be perplexity, got %v", search["provider"])
	}
	perplexity := search["perplexity"].(map[string]interface{})
	if _, ok := perplexity["apiKey"]; ok {
		t.Fatalf("expected perplexity apiKey to be omitted from generated config, got %v", perplexity["apiKey"])
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
	// Discord must require an explicit native mention; plain-name text matches
	// are too permissive in shared channels.
	patternStrs := make([]string, len(patterns))
	for i, p := range patterns {
		patternStrs[i] = p.(string)
	}
	hasText, hasMention := false, false
	for _, p := range patternStrs {
		if p == `\b@?tiverton\b` {
			hasText = true
		}
		if p == `<@!?123456789>` {
			hasMention = true
		}
	}
	if hasText {
		t.Errorf("did not expect plain-name Discord mention pattern, got %v", patternStrs)
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
	if ch["enabled"] != true {
		t.Error("expected channel.enabled=true")
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

func TestGenerateConfigDiscordPeerHandlesResolveEnvIDs(t *testing.T) {
	t.Setenv("OWN_DISCORD_ID", "OWN")
	t.Setenv("PEER_ONE_DISCORD_ID", "PEER1")

	rc := &driver.ResolvedClaw{
		Models:     make(map[string]string),
		Configures: []string{},
		Handles: map[string]*driver.HandleInfo{
			"discord": {
				ID:     "${OWN_DISCORD_ID}",
				Guilds: []driver.GuildInfo{{ID: "G1"}},
			},
		},
		PeerHandles: map[string]map[string]*driver.HandleInfo{
			"weston":   {"discord": {ID: "${PEER_ONE_DISCORD_ID}"}},
			"sentinel": {"discord": {ID: "${MISSING_PEER_DISCORD_ID}"}},
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
	for _, expected := range []string{"OWN", "PEER1"} {
		if !got[expected] {
			t.Errorf("expected resolved ID %q in guild users, got %v", expected, users)
		}
	}
	if got["${MISSING_PEER_DISCORD_ID}"] {
		t.Fatalf("did not expect unresolved placeholder in guild users: %v", users)
	}
}

func TestGenerateConfigHandleTelegram(t *testing.T) {
	rc := &driver.ResolvedClaw{
		ServiceName: "news-bot",
		ClawType:    "openclaw",
		Handles: map[string]*driver.HandleInfo{
			"telegram": {
				ID:       "7123456789",
				Username: "newsbot",
			},
		},
		PeerHandles: map[string]map[string]*driver.HandleInfo{},
	}

	config, err := GenerateConfig(rc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// channels.telegram.enabled
	if v, _ := getPath(config, "channels.telegram.enabled"); v != true {
		t.Error("expected channels.telegram.enabled=true")
	}

	// channels.telegram.token
	if v, _ := getPath(config, "channels.telegram.token"); v != "${TELEGRAM_BOT_TOKEN}" {
		t.Errorf("expected telegram token reference, got %v", v)
	}

	// plugins.entries.telegram.enabled
	if v, _ := getPath(config, "plugins.entries.telegram.enabled"); v != true {
		t.Error("expected plugins.entries.telegram.enabled=true")
	}

	// agents.list with mention patterns
	agentsList, _ := getPath(config, "agents.list")
	agents, ok := agentsList.([]interface{})
	if !ok || len(agents) == 0 {
		t.Fatal("expected agents.list to be populated")
	}
	agent := agents[0].(map[string]interface{})
	if agent["name"] != "Newsbot" {
		t.Errorf("expected agent name 'Newsbot', got %v", agent["name"])
	}
}

func TestGenerateConfigHandleSlack(t *testing.T) {
	rc := &driver.ResolvedClaw{
		ServiceName: "ops-bot",
		ClawType:    "openclaw",
		Handles: map[string]*driver.HandleInfo{
			"slack": {
				ID:       "U0123456789",
				Username: "opsbot",
			},
		},
		PeerHandles: map[string]map[string]*driver.HandleInfo{},
	}

	config, err := GenerateConfig(rc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if v, _ := getPath(config, "channels.slack.enabled"); v != true {
		t.Error("expected channels.slack.enabled=true")
	}
	if v, _ := getPath(config, "channels.slack.token"); v != "${SLACK_BOT_TOKEN}" {
		t.Errorf("expected slack token reference, got %v", v)
	}
	if v, _ := getPath(config, "plugins.entries.slack.enabled"); v != true {
		t.Error("expected plugins.entries.slack.enabled=true")
	}

	agentsList, _ := getPath(config, "agents.list")
	agents := agentsList.([]interface{})
	agent := agents[0].(map[string]interface{})
	if agent["name"] != "Opsbot" {
		t.Errorf("expected agent name 'Opsbot', got %v", agent["name"])
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
	// Discord contributes: <@!?111>
	// Telegram contributes: \b@?multibot\b
	// Minimum: 2 unique patterns (text + discord native)
	if len(patterns) < 2 {
		t.Errorf("expected at least 2 mention patterns from multi-platform, got %d: %v", len(patterns), patterns)
	}
}

func TestGenerateConfigTelegramPeerHandles(t *testing.T) {
	rc := &driver.ResolvedClaw{
		ServiceName: "bot-a",
		ClawType:    "openclaw",
		Handles: map[string]*driver.HandleInfo{
			"telegram": {ID: "111", Username: "bota"},
		},
		PeerHandles: map[string]map[string]*driver.HandleInfo{
			"bot-b": {
				"telegram": {ID: "222", Username: "botb"},
			},
		},
	}

	config, err := GenerateConfig(rc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify mention patterns include peer patterns
	agentsList, _ := getPath(config, "agents.list")
	agents := agentsList.([]interface{})
	agent := agents[0].(map[string]interface{})
	gc, ok := agent["groupChat"].(map[string]interface{})
	if !ok {
		t.Fatal("expected groupChat to exist")
	}
	patterns := gc["mentionPatterns"].([]interface{})
	if len(patterns) == 0 {
		t.Fatal("expected mention patterns for telegram handle")
	}
	// Verify own bot's text pattern is present
	hasOwnPattern := false
	for _, p := range patterns {
		if p == `\b@?bota\b` {
			hasOwnPattern = true
		}
	}
	if !hasOwnPattern {
		t.Errorf("expected own mention pattern \\b@?bota\\b in %v", patterns)
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

func TestGenerateConfigModelFallbackChainKeepsDeclaredOrder(t *testing.T) {
	rc := &driver.ResolvedClaw{
		Models: map[string]string{
			"primary":     "openai/gpt-5.6",
			"fallback":    "openai/gpt-5.1",
			"fallback-2":  "anthropic/claude-sonnet-5",
			"fallback-10": "anthropic/claude-haiku-4-5",
		},
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

	model := config["agents"].(map[string]interface{})["defaults"].(map[string]interface{})["model"].(map[string]interface{})
	fallbacks, ok := model["fallbacks"].([]interface{})
	if !ok {
		t.Fatalf("expected fallbacks array, got %T: %v", model["fallbacks"], model["fallbacks"])
	}
	want := []string{"openai/gpt-5.1", "anthropic/claude-sonnet-5", "anthropic/claude-haiku-4-5"}
	if len(fallbacks) != len(want) {
		t.Fatalf("fallbacks = %v, want %v", fallbacks, want)
	}
	for i, ref := range want {
		if fallbacks[i] != ref {
			t.Errorf("fallbacks[%d] = %v, want %q", i, fallbacks[i], ref)
		}
	}
	for _, key := range []string{"fallback", "fallback-2", "fallback-10"} {
		if _, exists := model[key]; exists {
			t.Errorf("agents.defaults.model.%s must not leak as a config key", key)
		}
	}
}
