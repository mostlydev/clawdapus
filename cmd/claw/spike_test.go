//go:build spike

package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mostlydev/clawdapus/internal/build"
	"github.com/mostlydev/clawdapus/internal/pod"
	"gopkg.in/yaml.v3"
)

// TestSpikeComposeUp is a full end-to-end integration test for the trading-desk
// example. It builds images, runs claw up, verifies generated artifacts
// (openclaw.json, jobs.json, compose.generated.yml), and checks that containers
// start healthy.
//
// Requires: Docker running, real Discord bot tokens in examples/trading-desk/.env
// Run with: go test -tags spike -v -run TestSpikeComposeUp ./cmd/claw/...
func TestSpikeComposeUp(t *testing.T) {
	// Locate the trading-desk example directory relative to this test file.
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	repoRoot := filepath.Join(filepath.Dir(thisFile), "..", "..")
	dir, err := filepath.Abs(filepath.Join(repoRoot, "examples", "trading-desk"))
	if err != nil {
		t.Fatalf("resolve trading-desk dir: %v", err)
	}

	// Load .env (format: export KEY=VALUE or KEY=VALUE)
	env := spikeLoadDotEnv(t, filepath.Join(dir, ".env"))
	if env["DESK_MANAGER_BOT_TOKEN"] == "" {
		t.Skip("DESK_MANAGER_BOT_TOKEN not set in .env — skipping spike test")
	}
	// Defaults for vars not in .env
	if env["POSTGRES_PASSWORD"] == "" {
		env["POSTGRES_PASSWORD"] = "spike_test_postgres_pw"
	}
	if env["SECRET_KEY_BASE"] == "" {
		env["SECRET_KEY_BASE"] = strings.Repeat("0", 64)
	}
	if env["OPENROUTER_API_KEY"] == "" {
		env["OPENROUTER_API_KEY"] = "sk-spike-openrouter"
	}
	if env["ANTHROPIC_API_KEY"] == "" {
		env["ANTHROPIC_API_KEY"] = "sk-spike-anthropic"
	}
	for _, key := range []string{
		"MOMENTUM_TRADER_BOT_TOKEN",
		"VALUE_TRADER_BOT_TOKEN",
		"HERMES_BOT_TOKEN",
	} {
		if env[key] == "" {
			env[key] = env["DESK_MANAGER_BOT_TOKEN"]
		}
	}
	requiredIDs := []string{
		"DESK_MANAGER_DISCORD_ID",
		"MOMENTUM_TRADER_DISCORD_ID",
		"VALUE_TRADER_DISCORD_ID",
		"HERMES_DISCORD_ID",
		"DISCORD_GUILD_ID",
		"DISCORD_TRADING_FLOOR_CHANNEL",
	}
	if missing := missingEnvKeys(env, requiredIDs); len(missing) > 0 {
		t.Skipf("trading-desk spike requires env-owned Discord identity/topology (%s)", strings.Join(missing, ", "))
	}
	if env["CLLAMA_UI_PORT"] == "" {
		env["CLLAMA_UI_PORT"] = spikeFreePort(t)
	}
	if env["CLAWDASH_ADDR"] == "" {
		env["CLAWDASH_ADDR"] = ":" + spikeFreePort(t)
	}
	t.Setenv("CLLAMA_UI_PORT", env["CLLAMA_UI_PORT"])
	t.Setenv("CLAWDASH_ADDR", env["CLAWDASH_ADDR"])

	// Build images before running compose up.
	// Base runtime images are built from local Dockerfiles if not already present.
	if !spikeImageExists("openclaw:latest") {
		spikeBuildImage(t, dir, "openclaw:latest", "Dockerfile.openclaw-base")
	}
	spikeBuildImage(t, dir, "trading-desk:latest", "Clawfile")
	spikeBuildImage(t, dir, "trading-desk-nanobot:latest", "Clawfile.nanobot")
	spikeBuildImage(t, dir, "trading-desk-hermes:latest", "Clawfile.hermes")
	spikeBuildImage(t, dir, "trading-api:latest", "Dockerfile.trading-api")
	spikeEnsureRepoInfraImages(t, repoRoot, infraComponentClawAPI, infraComponentClawdash, infraComponentClawWall)
	spikeEnsureCllamaPassthroughImage(t, repoRoot)

	// Write a pre-expanded spike pod YAML so Go YAML parser sees real IDs.
	rawPod := spikeReadFile(t, filepath.Join(dir, "claw-pod.yml"))
	expandedPod := spikeExpandEnvVars(rawPod, env)
	spikePodPath := filepath.Join(dir, "spike-pod.yml")
	if err := os.WriteFile(spikePodPath, []byte(expandedPod), 0644); err != nil {
		t.Fatalf("write spike-pod.yml: %v", err)
	}
	defer os.Remove(spikePodPath)

	if !strings.Contains(rawPod, "handles-defaults:") {
		t.Fatal("trading-desk claw-pod.yml should declare x-claw.handles-defaults")
	}

	var rawPodMap map[string]interface{}
	if err := yaml.Unmarshal([]byte(rawPod), &rawPodMap); err != nil {
		t.Fatalf("parse raw trading-desk pod YAML: %v", err)
	}
	servicesMap, ok := rawPodMap["services"].(map[string]interface{})
	if !ok {
		t.Fatal("raw trading-desk pod YAML missing services map")
	}
	deskManagerRaw, ok := servicesMap["desk-manager"].(map[string]interface{})
	if !ok {
		t.Fatal("raw trading-desk pod YAML missing desk-manager service")
	}
	deskManagerClaw, ok := deskManagerRaw["x-claw"].(map[string]interface{})
	if !ok {
		t.Fatal("raw trading-desk pod YAML missing desk-manager x-claw block")
	}
	deskManagerHandles, ok := deskManagerClaw["handles"].(map[string]interface{})
	if !ok {
		t.Fatal("raw trading-desk pod YAML missing desk-manager handles block")
	}
	deskManagerDiscord, ok := deskManagerHandles["discord"].(map[string]interface{})
	if !ok {
		t.Fatal("raw trading-desk pod YAML missing desk-manager discord handle map")
	}
	if _, hasGuilds := deskManagerDiscord["guilds"]; hasGuilds {
		t.Fatal("desk-manager handle should inherit guild topology from x-claw.handles-defaults, not redeclare guilds inline")
	}

	parsedPod, err := pod.Parse(strings.NewReader(expandedPod))
	if err != nil {
		t.Fatalf("parse expanded spike pod: %v", err)
	}
	for _, svcName := range []string{"desk-manager", "momentum-trader", "value-trader", "hermes"} {
		svc := parsedPod.Services[svcName]
		if svc == nil || svc.Claw == nil {
			t.Fatalf("parsed pod: missing claw service %q", svcName)
		}
		discordHandle := svc.Claw.Handles["discord"]
		if discordHandle == nil {
			t.Fatalf("parsed pod: service %q missing discord handle", svcName)
		}
		if len(discordHandle.Guilds) != 1 {
			t.Fatalf("parsed pod: service %q expected 1 inherited guild, got %d", svcName, len(discordHandle.Guilds))
		}
		guild := discordHandle.Guilds[0]
		if guild.ID != env["DISCORD_GUILD_ID"] {
			t.Fatalf("parsed pod: service %q expected inherited guild id %q, got %q", svcName, env["DISCORD_GUILD_ID"], guild.ID)
		}
		if len(guild.Channels) != 1 || guild.Channels[0].ID != env["DISCORD_TRADING_FLOOR_CHANNEL"] {
			t.Fatalf("parsed pod: service %q expected inherited trading-floor channel %q, got %+v", svcName, env["DISCORD_TRADING_FLOOR_CHANNEL"], guild.Channels)
		}
	}

	// Paths that runComposeUp will create
	generatedPath := filepath.Join(dir, "compose.generated.yml")
	runtimeDir := filepath.Join(dir, ".claw-runtime")
	defer os.Remove(generatedPath)
	defer os.RemoveAll(runtimeDir)

	// Set global detach flag so runComposeUp starts containers in background.
	prev := composeUpDetach
	composeUpDetach = true
	defer func() { composeUpDetach = prev }()

	// Pre-teardown: clean up any containers left over from a prior run.
	spikeCleanupProject("trading-desk", generatedPath)

	// Run the full pipeline: parse → materialize → generate → docker compose up.
	if err := runComposeUp(spikePodPath); err != nil {
		t.Fatalf("runComposeUp: %v", err)
	}

	// teardown runs the compose down and dumps logs.
	teardown := func() {
		for _, svc := range []string{"desk-manager", "momentum-trader", "value-trader", "hermes", "trading-api"} {
			name := fmt.Sprintf("trading-desk-%s-1", svc)
			out, _ := exec.Command("docker", "logs", "--tail", "100", name).CombinedOutput()
			t.Logf("=== %s logs ===\n%s", name, string(out))
		}
		spikeCleanupProject("trading-desk", generatedPath)
	}
	defer teardown()

	// Handle Ctrl-C so the containers are torn down on interrupt.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigCh)
	go func() {
		if _, ok := <-sigCh; ok {
			fmt.Println("[spike] interrupted — tearing down containers")
			teardown()
			os.Exit(130)
		}
	}()

	// ── Verify desk-manager's openclaw.json ──────────────────────────────────

	configPath := filepath.Join(runtimeDir, "desk-manager", "config", "openclaw.json")
	configData := spikeReadFile(t, configPath)
	var configMap map[string]interface{}
	if err := json.Unmarshal([]byte(configData), &configMap); err != nil {
		t.Fatalf("parse openclaw.json: %v", err)
	}
	agents, ok := configMap["agents"].(map[string]interface{})
	if !ok {
		t.Fatalf("openclaw.json: missing 'agents' object")
	}
	defaults, ok := agents["defaults"].(map[string]interface{})
	if !ok {
		t.Fatalf("openclaw.json: missing 'agents.defaults' object")
	}
	model, ok := defaults["model"].(map[string]interface{})
	if !ok {
		t.Fatalf("openclaw.json: missing 'agents.defaults.model' object")
	}

	modelsCfg, ok := configMap["models"].(map[string]interface{})
	if !ok {
		t.Fatalf("openclaw.json: missing 'models' object")
	}
	providersCfg, ok := modelsCfg["providers"].(map[string]interface{})
	if !ok {
		t.Fatalf("openclaw.json: missing 'models.providers' object")
	}

	expectedProviders := make(map[string]struct{})
	if primary, _ := model["primary"].(string); primary != "" {
		if parts := strings.SplitN(primary, "/", 2); len(parts) == 2 {
			expectedProviders[parts[0]] = struct{}{}
		}
	}
	if fallbacks, _ := model["fallbacks"].([]interface{}); len(fallbacks) > 0 {
		if fallback, _ := fallbacks[0].(string); fallback != "" {
			if parts := strings.SplitN(fallback, "/", 2); len(parts) == 2 {
				expectedProviders[parts[0]] = struct{}{}
			}
		}
	}
	if len(expectedProviders) == 0 {
		t.Fatalf("openclaw.json: expected at least one provider from model refs, got primary=%v fallback=%v", model["primary"], model["fallbacks"])
	}

	var cllamaToken string
	for provider := range expectedProviders {
		entry, ok := providersCfg[provider].(map[string]interface{})
		if !ok {
			t.Fatalf("openclaw.json: missing models.providers.%s object", provider)
		}
		if got := entry["baseUrl"]; got != "http://cllama:8080/v1" {
			t.Errorf("openclaw.json: expected models.providers.%s.baseUrl=http://cllama:8080/v1, got %v", provider, got)
		}
		providerToken, _ := entry["apiKey"].(string)
		if matched, _ := regexp.MatchString(`^desk-manager:[0-9a-f]{48}$`, providerToken); !matched {
			t.Errorf("openclaw.json: expected cllama token format desk-manager:<48-hex> for provider %s, got %q", provider, providerToken)
		}
		if providerToken == env["OPENROUTER_API_KEY"] || providerToken == env["ANTHROPIC_API_KEY"] {
			t.Errorf("openclaw.json: provider %s apiKey should be cllama token, not provider key", provider)
		}
		if cllamaToken == "" {
			cllamaToken = providerToken
		} else if providerToken != cllamaToken {
			t.Errorf("openclaw.json: expected one shared cllama token across providers, got %q and %q", cllamaToken, providerToken)
		}
	}

	channels, ok := configMap["channels"].(map[string]interface{})
	if !ok {
		t.Fatalf("openclaw.json: missing 'channels' object, got: %v", configMap)
	}
	discord, ok := channels["discord"].(map[string]interface{})
	if !ok {
		t.Fatalf("openclaw.json: missing 'channels.discord' object")
	}
	if discord["token"] != "${DISCORD_BOT_TOKEN}" {
		t.Errorf("openclaw.json: expected token=${DISCORD_BOT_TOKEN}, got %q", discord["token"])
	}
	guilds, ok := discord["guilds"].(map[string]interface{})
	if !ok {
		t.Fatalf("openclaw.json: 'channels.discord.guilds' should be an object, got %T", discord["guilds"])
	}
	guildID := env["DISCORD_GUILD_ID"]
	if _, found := guilds[guildID]; !found {
		t.Errorf("openclaw.json: expected guild %q in guilds map, keys=%v", guildID, spikeMapKeys(guilds))
	}

	// Channel surface routing config: allowFrom should contain operator ID if set.
	// This proves the map-form channel surface is parsed and applied to openclaw.json.
	if operatorID := env["OPERATOR_DISCORD_ID"]; operatorID != "" {
		allowFrom, _ := discord["allowFrom"].([]interface{})
		found := false
		for _, id := range allowFrom {
			if s, ok := id.(string); ok && s == operatorID {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("openclaw.json: expected channels.discord.allowFrom to contain operator ID %q, got %v",
				operatorID, allowFrom)
		}
	}

	// ── Verify desk-manager's jobs.json ──────────────────────────────────────

	jobsPath := filepath.Join(runtimeDir, "desk-manager", "config", "cron", "jobs.json")
	jobsData := spikeReadFile(t, jobsPath)
	var jobsStore struct {
		Version int                      `json:"version"`
		Jobs    []map[string]interface{} `json:"jobs"`
	}
	if err := json.Unmarshal([]byte(jobsData), &jobsStore); err != nil {
		t.Fatalf("parse jobs.json: %v", err)
	}
	if jobsStore.Version != 1 {
		t.Fatalf("jobs.json: expected version=1, got %d", jobsStore.Version)
	}
	jobs := jobsStore.Jobs
	if len(jobs) == 0 {
		t.Fatal("jobs.json: expected at least one job, got empty array")
	}

	channelID := env["DISCORD_TRADING_FLOOR_CHANNEL"]
	for i, job := range jobs {
		delivery, ok := job["delivery"].(map[string]interface{})
		if !ok {
			t.Errorf("jobs.json job[%d]: missing delivery object", i)
			continue
		}
		if to, _ := delivery["to"].(string); to != channelID {
			t.Errorf("jobs.json job[%d]: expected delivery.to=%q, got %q", i, channelID, to)
		}
		if delivery["mode"] != "announce" {
			t.Errorf("jobs.json job[%d]: expected delivery.mode=announce, got %q", i, delivery["mode"])
		}
		payload, ok := job["payload"].(map[string]interface{})
		if !ok {
			t.Errorf("jobs.json job[%d]: missing payload object", i)
			continue
		}
		if payload["kind"] != "agentTurn" {
			t.Errorf("jobs.json job[%d]: expected payload.kind=agentTurn, got %q", i, payload["kind"])
		}
	}

	// ── Verify compose.generated.yml ────────────────────────────────────────

	composeSrc := spikeReadFile(t, generatedPath)
	if !strings.Contains(composeSrc, "/root/.openclaw/config") {
		t.Errorf("compose.generated.yml: expected to contain %q", "/root/.openclaw/config")
	}
	if !strings.Contains(composeSrc, "cllama:") {
		t.Errorf("compose.generated.yml: expected cllama service")
	}
	if !strings.Contains(composeSrc, "CLAW_CONTEXT_ROOT: /claw/context") {
		t.Errorf("compose.generated.yml: expected cllama context root env")
	}

	// ── Verify cllama context artifacts ─────────────────────────────────────

	for _, agent := range []string{"desk-manager", "momentum-trader", "value-trader", "hermes"} {
		agentDir := filepath.Join(runtimeDir, "context", agent)
		for _, rel := range []string{"AGENTS.md", "CLAWDAPUS.md", "metadata.json"} {
			if _, err := os.Stat(filepath.Join(agentDir, rel)); err != nil {
				t.Errorf("cllama context missing %s/%s: %v", agent, rel, err)
			}
		}
	}
	metaPath := filepath.Join(runtimeDir, "context", "desk-manager", "metadata.json")
	metaData := spikeReadFile(t, metaPath)
	var meta map[string]interface{}
	if err := json.Unmarshal([]byte(metaData), &meta); err != nil {
		t.Fatalf("parse desk-manager metadata.json: %v", err)
	}
	if tok, _ := meta["token"].(string); tok != cllamaToken {
		t.Errorf("metadata token mismatch: metadata=%q provider.apiKey=%q", tok, cllamaToken)
	}

	// ── Verify containers can serve the mounted files ────────────────────────

	containerName := "trading-desk-desk-manager-1"
	spikeWaitRunning(t, containerName, 45*time.Second)

	// Config file must be readable inside container and contain 'discord'
	out, err := exec.Command("docker", "exec", containerName, "cat", "/root/.openclaw/config/openclaw.json").Output()
	if err != nil {
		t.Errorf("docker exec cat openclaw.json: %v", err)
	} else if !strings.Contains(string(out), "discord") {
		t.Errorf("openclaw.json in container doesn't contain 'discord': %q", string(out))
	}

	// jobs.json must be readable and contain the real channel ID
	out2, err2 := exec.Command("docker", "exec", containerName, "cat", "/root/.openclaw/cron/jobs.json").Output()
	if err2 != nil {
		t.Errorf("docker exec cat jobs.json: %v", err2)
	} else if !strings.Contains(string(out2), channelID) {
		t.Errorf("jobs.json in container doesn't contain channel ID %q", channelID)
	}

	// Skills directory must be populated
	out3, err3 := exec.Command("docker", "exec", containerName, "ls", "/claw/skills/").Output()
	if err3 != nil {
		t.Errorf("docker exec ls /claw/skills/: %v", err3)
	} else if strings.TrimSpace(string(out3)) == "" {
		t.Error("skills directory is empty — expected at least one skill file")
	} else {
		t.Logf("skills: %s", strings.TrimSpace(string(out3)))
		// Channel surface skill should be present (generated from map-form SURFACE channel://discord)
		if strings.Contains(string(out3), "surface-discord.md") {
			t.Logf("surface-discord.md confirmed in skills")
		} else {
			t.Errorf("expected surface-discord.md in /claw/skills/, got: %s", strings.TrimSpace(string(out3)))
		}
	}

	// AGENTS.md must be readable at the workspace root
	out4, err4 := exec.Command("docker", "exec", containerName, "cat", "/claw/AGENTS.md").Output()
	if err4 != nil {
		t.Errorf("docker exec cat /claw/AGENTS.md: %v (agent file not mounted at workspace root)", err4)
	} else if strings.TrimSpace(string(out4)) == "" {
		t.Error("/claw/AGENTS.md is empty — agent instructions not mounted")
	} else {
		t.Logf("AGENTS.md: %d bytes", len(out4))
	}

	// Log openclaw config workspace and health for diagnostics.
	wsOut, _ := exec.Command("docker", "exec", containerName, "openclaw", "config", "get", "agents.defaults.workspace").CombinedOutput()
	t.Logf("agents.defaults.workspace in container: %s", strings.TrimSpace(string(wsOut)))

	healthOut, _ := exec.Command("docker", "exec", containerName, "openclaw", "health", "--json").Output()
	t.Logf("openclaw health --json: %s", strings.TrimSpace(string(healthOut)))

	// ── Verify Value-Trader (nanobot) artifacts ──────────────────────────────

	valueTraderConfigPath := filepath.Join(runtimeDir, "value-trader", "nanobot-home", "config.json")
	valueTraderConfigData := spikeReadFile(t, valueTraderConfigPath)
	var valueTraderCfg map[string]interface{}
	if err := json.Unmarshal([]byte(valueTraderConfigData), &valueTraderCfg); err != nil {
		t.Fatalf("parse value-trader nanobot config.json: %v", err)
	}

	valueTraderAgents, ok := valueTraderCfg["agents"].(map[string]interface{})
	if !ok {
		t.Fatalf("value-trader config.json: missing agents object")
	}
	valueTraderDefaults, ok := valueTraderAgents["defaults"].(map[string]interface{})
	if !ok {
		t.Fatalf("value-trader config.json: missing agents.defaults object")
	}
	if got, _ := valueTraderDefaults["model"].(string); got != "anthropic/claude-sonnet-4" {
		t.Errorf("value-trader config.json: expected agents.defaults.model=anthropic/claude-sonnet-4, got %v", valueTraderDefaults["model"])
	}
	if providers, ok := valueTraderCfg["providers"].(map[string]interface{}); ok {
		if anthropic, ok := providers["anthropic"].(map[string]interface{}); ok {
			if got, _ := anthropic["base_url"].(string); !strings.Contains(got, "cllama") {
				t.Errorf("value-trader config.json: expected providers.anthropic.base_url to point at cllama, got %v", anthropic["base_url"])
			}
		} else {
			t.Errorf("value-trader config.json: missing providers.anthropic for cllama wiring")
		}
	} else {
		t.Errorf("value-trader config.json: missing providers object")
	}

	valueTraderSeedPath := filepath.Join(runtimeDir, "value-trader", "nanobot-home", "workspace", "AGENTS.md")
	valueTraderSeed := spikeReadFile(t, valueTraderSeedPath)
	if !strings.Contains(valueTraderSeed, "Value-Trader") {
		t.Errorf("value-trader seeded AGENTS.md doesn't mention Value-Trader")
	}

	valueTraderContainer := spikeContainerName("value-trader")
	spikeWaitHealthy(t, valueTraderContainer, 60*time.Second)
	if out, err := exec.Command("docker", "exec", valueTraderContainer, "cat", "/root/.nanobot/config.json").CombinedOutput(); err != nil {
		t.Errorf("value-trader: expected /root/.nanobot/config.json in container: %v (%s)", err, strings.TrimSpace(string(out)))
	}

	// ── Verify Hermes artifacts ──────────────────────────────────────────────

	hermesConfigPath := filepath.Join(runtimeDir, "hermes", "hermes-home", "config.yaml")
	hermesConfigData := spikeReadFile(t, hermesConfigPath)
	var hermesCfg map[string]interface{}
	if err := yaml.Unmarshal([]byte(hermesConfigData), &hermesCfg); err != nil {
		t.Fatalf("parse hermes config.yaml: %v", err)
	}
	if modelCfg, ok := hermesCfg["model"].(map[string]interface{}); ok {
		if modelCfg["default"] == nil || modelCfg["default"] == "" {
			t.Errorf("hermes config.yaml: model.default is empty")
		}
		t.Logf("hermes config.yaml model: default=%v provider=%v", modelCfg["default"], modelCfg["provider"])
	} else {
		t.Fatalf("hermes config.yaml: missing 'model' object")
	}

	hermesEnvPath := filepath.Join(runtimeDir, "hermes", "hermes-home", ".env")
	hermesEnvData := spikeReadFile(t, hermesEnvPath)
	if !strings.Contains(hermesEnvData, "OPENAI_BASE_URL=") {
		t.Errorf("hermes .env: expected OPENAI_BASE_URL for cllama wiring")
	}
	if !strings.Contains(hermesEnvData, "OPENAI_API_KEY=") {
		t.Errorf("hermes .env: expected OPENAI_API_KEY (cllama bearer token)")
	}

	hermesAgentsPath := filepath.Join(runtimeDir, "hermes", "workspace", "AGENTS.md")
	hermesAgentsData := spikeReadFile(t, hermesAgentsPath)
	if !strings.Contains(hermesAgentsData, "Hermes") {
		t.Errorf("hermes AGENTS.md: expected to mention 'Hermes'")
	}
	if !strings.Contains(hermesAgentsData, "infrastructure_context") {
		t.Errorf("hermes AGENTS.md: expected inlined CLAWDAPUS.md infrastructure context")
	}
	t.Logf("hermes AGENTS.md: %d bytes", len(hermesAgentsData))

	hermesContainer := spikeContainerName("hermes")
	spikeWaitHealthy(t, hermesContainer, 60*time.Second)
	if out, err := exec.Command("docker", "exec", hermesContainer, "cat", "/root/.hermes/config.yaml").CombinedOutput(); err != nil {
		t.Errorf("hermes: expected /root/.hermes/config.yaml in container: %v (%s)", err, strings.TrimSpace(string(out)))
	}
	if out, err := exec.Command("docker", "exec", hermesContainer, "cat", "/workspace/AGENTS.md").CombinedOutput(); err != nil {
		t.Errorf("hermes: expected /workspace/AGENTS.md in container: %v (%s)", err, strings.TrimSpace(string(out)))
	} else {
		t.Logf("hermes AGENTS.md in container: %d bytes", len(out))
	}

	// Wait for trading-api to be running so its startup announcement has fired.
	spikeWaitRunning(t, spikeContainerName("trading-api"), 30*time.Second)
	// Show what env vars trading-api actually received (no values — just key presence + webhook prefix).
	if envOut, err := exec.Command("docker", "exec", spikeContainerName("trading-api"),
		"python3", "-c",
		`import os; w=os.environ.get("DISCORD_TRADING_API_WEBHOOK",""); dm=os.environ.get("CLAW_HANDLE_DESK_MANAGER_DISCORD_ID",""); mt=os.environ.get("CLAW_HANDLE_MOMENTUM_TRADER_DISCORD_ID",""); print("WEBHOOK[:60]="+repr(w[:60]),"DESK_MANAGER_ID="+repr(dm),"MOMENTUM_TRADER_ID="+repr(mt))`,
	).CombinedOutput(); err == nil {
		t.Logf("trading-api env: %s", strings.TrimSpace(string(envOut)))
	}
	// Dump trading-api logs now so we can see startup output regardless of test outcome.
	if apiLogs, err := exec.Command("docker", "logs", "--tail", "50", spikeContainerName("trading-api")).CombinedOutput(); err == nil {
		t.Logf("=== trading-api early logs ===\n%s", string(apiLogs))
	}

	// Wait for the Docker healthcheck to report "healthy" before polling Discord.
	// This means openclaw gateway + Discord connection are ready.
	spikeWaitHealthy(t, containerName, 60*time.Second)

	// ── Verify startup greetings appeared in Discord ─────────────────────────
	// Each greeting-enabled service posts a startup message.
	// Poll the Discord channel until expected messages appear (or timeout).
	spikeVerifyDiscordGreeting(t, env["DESK_MANAGER_BOT_TOKEN"], channelID, "desk-manager online", 10*time.Second)
	spikeVerifyDiscordGreeting(t, env["MOMENTUM_TRADER_BOT_TOKEN"], channelID, "momentum-trader online", 10*time.Second)
	spikeVerifyDiscordGreeting(t, env["DESK_MANAGER_BOT_TOKEN"], channelID, "value-trader online", 15*time.Second)
	spikeVerifyDiscordGreeting(t, env["DESK_MANAGER_BOT_TOKEN"], channelID, "hermes online", 15*time.Second)

	// trading-api posts its own startup message to Discord via webhook — this
	// proves non-claw services receive env vars (DISCORD_TRADING_API_WEBHOOK).
	spikeVerifyDiscordGreeting(t, env["DESK_MANAGER_BOT_TOKEN"], channelID, "trading-api online", 15*time.Second)
	spikeVerifyChannelAwarenessSourceHandle(t, channelID, 60*time.Second)

	// The startup message must contain Discord mentions for openclaw agents.
	// CLAW_HANDLE_* vars are broadcast to all pod services by claw, so trading-api
	// picks up the agent IDs and includes <@ID> mentions in its webhook message.
	// Note: the mock_server.py only formats mentions for agents it knows about
	// (desk-manager, momentum-trader).
	if deskManagerID := env["DESK_MANAGER_DISCORD_ID"]; deskManagerID != "" {
		spikeVerifyDiscordGreeting(t, env["DESK_MANAGER_BOT_TOKEN"], channelID, "<@"+deskManagerID+">", 5*time.Second)
	}
	if momentumTraderID := env["MOMENTUM_TRADER_DISCORD_ID"]; momentumTraderID != "" {
		spikeVerifyDiscordGreeting(t, env["DESK_MANAGER_BOT_TOKEN"], channelID, "<@"+momentumTraderID+">", 5*time.Second)
	}
}

// ── Helpers ──────────────────────────────────────────────────────────────────

func spikeLoadDotEnv(t *testing.T, path string) map[string]string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open .env: %v", err)
	}
	defer f.Close()
	m := make(map[string]string)
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// Strip optional 'export ' prefix
		line = strings.TrimPrefix(line, "export ")
		eq := strings.IndexByte(line, '=')
		if eq < 0 {
			continue
		}
		key := strings.TrimSpace(line[:eq])
		val := strings.TrimSpace(line[eq+1:])
		m[key] = val
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("read .env: %v", err)
	}
	return m
}

