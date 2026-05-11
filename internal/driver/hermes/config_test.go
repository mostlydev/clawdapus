package hermes

import (
	"strings"
	"testing"

	"github.com/mostlydev/clawdapus/internal/driver"
	"gopkg.in/yaml.v3"
)

func TestGenerateConfigOpenRouterModel(t *testing.T) {
	rc := &driver.ResolvedClaw{
		Models: map[string]string{"primary": "openrouter/anthropic/claude-sonnet-4"},
		Handles: map[string]*driver.HandleInfo{
			"discord": {},
		},
		Environment: map[string]string{
			"DISCORD_BOT_TOKEN":  "discord-token",
			"OPENROUTER_API_KEY": "or-key",
		},
	}

	mc, err := resolveModelConfig(rc)
	if err != nil {
		t.Fatalf("resolveModelConfig returned error: %v", err)
	}
	data, err := GenerateConfig(rc, mc)
	if err != nil {
		t.Fatalf("GenerateConfig returned error: %v", err)
	}

	var cfg map[string]any
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("parse generated yaml: %v", err)
	}

	model, _ := cfg["model"].(map[string]any)
	if got := model["provider"]; got != "openrouter" {
		t.Fatalf("expected openrouter provider, got %#v", got)
	}
	if got := model["default"]; got != "anthropic/claude-sonnet-4" {
		t.Fatalf("expected bare OpenRouter model id, got %#v", got)
	}
}

func TestGenerateConfigAnthropicModelUsesBareModelName(t *testing.T) {
	rc := &driver.ResolvedClaw{
		Models: map[string]string{"primary": "anthropic/claude-sonnet-4"},
		Handles: map[string]*driver.HandleInfo{
			"telegram": {},
		},
		Environment: map[string]string{
			"TELEGRAM_BOT_TOKEN": "telegram-token",
			"ANTHROPIC_API_KEY":  "anthropic-key",
		},
	}

	mc, err := resolveModelConfig(rc)
	if err != nil {
		t.Fatalf("resolveModelConfig returned error: %v", err)
	}
	data, err := GenerateConfig(rc, mc)
	if err != nil {
		t.Fatalf("GenerateConfig returned error: %v", err)
	}

	var cfg map[string]any
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("parse generated yaml: %v", err)
	}

	model, _ := cfg["model"].(map[string]any)
	if got := model["provider"]; got != "anthropic" {
		t.Fatalf("expected anthropic provider, got %#v", got)
	}
	if got := model["default"]; got != "claude-sonnet-4" {
		t.Fatalf("expected bare anthropic model, got %#v", got)
	}
}

func TestResolvedEnvValueExpandsCompoundVars(t *testing.T) {
	rc := &driver.ResolvedClaw{
		Environment: map[string]string{
			"SINGLE":   "${TEST_ID_A}",
			"COMPOUND": "${TEST_ID_A},${TEST_ID_B},${TEST_ID_C}",
			"LITERAL":  "plain-value",
		},
		RuntimeEnv: map[string]string{
			"TEST_ID_A": "111",
			"TEST_ID_B": "222",
			"TEST_ID_C": "333",
		},
	}

	if got, err := resolvedEnvValue(rc, "SINGLE"); err != nil || got != "111" {
		t.Fatalf("single var: expected 111, got %q (err=%v)", got, err)
	}
	if got, err := resolvedEnvValue(rc, "COMPOUND"); err != nil || got != "111,222,333" {
		t.Fatalf("compound var: expected 111,222,333, got %q (err=%v)", got, err)
	}
	if got, err := resolvedEnvValue(rc, "LITERAL"); err != nil || got != "plain-value" {
		t.Fatalf("literal: expected plain-value, got %q (err=%v)", got, err)
	}
}

func TestGenerateEnvFileUsesRuntimeEnvForComposeReferences(t *testing.T) {
	rc := &driver.ResolvedClaw{
		Environment: map[string]string{
			"DISCORD_ALLOWED_USERS": "${OPERATOR_DISCORD_ID},${WESTON_DISCORD_ID}",
		},
		RuntimeEnv: map[string]string{
			"OPERATOR_DISCORD_ID": "167037070349434880",
			"WESTON_DISCORD_ID":   "1464508146579148851",
		},
	}

	data, err := GenerateEnvFile(rc, &modelConfig{Env: map[string]string{}})
	if err != nil {
		t.Fatalf("GenerateEnvFile returned error: %v", err)
	}
	env := string(data)
	if !strings.Contains(env, "DISCORD_ALLOWED_USERS=167037070349434880,1464508146579148851\n") {
		t.Fatalf("expected resolved Discord allowlist in .env, got:\n%s", env)
	}
}

