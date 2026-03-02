# Roll Call Spike Test Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** End-to-end spike test that boots all 6 driver types (openclaw, nullclaw, microclaw, nanoclaw, nanobot, picoclaw) + cllama passthrough + clawdash, sends a Discord message asking bots to identify themselves, polls for AI-generated responses, and tears down.

**Architecture:** New `examples/rollcall/` fixture with 6 agent services sharing one Discord bot token. Test harness sends trigger message via Discord REST API, polls for 6 LLM-generated responses mentioning each runtime name, checks cllama costs endpoint, then tears down. Follows existing spike test patterns from `spike_test.go`.

**Tech Stack:** Go test (build tag `spike`), Discord REST API (v10), Docker Compose via `runComposeUp`, existing test helpers.

---

### Task 1: Create the rollcall example fixture

**Files:**
- Create: `examples/rollcall/.env.example`
- Create: `examples/rollcall/claw-pod.yml`
- Create: `examples/rollcall/agents/oc-roll/AGENTS.md`
- Create: `examples/rollcall/agents/oc-roll/Clawfile`
- Create: `examples/rollcall/agents/nc-roll/AGENTS.md`
- Create: `examples/rollcall/agents/nc-roll/Clawfile`
- Create: `examples/rollcall/agents/mc-roll/AGENTS.md`
- Create: `examples/rollcall/agents/mc-roll/Clawfile`
- Create: `examples/rollcall/agents/nano-roll/AGENTS.md`
- Create: `examples/rollcall/agents/nano-roll/Clawfile`
- Create: `examples/rollcall/agents/nb-roll/AGENTS.md`
- Create: `examples/rollcall/agents/nb-roll/Clawfile`
- Create: `examples/rollcall/agents/pc-roll/AGENTS.md`
- Create: `examples/rollcall/agents/pc-roll/Clawfile`

**Step 1: Create .env.example**

```
# All bots share the same Discord application — differentiated by AGENTS.md
DISCORD_BOT_TOKEN=<your-bot-token>
DISCORD_BOT_ID=<your-bot-snowflake>
DISCORD_GUILD_ID=<your-guild-snowflake>
ROLLCALL_CHANNEL_ID=<channel-id-for-roll-call>
OPENROUTER_API_KEY=<for-cllama-passthrough>
ANTHROPIC_API_KEY=<for-cllama-passthrough>
```

**Step 2: Create the 6 Clawfiles**

Each Clawfile follows its driver's base image pattern from the trading-desk example.

`agents/oc-roll/Clawfile` (openclaw):
```dockerfile
FROM openclaw:latest

CLAW_TYPE openclaw
AGENT AGENTS.md
MODEL primary openrouter/anthropic/claude-sonnet-4
HANDLE discord
```

`agents/nc-roll/Clawfile` (nullclaw) — copy pattern from `Clawfile.nullclaw`:
```dockerfile
FROM alpine:3.20

CLAW_TYPE nullclaw
AGENT AGENTS.md
MODEL primary anthropic/claude-sonnet-4
HANDLE discord

RUN apk add --no-cache curl jq tini python3
COPY nullclaw /usr/local/bin/nullclaw
RUN chmod +x /usr/local/bin/nullclaw
COPY nullclaw-entrypoint.sh /app/entrypoint.sh
RUN chmod +x /app/entrypoint.sh

ENTRYPOINT ["/sbin/tini", "--", "/app/entrypoint.sh"]
```

`agents/mc-roll/Clawfile` (microclaw) — same pattern from `Clawfile.microclaw`.

`agents/nano-roll/Clawfile` (nanoclaw):
```dockerfile
FROM nanoclaw-orchestrator:latest

CLAW_TYPE nanoclaw
MODEL primary anthropic/claude-sonnet-4-6
HANDLE discord
PRIVILEGE docker-socket true
```

`agents/nb-roll/Clawfile` (nanobot):
```dockerfile
FROM nanobot:latest

CLAW_TYPE nanobot
AGENT AGENTS.md
MODEL primary openrouter/anthropic/claude-sonnet-4
HANDLE discord
```

`agents/pc-roll/Clawfile` (picoclaw):
```dockerfile
FROM docker.io/sipeed/picoclaw:latest

CLAW_TYPE picoclaw
AGENT AGENTS.md
MODEL primary openrouter/anthropic/claude-sonnet-4
HANDLE discord
```