func spikeFreePort(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("allocate free port: %v", err)
	}
	defer ln.Close()
	addr, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatalf("expected TCP listener addr, got %T", ln.Addr())
	}
	return fmt.Sprintf("%d", addr.Port)
}

func spikeCleanupProject(project, generatedPath string) {
	project = strings.TrimSpace(project)
	generatedPath = strings.TrimSpace(generatedPath)

	if generatedPath != "" {
		if _, err := os.Stat(generatedPath); err == nil {
			cmd := exec.Command("docker", "compose", "-f", generatedPath, "down", "--volumes", "--remove-orphans")
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			_ = cmd.Run()
		}
	}

	if project == "" {
		return
	}

	out, err := exec.Command("docker", "ps", "-aq", "--filter", "label=com.docker.compose.project="+project).Output()
	if err != nil {
		return
	}
	ids := strings.Fields(string(out))
	if len(ids) == 0 {
		return
	}

	{
		cmd := exec.Command("docker", "compose", "-p", project, "down", "--volumes", "--remove-orphans")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		_ = cmd.Run()
	}

	rmArgs := append([]string{"rm", "-f"}, ids...)
	rm := exec.Command("docker", rmArgs...)
	rm.Stdout = os.Stdout
	rm.Stderr = os.Stderr
	_ = rm.Run()
}

