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
	geminiKey := strings.TrimSpace(env["GEMINI_API_KEY"])
	if _, ok := env["GEMINI_API_KEY"]; !ok {
		env["GEMINI_API_KEY"] = ""
	}
	availableKeys := map[string]string{
		"XAI_API_KEY":        xaiKey,
		"ANTHROPIC_API_KEY":  anthropicKey,
		"OPENROUTER_API_KEY": openrouterKey,
		"GEMINI_API_KEY":     geminiKey,
	}
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
		{"nanobot:latest", "Dockerfile.nanobot-base", "", true},
		{"picoclaw:latest", "Dockerfile.picoclaw-base", "", true},
		// Hermes is a real runtime — build from the canonical dockerfiles dir so
		// patch-hermes-runtime.py and minisweagent_path.py are in the build context.
		{"hermes:latest", "Dockerfile", filepath.Join(repoRoot, "dockerfiles", "hermes-base"), false},
		// Memory stub — shared infrastructure service used by capability-wave agents.
		{"rollcall-memory-stub:latest", "Dockerfile", filepath.Join(repoRoot, "testdata", "memory-stub"), false},
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
	spikeEnsureRepoInfraImages(t, repoRoot, infraComponentClawdash, infraComponentClawWall)
	spikeEnsureCllamaPassthroughImage(t, repoRoot)

	// Build agent images (Clawfile on top of base)
	agentImages := []struct {
		image      string
		dockerfile string
	}{
		{"rollcall-openclaw:latest", "agents/oc-roll/Clawfile"},
		{"rollcall-nanobot:latest", "agents/nb-roll/Clawfile"},
		{"rollcall-picoclaw:latest", "agents/pc-roll/Clawfile"},
		{"rollcall-hermes:latest", "agents/hm-roll/Clawfile"},
	}
	for _, a := range agentImages {
		spikeBuildImage(t, dir, a.image, a.dockerfile)
	}

	// allAgents is the per-runtime test matrix. Each entry pins a model and
	// (for stub runtimes) an inbound proxy request format so the spike
	// exercises both cllama ingress surfaces and multiple distinct
	// provider/model pairs in a single run.
	//
	// expectedSurface is the cllama ingress surface a runtime is expected to
	// hit when its request reaches the proxy. It is asserted at the end of the
	// test as coverage protection for ADR-023.
	allAgents := []rollcallAgentEntry{
		{
			name:            "oc-roll",
			runtime:         "openclaw",
			subtestName:     "openclaw_openai_surface",
			modelOverride:   "openrouter/anthropic/claude-sonnet-4",
			expectedSurface: "openai-chat-completions",
			requireKeys:     []string{"OPENROUTER_API_KEY"},
		},
		{
			name:            "oc-roll",
			runtime:         "openclaw",
			subtestName:     "openclaw_anthropic_surface",
			modelOverride:   "anthropic/claude-sonnet-5",
			expectedSurface: "anthropic-messages",
			requireKeys:     []string{"ANTHROPIC_API_KEY"},
		},
		{
			// Direct regression test for issue #127: openclaw + google/gemini-*
			// behind cllama. This is the exact provider that triggered the bug
			// fixed by ADR-023's shared ingress surface matrix. The new code
			// must compile this to api="openai-completions" — not the old
			// vendor-native "google-generative-ai".
			name:            "oc-roll",
			runtime:         "openclaw",
			subtestName:     "openclaw_google_surface",
			modelOverride:   "google/gemini-3.6-flash",
			expectedSurface: "openai-chat-completions",
			requireKeys:     []string{"GEMINI_API_KEY"},
		},
		{
			// nb-roll carries the anthropic-messages ingress surface for the
			// default run. The retired nullclaw/nanoclaw stubs used to cover
			// it (ADR-026). Stubs send the
			// bare provider/model ref directly via curl, so we must use a
			// model name that Anthropic actually recognises today
			// (claude-sonnet-4 alone is no longer a valid alias upstream).
			name:            "nb-roll",
			runtime:         "nanobot",
			proxyFormat:     "anthropic",
			proxyModel:      "anthropic/claude-sonnet-5",
			expectedSurface: "anthropic-messages",
			requireKeys:     []string{"ANTHROPIC_API_KEY"},
		},
		{
			// #137 fixed the upstream gateway-port regression; keep PicoClaw in
			// the default matrix so rollcall covers every retained driver.
			name:            "pc-roll",
			runtime:         "picoclaw",
			proxyFormat:     "anthropic",
			proxyModel:      "anthropic/claude-sonnet-5",
			expectedSurface: "anthropic-messages",
			requireKeys:     []string{"ANTHROPIC_API_KEY"},
		},
		{
			name:            "hm-roll",
			runtime:         "hermes",
			modelOverride:   "openrouter/anthropic/claude-sonnet-4",
			expectedSurface: "openai-chat-completions",
			requireKeys:     []string{"OPENROUTER_API_KEY"},
		},
	}

	if !rollcallMatrixHasUsableEntry(allAgents, availableKeys) {
		t.Skip("no API keys available for any rollcall matrix entry — skipping")
	}

	var (
		exercisedSurfacesMu sync.Mutex
		exercisedSurfaces   = make(map[string]bool)
	)
	markSurfaceExercised := func(surface string) {
		if surface == "" {
			return
		}
		exercisedSurfacesMu.Lock()
		exercisedSurfaces[surface] = true
		exercisedSurfacesMu.Unlock()
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
		subtest := agent.subtestName
		if subtest == "" {
			subtest = agent.runtime
		}
		t.Run(subtest, func(t *testing.T) {
			missing := rollcallMissingKeys(agent.requireKeys, availableKeys)
			if len(missing) > 0 {
				t.Skipf("missing API keys for %s: %v", subtest, missing)
			}
			proxyRequest := rollcallProxyRequestForEntry(agent, availableKeys)
			const composeProject = "rollcall"
			podPath := filepath.Join(dir, fmt.Sprintf("spike-%s-pod.yml", subtest))
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
			clawWallContainerID := rollcallResolveContainerID(t, generatedPath, "claw-wall")

			var teardownOnce sync.Once
			teardown := func() {
				teardownOnce.Do(func() {
					rollcallLogContainer(t, agentContainerID)
					rollcallLogContainer(t, cllamaContainerID)
					rollcallLogContainer(t, clawdashContainerID)
					rollcallLogContainer(t, clawWallContainerID)
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
			spikeWaitHealthy(t, clawWallContainerID, 60*time.Second)

			auditWindowStart := time.Now()
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
			spikeVerifyContainerChannelAwarenessSourceHandle(t, clawWallContainerID, channelID, 60*time.Second)

			rollcallAssertAuditTelemetry(t, podPath, agent.name, agent.runtime, auditWindowStart)
			rollcallAssertSessionHistory(t, sessionHistoryDir, agent.name)

			// oc-roll has memory configured — confirm memory_op telemetry fired.
			// Only assert memory on the openai-surface oc-roll variant so we don't
			// double-spend the assertion against memory state from a prior subtest.
			if agent.name == "oc-roll" && agent.subtestName == "openclaw_openai_surface" {
				rollcallAssertMemoryTelemetry(t, cllamaContainerID, agent.name)
			}

			spikeWaitRunning(t, clawdashContainerID, 30*time.Second)
			t.Log("clawdash sidecar confirmed running")

			// Reaching this line means the runtime successfully completed an LLM
			// call through cllama using its assigned ingress surface.
			markSurfaceExercised(agent.expectedSurface)
		})
	}

	// Verify session history survived every teardown — each agent's JSONL must
	// still exist after all matrix entries ran and tore down in sequence.
	// Skip entries that never ran (missing API keys) so partial coverage runs
	// don't fail this check.
	t.Run("session_history_persistence", func(t *testing.T) {
		seen := make(map[string]bool)
		for _, agent := range allAgents {
			if len(rollcallMissingKeys(agent.requireKeys, availableKeys)) > 0 {
				continue
			}
			if seen[agent.name] {
				continue
			}
			seen[agent.name] = true
			histFile := filepath.Join(sessionHistoryDir, agent.name, "history.jsonl")
			if _, err := os.Stat(histFile); os.IsNotExist(err) {
				t.Errorf("session history for %s (%s) missing after all runtimes completed — did not survive teardown", agent.name, agent.runtime)
			}
		}
		t.Logf("session history confirmed persistent for %d distinct agents across %d matrix entries", len(seen), len(allAgents))
	})

	// Confirm the matrix exercised both canonical cllama ingress surfaces.
	// This is the spike-level regression for ADR-023: if a future change
	// reroutes everything to a single surface (or breaks one of them) it
	// will be caught here even if individual subtests still pass.
	t.Run("ingress_surface_coverage", func(t *testing.T) {
		exercisedSurfacesMu.Lock()
		defer exercisedSurfacesMu.Unlock()
		required := []string{"anthropic-messages", "openai-chat-completions"}
		for _, surface := range required {
			if !exercisedSurfaces[surface] {
				t.Errorf("cllama ingress surface %q was not exercised by any successful subtest", surface)
			}
		}
		exercised := make([]string, 0, len(exercisedSurfaces))
		for surface := range exercisedSurfaces {
			exercised = append(exercised, surface)
		}
		t.Logf("exercised cllama ingress surfaces: %v", exercised)
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
	APIFormat     string
	Model         string
	ModelOverride string // sets x-claw.models.primary for real runners (oc-roll, hm-roll)
	CllamaEnv     map[string]string
}

// rollcallAgentEntry describes a single matrix row in the rollcall spike. It
// pins both the model that the runtime should request and the cllama ingress
// surface that request is expected to traverse.
type rollcallAgentEntry struct {
	name            string   // agent service name in the rollcall pod
	runtime         string   // runtime label, used for log/keyword matching
	subtestName     string   // optional t.Run name; defaults to runtime
	modelOverride   string   // x-claw.models.primary override (real runners)
	proxyFormat     string   // ROLLCALL_CLLAMA_API_FORMAT (stub runners)
	proxyModel      string   // ROLLCALL_CLLAMA_MODEL (stub runners)
	expectedSurface string   // "anthropic-messages" | "openai-chat-completions"
	requireKeys     []string // env keys required for this entry to run
}

// rollcallMatrixHasUsableEntry returns true if at least one matrix entry has
// every required key present in availableKeys.
func rollcallMatrixHasUsableEntry(entries []rollcallAgentEntry, availableKeys map[string]string) bool {
	for _, entry := range entries {
		if len(rollcallMissingKeys(entry.requireKeys, availableKeys)) == 0 {
			return true
		}
	}
	return false
}

// rollcallMissingKeys reports which of required are absent or empty in
// availableKeys.
func rollcallMissingKeys(required []string, availableKeys map[string]string) []string {
	var missing []string
	for _, key := range required {
		if strings.TrimSpace(availableKeys[key]) == "" {
			missing = append(missing, key)
		}
	}
	return missing
}

// rollcallProxyRequestForEntry builds the inbound proxy-request and cllama-env
// configuration for a single matrix entry.
func rollcallProxyRequestForEntry(entry rollcallAgentEntry, availableKeys map[string]string) rollcallProxyRequest {
	cfg := rollcallProxyRequest{
		APIFormat:     entry.proxyFormat,
		Model:         entry.proxyModel,
		ModelOverride: entry.modelOverride,
		CllamaEnv:     make(map[string]string),
	}
	// Forward only the keys this entry actually needs to the cllama sidecar.
	// We deliberately do not flood every container with every API key.
	for _, key := range entry.requireKeys {
		if v := strings.TrimSpace(availableKeys[key]); v != "" {
			cfg.CllamaEnv[key] = v
		}
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

	// Keep the selected agent plus any infrastructure services (services without
	// an x-claw.agent field, e.g. mem-svc). This allows capability-wave features
	// like memory to be exercised without restructuring the pod fixture.
	kept := map[string]interface{}{serviceName: selectedMap}
	for name, svc := range services {
		if name == serviceName {
			continue
		}
		svcMap, ok := svc.(map[string]interface{})
		if !ok {
			continue
		}
		xClaw, _ := svcMap["x-claw"].(map[string]interface{})
		if xClaw == nil || xClaw["agent"] == nil {
			kept[name] = svc
		}
	}
	doc["services"] = kept

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
	if proxyRequest.APIFormat != "" {
		rawEnv["ROLLCALL_CLLAMA_API_FORMAT"] = proxyRequest.APIFormat
	}
	if proxyRequest.Model != "" {
		rawEnv["ROLLCALL_CLLAMA_MODEL"] = proxyRequest.Model
	}
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

	// Apply x-claw.models.primary override for both real runners and stubs.
	// Pod-level model slots overlay image MODEL labels at compile time, which
	// also seeds cllama's per-agent model policy. Stubs that send a model the
	// policy doesn't allow get clamped via cllama's "disallowed_clamped"
	// intervention back to the policy default — so the policy must allow the
	// model the request will actually carry. For real runners we use
	// proxyRequest.ModelOverride; for stubs we mirror proxyRequest.Model.
	policyModel := strings.TrimSpace(proxyRequest.ModelOverride)
	if policyModel == "" {
		policyModel = strings.TrimSpace(proxyRequest.Model)
	}
	if policyModel != "" {
		rawModels := make(map[string]interface{})
		if existing, ok := rawClaw["models"]; ok && existing != nil {
			existingMap, ok := existing.(map[string]interface{})
			if !ok {
				t.Fatalf("rollcall x-claw.models is not a map: %T", existing)
			}
			for k, v := range existingMap {
				rawModels[k] = v
			}
		}
		rawModels["primary"] = policyModel
		rawClaw["models"] = rawModels
	}
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
	return []string{strings.ToLower(runtime)}
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

func rollcallAssertAuditTelemetry(t *testing.T, spikePodPath, clawID, runtime string, windowStart time.Time) {
	t.Helper()
	since := time.Since(windowStart) + 10*time.Second
	if since < 30*time.Second {
		since = 30 * time.Second
	}
	since = since.Round(time.Second)

	auditOut, auditErr := exec.Command(
		"go", "run", "../../cmd/claw/", "audit",
		"-f", spikePodPath,
		"--json", "--since", since.String(),
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
		ID       string `json:"id"`
		Version  int    `json:"version"`
		ClawID   string `json:"claw_id"`
		TS       string `json:"ts"`
		Status   string `json:"status"`
		Response struct {
			Format string `json:"format"`
		} `json:"response"`
		ToolTrace []json.RawMessage `json:"tool_trace,omitempty"`
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
	if !strings.HasPrefix(entry.ID, "hist1_") {
		t.Errorf("session history for %s: id = %q, want hist1_ prefix (EnsureID not called)", agentName, entry.ID)
	}
	if entry.Version != 1 {
		t.Errorf("session history for %s: version = %d, want 1", agentName, entry.Version)
	}
	t.Logf("session history for %s: %d turn(s), format=%s, ts=%s, id=%s, tool_rounds=%d",
		agentName, len(lines), entry.Response.Format, entry.TS, entry.ID[:min(len(entry.ID), 20)], len(entry.ToolTrace))
}

// rollcallAssertMemoryTelemetry reads the cllama container logs and confirms
// that at least one memory_op event (recall or retain) was emitted for the
// given agent. It is called only for agents that have memory configured.
func rollcallAssertMemoryTelemetry(t *testing.T, cllamaContainerID, agentName string) {
	t.Helper()

	out, err := exec.Command("docker", "logs", cllamaContainerID).CombinedOutput()
	if err != nil {
		t.Logf("warning: could not read cllama logs for memory telemetry check: %v", err)
		return
	}

	var recallSeen, retainSeen bool
	for _, line := range strings.Split(string(out), "\n") {
		if !strings.Contains(line, `"type":"memory_op"`) && !strings.Contains(line, `"type": "memory_op"`) {
			continue
		}
		if strings.Contains(line, `"`+agentName+`"`) {
			if strings.Contains(line, `"recall"`) {
				recallSeen = true
			}
			if strings.Contains(line, `"retain"`) {
				retainSeen = true
			}
		}
	}

	if !recallSeen {
		t.Errorf("cllama logs: no memory_op recall event found for agent %s", agentName)
	} else {
		t.Logf("cllama logs: memory_op recall confirmed for %s", agentName)
	}
	if !retainSeen {
		t.Errorf("cllama logs: no memory_op retain event found for agent %s", agentName)
	} else {
		t.Logf("cllama logs: memory_op retain confirmed for %s", agentName)
	}
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