func TestGenerateEnvFileRejectsUnresolvedComposeReferences(t *testing.T) {
	rc := &driver.ResolvedClaw{
		Environment: map[string]string{
			"DISCORD_ALLOWED_USERS": "${OPERATOR_DISCORD_ID},${MISSING_DISCORD_ID}",
		},
		RuntimeEnv: map[string]string{
			"OPERATOR_DISCORD_ID": "167037070349434880",
		},
	}

	_, err := GenerateEnvFile(rc, &modelConfig{Env: map[string]string{}})
	if err == nil {
		t.Fatal("expected unresolved Compose reference error")
	}
	if !strings.Contains(err.Error(), "MISSING_DISCORD_ID") {
		t.Fatalf("expected missing variable in error, got: %v", err)
	}
}

func TestResolvedEnvValueFallsBackToProcessEnv(t *testing.T) {
	t.Setenv("TEST_ID_A", "111")
	rc := &driver.ResolvedClaw{
		Environment: map[string]string{"SINGLE": "${TEST_ID_A}"},
	}

	if got, err := resolvedEnvValue(rc, "SINGLE"); err != nil || got != "111" {
		t.Fatalf("single var: expected 111, got %q", got)
	}
}

func TestGenerateConfigNoBaseURLWithoutCllama(t *testing.T) {
	rc := &driver.ResolvedClaw{
		Models: map[string]string{"primary": "openrouter/anthropic/claude-sonnet-4"},
		Handles: map[string]*driver.HandleInfo{
			"discord": {},
		},
		Environment: map[string]string{
			"DISCORD_BOT_TOKEN":  "discord-token",
			"OPENROUTER_API_KEY": "or-key",
		},
	}

	mc, err := resolveModelConfig(rc)
	if err != nil {
		t.Fatalf("resolveModelConfig returned error: %v", err)
	}
	data, err := GenerateConfig(rc, mc)
	if err != nil {
		t.Fatalf("GenerateConfig returned error: %v", err)
	}
	var cfg map[string]any
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("parse generated yaml: %v", err)
	}
	model, _ := cfg["model"].(map[string]any)
	if _, exists := model["base_url"]; exists {
		t.Fatal("expected no base_url when cllama is not enabled")
	}
	if _, exists := model["api_key"]; exists {
		t.Fatal("expected no api_key when cllama is not enabled")
	}
}

func TestGenerateConfigIncludesCllamaRouting(t *testing.T) {
	rc := &driver.ResolvedClaw{
		Models:      map[string]string{"primary": "openrouter/anthropic/claude-sonnet-4"},
		Cllama:      []string{"passthrough"},
		CllamaToken: "agent-token",
		Handles: map[string]*driver.HandleInfo{
			"slack": {},
		},
		Environment: map[string]string{
			"SLACK_BOT_TOKEN": "slack-bot",
			"SLACK_APP_TOKEN": "slack-app",
		},
	}

	mc, err := resolveModelConfig(rc)
	if err != nil {
		t.Fatalf("resolveModelConfig returned error: %v", err)
	}
	data, err := GenerateConfig(rc, mc)
	if err != nil {
		t.Fatalf("GenerateConfig returned error: %v", err)
	}
	var cfg map[string]any
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("parse generated yaml: %v", err)
	}
	model, _ := cfg["model"].(map[string]any)
	if got := model["base_url"]; got != "http://cllama:8080/v1" {
		t.Fatalf("expected cllama base_url in config, got %#v", got)
	}
	if got := model["api_key"]; got != "agent-token" {
		t.Fatalf("expected cllama api_key in config, got %#v", got)
	}
	if got := model["provider"]; got != "custom" {
		t.Fatalf("expected custom provider, got %#v", got)
	}
}

func TestGenerateEnvFileSetsManagedDefaultIdentity(t *testing.T) {
	rc := &driver.ResolvedClaw{}
	data, err := GenerateEnvFile(rc, &modelConfig{Env: map[string]string{}})
	if err != nil {
		t.Fatalf("GenerateEnvFile returned error: %v", err)
	}

	env := string(data)
	if !strings.Contains(env, hermesDefaultAgentIdentityEnv+"=") {
		t.Fatalf("expected %s in .env, got:\n%s", hermesDefaultAgentIdentityEnv, env)
	}
	if !strings.Contains(env, "Clawdapus-managed agent") {
		t.Fatalf("expected managed identity in .env, got:\n%s", env)
	}
	if strings.Contains(env, "You are Hermes Agent") || strings.Contains(env, "Nous Research") {
		t.Fatalf("managed identity should not retain upstream Hermes identity, got:\n%s", env)
	}
	if !strings.Contains(env, "persistent memory behavior") {
		t.Fatalf("managed identity should preserve Hermes memory guidance contract, got:\n%s", env)
	}
}

