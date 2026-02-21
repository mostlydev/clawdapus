//go:build spike

package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"
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
	spikeBuildImage(t, dir, "trading-desk:latest", "Clawfile")
	spikeBuildImage(t, dir, "trading-api:latest", "Dockerfile.trading-api")

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

	// Run the full pipeline: parse → materialize → generate → docker compose up.
	if err := runComposeUp(spikePodPath); err != nil {
		t.Fatalf("runComposeUp: %v", err)
	}

	// Tear down containers (and volumes) when test ends.
	defer func() {
		cmd := exec.Command("docker", "compose", "-f", generatedPath, "down", "--volumes")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		_ = cmd.Run()
	}()

	// ── Verify tiverton's openclaw.json ──────────────────────────────────────

	configPath := filepath.Join(runtimeDir, "tiverton", "openclaw.json")
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
	for _, want := range []string{"jobs.json", "openclaw.json", "/app/state/cron/jobs.json", "/app/config/openclaw.json"} {
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

func spikeBuildImage(t *testing.T, contextDir, tag, dockerfile string) {
	t.Helper()
	t.Logf("building %s from %s...", tag, dockerfile)
	cmd := exec.Command("docker", "build", "-t", tag, "-f", dockerfile, ".")
	cmd.Dir = contextDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("docker build %s: %v\n%s", tag, err, out)
	}
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