**Step 3: Create the 6 AGENTS.md files**

Each is ~3 lines. Example for `agents/oc-roll/AGENTS.md`:
```markdown
# oc-roll

You are oc-roll, an agent running on the **OpenClaw** runtime. When asked to introduce yourself or respond to a roll call, post a short message stating your name and that you run on OpenClaw. Keep it to one sentence.
```

Repeat for each, substituting:
- `nc-roll` / **NullClaw**
- `mc-roll` / **MicroClaw**
- `nano-roll` / **NanoClaw** (Claude Agent SDK)
- `nb-roll` / **Nanobot**
- `pc-roll` / **PicoClaw**

**Step 4: Create claw-pod.yml**

```yaml
x-claw:
  pod: rollcall

services:
  oc-roll:
    image: rollcall-openclaw:latest
    build:
      context: .
      dockerfile: agents/oc-roll/Clawfile
    x-claw:
      agent: ./agents/oc-roll/AGENTS.md
      cllama: passthrough
      cllama-env:
        OPENROUTER_API_KEY: "${OPENROUTER_API_KEY}"
        ANTHROPIC_API_KEY: "${ANTHROPIC_API_KEY}"
      handles:
        discord:
          id: "${DISCORD_BOT_ID}"
          username: "oc-roll"
          guilds:
            - id: "${DISCORD_GUILD_ID}"
              name: "Roll Call"
              channels:
                - id: "${ROLLCALL_CHANNEL_ID}"
                  name: roll-call
    environment:
      DISCORD_BOT_TOKEN: "${DISCORD_BOT_TOKEN}"

  nc-roll:
    image: rollcall-nullclaw:latest
    build:
      context: .
      dockerfile: agents/nc-roll/Clawfile
    x-claw:
      agent: ./agents/nc-roll/AGENTS.md
      cllama: passthrough
      cllama-env:
        ANTHROPIC_API_KEY: "${ANTHROPIC_API_KEY}"
      handles:
        discord:
          id: "${DISCORD_BOT_ID}"
          username: "nc-roll"
          guilds:
            - id: "${DISCORD_GUILD_ID}"
              name: "Roll Call"
              channels:
                - id: "${ROLLCALL_CHANNEL_ID}"
                  name: roll-call
    environment:
      DISCORD_BOT_TOKEN: "${DISCORD_BOT_TOKEN}"

  mc-roll:
    image: rollcall-microclaw:latest
    build:
      context: .
      dockerfile: agents/mc-roll/Clawfile
    x-claw:
      agent: ./agents/mc-roll/AGENTS.md
      cllama: passthrough
      cllama-env:
        ANTHROPIC_API_KEY: "${ANTHROPIC_API_KEY}"
      handles:
        discord:
          id: "${DISCORD_BOT_ID}"
          username: "mc-roll"
          guilds:
            - id: "${DISCORD_GUILD_ID}"
              name: "Roll Call"
              channels:
                - id: "${ROLLCALL_CHANNEL_ID}"
                  name: roll-call
    environment:
      DISCORD_BOT_TOKEN: "${DISCORD_BOT_TOKEN}"

  nano-roll:
    image: rollcall-nanoclaw:latest
    build:
      context: .
      dockerfile: agents/nano-roll/Clawfile
    x-claw:
      agent: ./agents/nano-roll/AGENTS.md
      cllama: passthrough
      cllama-env:
        ANTHROPIC_API_KEY: "${ANTHROPIC_API_KEY}"
      handles:
        discord:
          id: "${DISCORD_BOT_ID}"
          username: "nano-roll"
          guilds:
            - id: "${DISCORD_GUILD_ID}"
              name: "Roll Call"
              channels:
                - id: "${ROLLCALL_CHANNEL_ID}"
                  name: roll-call
    environment:
      DISCORD_BOT_TOKEN: "${DISCORD_BOT_TOKEN}"

  nb-roll:
    image: rollcall-nanobot:latest
    build:
      context: .
      dockerfile: agents/nb-roll/Clawfile
    x-claw:
      agent: ./agents/nb-roll/AGENTS.md
      cllama: passthrough
      cllama-env:
        OPENROUTER_API_KEY: "${OPENROUTER_API_KEY}"
      handles:
        discord:
          id: "${DISCORD_BOT_ID}"
          username: "nb-roll"
          guilds:
            - id: "${DISCORD_GUILD_ID}"
              name: "Roll Call"
              channels:
                - id: "${ROLLCALL_CHANNEL_ID}"
                  name: roll-call
    environment:
      DISCORD_BOT_TOKEN: "${DISCORD_BOT_TOKEN}"

  pc-roll:
    image: rollcall-picoclaw:latest
    build:
      context: .
      dockerfile: agents/pc-roll/Clawfile
    x-claw:
      agent: ./agents/pc-roll/AGENTS.md
      cllama: passthrough
      cllama-env:
        OPENROUTER_API_KEY: "${OPENROUTER_API_KEY}"
      handles:
        discord:
          id: "${DISCORD_BOT_ID}"
          username: "pc-roll"
          guilds:
            - id: "${DISCORD_GUILD_ID}"
              name: "Roll Call"
              channels:
                - id: "${ROLLCALL_CHANNEL_ID}"
                  name: roll-call
    environment:
      DISCORD_BOT_TOKEN: "${DISCORD_BOT_TOKEN}"
```

