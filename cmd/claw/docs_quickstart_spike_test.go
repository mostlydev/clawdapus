//go:build spike

package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

var docShellBlockRE = regexp.MustCompile("(?s)```(?:bash|sh)\\r?\\n(.*?)\\r?\\n```")

type quickstartDocCase struct {
	name         string
	path         string
	startHeading string
	endHeading   string
	aggregate    bool
}

func TestQuickstartDocsRunInFreshDockerContainer(t *testing.T) {
	requireDockerForQuickstartDocs(t)

	repoRoot := quickstartRepoRoot(t)
	env := loadQuickstartDocEnv(t, repoRoot)
	required := []string{"OPENROUTER_API_KEY", "DISCORD_BOT_TOKEN", "DISCORD_BOT_ID", "DISCORD_GUILD_ID"}
	missing := missingEnvKeys(env, required)
	if len(missing) > 0 {
		t.Skipf("quickstart docs test requires credentials (%s)", strings.Join(missing, ", "))
	}

	linuxClaw := buildLinuxClawBinary(t, repoRoot)

	cases := []quickstartDocCase{
		{
			name:         "root README quickstart",
			path:         filepath.Join(repoRoot, "README.md"),
			startHeading: "## Quickstart (5 minutes)",
			endHeading:   "## Install",
			aggregate:    false,
		},
		{
			name:         "examples quickstart README",
			path:         filepath.Join(repoRoot, "examples", "quickstart", "README.md"),
			startHeading: "## 1. Install",
			endHeading:   "## What's happening under the hood",
			aggregate:    true,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			commandSets := extractRunnableQuickstartCommandSets(t, tc)
			for i, commands := range commandSets {
				idx := i + 1
				t.Run(fmt.Sprintf("block_%d", idx), func(t *testing.T) {
					runQuickstartDocCommandsInContainer(t, repoRoot, linuxClaw, env, commands)
				})
			}
		})
	}
}

func requireDockerForQuickstartDocs(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skipf("docker not found: %v", err)
	}
	cmd := exec.Command("docker", "info")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("docker unavailable: %v\n%s", err, out)
	}
}

func quickstartRepoRoot(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	root := filepath.Join(filepath.Dir(thisFile), "..", "..")
	abs, err := filepath.Abs(root)
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	return abs
}

func loadQuickstartDocEnv(t *testing.T, repoRoot string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, key := range []string{"OPENROUTER_API_KEY", "DISCORD_BOT_TOKEN", "DISCORD_BOT_ID", "DISCORD_GUILD_ID"} {
		if v := strings.TrimSpace(os.Getenv(key)); v != "" {
			out[key] = v
		}
	}

	dotEnvPath := filepath.Join(repoRoot, "examples", "quickstart", ".env")
	fileVals := readDotEnvFile(t, dotEnvPath)
	for key, value := range fileVals {
		if _, exists := out[key]; exists {
			continue
		}
		out[key] = value
	}

	return out
}

func missingEnvKeys(values map[string]string, required []string) []string {
	missing := make([]string, 0)
	for _, key := range required {
		if strings.TrimSpace(values[key]) == "" {
			missing = append(missing, key)
		}
	}
	return missing
}

func readDotEnvFile(t *testing.T, path string) map[string]string {
	t.Helper()
	out := map[string]string{}
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return out
		}
		t.Fatalf("open %s: %v", path, err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		idx := strings.IndexByte(line, '=')
		if idx < 0 {
			continue
		}
		key := strings.TrimSpace(line[:idx])
		val := strings.TrimSpace(line[idx+1:])
		if key != "" && val != "" {
			out[key] = val
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan %s: %v", path, err)
	}
	return out
}

func buildLinuxClawBinary(t *testing.T, repoRoot string) string {
	t.Helper()
	binPath := filepath.Join(t.TempDir(), "claw-linux")
	cmd := exec.Command("go", "build", "-o", binPath, "./cmd/claw")
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(),
		"GOOS=linux",
		"GOARCH=amd64",
		"CGO_ENABLED=0",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build linux claw binary: %v\n%s", err, out)
	}
	return binPath
}

