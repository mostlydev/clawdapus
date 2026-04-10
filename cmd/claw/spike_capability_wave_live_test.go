//go:build spike

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

// TestSpikeCapabilityWaveLive exercises the full capability wave with a real
// Discord-triggered turn:
//   - oc-roll runs behind cllama with memory enabled
//   - a managed HTTP tool is compiled and injected
//   - the model executes the managed tool through cllama mediation
//   - the final text response is posted back to Discord
//   - session history records tool_trace and cllama logs emit memory_op events
//
// Requires: Docker, real Discord credentials in examples/rollcall/.env, and at
// least one real provider API key.
func TestSpikeCapabilityWaveLive(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	repoRoot := filepath.Join(filepath.Dir(thisFile), "..", "..")
	dir, err := filepath.Abs(filepath.Join(repoRoot, "examples", "rollcall"))
	if err != nil {
		t.Fatalf("resolve rollcall dir: %v", err)
	}

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
	if env["DISCORD_WEBHOOK_URL"] == "" {
		t.Skip("DISCORD_WEBHOOK_URL not set — skipping")
	}
	if env["OPENROUTER_API_KEY"] == "" && env["ANTHROPIC_API_KEY"] == "" && env["XAI_API_KEY"] == "" {
		t.Skip("No LLM API key set — skipping")
	}
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

	proxyRequest := capabilityWaveProxyRequest(t, env)

	spikeBuildImage(t, dir, "openclaw:latest", "Dockerfile.openclaw-base")
	spikeBuildImage(t, dir, "rollcall-openclaw:latest", "agents/oc-roll/Clawfile")
	spikeBuildImage(t, filepath.Join(repoRoot, "examples", "reference-memory"), "rollcall-reference-memory:latest", "Dockerfile")
	spikeBuildImage(t, filepath.Join(repoRoot, "testdata", "tool-stub"), "rollcall-tool-stub:latest", "Dockerfile")
	spikeEnsureRepoInfraImages(t, repoRoot, infraComponentClawdash, infraComponentClawWall)
	spikeEnsureCllamaPassthroughImage(t, repoRoot)

	expandedPod := spikeExpandEnvVars(spikeReadFile(t, filepath.Join(dir, "claw-pod.yml")), env)
	podYAML := capabilityWaveLivePod(t, expandedPod, proxyRequest)
	podPath := filepath.Join(dir, "spike-capability-wave-live-pod.yml")
	if err := os.WriteFile(podPath, []byte(podYAML), 0o644); err != nil {
		t.Fatalf("write %s: %v", filepath.Base(podPath), err)
	}
	defer os.Remove(podPath)

	generatedPath := filepath.Join(dir, "compose.generated.yml")
	runtimeDir := filepath.Join(dir, ".claw-runtime")
	sessionHistoryDir := filepath.Join(dir, ".claw-session-history")

	prevDetach := composeUpDetach
	composeUpDetach = true
	defer func() { composeUpDetach = prevDetach }()

	const composeProject = "capability-wave-live"
	spikeCleanupProject(composeProject, generatedPath)
	t.Cleanup(func() {
		spikeCleanupProject(composeProject, generatedPath)
		_ = os.Remove(generatedPath)
		_ = os.RemoveAll(runtimeDir)
	})

	if err := runComposeUp(podPath); err != nil {
		t.Fatalf("runComposeUp(%s): %v", filepath.Base(podPath), err)
	}

	agentContainerID := rollcallResolveContainerID(t, generatedPath, "oc-roll")
	cllamaContainerID := rollcallResolveContainerID(t, generatedPath, "cllama")
	clawdashContainerID := rollcallResolveContainerID(t, generatedPath, "clawdash")
	toolContainerID := rollcallResolveContainerID(t, generatedPath, "tool-svc")

	t.Cleanup(func() {
		if !t.Failed() {
			return
		}
		rollcallLogContainer(t, agentContainerID)
		rollcallLogContainer(t, cllamaContainerID)
		rollcallLogContainer(t, toolContainerID)
	})

	capabilityWaveAssertToolsManifest(t, filepath.Join(dir, ".claw-runtime", "context", "oc-roll", "tools.json"))

	spikeWaitHealthy(t, agentContainerID, 120*time.Second)

	auditWindowStart := time.Now()
	triggerMsg := fmt.Sprintf(
		"<@%s> Call the managed tool tool-svc.get_runtime_context before replying. Then answer in one sentence with the runtime and the exact phrase capability wave online.",
		env["DISCORD_BOT_ID"],
	)
	triggerMsgID := rollcallSendWebhookMessage(t, env["DISCORD_WEBHOOK_URL"], triggerMsg)
	t.Logf("sent capability-wave trigger to channel %s via webhook (message ID: %s)", env["ROLLCALL_CHANNEL_ID"], triggerMsgID)

	response := rollcallWaitForRuntimeResponse(
		t,
		env["DISCORD_BOT_TOKEN"],
		env["ROLLCALL_CHANNEL_ID"],
		triggerMsgID,
		[]string{"openclaw", "capability wave online"},
		2*time.Minute,
	)
	t.Logf("found capability-wave response: %q", rollcallTruncate(response, 160))

	rollcallAssertAuditTelemetry(t, podPath, "oc-roll", "openclaw", auditWindowStart)
	rollcallAssertSessionHistory(t, sessionHistoryDir, "oc-roll")
	rollcallAssertManagedToolTrace(t, sessionHistoryDir, "oc-roll", "tool-svc")
	rollcallAssertMemoryTelemetry(t, cllamaContainerID, "oc-roll")

	spikeWaitRunning(t, clawdashContainerID, 30*time.Second)
	t.Log("clawdash sidecar confirmed running")
}

