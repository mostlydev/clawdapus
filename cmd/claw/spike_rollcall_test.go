//go:build spike

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

// TestSpikeRollCall reuses one Discord bot identity across all runtime spikes,
// so it exercises each runtime sequentially rather than pretending to validate
// concurrent social topology. Each subtest boots one runtime with cllama +
// clawdash, sends a Discord mention, and verifies the bot responds with a
// runtime-specific introduction.
//
// Requires: Docker, real Discord tokens + LLM API keys in examples/rollcall/.env
// Run with: go test -tags spike -v -run TestSpikeRollCall -timeout 30m ./cmd/claw/...
func TestSpikeRollCall(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	repoRoot := filepath.Join(filepath.Dir(thisFile), "..", "..")
	dir, err := filepath.Abs(filepath.Join(repoRoot, "examples", "rollcall"))
	if err != nil {
		t.Fatalf("resolve rollcall dir: %v", err)
	}

	// ── Load environment ────────────────────────────────────────────────
	env := spikeLoadDotEnv(t, filepath.Join(dir, ".env"))
	if env["DISCORD_BOT_TOKEN"] == "" {
		t.Skip("DISCORD_BOT_TOKEN not set in rollcall/.env — skipping")
	}
	if env["ROLLCALL_CHANNEL_ID"] == "" {
		env["ROLLCALL_CHANNEL_ID"] = env["DISCORD_TRADING_FLOOR_CHANNEL"]
	}
	if env["ROLLCALL_CHANNEL_ID"] == "" {
		t.Skip("ROLLCALL_CHANNEL_ID not set — skipping")
	}
	if env["DISCORD_BOT_ID"] == "" {
		t.Skip("DISCORD_BOT_ID not set — skipping")
	}
	if env["OPENROUTER_API_KEY"] == "" && env["ANTHROPIC_API_KEY"] == "" && env["XAI_API_KEY"] == "" {
		t.Skip("No LLM API key set — skipping")
	}
	xaiKey := strings.TrimSpace(env["XAI_API_KEY"])
	anthropicKey := strings.TrimSpace(env["ANTHROPIC_API_KEY"])
	openrouterKey := strings.TrimSpace(env["OPENROUTER_API_KEY"])
	if _, ok := env["XAI_API_KEY"]; !ok {
		env["XAI_API_KEY"] = ""
	}
	if _, ok := env["ANTHROPIC_API_KEY"]; !ok {
		env["ANTHROPIC_API_KEY"] = ""
	}
	if _, ok := env["OPENROUTER_API_KEY"]; !ok {
		env["OPENROUTER_API_KEY"] = ""
	}
	if env["CLLAMA_UI_PORT"] == "" {
		env["CLLAMA_UI_PORT"] = spikeFreePort(t)
	}
	if env["CLAWDASH_ADDR"] == "" {
		env["CLAWDASH_ADDR"] = ":" + spikeFreePort(t)
	}
	t.Setenv("CLLAMA_UI_PORT", env["CLLAMA_UI_PORT"])
	t.Setenv("CLAWDASH_ADDR", env["CLAWDASH_ADDR"])

	channelID := env["ROLLCALL_CHANNEL_ID"]
	botToken := env["DISCORD_BOT_TOKEN"]
	botID := env["DISCORD_BOT_ID"]
	webhookURL := env["DISCORD_WEBHOOK_URL"]
	proxyRequest := chooseRollcallProxyRequest(t, xaiKey, anthropicKey, openrouterKey)
	if webhookURL == "" {
		t.Fatal("DISCORD_WEBHOOK_URL not set in rollcall/.env")
	}

	// ── Build base images (each type has its own Dockerfile) ────────────
	// Stub runtimes always rebuild because discord-responder.sh is baked in
	// and the script may change. Real runtimes are expensive to build and
	// are skipped when they already exist locally.
	baseImages := []struct {
		tag           string
		dockerfile    string
		contextDir    string // empty = use rollcall dir
		alwaysRebuild bool   // true for stubs that embed discord-responder.sh
	}{
		{"openclaw:latest", "Dockerfile.openclaw-base", "", true},
		{"nullclaw:latest", "Dockerfile.nullclaw-base", "", true},
		{"microclaw:latest", "Dockerfile.microclaw-base", "", true},
		{"nanoclaw-orchestrator:latest", "Dockerfile.nanoclaw-base", "", true},
		{"nanobot:latest", "Dockerfile.nanobot-base", "", true},
		{"picoclaw:latest", "Dockerfile.picoclaw-base", "", true},
		// Hermes is a real runtime — build from the canonical dockerfiles dir so
		// patch-hermes-runtime.py and minisweagent_path.py are in the build context.
		{"hermes:latest", "Dockerfile", filepath.Join(repoRoot, "dockerfiles", "hermes-base"), false},
	}
	for _, b := range baseImages {
		if b.alwaysRebuild || !spikeImageExists(b.tag) {
			ctxDir := dir
			if b.contextDir != "" {
				ctxDir = b.contextDir
			}
			spikeBuildImage(t, ctxDir, b.tag, b.dockerfile)
		}
	}
	spikeEnsureCllamaPassthroughImage(t, repoRoot)

	// Build agent images (Clawfile on top of base)
	agentImages := []struct {
		image      string
		dockerfile string
	}{
		{"rollcall-openclaw:latest", "agents/oc-roll/Clawfile"},
		{"rollcall-nullclaw:latest", "agents/nc-roll/Clawfile"},
		{"rollcall-microclaw:latest", "agents/mc-roll/Clawfile"},
		{"rollcall-nanoclaw:latest", "agents/nano-roll/Clawfile"},
		{"rollcall-nanobot:latest", "agents/nb-roll/Clawfile"},
		{"rollcall-picoclaw:latest", "agents/pc-roll/Clawfile"},
		{"rollcall-hermes:latest", "agents/hm-roll/Clawfile"},
	}
	for _, a := range agentImages {
		spikeBuildImage(t, dir, a.image, a.dockerfile)
	}

	allAgents := []struct {
		name    string
		runtime string
	}{
		{"oc-roll", "openclaw"},
		{"nc-roll", "nullclaw"},
		{"mc-roll", "microclaw"},
		{"nano-roll", "nanoclaw"},
		{"nb-roll", "nanobot"},
		{"pc-roll", "picoclaw"},
		{"hm-roll", "hermes"},
	}

	// ── Expand env vars in pod YAML ─────────────────────────────────────
	expandedPod := spikeExpandEnvVars(spikeReadFile(t, filepath.Join(dir, "claw-pod.yml")), env)
	generatedPath := filepath.Join(dir, "compose.generated.yml")
	runtimeDir := filepath.Join(dir, ".claw-runtime")
	sessionHistoryDir := filepath.Join(dir, ".claw-session-history")
	defer os.Remove(generatedPath)
	defer os.RemoveAll(runtimeDir)
	defer os.RemoveAll(sessionHistoryDir)

	prev := composeUpDetach
	composeUpDetach = true
	defer func() { composeUpDetach = prev }()

	var teardownMu sync.Mutex
	var activeTeardown func()
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigCh)
	go func() {
		if _, ok := <-sigCh; ok {
			fmt.Println("[rollcall] interrupted — tearing down")
			teardownMu.Lock()
			teardown := activeTeardown
			teardownMu.Unlock()
			if teardown != nil {
				teardown()
			}
			os.Exit(130)
		}
	}()

	for _, agent := range allAgents {
		agent := agent
		t.Run(agent.runtime, func(t *testing.T) {
			const composeProject = "rollcall"
			podPath := filepath.Join(dir, fmt.Sprintf("spike-%s-pod.yml", agent.name))
			podYAML := rollcallSingleServicePod(t, expandedPod, agent.name, proxyRequest)
			if err := os.WriteFile(podPath, []byte(podYAML), 0o644); err != nil {
				t.Fatalf("write %s: %v", filepath.Base(podPath), err)
			}
			defer os.Remove(podPath)

			spikeCleanupProject(composeProject, generatedPath)

			if err := runComposeUp(podPath); err != nil {
				t.Fatalf("runComposeUp(%s): %v", filepath.Base(podPath), err)
			}

			agentContainerID := rollcallResolveContainerID(t, generatedPath, agent.name)
			cllamaContainerID := rollcallResolveContainerID(t, generatedPath, "cllama")
			clawdashContainerID := rollcallResolveContainerID(t, generatedPath, "clawdash")

			var teardownOnce sync.Once
			teardown := func() {
				teardownOnce.Do(func() {
					rollcallLogContainer(t, agentContainerID)
					rollcallLogContainer(t, cllamaContainerID)
					rollcallLogContainer(t, clawdashContainerID)
					spikeCleanupProject(composeProject, generatedPath)
					_ = os.Remove(generatedPath)
					_ = os.RemoveAll(runtimeDir)
				})
			}
			teardownMu.Lock()
			activeTeardown = teardown
			teardownMu.Unlock()
			t.Cleanup(func() {
				teardown()
				teardownMu.Lock()
				activeTeardown = nil
				teardownMu.Unlock()
			})

			spikeWaitHealthy(t, agentContainerID, 120*time.Second)

			triggerMsg := fmt.Sprintf("<@%s> Runtime check: introduce yourself and state what runtime you are running on.", botID)
			triggerMsgID := rollcallSendWebhookMessage(t, webhookURL, triggerMsg)
			t.Logf("sent runtime check for %s to channel %s via webhook (message ID: %s)", agent.runtime, channelID, triggerMsgID)

			response := rollcallWaitForRuntimeResponse(
				t,
				botToken,
				channelID,
				triggerMsgID,
				rollcallExpectedRuntimeKeywords(agent.runtime),
				2*time.Minute,
			)
			t.Logf("found %s response: %q", agent.runtime, rollcallTruncate(response, 120))

			rollcallAssertAuditTelemetry(t, podPath, agent.name, agent.runtime)
			rollcallAssertSessionHistory(t, sessionHistoryDir, agent.name)

			spikeWaitRunning(t, clawdashContainerID, 30*time.Second)
			t.Log("clawdash sidecar confirmed running")
		})
	}

	// Verify session history survived every teardown — each agent's JSONL must
	// still exist after all seven runtimes ran and tore down in sequence.
	t.Run("session_history_persistence", func(t *testing.T) {
		for _, agent := range allAgents {
			histFile := filepath.Join(sessionHistoryDir, agent.name, "history.jsonl")
			if _, err := os.Stat(histFile); os.IsNotExist(err) {
				t.Errorf("session history for %s (%s) missing after all runtimes completed — did not survive teardown", agent.name, agent.runtime)
			}
		}
		t.Logf("session history confirmed persistent for all %d agents", len(allAgents))
	})
}