var envVarRe = regexp.MustCompile(`\$\{([A-Z_][A-Z0-9_]*)\}`)

func spikeExpandEnvVars(s string, env map[string]string) string {
	return envVarRe.ReplaceAllStringFunc(s, func(match string) string {
		key := match[2 : len(match)-1] // strip ${ and }
		if v, ok := env[key]; ok {
			return v
		}
		return match // leave unexpanded if not found
	})
}

func spikeReadFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

// spikeBuildImage builds a Docker image. If dockerfile is a Clawfile it
// transpiles it first via the build package; otherwise it calls docker build
// directly (regular Dockerfile).
func spikeBuildImage(t *testing.T, contextDir, tag, dockerfile string) {
	t.Helper()
	t.Logf("building %s from %s...", tag, dockerfile)

	clawfilePath := filepath.Join(contextDir, dockerfile)

	if strings.HasPrefix(filepath.Base(dockerfile), "Clawfile") {
		// Transpile Clawfile → Dockerfile.generated, then docker build
		generatedPath, err := build.Generate(clawfilePath)
		if err != nil {
			t.Fatalf("claw build generate %s: %v", clawfilePath, err)
		}
		if err := build.BuildFromGenerated(generatedPath, tag, contextDir); err != nil {
			t.Fatalf("claw build %s: %v", tag, err)
		}
		return
	}

	cmd := exec.Command("docker", "build", "-t", tag, "-f", dockerfile, ".")
	cmd.Dir = contextDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("docker build %s: %v\n%s", tag, err, out)
	}
	spikeTagRunnerBaseImage(t, tag)
}

