//go:build spike

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestSpikeHermesBaseImageContract(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", ".."))
	tag := fmt.Sprintf("clawdapus-hermes-base-contract:%d", os.Getpid())

	spikeBuildImage(t, filepath.Join(repoRoot, "dockerfiles", "hermes-base"), tag, "Dockerfile")
	t.Cleanup(func() {
		_ = exec.Command("docker", "image", "rm", "-f", tag).Run()
	})

	assertDockerInspectJSON(t, tag, "Config.Entrypoint", `["/usr/bin/tini","--"]`)
	assertDockerInspectJSON(t, tag, "Config.Cmd", `["hermes-entrypoint"]`)

	script := `grep -q 'exec hermes gateway run' /usr/local/bin/hermes-entrypoint
python - <<'PY'
import importlib.util

assert importlib.util.find_spec("minisweagent_path") is not None
import tools.terminal_tool
print("ok")
PY`
	cmd := exec.Command("docker", "run", "--rm", tag, "sh", "-lc", script)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Hermes base image contract failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "ok") {
		t.Fatalf("expected import probe to print ok, got:\n%s", out)
	}
}

func assertDockerInspectJSON(t *testing.T, tag, field, want string) {
	t.Helper()
	cmd := exec.Command("docker", "image", "inspect", "--format", "{{json ."+field+"}}", tag)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("inspect %s: %v\n%s", field, err, out)
	}
	got := strings.TrimSpace(string(out))
	if got != want {
		t.Fatalf("inspect %s = %s, want %s", field, got, want)
	}
}