func extractRunnableQuickstartCommandSets(t *testing.T, tc quickstartDocCase) [][]string {
	t.Helper()
	data, err := os.ReadFile(tc.path)
	if err != nil {
		t.Fatalf("read %s: %v", tc.path, err)
	}

	section, err := extractMarkdownSection(string(data), tc.startHeading, tc.endHeading)
	if err != nil {
		t.Fatalf("extract section from %s: %v", tc.path, err)
	}

	matches := docShellBlockRE.FindAllStringSubmatch(section, -1)
	if len(matches) == 0 {
		t.Fatalf("no shell code blocks found in %s section %q", tc.path, tc.startHeading)
	}

	commandSets := make([][]string, 0, len(matches))
	for _, match := range matches {
		block := strings.ReplaceAll(match[1], "\r\n", "\n")
		cmds := normalizeQuickstartBlockCommands(block)
		if len(cmds) == 0 {
			continue
		}
		commandSets = append(commandSets, cmds)
	}
	if len(commandSets) == 0 {
		t.Fatalf("no runnable quickstart commands extracted from %s", tc.path)
	}

	if tc.aggregate {
		merged := make([]string, 0, 32)
		for _, set := range commandSets {
			merged = append(merged, set...)
		}
		commandSets = [][]string{merged}
	}

	for _, set := range commandSets {
		seenUp := false
		for _, cmd := range set {
			if strings.Contains(cmd, "claw up -f claw-pod.yml -d") || strings.Contains(cmd, "claw up -d") {
				seenUp = true
				break
			}
		}
		if !seenUp {
			t.Fatalf("expected quickstart command set from %s to include claw up", tc.path)
		}
	}

	return commandSets
}

func extractMarkdownSection(content, startHeading, endHeading string) (string, error) {
	start := strings.Index(content, startHeading)
	if start < 0 {
		return "", fmt.Errorf("start heading %q not found", startHeading)
	}

	rest := content[start:]
	if strings.TrimSpace(endHeading) == "" {
		return rest, nil
	}

	endRel := strings.Index(rest, "\n"+endHeading)
	if endRel < 0 {
		return rest, nil
	}
	return rest[:endRel], nil
}

func normalizeQuickstartBlockCommands(block string) []string {
	lines := strings.Split(block, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		switch {
		case strings.Contains(trimmed, "raw.githubusercontent.com/mostlydev/clawdapus/master/install.sh"):
			continue
		case strings.HasPrefix(trimmed, "git clone "):
			continue
		case strings.HasPrefix(trimmed, "cd clawdapus/examples/quickstart"):
			continue
		case strings.HasPrefix(trimmed, "source .env"):
			continue
		case strings.HasPrefix(trimmed, "claw logs -f "):
			continue
		case strings.HasPrefix(trimmed, "claw down -f "):
			continue
		case strings.HasPrefix(trimmed, "cp .env.example .env"):
			out = append(out, trimmed)
			out = append(out, quickstartEnvRewriteSnippet())
		case strings.HasPrefix(trimmed, "claw health -f claw-pod.yml"):
			out = append(out, "wait_for_health")
		case strings.HasPrefix(trimmed, "claw agent add "):
			if strings.Contains(trimmed, "--yes") {
				out = append(out, trimmed)
			} else {
				out = append(out, trimmed+" --yes")
			}
		default:
			out = append(out, trimmed)
		}
	}
	return out
}

func quickstartEnvRewriteSnippet() string {
	return strings.Join([]string{
		"cat > .env <<EOF",
		"OPENROUTER_API_KEY=${OPENROUTER_API_KEY}",
		"DISCORD_BOT_TOKEN=${DISCORD_BOT_TOKEN}",
		"DISCORD_BOT_ID=${DISCORD_BOT_ID}",
		"DISCORD_GUILD_ID=${DISCORD_GUILD_ID}",
		"EOF",
	}, "\n")
}