func spikeTagRunnerBaseImage(t *testing.T, tag string) {
	t.Helper()
	if !spikeIsRunnerBaseTag(tag) {
		return
	}

	out, err := exec.Command("docker", "image", "inspect", "--format", "{{.Id}}", tag).Output()
	if err != nil {
		t.Fatalf("inspect runner base image %s: %v", tag, err)
	}
	imageID := strings.TrimPrefix(strings.TrimSpace(string(out)), "sha256:")
	if len(imageID) > 12 {
		imageID = imageID[:12]
	}
	if imageID == "" {
		imageID = "unknown"
	}

	versioned := spikeImageRepo(tag) + ":built-" + time.Now().UTC().Format("20060102") + "-" + imageID
	t.Logf("tagging runner base %s as %s", tag, versioned)
	if out, err := exec.Command("docker", "tag", tag, versioned).CombinedOutput(); err != nil {
		t.Fatalf("tag runner base %s as %s: %v\n%s", tag, versioned, err, out)
	}
}

func spikeIsRunnerBaseTag(tag string) bool {
	switch tag {
	case "openclaw:latest",
		"nanobot:latest",
		"picoclaw:latest":
		return true
	default:
		return false
	}
}

func spikeImageRepo(imageRef string) string {
	imageRef = strings.TrimSpace(imageRef)
	slash := strings.LastIndex(imageRef, "/")
	colon := strings.LastIndex(imageRef, ":")
	if colon > slash {
		return imageRef[:colon]
	}
	return imageRef
}

