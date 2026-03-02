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
	"syscall"
	"testing"
	"time"
)

// TestSpikeRollCall boots all 6 driver types with cllama + clawdash, sends a
// Discord roll-call message, and verifies each agent responds with an
// AI-generated introduction mentioning its runtime.
//
// Requires: Docker, real Discord tokens + LLM API keys in examples/rollcall/.env
// Run with: go test -tags spike -v -run TestSpikeRollCall -timeout 10m ./cmd/claw/...
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
	if env["OPENROUTER_API_KEY"] == "" && env["ANTHROPIC_API_KEY"] == "" {
		t.Skip("No LLM API key set — skipping")
	}
	if env["ANTHROPIC_API_KEY"] == "" {
		env["ANTHROPIC_API_KEY"] = "sk-unused-anthropic"
	}
	if env["OPENROUTER_API_KEY"] == "" {
		env["OPENROUTER_API_KEY"] = "sk-unused-openrouter"
	}

	channelID := env["ROLLCALL_CHANNEL_ID"]
	botToken := env["DISCORD_BOT_TOKEN"]

	// ── Build base images (each type has its own Dockerfile) ────────────
	baseImages := []struct {
		tag        string
		dockerfile string
	}{
		{"openclaw:latest", "Dockerfile.openclaw-base"},
		{"nullclaw:latest", "Dockerfile.nullclaw-base"},
		{"microclaw:latest", "Dockerfile.microclaw-base"},
		{"nanoclaw-orchestrator:latest", "Dockerfile.nanoclaw-base"},
		{"nanobot:latest", "Dockerfile.nanobot-base"},
		{"picoclaw:latest", "Dockerfile.picoclaw-base"},
	}
	for _, b := range baseImages {
		if !spikeImageExists(b.tag) {
			spikeBuildImage(t, dir, b.tag, b.dockerfile)
		}
	}
	spikeEnsureCllamaPassthroughImage(t)

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
	}

	// ── Expand env vars in pod YAML ─────────────────────────────────────
	rawPod := spikeReadFile(t, filepath.Join(dir, "claw-pod.yml"))
	expandedPod := spikeExpandEnvVars(rawPod, env)
	spikePodPath := filepath.Join(dir, "spike-pod.yml")
	if err := os.WriteFile(spikePodPath, []byte(expandedPod), 0644); err != nil {
		t.Fatalf("write spike-pod.yml: %v", err)
	}
	defer os.Remove(spikePodPath)

	generatedPath := filepath.Join(dir, "compose.generated.yml")
	runtimeDir := filepath.Join(dir, ".claw-runtime")
	defer os.Remove(generatedPath)
	defer os.RemoveAll(runtimeDir)

	// ── Pre-teardown ────────────────────────────────────────────────────
	preClean := exec.Command("docker", "compose", "-p", "rollcall", "down", "--volumes", "--remove-orphans")
	preClean.Stdout = os.Stdout
	preClean.Stderr = os.Stderr
	_ = preClean.Run()

	// ── Compose up ──────────────────────────────────────────────────────
	prev := composeUpDetach
	composeUpDetach = true
	defer func() { composeUpDetach = prev }()

	if err := runComposeUp(spikePodPath); err != nil {
		t.Fatalf("runComposeUp: %v", err)
	}

	teardown := func() {
		for _, a := range allAgents {
			name := fmt.Sprintf("rollcall-%s-1", a.name)
			out, _ := exec.Command("docker", "logs", "--tail", "50", name).CombinedOutput()
			t.Logf("=== %s logs ===\n%s", name, string(out))
		}
		cmd := exec.Command("docker", "compose", "-f", generatedPath, "down", "--volumes")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		_ = cmd.Run()
	}
	defer teardown()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigCh)
	go func() {
		if _, ok := <-sigCh; ok {
			fmt.Println("[rollcall] interrupted — tearing down")
			teardown()
			os.Exit(130)
		}
	}()

	// ── Wait for healthy ────────────────────────────────────────────────
	for _, a := range allAgents {
		container := fmt.Sprintf("rollcall-%s-1", a.name)
		spikeWaitHealthy(t, container, 120*time.Second)
	}

	// ── Send trigger message ────────────────────────────────────────────
	triggerMsg := "Roll call! Each bot, introduce yourself and state what runtime you are running on."
	rollcallSendDiscordMessage(t, botToken, channelID, triggerMsg)
	t.Logf("sent roll-call trigger to channel %s", channelID)

	// ── Poll for responses ──────────────────────────────────────────────
	runtimeKeywords := map[string]bool{
		"openclaw":  false,
		"nullclaw":  false,
		"microclaw": false,
		"nanoclaw":  false,
		"nanobot":   false,
		"picoclaw":  false,
	}

	deadline := time.Now().Add(3 * time.Minute)
	for time.Now().Before(deadline) {
		messages := rollcallFetchMessages(t, botToken, channelID, 50)
		for _, msg := range messages {
			content := strings.ToLower(msg.Content)
			if msg.Author.Bot {
				for keyword := range runtimeKeywords {
					if !runtimeKeywords[keyword] && strings.Contains(content, keyword) {
						runtimeKeywords[keyword] = true
						t.Logf("found %s response: %q", keyword, rollcallTruncate(msg.Content, 120))
					}
				}
				// nanoclaw might say "claude agent sdk" instead
				if !runtimeKeywords["nanoclaw"] && strings.Contains(content, "claude agent") {
					runtimeKeywords["nanoclaw"] = true
					t.Logf("found nanoclaw response (via 'claude agent'): %q", rollcallTruncate(msg.Content, 120))
				}
			}
		}

		allFound := true
		for _, found := range runtimeKeywords {
			if !found {
				allFound = false
				break
			}
		}
		if allFound {
			t.Log("all 6 runtime responses received")
			break
		}
		time.Sleep(10 * time.Second)
	}

	for keyword, found := range runtimeKeywords {
		if !found {
			t.Errorf("missing roll-call response from %s agent", keyword)
		}
	}

	// ── Check cllama costs ──────────────────────────────────────────────
	cllamaContainer := "rollcall-cllama-1"
	costsOut, err := exec.Command("docker", "exec", cllamaContainer,
		"wget", "-q", "-O-", "http://localhost:8081/costs/api").Output()
	if err != nil {
		t.Logf("warning: could not fetch cllama costs: %v", err)
	} else {
		var costs map[string]interface{}
		if json.Unmarshal(costsOut, &costs) == nil {
			t.Logf("cllama costs: %s", string(costsOut))
			if reqs, ok := costs["total_requests"].(float64); ok && reqs < 6 {
				t.Errorf("expected at least 6 cllama requests, got %.0f", reqs)
			}
		}
	}

	// ── Verify clawdash is running ──────────────────────────────────────
	clawdashContainer := "rollcall-clawdash-1"
	spikeWaitRunning(t, clawdashContainer, 30*time.Second)
	t.Log("clawdash sidecar confirmed running")
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

func rollcallSendDiscordMessage(t *testing.T, token, channelID, content string) {
	t.Helper()
	url := fmt.Sprintf("https://discord.com/api/v10/channels/%s/messages", channelID)
	body := fmt.Sprintf(`{"content":%q}`, content)
	req, err := http.NewRequest("POST", url, strings.NewReader(body))
	if err != nil {
		t.Fatalf("build Discord POST: %v", err)
	}
	req.Header.Set("Authorization", "Bot "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "DiscordBot (https://github.com/mostlydev/clawdapus, 1.0)")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("send Discord message: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("Discord POST failed: %d %s", resp.StatusCode, string(respBody))
	}
}

func rollcallFetchMessages(t *testing.T, token, channelID string, limit int) []rollcallDiscordMessage {
	t.Helper()
	url := fmt.Sprintf("https://discord.com/api/v10/channels/%s/messages?limit=%d", channelID, limit)
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
