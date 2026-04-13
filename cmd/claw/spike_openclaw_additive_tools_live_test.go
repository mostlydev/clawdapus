//go:build spike

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

// TestSpikeOpenClawAdditiveToolsLive exercises a real OpenClaw runner behind
// cllama with managed tools enabled and validates additive tool availability in
// one live session:
//   - turn 1 reads a nonce file via a runner-native tool
//   - turn 2 calls a cllama-mediated HTTP tool on the same session
//   - session history contains both non-mediated and mediated entries
func TestSpikeOpenClawAdditiveToolsLive(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	repoRoot := filepath.Join(filepath.Dir(thisFile), "..", "..")
	rollcallDir, err := filepath.Abs(filepath.Join(repoRoot, "examples", "rollcall"))
	if err != nil {
		t.Fatalf("resolve rollcall dir: %v", err)
	}
	tradingDeskDir, err := filepath.Abs(filepath.Join(repoRoot, "examples", "trading-desk"))
	if err != nil {
		t.Fatalf("resolve trading-desk dir: %v", err)
	}

	env := spikeLoadDotEnv(t, filepath.Join(rollcallDir, ".env"))
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

	proxyRequest := openClawAdditiveProxyRequest(t, env)

	baseTag := fmt.Sprintf("spike-openclaw-real:%d", time.Now().UnixNano())
	agentTag := fmt.Sprintf("spike-openclaw-additive:%d", time.Now().UnixNano())
	toolTag := fmt.Sprintf("spike-openclaw-additive-tool:%d", time.Now().UnixNano())

	fixtureDir := t.TempDir()
	nativeProof := fmt.Sprintf("native-proof-%d", time.Now().UnixNano())
	openClawAdditiveWriteFixture(t, fixtureDir, baseTag, proxyRequest.Model, nativeProof)

	spikeBuildImage(t, tradingDeskDir, baseTag, "Dockerfile.openclaw-base")
	spikeBuildImage(t, fixtureDir, agentTag, "Clawfile")
	spikeBuildImage(t, filepath.Join(repoRoot, "testdata", "tool-stub"), toolTag, "Dockerfile")
	spikeEnsureRepoInfraImages(t, repoRoot, infraComponentClawdash, infraComponentClawWall)
	spikeEnsureCllamaPassthroughImage(t, repoRoot)

	podYAML := openClawAdditiveLivePod(t, agentTag, toolTag, proxyRequest)
	podPath := filepath.Join(fixtureDir, "claw-pod.yml")
	if err := os.WriteFile(podPath, []byte(podYAML), 0o644); err != nil {
		t.Fatalf("write claw-pod.yml: %v", err)
	}

	generatedPath := filepath.Join(fixtureDir, "compose.generated.yml")
	sessionHistoryDir := filepath.Join(fixtureDir, ".claw-session-history")

	prevDetach := composeUpDetach
	composeUpDetach = true
	defer func() { composeUpDetach = prevDetach }()

	const composeProject = "openclaw-additive-live"
	spikeCleanupProject(composeProject, generatedPath)
	t.Cleanup(func() {
		spikeCleanupProject(composeProject, generatedPath)
	})

	if err := runComposeUp(podPath); err != nil {
		t.Fatalf("runComposeUp(%s): %v", filepath.Base(podPath), err)
	}

	agentContainerID := rollcallResolveContainerID(t, generatedPath, "oc-additive")
	cllamaContainerID := rollcallResolveContainerID(t, generatedPath, "cllama")
	toolContainerID := rollcallResolveContainerID(t, generatedPath, "tool-svc")

	t.Cleanup(func() {
		if !t.Failed() {
			return
		}
		rollcallLogContainer(t, agentContainerID)
		rollcallLogContainer(t, cllamaContainerID)
		rollcallLogContainer(t, toolContainerID)
	})

	capabilityWaveAssertToolsManifest(t, filepath.Join(fixtureDir, ".claw-runtime", "context", "oc-additive", "tools.json"))

	spikeWaitHealthy(t, agentContainerID, 120*time.Second)
	spikeWaitRunning(t, cllamaContainerID, 30*time.Second)
	spikeWaitRunning(t, toolContainerID, 30*time.Second)

	auditWindowStart := time.Now()
	sessionID := fmt.Sprintf("additive-live-%d", time.Now().UnixNano())

	nativeOut := openClawAdditiveRunAgent(
		t,
		agentContainerID,
		sessionID,
		"Use the native read tool to read /proof/native-proof.txt. Reply with exactly the file contents and nothing else. Do not call the managed tool.",
	)
	if !strings.Contains(nativeOut, nativeProof) {
		t.Fatalf("native tool turn did not surface proof %q\n%s", nativeProof, nativeOut)
	}

	managedPhrase := fmt.Sprintf("managed-additive-%d", time.Now().UnixNano())
	managedOut := openClawAdditiveRunAgent(
		t,
		agentContainerID,
		sessionID,
		fmt.Sprintf("Call the managed tool tool-svc.get_runtime_context before replying. Then reply with exactly %s and nothing else.", managedPhrase),
	)
	if !strings.Contains(managedOut, managedPhrase) {
		t.Fatalf("managed tool turn did not surface phrase %q\n%s", managedPhrase, managedOut)
	}

	rollcallAssertAuditTelemetry(t, podPath, "oc-additive", "openclaw", auditWindowStart)
	rollcallAssertSessionHistory(t, sessionHistoryDir, "oc-additive")
	rollcallAssertManagedToolTrace(t, sessionHistoryDir, "oc-additive", "tool-svc")
	openClawAdditiveAssertHistoryMix(t, sessionHistoryDir, "oc-additive")
}