func spikeEnsureRepoInfraImages(t *testing.T, repoRoot string, components ...string) {
	t.Helper()
	for _, component := range components {
		spec := infraImageSpecFor(component)
		ref := strings.TrimSpace(spec.ExpectedRef)
		if spec.Component == "" {
			t.Fatalf("unknown infra component %q", component)
		}
		if ref == "" {
			t.Fatalf("infra component %q has no pinned ref configured", component)
		}
		if spikeImageExists(ref) {
			continue
		}

		contextDir := repoRoot
		dockerfile := spec.DockerfilePath
		if strings.TrimSpace(spec.ContextDir) != "" && spec.ContextDir != "." {
			contextDir = filepath.Join(repoRoot, spec.ContextDir)
			if rel, err := filepath.Rel(contextDir, filepath.Join(repoRoot, spec.DockerfilePath)); err == nil {
				dockerfile = rel
			}
		}

		t.Logf("building local %s image as %s", component, ref)
		spikeBuildImage(t, contextDir, ref, dockerfile)
	}
}

// spikeEnsureCllamaPassthroughImage guarantees a local image exists for
// ghcr.io/mostlydev/cllama:latest. For spike coverage we prefer the local
// submodule under test over any cached image, then fall back to GitHub, then
// finally to a stub image if no real build is possible.
func spikeEnsureCllamaPassthroughImage(t *testing.T, repoRoot string) {
	t.Helper()
	tags := []string{"ghcr.io/mostlydev/cllama:latest"}
	if preferred := preferredInfraImageRef(infraComponentCllama); preferred != "" && preferred != tags[0] {
		tags = append(tags, preferred)
	}

	localContext := filepath.Join(repoRoot, "cllama")
	localDockerfile := filepath.Join(localContext, "Dockerfile")
	if repoRoot != "" {
		if _, err := os.Stat(localDockerfile); err == nil {
			t.Logf("building local cllama image from %s as %s", localContext, strings.Join(tags, ", "))
			args := []string{"build"}
			for _, tag := range tags {
				args = append(args, "-t", tag)
			}
			args = append(args, localContext)
			cmd := exec.Command("docker", args...)
			out, err := cmd.CombinedOutput()
			if err == nil {
				t.Logf("built local cllama image from working tree")
				return
			}
			t.Logf("local cllama build failed, falling back to GitHub: %v\n%s", err, out)
		}
	}

	// Try building from the GitHub repo.
	const repo = "https://github.com/mostlydev/cllama.git"
	t.Logf("building real cllama from %s", repo)
	args := []string{"build"}
	for _, tag := range tags {
		args = append(args, "-t", tag)
	}
	args = append(args, repo)
	cmd := exec.Command("docker", args...)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Logf("built real cllama image from GitHub")
		return
	}
	t.Logf("GitHub build failed, falling back to stub: %v\n%s", err, out)

	// Fallback: minimal stub that passes healthcheck but doesn't proxy.
	dockerfile := strings.NewReader(`FROM alpine:3.20
RUN cat >/cllama <<'EOF'
#!/bin/sh
if [ "$1" = "-healthcheck" ]; then
  exit 0
fi
while true; do
  sleep 3600
done
EOF
RUN chmod +x /cllama
ENTRYPOINT ["/cllama"]
`)

	stubArgs := []string{"build"}
	for _, tag := range tags {
		stubArgs = append(stubArgs, "-t", tag)
	}
	stubArgs = append(stubArgs, "-")
	stubCmd := exec.Command("docker", stubArgs...)
	stubCmd.Stdin = dockerfile
	stubOut, stubErr := stubCmd.CombinedOutput()
	if stubErr != nil {
		t.Fatalf("build cllama stub image: %v\n%s", stubErr, stubOut)
	}
	t.Logf("built cllama stub image (no real proxy) as %s", strings.Join(tags, ", "))
}