func TestGenerateEnvFileDefaultsToolProgressOffForDiscord(t *testing.T) {
	rc := &driver.ResolvedClaw{
		Handles: map[string]*driver.HandleInfo{"discord": {}},
	}
	data, err := GenerateEnvFile(rc, &modelConfig{Env: map[string]string{}})
	if err != nil {
		t.Fatalf("GenerateEnvFile returned error: %v", err)
	}

	if !strings.Contains(string(data), hermesToolProgressModeEnv+"=off\n") {
		t.Fatalf("expected %s=off in .env, got:\n%s", hermesToolProgressModeEnv, data)
	}
}

func TestGenerateEnvFileDefaultsToolProgressOffForSlack(t *testing.T) {
	rc := &driver.ResolvedClaw{
		Handles: map[string]*driver.HandleInfo{"slack": {}},
	}
	data, err := GenerateEnvFile(rc, &modelConfig{Env: map[string]string{}})
	if err != nil {
		t.Fatalf("GenerateEnvFile returned error: %v", err)
	}

	if !strings.Contains(string(data), hermesToolProgressModeEnv+"=off\n") {
		t.Fatalf("expected %s=off in .env, got:\n%s", hermesToolProgressModeEnv, data)
	}
}

func TestGenerateEnvFileAllowsToolProgressOverride(t *testing.T) {
	rc := &driver.ResolvedClaw{
		Handles: map[string]*driver.HandleInfo{"discord": {}},
		Environment: map[string]string{
			hermesToolProgressModeEnv: "verbose",
		},
	}
	data, err := GenerateEnvFile(rc, &modelConfig{Env: map[string]string{}})
	if err != nil {
		t.Fatalf("GenerateEnvFile returned error: %v", err)
	}

	if !strings.Contains(string(data), hermesToolProgressModeEnv+"=verbose\n") {
		t.Fatalf("expected %s override in .env, got:\n%s", hermesToolProgressModeEnv, data)
	}
}

func TestGenerateEnvFilePassesThroughSlackRuntimeKnobs(t *testing.T) {
	rc := &driver.ResolvedClaw{
		Environment: map[string]string{
			"SLACK_ALLOWED_USERS":          "U111,U222",
			"SLACK_ALLOW_ALL_USERS":        "false",
			"SLACK_ALLOW_BOTS":             "true",
			"SLACK_FREE_RESPONSE_CHANNELS": "C111,C222",
			"SLACK_REACTIONS":              "false",
			"SLACK_REQUIRE_MENTION":        "true",
		},
	}
	data, err := GenerateEnvFile(rc, &modelConfig{Env: map[string]string{}})
	if err != nil {
		t.Fatalf("GenerateEnvFile returned error: %v", err)
	}

	env := string(data)
	for _, expected := range []string{
		"SLACK_ALLOWED_USERS=U111,U222\n",
		"SLACK_ALLOW_ALL_USERS=false\n",
		"SLACK_ALLOW_BOTS=true\n",
		"SLACK_FREE_RESPONSE_CHANNELS=C111,C222\n",
		"SLACK_REACTIONS=false\n",
		"SLACK_REQUIRE_MENTION=true\n",
	} {
		if !strings.Contains(env, expected) {
			t.Fatalf("expected %q in .env, got:\n%s", expected, env)
		}
	}
}

func TestGenerateEnvFileSetsAllowSilentEnv(t *testing.T) {
	rc := &driver.ResolvedClaw{
		Hermes: &driver.HermesConfig{AllowSilent: true},
	}
	data, err := GenerateEnvFile(rc, &modelConfig{Env: map[string]string{}})
	if err != nil {
		t.Fatalf("GenerateEnvFile returned error: %v", err)
	}

	if !strings.Contains(string(data), hermesAllowSilentFinalEnv+"=1\n") {
		t.Fatalf("expected %s=1 in .env, got:\n%s", hermesAllowSilentFinalEnv, data)
	}
}

func TestGenerateEnvFileDisablesTTSForDiscordByDefault(t *testing.T) {
	rc := &driver.ResolvedClaw{
		Handles: map[string]*driver.HandleInfo{"discord": {}},
	}
	data, err := GenerateEnvFile(rc, &modelConfig{Env: map[string]string{}})
	if err != nil {
		t.Fatalf("GenerateEnvFile returned error: %v", err)
	}

	if !strings.Contains(string(data), clawdapusDisabledToolsEnv+"="+hermesTextToSpeechTool+"\n") {
		t.Fatalf("expected %s=%s in .env, got:\n%s", clawdapusDisabledToolsEnv, hermesTextToSpeechTool, data)
	}
}