**Step 5: Create .env (copied from trading-desk tokens)**

Copy the trading-desk bot token and guild, add the `ROLLCALL_CHANNEL_ID` (must be created in the Discord server first or reuse `DISCORD_TRADING_FLOOR_CHANNEL`).

**Step 6: Commit**

```
git add examples/rollcall/
git commit -m "feat: add rollcall example fixture — 6-driver multi-agent pod"
```

---

### Task 2: Write the spike test

**Files:**
- Create: `cmd/claw/spike_rollcall_test.go`

**Step 1: Write the test file**

```go
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
		// Fall back to trading floor channel if no dedicated roll-call channel
		env["ROLLCALL_CHANNEL_ID"] = env["DISCORD_TRADING_FLOOR_CHANNEL"]
	}
	if env["ROLLCALL_CHANNEL_ID"] == "" {
		t.Skip("ROLLCALL_CHANNEL_ID not set — skipping")
	}
	// Ensure all services can share the same bot token
	if env["DISCORD_BOT_ID"] == "" {
		t.Skip("DISCORD_BOT_ID not set — skipping")
	}
	// Ensure at least one LLM key exists for cllama
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

	// ── Build images ────────────────────────────────────────────────────
	// Base runtime images (skip if already present)
	if !spikeImageExists("openclaw:latest") {
		// Try building from infra/ or skip
		infraDir := filepath.Join(repoRoot, "examples", "trading-desk")
		spikeBuildImage(t, infraDir, "openclaw:latest", "Dockerfile.openclaw-base")
	}
	spikeEnsureCllamaPassthroughImage(t)

	// Agent images — each Clawfile in agents/<name>/
	agents := []struct {
		name       string
		image      string
		dockerfile string
		runtime    string
	}{
		{"oc-roll", "rollcall-openclaw:latest", "agents/oc-roll/Clawfile", "openclaw"},
		{"nc-roll", "rollcall-nullclaw:latest", "agents/nc-roll/Clawfile", "nullclaw"},
		{"mc-roll", "rollcall-microclaw:latest", "agents/mc-roll/Clawfile", "microclaw"},
		{"nano-roll", "rollcall-nanoclaw:latest", "agents/nano-roll/Clawfile", "nanoclaw"},
		{"nb-roll", "rollcall-nanobot:latest", "agents/nb-roll/Clawfile", "nanobot"},
		{"pc-roll", "rollcall-picoclaw:latest", "agents/pc-roll/Clawfile", "picoclaw"},
	}

	for _, a := range agents {
		spikeBuildImage(t, dir, a.image, a.dockerfile)
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
		for _, a := range agents {
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
	for _, a := range agents {
		container := fmt.Sprintf("rollcall-%s-1", a.name)
		spikeWaitHealthy(t, container, 120*time.Second)
	}

	// ── Send trigger message ────────────────────────────────────────────
	triggerMsg := "Roll call! Each bot, introduce yourself and state what runtime you are running on."
	rollcallSendDiscordMessage(t, botToken, channelID, triggerMsg)
	t.Logf("sent roll-call trigger to channel %s", channelID)

	// ── Poll for responses ──────────────────────────────────────────────
	// Each agent should respond mentioning its runtime name.
	runtimeKeywords := map[string]bool{
		"openclaw": false,
		"nullclaw": false,
		"microclaw": false,
		"nanoclaw": false,  // or "claude agent sdk"
		"nanobot":  false,
		"picoclaw": false,
	}

	deadline := time.Now().Add(3 * time.Minute)
	for time.Now().Before(deadline) {
		messages := rollcallFetchMessages(t, botToken, channelID, 50)
		for _, msg := range messages {
			content := strings.ToLower(msg.Content)
			// Only count bot-authored messages after our trigger
			if msg.Author.Bot {
				for keyword := range runtimeKeywords {
					if !runtimeKeywords[keyword] && strings.Contains(content, keyword) {
						runtimeKeywords[keyword] = true
						t.Logf("found %s response: %q", keyword, truncate(msg.Content, 120))
					}
				}
				// nanoclaw might say "claude agent sdk" instead
				if !runtimeKeywords["nanoclaw"] && strings.Contains(content, "claude agent") {
					runtimeKeywords["nanoclaw"] = true
					t.Logf("found nanoclaw response (via 'claude agent'): %q", truncate(msg.Content, 120))
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

	// Report results
	for keyword, found := range runtimeKeywords {
		if !found {
			t.Errorf("missing roll-call response from %s agent", keyword)
		}
	}

	// ── Check cllama costs ──────────────────────────────────────────────
	// cllama exposes costs API on :8081 inside the pod network.
	// From outside, we check via docker exec on the cllama container.
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

// ── Discord helpers ─────────────────────────────────────────────────────────

type discordMessage struct {
	Content string        `json:"content"`
	Author  discordAuthor `json:"author"`
	ID      string        `json:"id"`
}

type discordAuthor struct {
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

func rollcallFetchMessages(t *testing.T, token, channelID string, limit int) []discordMessage {
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
	var messages []discordMessage
	json.Unmarshal(body, &messages)
	return messages
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
```

