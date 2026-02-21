//go:build spike

package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
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
)

// TestSpikeComposeUp is a full end-to-end integration test for the trading-desk
// example. It builds images, runs claw compose up, verifies generated artifacts
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
	if env["TIVERTON_BOT_TOKEN"] == "" {
		t.Skip("TIVERTON_BOT_TOKEN not set in .env — skipping spike test")
	}
	// Defaults for vars not in .env
	if env["POSTGRES_PASSWORD"] == "" {
		env["POSTGRES_PASSWORD"] = "spike_test_postgres_pw"
	}
	if env["SECRET_KEY_BASE"] == "" {
		env["SECRET_KEY_BASE"] = strings.Repeat("0", 64)
	}

	// Build images before running compose up.
	// openclaw:latest is the base runtime image; build it from the local
	// Dockerfile.openclaw-base if it isn't already present.
	if !spikeImageExists("openclaw:latest") {
		spikeBuildImage(t, dir, "openclaw:latest", "Dockerfile.openclaw-base")
	}
	spikeBuildImage(t, dir, "trading-desk:latest", "Clawfile")
	if !spikeImageExists("trading-api:latest") {
		spikeBuildImage(t, dir, "trading-api:latest", "Dockerfile.trading-api")
	}

	// Write a pre-expanded spike pod YAML so Go YAML parser sees real IDs.
	rawPod := spikeReadFile(t, filepath.Join(dir, "claw-pod.yml"))
	expandedPod := spikeExpandEnvVars(rawPod, env)
	spikePodPath := filepath.Join(dir, "spike-pod.yml")
	if err := os.WriteFile(spikePodPath, []byte(expandedPod), 0644); err != nil {
		t.Fatalf("write spike-pod.yml: %v", err)
	}
	defer os.Remove(spikePodPath)

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
	preClean := exec.Command("docker", "compose", "-p", "trading-desk", "down", "--volumes", "--remove-orphans")
	preClean.Stdout = os.Stdout
	preClean.Stderr = os.Stderr
	_ = preClean.Run()

	// Run the full pipeline: parse → materialize → generate → docker compose up.
	if err := runComposeUp(spikePodPath); err != nil {
		t.Fatalf("runComposeUp: %v", err)
	}

	// teardown runs the compose down and dumps logs.
	teardown := func() {
		for _, svc := range []string{"tiverton", "westin"} {
			name := fmt.Sprintf("trading-desk-%s-1", svc)
			out, _ := exec.Command("docker", "logs", "--tail", "100", name).CombinedOutput()
			t.Logf("=== %s logs ===\n%s", name, string(out))
		}
		cmd := exec.Command("docker", "compose", "-f", generatedPath, "down", "--volumes")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		_ = cmd.Run()
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

	// ── Verify tiverton's openclaw.json ──────────────────────────────────────

	configPath := filepath.Join(runtimeDir, "tiverton", "config", "openclaw.json")
	configData := spikeReadFile(t, configPath)
	var configMap map[string]interface{}
	if err := json.Unmarshal([]byte(configData), &configMap); err != nil {
		t.Fatalf("parse openclaw.json: %v", err)
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

	// ── Verify tiverton's jobs.json ──────────────────────────────────────────

	jobsPath := filepath.Join(runtimeDir, "tiverton", "state", "cron", "jobs.json")
	jobsData := spikeReadFile(t, jobsPath)
	var jobs []map[string]interface{}
	if err := json.Unmarshal([]byte(jobsData), &jobs); err != nil {
		t.Fatalf("parse jobs.json: %v", err)
	}
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
	for _, want := range []string{"jobs.json", "/app/state/cron/jobs.json", "/app/config"} {
		if !strings.Contains(composeSrc, want) {
			t.Errorf("compose.generated.yml: expected to contain %q", want)
		}
	}

	// ── Verify containers can serve the mounted files ────────────────────────

	containerName := "trading-desk-tiverton-1"
	spikeWaitRunning(t, containerName, 45*time.Second)

	// Config file must be readable inside container and contain 'discord'
	out, err := exec.Command("docker", "exec", containerName, "cat", "/app/config/openclaw.json").Output()
	if err != nil {
		t.Errorf("docker exec cat openclaw.json: %v", err)
	} else if !strings.Contains(string(out), "discord") {
		t.Errorf("openclaw.json in container doesn't contain 'discord': %q", string(out))
	}

	// jobs.json must be readable and contain the real channel ID
	out2, err2 := exec.Command("docker", "exec", containerName, "cat", "/app/state/cron/jobs.json").Output()
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

	// Wait for the Docker healthcheck to report "healthy" before polling Discord.
	// This means openclaw gateway + Discord connection are ready.
	spikeWaitHealthy(t, containerName, 60*time.Second)

	// ── Verify startup greetings appeared in Discord ─────────────────────────
	// Each agent sends a greeting via openclaw message send on startup.
	// Poll the Discord channel until both messages appear (or timeout).
	spikeVerifyDiscordGreeting(t, env["TIVERTON_BOT_TOKEN"], channelID, "tiverton online", 10*time.Second)
	spikeVerifyDiscordGreeting(t, env["WESTIN_BOT_TOKEN"], channelID, "westin online", 10*time.Second)
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

	if filepath.Base(dockerfile) == "Clawfile" {
		// Transpile Clawfile → Dockerfile.generated, then docker build
		generatedPath, err := build.Generate(clawfilePath)
		if err != nil {
			t.Fatalf("claw build generate %s: %v", clawfilePath, err)
		}
		if err := build.BuildFromGenerated(generatedPath, tag); err != nil {
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