func TestGenerateEnvFileDisablesTTSForSlackByDefault(t *testing.T) {
	rc := &driver.ResolvedClaw{
		Handles: map[string]*driver.HandleInfo{"slack": {}},
	}
	data, err := GenerateEnvFile(rc, &modelConfig{Env: map[string]string{}})
	if err != nil {
		t.Fatalf("GenerateEnvFile returned error: %v", err)
	}

	if !strings.Contains(string(data), clawdapusDisabledToolsEnv+"="+hermesTextToSpeechTool+"\n") {
		t.Fatalf("expected %s=%s in .env, got:\n%s", clawdapusDisabledToolsEnv, hermesTextToSpeechTool, data)
	}
}

func TestGenerateEnvFileAllowsTTSOptInForDiscord(t *testing.T) {
	rc := &driver.ResolvedClaw{
		Handles: map[string]*driver.HandleInfo{"discord": {}},
		Hermes: &driver.HermesConfig{
			AllowTools: []string{hermesTextToSpeechTool},
		},
	}
	data, err := GenerateEnvFile(rc, &modelConfig{Env: map[string]string{}})
	if err != nil {
		t.Fatalf("GenerateEnvFile returned error: %v", err)
	}

	if strings.Contains(string(data), clawdapusDisabledToolsEnv+"=") {
		t.Fatalf("expected no %s when text_to_speech is allowed, got:\n%s", clawdapusDisabledToolsEnv, data)
	}
}

func TestGenerateEnvFileAllowsTTSOptInForSlack(t *testing.T) {
	rc := &driver.ResolvedClaw{
		Handles: map[string]*driver.HandleInfo{"slack": {}},
		Hermes: &driver.HermesConfig{
			AllowTools: []string{hermesTextToSpeechTool},
		},
	}
	data, err := GenerateEnvFile(rc, &modelConfig{Env: map[string]string{}})
	if err != nil {
		t.Fatalf("GenerateEnvFile returned error: %v", err)
	}

	if strings.Contains(string(data), clawdapusDisabledToolsEnv+"=") {
		t.Fatalf("expected no %s when text_to_speech is allowed, got:\n%s", clawdapusDisabledToolsEnv, data)
	}
}

func TestGenerateEnvFileKeepsTTSDisabledForUnrelatedAllowTool(t *testing.T) {
	rc := &driver.ResolvedClaw{
		Handles: map[string]*driver.HandleInfo{"discord": {}},
		Hermes: &driver.HermesConfig{
			AllowTools: []string{"other_tool"},
		},
	}
	data, err := GenerateEnvFile(rc, &modelConfig{Env: map[string]string{}})
	if err != nil {
		t.Fatalf("GenerateEnvFile returned error: %v", err)
	}

	if !strings.Contains(string(data), clawdapusDisabledToolsEnv+"="+hermesTextToSpeechTool+"\n") {
		t.Fatalf("expected %s=%s in .env, got:\n%s", clawdapusDisabledToolsEnv, hermesTextToSpeechTool, data)
	}
}

func TestGenerateEnvFileDoesNotDisableTTSForTelegramOnly(t *testing.T) {
	rc := &driver.ResolvedClaw{
		Handles: map[string]*driver.HandleInfo{"telegram": {}},
	}
	data, err := GenerateEnvFile(rc, &modelConfig{Env: map[string]string{}})
	if err != nil {
		t.Fatalf("GenerateEnvFile returned error: %v", err)
	}

	if strings.Contains(string(data), clawdapusDisabledToolsEnv+"=") {
		t.Fatalf("expected no %s for Telegram-only Hermes agent, got:\n%s", clawdapusDisabledToolsEnv, data)
	}
}

func TestGenerateEnvFileDisablesTTSForMultiPlatformWithDiscord(t *testing.T) {
	rc := &driver.ResolvedClaw{
		Handles: map[string]*driver.HandleInfo{
			"telegram": {},
			"discord":  {},
		},
	}
	data, err := GenerateEnvFile(rc, &modelConfig{Env: map[string]string{}})
	if err != nil {
		t.Fatalf("GenerateEnvFile returned error: %v", err)
	}

	if !strings.Contains(string(data), clawdapusDisabledToolsEnv+"="+hermesTextToSpeechTool+"\n") {
		t.Fatalf("expected %s=%s in .env, got:\n%s", clawdapusDisabledToolsEnv, hermesTextToSpeechTool, data)
	}
}