// spikeWaitHealthy waits until the Docker healthcheck reports "healthy".
// Non-fatal: logs the health state and continues even if the deadline is exceeded.
func spikeWaitHealthy(t *testing.T, containerName string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		out, err := exec.Command("docker", "inspect", "-f", "{{.State.Health.Status}}", containerName).Output()
		if err == nil && strings.TrimSpace(string(out)) == "healthy" {
			t.Logf("container %q is healthy", containerName)
			return
		}
		time.Sleep(5 * time.Second)
	}
	out, _ := exec.Command("docker", "inspect", "-f", "{{json .State.Health}}", containerName).Output()
	t.Logf("warning: container %q not healthy after %v; health state: %s", containerName, timeout, strings.TrimSpace(string(out)))
}

func spikeWaitRunning(t *testing.T, containerName string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		out, err := exec.Command("docker", "inspect", "-f", "{{.State.Status}}", containerName).Output()
		if err == nil && strings.TrimSpace(string(out)) == "running" {
			return
		}
		time.Sleep(2 * time.Second)
	}
	// Get container logs to help diagnose failures
	logs, _ := exec.Command("docker", "logs", "--tail", "20", containerName).CombinedOutput()
	t.Fatalf("container %q not running after %v\nlogs:\n%s", containerName, timeout, logs)
}