func runQuickstartDocCommandsInContainer(t *testing.T, repoRoot, linuxClaw string, env map[string]string, commands []string) {
	t.Helper()

	scriptLines := []string{
		"set -eu",
		"RUN_DIR=\"$PWD/.quickstart-docs-run\"",
		"compose_project() { basename \"$PWD\" | tr '[:upper:]' '[:lower:]' | sed -E 's/^[^a-z0-9]+//; s/[^a-z0-9]+/-/g; s/-+$//'; }",
		"service_container() { svc=\"$1\"; project=\"$(compose_project)\"; docker ps --filter \"label=com.docker.compose.project=${project}\" --filter \"label=com.docker.compose.service=${svc}\" --format '{{.ID}}' | head -n1; }",
		"service_health() { svc=\"$1\"; cid=\"$(service_container \"$svc\")\"; if [ -z \"$cid\" ]; then echo missing; return 0; fi; docker inspect -f '{{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}}' \"$cid\" 2>/dev/null || echo inspect-error; }",
		"cleanup() { if [ -d \"$RUN_DIR\" ]; then (cd \"$RUN_DIR\" && claw down -f claw-pod.yml >/dev/null 2>&1 || true); fi; if [ -d \"$RUN_DIR/my-pod\" ]; then (cd \"$RUN_DIR/my-pod\" && claw down -f claw-pod.yml >/dev/null 2>&1 || true); fi; }",
		"wait_for_health() { i=0; while [ \"$i\" -lt 60 ]; do assistant=\"$(service_health assistant)\"; cllama=\"$(service_health cllama-passthrough)\"; if [ \"$assistant\" = healthy ] && [ \"$cllama\" = healthy ]; then echo \"assistant=$assistant\"; echo \"cllama-passthrough=$cllama\"; return 0; fi; i=$((i+1)); sleep 2; done; echo \"assistant_docker_health=$(service_health assistant)\"; echo \"cllama_docker_health=$(service_health cllama-passthrough)\"; return 1; }",
		"assert_runtime_signals() { c=\"$(service_container cllama-passthrough)\"; [ -n \"$c\" ] || { echo missing cllama-passthrough container; return 1; }; clog=\"$(docker logs --tail 120 \"$c\" 2>&1 || true)\"; printf '%s\\n' \"$clog\" | grep -q 'api listening on :8080' || { echo \"missing cllama api listening signal\"; printf '%s\\n' \"$clog\"; return 1; }; printf '%s\\n' \"$clog\" | grep -q 'ui listening on :8081' || { echo \"missing cllama ui listening signal\"; printf '%s\\n' \"$clog\"; return 1; }; a=\"$(service_container assistant)\"; [ -n \"$a\" ] || { echo missing assistant container; return 1; }; alog=\"$(docker logs --tail 200 \"$a\" 2>&1 || true)\"; if printf '%s\\n' \"$alog\" | grep -q 'Missing env var'; then echo 'assistant has unresolved env vars'; printf '%s\\n' \"$alog\"; return 1; fi; }",
		"rm -rf \"$RUN_DIR\"",
		"mkdir -p \"$RUN_DIR\"",
		"cp -R ./examples/quickstart/. \"$RUN_DIR/\"",
		"rm -rf \"$RUN_DIR/my-pod\"",
		"cd \"$RUN_DIR\"",
		"cleanup",
		"trap cleanup EXIT",
	}
	scriptLines = append(scriptLines, commands...)
	scriptLines = append(scriptLines, "wait_for_health", "assert_runtime_signals")
	script := strings.Join(scriptLines, "\n")

	args := []string{
		"run", "--rm",
		"-v", repoRoot + ":" + repoRoot,
		"-v", linuxClaw + ":/usr/local/bin/claw:ro",
		"-v", "/var/run/docker.sock:/var/run/docker.sock",
		"-w", repoRoot,
	}

	for _, key := range []string{"OPENROUTER_API_KEY", "DISCORD_BOT_TOKEN", "DISCORD_BOT_ID", "DISCORD_GUILD_ID"} {
		args = append(args, "-e", fmt.Sprintf("%s=%s", key, env[key]))
	}

	args = append(args, "docker:27-cli", "sh", "-lc", script)

	cmd := exec.Command("docker", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("quickstart commands failed in fresh container: %v\n%s", err, string(out))
	}

	output := string(out)
	if !strings.Contains(output, "assistant") {
		t.Fatalf("quickstart output did not include assistant service details:\n%s", output)
	}
	if !strings.Contains(output, "cllama-passthrough") {
		t.Fatalf("quickstart output did not include cllama-passthrough service details:\n%s", output)
	}
	if strings.Contains(output, "unhealthy") || strings.Contains(output, "missing cllama") || strings.Contains(output, "assistant has unresolved env vars") {
		t.Fatalf("quickstart runtime did not stabilize as healthy:\n%s", output)
	}
}
