//go:build spike

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestSpikeChannelDigestGeneratedPod runs a generated pod with fake Discord,
// real claw-wall, real channel-memory, real cllama, and a fake upstream
// provider. It proves #267's generated-compose seam and captures the
// provider-visible raw_window+digest block that cllama injects.
//
// Requires: Docker. Does NOT require Discord or provider credentials.
// Run with: go test -tags spike -v -run TestSpikeChannelDigestGeneratedPod -timeout 10m ./cmd/claw/...
func TestSpikeChannelDigestGeneratedPod(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not on PATH - skipping")
	}

	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	repoRoot, err := filepath.Abs(filepath.Join(filepath.Dir(thisFile), "..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}

	workDir := t.TempDir()
	generatedPath := filepath.Join(workDir, "compose.generated.yml")
	projectName := "channel-digest-spike"
	networkName := projectName + "_claw-internal"
	spikeCleanupProject(projectName, generatedPath)
	t.Cleanup(func() {
		spikeCleanupProject(projectName, generatedPath)
	})
	t.Cleanup(func() {
		if !t.Failed() {
			return
		}
		out, _ := exec.Command("docker", "compose", "-f", generatedPath, "logs", "--tail", "200").CombinedOutput()
		t.Logf("compose logs:\n%s", out)
	})

	channelID := "267000000000000001"
	agentImage := "channel-digest-agent:spike-267"
	channelMemoryImage := "channel-memory:spike-267"
	pythonImage := "python:3.12-alpine"

	// The generated pod points at the normal infra image refs, so overwrite
	// those tags with images built from the worktree under test.
	spikeEnsurePulledImage(t, pythonImage)
	spikeBuildImage(t, repoRoot, resolveConversationWallImageRef(), conversationWallDockerfile)
	spikeEnsureRepoInfraImages(t, repoRoot, infraComponentClawdash)
	spikeEnsureCllamaPassthroughImage(t, repoRoot)
	spikeBuildImage(t, repoRoot, channelMemoryImage, "examples/channel-memory/Dockerfile")

	rollcallDir := filepath.Join(repoRoot, "examples", "rollcall")
	spikeBuildImage(t, rollcallDir, "nullclaw:latest", "Dockerfile.nullclaw-base")
	spikeWriteFile(t, filepath.Join(workDir, "AGENTS.md"), "# Digest Spike Agent\n\nUse runtime channel context.")
	spikeWriteFile(t, filepath.Join(workDir, "Clawfile"), `FROM nullclaw:latest

CLAW_TYPE nullclaw
AGENT AGENTS.md
MODEL primary openai/gpt-4o
HANDLE discord
`)
	spikeBuildImage(t, workDir, agentImage, "Clawfile")

	capturesDir := filepath.Join(workDir, "captures")
	if err := os.MkdirAll(capturesDir, 0o777); err != nil {
		t.Fatalf("create captures dir: %v", err)
	}
	spikeWriteFile(t, filepath.Join(workDir, "fake_discord.py"), fakeDiscordDigestSpikeScript())
	spikeWriteFile(t, filepath.Join(workDir, "fake_provider.py"), fakeProviderDigestSpikeScript())
	spikeWriteFile(t, filepath.Join(workDir, "claw-pod.yml"), fmt.Sprintf(`name: channel-digest-spike

x-claw:
  pod: channel-digest-spike
  channel-memory:
    service: channel-memory

services:
  fake-discord:
    image: %s
    command: ["python", "/app/fake_discord.py"]
    environment:
      CHANNEL_ID: "%s"
      MESSAGE_COUNT: "30"
    volumes:
      - ./fake_discord.py:/app/fake_discord.py:ro
    expose:
      - "8090"
    networks:
      - claw-internal

  fake-provider:
    image: %s
    command: ["python", "/app/fake_provider.py"]
    volumes:
      - ./fake_provider.py:/app/fake_provider.py:ro
      - ./captures:/captures:rw
    expose:
      - "8080"
    networks:
      - claw-internal

  channel-memory:
    image: %s
    expose:
      - "8080"

  digest-agent:
    image: %s
    environment:
      DISCORD_BOT_TOKEN: fake-token
    x-claw:
      agent: AGENTS.md
      cllama: passthrough
      cllama-env:
        OPENAI_API_KEY: sk-fake
        OPENAI_BASE_URL: http://fake-provider:8080/v1
        CLLAMA_FEED_MAX_RESPONSE_BYTES: "200000"
        CLLAMA_FEED_MAX_TOTAL_BYTES: "400000"
      models:
        primary: openai/gpt-4o
      handles:
        discord:
          id: digest-bot
          guilds:
            - id: guild-1
              channels:
                - id: "%s"
`, pythonImage, channelID, pythonImage, channelMemoryImage, agentImage, channelID))

	t.Setenv(conversationWallDiscordBaseEnv, "http://fake-discord:8090")
	t.Setenv("CLAW_WALL_RETENTION", "24h")
	t.Setenv("CLAW_WALL_BACKFILL_MAX_PAGES", "2")
	t.Setenv("CLAW_WALL_POLL_INTERVAL", "1")
	t.Setenv("CLAW_WALL_CHANNEL_MEMORY_TIMEOUT", "5s")
	t.Setenv("CLLAMA_UI_PORT", spikeFreePort(t))
	t.Setenv("CLAWDASH_ADDR", ":"+spikeFreePort(t))

	prevDetach := composeUpDetach
	composeUpDetach = true
	defer func() { composeUpDetach = prevDetach }()

	if err := runComposeUp(filepath.Join(workDir, "claw-pod.yml")); err != nil {
		t.Fatalf("runComposeUp: %v", err)
	}

	composeText := spikeReadFile(t, generatedPath)
	if !strings.Contains(composeText, conversationWallMemoryDigestEnv+": http://channel-memory:8080/digest") {
		t.Fatalf("compose did not wire claw-wall digest URL:\n%s", composeText)
	}
	if !strings.Contains(composeText, conversationWallDiscordBaseEnv+": http://fake-discord:8090") {
		t.Fatalf("compose did not forward fake Discord base URL:\n%s", composeText)
	}
	feedsText := spikeReadFile(t, filepath.Join(workDir, ".claw-runtime", "context", "digest-agent", "feeds.json"))
	if !strings.Contains(feedsText, "context_kind=raw_window%2Bdigest") {
		t.Fatalf("generated feeds.json did not request digest-backed awareness:\n%s", feedsText)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	rawURL := "http://claw-wall:8080/channel-awareness?channels=" + channelID + "&since=24h&limit=60&max_chars=200000&context_kind=raw_window"
	digestURL := "http://claw-wall:8080/channel-awareness?channels=" + channelID + "&since=24h&limit=60&max_chars=200000&context_kind=raw_window%2Bdigest"

	var (
		rawBody    string
		digestBody string
		lastProbe  string
	)
	if err := waitForCondition(ctx, func() bool {
		var err error
		rawBody, err = spikeDockerProbe(networkName, pythonImage, probeGetURLScript, rawURL)
		if err != nil {
			lastProbe = err.Error()
			return false
		}
		digestBody, err = spikeDockerProbe(networkName, pythonImage, probeGetURLScript, digestURL)
		if err != nil {
			lastProbe = err.Error()
			return false
		}
		lastProbe = digestBody
		messages, hasMessages := spikeHeaderInt(digestBody, "messages")
		return hasMessages &&
			messages > 0 &&
			strings.Contains(digestBody, "digest=ok") &&
			!strings.Contains(digestBody, "digest_blocks=0")
	}); err != nil {
		t.Fatalf("digest-backed awareness never became available: %v\nlast probe:\n%s", err, lastProbe)
	}

	rawBytes, ok := spikeHeaderInt(rawBody, "raw_bytes")
	if !ok || rawBytes <= 0 {
		t.Fatalf("raw awareness missing raw_bytes header:\n%s", rawBody)
	}
	digestRawBytes, ok := spikeHeaderInt(digestBody, "raw_bytes")
	if !ok || digestRawBytes <= 0 {
		t.Fatalf("digest awareness missing raw_bytes header:\n%s", digestBody)
	}
	digestBytes, ok := spikeHeaderInt(digestBody, "digest_bytes")
	if !ok || digestBytes <= 0 {
		t.Fatalf("digest awareness missing digest_bytes header:\n%s", digestBody)
	}
	if digestRawBytes >= rawBytes {
		t.Fatalf("expected digest mode to reduce retained raw bytes, raw=%d digest-raw=%d\nraw:\n%s\ndigest:\n%s", rawBytes, digestRawBytes, rawBody, digestBody)
	}
	if len(digestBody) >= len(rawBody) {
		t.Fatalf("expected digest-backed awareness body to be smaller than raw-only, raw=%d digest=%d\nraw:\n%s\ndigest:\n%s", len(rawBody), len(digestBody), rawBody, digestBody)
	}
	for _, want := range []string{"[channel-awareness] kind=raw_window+digest", "[digest]", "deterministic_only=true", "source_channel=" + channelID, "[raw recent]"} {
		if !strings.Contains(digestBody, want) {
			t.Fatalf("digest awareness missing %q:\n%s", want, digestBody)
		}
	}

	token := spikeReadAgentToken(t, filepath.Join(workDir, ".claw-runtime", "context", "digest-agent", "metadata.json"))
	if out, err := spikeDockerProbe(networkName, pythonImage, probePostCllamaScript, token); err != nil {
		t.Fatalf("cllama provider request failed: %v\n%s", err, out)
	} else if !strings.Contains(out, "chatcmpl-spike") {
		t.Fatalf("unexpected cllama response:\n%s", out)
	}

	capturePath := filepath.Join(capturesDir, "latest.json")
	if err := waitForCondition(ctx, func() bool {
		_, err := os.Stat(capturePath)
		return err == nil
	}); err != nil {
		t.Fatalf("fake provider did not write capture: %v", err)
	}
	captured := spikeReadFile(t, capturePath)
	providerText := spikeOpenAIMessageText(t, captured)
	for _, want := range []string{"BEGIN FEED: channel-awareness", "[channel-awareness] kind=raw_window+digest", "[digest]", "deterministic_only=true", "source_channel=" + channelID} {
		if !strings.Contains(providerText, want) {
			t.Fatalf("provider-visible request missing %q:\n%s", want, providerText)
		}
	}
}

const probeGetURLScript = `
import sys, urllib.request
with urllib.request.urlopen(sys.argv[1], timeout=5) as resp:
    sys.stdout.write(resp.read().decode("utf-8"))
`

const probePostCllamaScript = `
import json, sys, urllib.request
payload = {"model":"openai/gpt-4o","messages":[{"role":"user","content":"Use the channel context."}]}
body = json.dumps(payload).encode("utf-8")
req = urllib.request.Request(
    "http://cllama:8080/v1/chat/completions",
    data=body,
    headers={"Content-Type":"application/json","Authorization":"Bearer " + sys.argv[1]},
)
with urllib.request.urlopen(req, timeout=15) as resp:
    sys.stdout.write(resp.read().decode("utf-8"))
`

func spikeDockerProbe(networkName, image, script string, args ...string) (string, error) {
	cmdArgs := []string{"run", "--rm", "--network", networkName, image, "python", "-c", script}
	cmdArgs = append(cmdArgs, args...)
	cmd := exec.Command("docker", cmdArgs...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

func spikeWriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func spikeEnsurePulledImage(t *testing.T, image string) {
	t.Helper()
	if spikeImageExists(image) {
		return
	}
	out, err := exec.Command("docker", "pull", image).CombinedOutput()
	if err != nil {
		t.Fatalf("docker pull %s: %v\n%s", image, err, out)
	}
}

func spikeReadAgentToken(t *testing.T, path string) string {
	t.Helper()
	var metadata struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal([]byte(spikeReadFile(t, path)), &metadata); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	if strings.TrimSpace(metadata.Token) == "" {
		t.Fatalf("%s did not contain token", path)
	}
	return metadata.Token
}

func spikeHeaderInt(body, key string) (int, bool) {
	header, _, _ := strings.Cut(body, "\n")
	for _, field := range strings.Fields(header) {
		raw, ok := strings.CutPrefix(field, key+"=")
		if !ok {
			continue
		}
		value, err := strconv.Atoi(strings.TrimSpace(raw))
		return value, err == nil
	}
	return 0, false
}

func spikeOpenAIMessageText(t *testing.T, raw string) string {
	t.Helper()
	var payload map[string]any
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		t.Fatalf("parse captured provider request: %v\n%s", err, raw)
	}
	messages, _ := payload["messages"].([]any)
	var b strings.Builder
	for _, item := range messages {
		msg, _ := item.(map[string]any)
		if msg == nil {
			continue
		}
		switch content := msg["content"].(type) {
		case string:
			b.WriteString(content)
			b.WriteByte('\n')
		case []any:
			for _, block := range content {
				m, _ := block.(map[string]any)
				if text, _ := m["text"].(string); text != "" {
					b.WriteString(text)
					b.WriteByte('\n')
				}
			}
		}
	}
	return b.String()
}

func fakeDiscordDigestSpikeScript() string {
	return `import json
import os
import urllib.parse
from datetime import datetime, timedelta, timezone
from http.server import BaseHTTPRequestHandler, HTTPServer

channel_id = os.environ["CHANNEL_ID"]
count = int(os.environ.get("MESSAGE_COUNT", "30"))
base_id = 2670000000001000000
now = datetime.now(timezone.utc).replace(microsecond=0)
messages = []
for i in range(count):
    ts = (now - timedelta(minutes=count - i)).isoformat().replace("+00:00", "Z")
    content = "heartbeat_ok runtime status seq=%03d " % i + ("diagnostic-payload-%03d " % i) * 18
    messages.append({
        "id": str(base_id + i),
        "content": content,
        "timestamp": ts,
        "author": {"id": "user-%03d" % i, "username": "ops-%02d" % (i % 3), "global_name": "Ops %02d" % (i % 3)},
        "channel_id": channel_id,
    })

class Handler(BaseHTTPRequestHandler):
    def do_GET(self):
        parsed = urllib.parse.urlparse(self.path)
        if parsed.path != "/channels/%s/messages" % channel_id:
            self.send_response(404)
            self.end_headers()
            return
        query = urllib.parse.parse_qs(parsed.query)
        limit = min(100, max(1, int(query.get("limit", ["100"])[0])))
        after = query.get("after", [""])[0]
        before = query.get("before", [""])[0]
        selected = []
        for msg in messages:
            if after and int(msg["id"]) <= int(after):
                continue
            if before and int(msg["id"]) >= int(before):
                continue
            selected.append(msg)
        selected = selected[-limit:]
        selected = list(reversed(selected))
        body = json.dumps(selected).encode("utf-8")
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, fmt, *args):
        return

HTTPServer(("0.0.0.0", 8090), Handler).serve_forever()
`
}

func fakeProviderDigestSpikeScript() string {
	return `import json
import os
from http.server import BaseHTTPRequestHandler, HTTPServer

class Handler(BaseHTTPRequestHandler):
    def do_GET(self):
        if self.path == "/health":
            body = b"ok"
            self.send_response(200)
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)
            return
        self.send_response(404)
        self.end_headers()

    def do_POST(self):
        length = int(self.headers.get("Content-Length", "0"))
        body = self.rfile.read(length)
        os.makedirs("/captures", exist_ok=True)
        with open("/captures/latest.json", "wb") as f:
            f.write(body)
        response = {
            "id": "chatcmpl-spike",
            "object": "chat.completion",
            "choices": [{"index": 0, "message": {"role": "assistant", "content": "ok"}, "finish_reason": "stop"}],
            "usage": {"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2},
        }
        encoded = json.dumps(response).encode("utf-8")
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(encoded)))
        self.end_headers()
        self.wfile.write(encoded)

    def log_message(self, fmt, *args):
        return

HTTPServer(("0.0.0.0", 8080), Handler).serve_forever()
`
}