// spikeVerifyDiscordGreeting polls the Discord channel REST API until a message
// containing expectedSubstr appears, or until timeout is exceeded.
func spikeVerifyDiscordGreeting(t *testing.T, botToken, channelID, expectedSubstr string, timeout time.Duration) {
	t.Helper()
	url := fmt.Sprintf("https://discord.com/api/v10/channels/%s/messages?limit=20", channelID)
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		req, err := http.NewRequest("GET", url, nil)
		if err != nil {
			t.Fatalf("build Discord request: %v", err)
		}
		req.Header.Set("Authorization", "Bot "+botToken)
		resp, err := http.DefaultClient.Do(req)
		if err == nil && resp.StatusCode == http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			var messages []map[string]interface{}
			if json.Unmarshal(body, &messages) == nil {
				for _, msg := range messages {
					if content, ok := msg["content"].(string); ok {
						if strings.Contains(strings.ToLower(content), strings.ToLower(expectedSubstr)) {
							t.Logf("found Discord greeting %q", content)
							return
						}
					}
				}
			}
		} else if resp != nil {
			resp.Body.Close()
		}
		time.Sleep(3 * time.Second)
	}
	t.Errorf("Discord greeting %q not found in channel %s after %v", expectedSubstr, channelID, timeout)
}

func spikeVerifyChannelAwarenessSourceHandle(t *testing.T, channelID string, timeout time.Duration) {
	t.Helper()
	spikeVerifyContainerChannelAwarenessSourceHandle(t, spikeContainerName("claw-wall"), channelID, timeout)
}