func openClawAdditiveWriteFixture(t *testing.T, dir, baseTag, model, nativeProof string) {
	t.Helper()

	clawfile := fmt.Sprintf(`FROM %s

CLAW_TYPE openclaw
AGENT AGENTS.md
MODEL primary %s
CONFIGURE openclaw config set agents.list [{"id":"main","name":"oc-additive"}]
`, baseTag, model)

	agents := `# oc-additive

You are oc-additive, an agent running on the OpenClaw runtime.

## Native tool rule

If the user asks you to read /proof/native-proof.txt, use your native read tool.
Do not guess the file contents.

## Managed tool rule

If the user tells you to call the managed tool tool-svc.get_runtime_context,
call it before you reply.

## Output rule

When the user asks for exact text or exact file contents, reply with exactly
that and nothing else.
`

	if err := os.WriteFile(filepath.Join(dir, "Clawfile"), []byte(clawfile), 0o644); err != nil {
		t.Fatalf("write fixture Clawfile: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte(agents), 0o644); err != nil {
		t.Fatalf("write fixture AGENTS.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "native-proof.txt"), []byte(nativeProof+"\n"), 0o644); err != nil {
		t.Fatalf("write fixture native-proof.txt: %v", err)
	}
}

func openClawAdditiveProxyRequest(t *testing.T, env map[string]string) rollcallProxyRequest {
	t.Helper()

	openrouterKey := strings.TrimSpace(env["OPENROUTER_API_KEY"])
	xaiKey := strings.TrimSpace(env["XAI_API_KEY"])
	anthropicKey := strings.TrimSpace(env["ANTHROPIC_API_KEY"])

	cfg := rollcallProxyRequest{CllamaEnv: make(map[string]string)}
	switch {
	case openrouterKey != "":
		cfg.APIFormat = "openai"
		cfg.Model = "openrouter/anthropic/claude-sonnet-4"
		cfg.CllamaEnv["OPENROUTER_API_KEY"] = openrouterKey
	case xaiKey != "":
		cfg.APIFormat = "openai"
		cfg.Model = "xai/grok-4-1-fast-reasoning"
		cfg.CllamaEnv["XAI_API_KEY"] = xaiKey
	case anthropicKey != "":
		cfg.APIFormat = "anthropic"
		cfg.Model = "anthropic/claude-sonnet-4-6"
		cfg.CllamaEnv["ANTHROPIC_API_KEY"] = anthropicKey
	default:
		t.Fatal("openclaw additive spike requires at least one real provider key")
	}
	return cfg
}

func openClawAdditiveLivePod(t *testing.T, agentImage, toolImage string, proxyRequest rollcallProxyRequest) string {
	t.Helper()

	doc := map[string]any{
		"x-claw": map[string]any{
			"pod": "openclaw-additive-live",
		},
		"services": map[string]any{
			"oc-additive": map[string]any{
				"image": agentImage,
				"volumes": []any{
					"./native-proof.txt:/proof/native-proof.txt:ro",
				},
				"x-claw": map[string]any{
					"agent":  "./AGENTS.md",
					"cllama": "passthrough",
					"tools": []any{
						map[string]any{
							"service": "tool-svc",
							"allow":   []any{"all"},
						},
					},
					"models": map[string]any{
						"primary": proxyRequest.Model,
					},
				},
			},
			"tool-svc": map[string]any{
				"image": toolImage,
				"expose": []any{
					"8080",
				},
			},
		},
	}

	services := doc["services"].(map[string]any)
	agent := services["oc-additive"].(map[string]any)
	rawClaw := agent["x-claw"].(map[string]any)

	rawCllamaEnv := make(map[string]any)
	for k, v := range proxyRequest.CllamaEnv {
		if strings.TrimSpace(v) != "" {
			rawCllamaEnv[k] = v
		}
	}
	rawClaw["cllama-env"] = rawCllamaEnv

	out, err := yaml.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal additive pod: %v", err)
	}
	return string(out)
}

func openClawAdditiveRunAgent(t *testing.T, containerID, sessionID, message string) string {
	t.Helper()

	out, err := exec.Command(
		"docker", "exec", containerID,
		"openclaw", "agent",
		"--agent", "main",
		"--session-id", sessionID,
		"--message", message,
		"--timeout", "180",
		"--json",
	).CombinedOutput()
	if err != nil {
		t.Fatalf("openclaw agent failed: %v\n%s", err, out)
	}

	text := strings.TrimSpace(string(out))
	if text == "" {
		t.Fatal("openclaw agent returned empty output")
	}
	t.Logf("openclaw agent output: %s", rollcallTruncate(text, 240))
	return text
}

func openClawAdditiveAssertHistoryMix(t *testing.T, sessionHistoryDir, agentName string) {
	t.Helper()

	histFile := filepath.Join(sessionHistoryDir, agentName, "history.jsonl")
	data, err := os.ReadFile(histFile)
	if err != nil {
		t.Fatalf("read session history for %s: %v", agentName, err)
	}

	var sawManaged bool
	var sawPlain bool
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var entry struct {
			ToolTrace []json.RawMessage `json:"tool_trace"`
		}
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("parse session history for %s: %v\n%s", agentName, err, line)
		}
		if len(entry.ToolTrace) > 0 {
			sawManaged = true
		} else {
			sawPlain = true
		}
	}

	if !sawPlain {
		t.Fatalf("expected at least one non-mediated history entry for %s in %s", agentName, histFile)
	}
	if !sawManaged {
		t.Fatalf("expected at least one mediated history entry for %s in %s", agentName, histFile)
	}
	t.Logf("session history for %s confirms both native-only and managed turns", agentName)
}