// ── Discord helpers (rollcall-specific) ─────────────────────────────────────

type rollcallDiscordMessage struct {
	Content string                `json:"content"`
	Author  rollcallDiscordAuthor `json:"author"`
	ID      string                `json:"id"`
}

type rollcallDiscordAuthor struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	Bot      bool   `json:"bot"`
}

type rollcallProxyRequest struct {
	APIFormat string
	Model     string
	CllamaEnv map[string]string
}

func chooseRollcallProxyRequest(t *testing.T, xaiKey, anthropicKey, openrouterKey string) rollcallProxyRequest {
	t.Helper()

	cfg := rollcallProxyRequest{
		CllamaEnv: make(map[string]string),
	}
	if xaiKey != "" {
		cfg.CllamaEnv["XAI_API_KEY"] = xaiKey
	}
	if anthropicKey != "" {
		cfg.CllamaEnv["ANTHROPIC_API_KEY"] = anthropicKey
	}
	if openrouterKey != "" {
		cfg.CllamaEnv["OPENROUTER_API_KEY"] = openrouterKey
	}

	switch {
	case xaiKey != "":
		cfg.APIFormat = "openai"
		cfg.Model = "xai/grok-4-1-fast-reasoning"
	case anthropicKey != "":
		cfg.APIFormat = "anthropic"
		cfg.Model = "claude-sonnet-4"
	case openrouterKey != "":
		cfg.APIFormat = "openai"
		cfg.Model = "openrouter/anthropic/claude-sonnet-4"
	default:
		t.Fatal("rollcall proxy request requires at least one real provider key")
	}

	return cfg
}