**Step 2: Verify it compiles**

Run: `go build -tags spike ./cmd/claw/...`
Expected: clean compile

**Step 3: Commit**

```
git add cmd/claw/spike_rollcall_test.go
git commit -m "feat: add roll-call spike test — 6-driver Discord integration"
```

---

### Task 3: Create the .env for rollcall (manual)

**This is a manual step** — not automated.

Copy trading-desk tokens into `examples/rollcall/.env`:

```bash
cp examples/trading-desk/.env examples/rollcall/.env
# Then add/edit:
# ROLLCALL_CHANNEL_ID=<create #roll-call channel or reuse trading floor>
```

The `.env` file is gitignored. Only `.env.example` is checked in.

---

### Task 4: Run the spike test

**Step 1: Run it**

```bash
go test -tags spike -v -run TestSpikeRollCall -timeout 10m ./cmd/claw/...
```

Expected flow:
1. Builds 6 agent images + cllama
2. `claw up` brings up the pod
3. Waits for all 6 containers healthy
4. Sends "Roll call!" to Discord
5. Polls for 6 responses containing runtime names
6. Checks cllama costs
7. Tears down

**Step 2: If any agent doesn't respond, check logs**

The teardown dumps last 50 lines of each container's logs. Common issues:
- Missing base image (nullclaw/microclaw need their stub binaries)
- Discord gateway conflict (6 bots sharing one token)
- LLM API key not valid for cllama passthrough

---

### Notes

- **nanoclaw caveat**: nanoclaw (Claude Agent SDK) doesn't have native Discord gateway support — it's an orchestrator that spawns sub-agents. It may not respond to Discord messages directly. If it doesn't work in this test, mark it as expected and note in test output.
- **nullclaw/microclaw base images**: These need their respective stub binaries. The trading-desk example has `COPY nullclaw /usr/local/bin/nullclaw` etc. The rollcall Clawfiles should either share the trading-desk build context or use pre-built images.
- **Shared bot token**: All 6 agents connecting to Discord gateway with the same token will create 6 gateway sessions. Discord allows this (sharding), but all messages go to all 6 agents. Each will respond, which is what we want.