// capabilityWaveProxyRequest picks a single inbound proxy-request shape for
// the capability-wave-live spike. Unlike TestSpikeRollCall this test runs only
// one runtime, so it just needs one provider/model that the local environment
// can serve.
func capabilityWaveProxyRequest(t *testing.T, env map[string]string) rollcallProxyRequest {
	t.Helper()

	xaiKey := strings.TrimSpace(env["XAI_API_KEY"])
	anthropicKey := strings.TrimSpace(env["ANTHROPIC_API_KEY"])
	openrouterKey := strings.TrimSpace(env["OPENROUTER_API_KEY"])

	cfg := rollcallProxyRequest{CllamaEnv: make(map[string]string)}
	switch {
	case xaiKey != "":
		cfg.APIFormat = "openai"
		cfg.Model = "xai/grok-4-1-fast-reasoning"
		cfg.CllamaEnv["XAI_API_KEY"] = xaiKey
	case anthropicKey != "":
		cfg.APIFormat = "anthropic"
		cfg.Model = "claude-sonnet-4"
		cfg.CllamaEnv["ANTHROPIC_API_KEY"] = anthropicKey
	case openrouterKey != "":
		cfg.APIFormat = "openai"
		cfg.Model = "openrouter/anthropic/claude-sonnet-4"
		cfg.CllamaEnv["OPENROUTER_API_KEY"] = openrouterKey
	default:
		t.Fatal("capability-wave proxy request requires at least one real provider key")
	}
	return cfg
}

func capabilityWaveLivePod(t *testing.T, expandedPod string, proxyRequest rollcallProxyRequest) string {
	t.Helper()

	base := rollcallSingleServicePod(t, expandedPod, "oc-roll", proxyRequest)

	var doc map[string]interface{}
	if err := yaml.Unmarshal([]byte(base), &doc); err != nil {
		t.Fatalf("parse capability-wave pod: %v", err)
	}

	if top, ok := doc["x-claw"].(map[string]interface{}); ok {
		top["pod"] = "capability-wave-live"
	}

	services, ok := doc["services"].(map[string]interface{})
	if !ok {
		t.Fatal("capability-wave pod missing services map")
	}

	selected, ok := services["oc-roll"].(map[string]interface{})
	if !ok {
		t.Fatal("capability-wave pod missing oc-roll service map")
	}

	rawEnv := make(map[string]interface{})
	if existing, ok := selected["environment"].(map[string]interface{}); ok {
		for k, v := range existing {
			rawEnv[k] = v
		}
	}
	rawEnv["ROLLCALL_REPLY_MODE"] = "managed_text"
	selected["environment"] = rawEnv

	rawClaw, ok := selected["x-claw"].(map[string]interface{})
	if !ok {
		t.Fatal("capability-wave pod missing oc-roll x-claw map")
	}
	rawClaw["tools"] = []interface{}{
		map[string]interface{}{
			"service": "tool-svc",
			"allow":   []interface{}{"all"},
		},
	}

	services["tool-svc"] = map[string]interface{}{
		"image": "rollcall-tool-stub:latest",
		"build": map[string]interface{}{
			"context":    "../../testdata/tool-stub",
			"dockerfile": "Dockerfile",
		},
		"expose": []interface{}{"8080"},
	}

	out, err := yaml.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal capability-wave pod: %v", err)
	}
	return string(out)
}

func capabilityWaveAssertToolsManifest(t *testing.T, path string) {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read tools manifest %s: %v", path, err)
	}

	var manifest struct {
		Version int `json:"version"`
		Tools   []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("parse tools manifest %s: %v\n%s", path, err, data)
	}
	if manifest.Version != 1 {
		t.Fatalf("tools manifest version = %d, want 1", manifest.Version)
	}
	for _, tool := range manifest.Tools {
		if tool.Name == "tool-svc.get_runtime_context" {
			t.Logf("tools.json confirms managed tool %s", tool.Name)
			return
		}
	}
	t.Fatalf("tools.json missing tool-svc.get_runtime_context: %s", data)
}

func rollcallAssertManagedToolTrace(t *testing.T, sessionHistoryDir, agentName, serviceName string) {
	t.Helper()

	histFile := filepath.Join(sessionHistoryDir, agentName, "history.jsonl")
	data, err := os.ReadFile(histFile)
	if err != nil {
		t.Fatalf("read session history for %s: %v", agentName, err)
	}

	type toolCall struct {
		Name       string `json:"name"`
		Service    string `json:"service"`
		StatusCode int    `json:"status_code"`
	}
	type toolRound struct {
		Round     int        `json:"round"`
		ToolCalls []toolCall `json:"tool_calls"`
	}
	type historyEntry struct {
		ToolTrace []toolRound `json:"tool_trace"`
	}

	var found bool
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var entry historyEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("parse session history for %s: %v\n%s", agentName, err, line)
		}
		for _, round := range entry.ToolTrace {
			for _, call := range round.ToolCalls {
				if call.Service != serviceName {
					continue
				}
				found = true
				if call.StatusCode != 200 {
					t.Fatalf("managed tool call for %s returned status %d", serviceName, call.StatusCode)
				}
				t.Logf("managed tool trace confirmed: round=%d service=%s tool=%s status=%d", round.Round, call.Service, call.Name, call.StatusCode)
			}
		}
	}

	if !found {
		t.Fatalf("expected at least one managed tool trace for service %s in %s", serviceName, histFile)
	}
}