func rollcallSingleServicePod(t *testing.T, expandedPod, serviceName string, proxyRequest rollcallProxyRequest) string {
	t.Helper()

	var doc map[string]interface{}
	if err := yaml.Unmarshal([]byte(expandedPod), &doc); err != nil {
		t.Fatalf("parse expanded rollcall pod: %v", err)
	}

	services, ok := doc["services"].(map[string]interface{})
	if !ok {
		t.Fatal("rollcall pod missing services map")
	}
	selected, ok := services[serviceName]
	if !ok {
		t.Fatalf("rollcall pod missing service %q", serviceName)
	}
	selectedMap, ok := selected.(map[string]interface{})
	if !ok {
		t.Fatalf("rollcall service %q is not a map", serviceName)
	}
	rollcallInjectProxyRequest(t, selectedMap, proxyRequest)
	doc["services"] = map[string]interface{}{
		serviceName: selectedMap,
	}

	out, err := yaml.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal single-service rollcall pod: %v", err)
	}
	return string(out)
}

func rollcallInjectProxyRequest(t *testing.T, service map[string]interface{}, proxyRequest rollcallProxyRequest) {
	t.Helper()

	rawEnv := make(map[string]interface{})
	if existing, ok := service["environment"]; ok && existing != nil {
		existingMap, ok := existing.(map[string]interface{})
		if !ok {
			t.Fatalf("rollcall service environment is not a map: %T", existing)
		}
		for k, v := range existingMap {
			rawEnv[k] = v
		}
	}
	rawEnv["ROLLCALL_CLLAMA_API_FORMAT"] = proxyRequest.APIFormat
	rawEnv["ROLLCALL_CLLAMA_MODEL"] = proxyRequest.Model
	service["environment"] = rawEnv

	rawClaw, ok := service["x-claw"].(map[string]interface{})
	if !ok {
		t.Fatal("rollcall service missing x-claw map")
	}

	rawCllamaEnv := make(map[string]interface{})
	if existing, ok := rawClaw["cllama-env"]; ok && existing != nil {
		existingMap, ok := existing.(map[string]interface{})
		if !ok {
			t.Fatalf("rollcall cllama-env is not a map: %T", existing)
		}
		for k, v := range existingMap {
			rawCllamaEnv[k] = v
		}
	}
	for k, v := range proxyRequest.CllamaEnv {
		if strings.TrimSpace(v) != "" {
			rawCllamaEnv[k] = v
		}
	}
	rawClaw["cllama-env"] = rawCllamaEnv
}