func spikeVerifyContainerChannelAwarenessSourceHandle(t *testing.T, containerName, channelID string, timeout time.Duration) {
	t.Helper()
	url := fmt.Sprintf("http://127.0.0.1:8080/channel-awareness?channels=%s&since=24h&limit=50&max_chars=200000", channelID)
	want := "source=" + channelID + "/"
	deadline := time.Now().Add(timeout)
	var lastBody string
	for time.Now().Before(deadline) {
		out, err := exec.Command("docker", "exec", containerName, "wget", "-qO-", url).CombinedOutput()
		lastBody = string(out)
		if err == nil && strings.Contains(lastBody, want) {
			t.Logf("found channel-awareness source handle for channel %s", channelID)
			return
		}
		time.Sleep(3 * time.Second)
	}
	t.Errorf("channel-awareness source handle %q not found after %v; last body:\n%s", want, timeout, lastBody)
}

func spikeImageExists(tag string) bool {
	out, err := exec.Command("docker", "image", "inspect", "--format", "{{.Id}}", tag).Output()
	return err == nil && len(strings.TrimSpace(string(out))) > 0
}

func spikeMapKeys(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// spikeContainerName returns the Docker Compose container name for a service
// in the trading-desk project.
func spikeContainerName(service string) string {
	return fmt.Sprintf("trading-desk-%s-1", service)
}