func rollcallResolveContainerID(t *testing.T, composePath, serviceName string) string {
	t.Helper()

	ids, err := resolveContainerIDs(composePath, serviceName)
	if err != nil {
		t.Fatalf("resolve container id for %s: %v", serviceName, err)
	}
	if len(ids) != 1 {
		t.Fatalf("expected 1 container for %s, got %v", serviceName, ids)
	}
	return ids[0]
}

func rollcallExpectedRuntimeKeywords(runtime string) []string {
	switch strings.ToLower(runtime) {
	case "nanoclaw":
		return []string{"nanoclaw", "claude agent"}
	default:
		return []string{strings.ToLower(runtime)}
	}
}

func rollcallWaitForRuntimeResponse(t *testing.T, token, channelID, afterMessageID string, wantKeywords []string, timeout time.Duration) string {
	t.Helper()

	deadline := time.Now().Add(timeout)
	seen := make(map[string]struct{})
	var botResponses []string
	for time.Now().Before(deadline) {
		messages := rollcallFetchMessages(t, token, channelID, 50, afterMessageID)
		for _, msg := range messages {
			if msg.ID != "" {
				if _, ok := seen[msg.ID]; ok {
					continue
				}
				seen[msg.ID] = struct{}{}
			}
			if !msg.Author.Bot {
				continue
			}
			botResponses = append(botResponses, rollcallTruncate(msg.Content, 200))
			content := strings.ToLower(msg.Content)
			for _, keyword := range wantKeywords {
				if strings.Contains(content, keyword) {
					return msg.Content
				}
			}
		}
		time.Sleep(5 * time.Second)
	}

	t.Fatalf("missing bot response containing any of %v after trigger %s; saw bot responses: %q", wantKeywords, afterMessageID, botResponses)
	return ""
}

func rollcallAssertAuditTelemetry(t *testing.T, spikePodPath, clawID, runtime string) {
	t.Helper()

	auditOut, auditErr := exec.Command(
		"go", "run", "../../cmd/claw/", "audit",
		"-f", spikePodPath,
		"--json", "--since", "10m",
	).CombinedOutput()
	if auditErr != nil {
		t.Logf("warning: claw audit failed for %s (%s): %v\n%s", clawID, runtime, auditErr, string(auditOut))
		return
	}

	var auditResult struct {
		Summary struct {
			Agents []struct {
				ClawID   string `json:"claw_id"`
				Requests int    `json:"requests"`
			} `json:"agents"`
		} `json:"summary"`
	}
	if err := json.Unmarshal(auditOut, &auditResult); err != nil {
		t.Logf("warning: could not parse claw audit JSON for %s (%s): %v\n%s", clawID, runtime, err, string(auditOut))
		return
	}

	agentSet := make(map[string]bool)
	for _, agent := range auditResult.Summary.Agents {
		agentSet[agent.ClawID] = true
	}
	if !agentSet[clawID] {
		t.Errorf("claw audit: missing telemetry for %s (%s) — inference did not route through cllama", clawID, runtime)
		return
	}
	if len(agentSet) != 1 {
		t.Errorf("claw audit: expected telemetry for only %s, got %v", clawID, agentSet)
		return
	}
	t.Logf("claw audit: confirmed telemetry for %s (%s)", clawID, runtime)
}

func rollcallAssertSessionHistory(t *testing.T, sessionHistoryDir, agentName string) {
	t.Helper()

	histFile := filepath.Join(sessionHistoryDir, agentName, "history.jsonl")
	data, err := os.ReadFile(histFile)
	if err != nil {
		t.Errorf("session history for %s not found at %s: %v", agentName, histFile, err)
		return
	}

	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) == 0 || (len(lines) == 1 && lines[0] == "") {
		t.Errorf("session history for %s is empty — no turns recorded", agentName)
		return
	}

	var entry struct {
		ClawID   string `json:"claw_id"`
		TS       string `json:"ts"`
		Response struct {
			Format string `json:"format"`
		} `json:"response"`
	}
	if err := json.Unmarshal([]byte(lines[0]), &entry); err != nil {
		t.Errorf("session history for %s: first line is not valid JSON: %v\n%s", agentName, err, lines[0])
		return
	}
	if entry.ClawID != agentName {
		t.Errorf("session history for %s: claw_id = %q, want %q", agentName, entry.ClawID, agentName)
	}
	if entry.TS == "" {
		t.Errorf("session history for %s: ts field is empty", agentName)
	}
	if entry.Response.Format != "json" && entry.Response.Format != "sse" {
		t.Errorf("session history for %s: response.format = %q, want \"json\" or \"sse\"", agentName, entry.Response.Format)
	}
	t.Logf("session history for %s: %d turn(s), format=%s, ts=%s", agentName, len(lines), entry.Response.Format, entry.TS)
}

func rollcallLogContainer(t *testing.T, name string) {
	t.Helper()
	out, err := exec.Command("docker", "logs", "--tail", "80", name).CombinedOutput()
	switch {
	case err == nil && len(out) > 0:
		t.Logf("=== %s logs ===\n%s", name, string(out))
	case err == nil:
		t.Logf("=== %s logs ===\n", name)
	case len(out) > 0:
		t.Logf("=== %s logs (with error: %v) ===\n%s", name, err, string(out))
	default:
		t.Logf("=== %s logs unavailable: %v ===", name, err)
	}
}

// rollcallFetchMessages fetches messages from the channel that were sent AFTER
// the given afterMessageID. This prevents false positives from old messages.
func rollcallFetchMessages(t *testing.T, token, channelID string, limit int, afterMessageID string) []rollcallDiscordMessage {
	t.Helper()
	url := fmt.Sprintf("https://discord.com/api/v10/channels/%s/messages?limit=%d", channelID, limit)
	if afterMessageID != "" {
		url += "&after=" + afterMessageID
	}
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		t.Fatalf("build Discord GET: %v", err)
	}
	req.Header.Set("Authorization", "Bot "+token)
	req.Header.Set("User-Agent", "DiscordBot (https://github.com/mostlydev/clawdapus, 1.0)")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Logf("warning: Discord GET failed: %v", err)
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil
	}
	body, _ := io.ReadAll(resp.Body)
	var messages []rollcallDiscordMessage
	json.Unmarshal(body, &messages)
	return messages
}

func rollcallTruncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// rollcallSendWebhookMessage sends a message via Discord webhook and returns
// the message ID (used for filtering subsequent message fetches).
func rollcallSendWebhookMessage(t *testing.T, webhookURL, content string) string {
	t.Helper()
	// Append ?wait=true to get the message object back (includes message ID).
	url := webhookURL
	if strings.Contains(url, "?") {
		url += "&wait=true"
	} else {
		url += "?wait=true"
	}
	body := fmt.Sprintf(`{"content":%q,"username":"Roll Call Master","allowed_mentions":{"parse":["users"]}}`, content)
	req, err := http.NewRequest("POST", url, strings.NewReader(body))
	if err != nil {
		t.Fatalf("build webhook POST: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "DiscordBot (https://github.com/mostlydev/clawdapus, 1.0)")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("send webhook message: %v", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusCreated {
		t.Fatalf("webhook POST failed: %d %s", resp.StatusCode, string(respBody))
	}

	// Extract message ID from response.
	var msg struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(respBody, &msg) == nil && msg.ID != "" {
		return msg.ID
	}
	return ""
}
