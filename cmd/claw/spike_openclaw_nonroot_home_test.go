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
	"time"
)

// TestSpikeOpenClawNonRootHomeReachable is a live regression test for the
// OpenClaw canonical-home non-root crash-loop family.
//
// PR #149 (v0.8.8) moved the openclaw runtime state to /root/.openclaw and
// initially mounted ONLY /root/.openclaw as a tmpfs. That worked for images
// whose USER is root, but non-root users could not even traverse into /root:
//
//	Gateway failed to start: Error: EACCES: permission denied,
//	mkdir '/root/.openclaw/config'
//
// The next fix added a tmpfs at /root so the parent became traversable, but
// Docker still created /root/.openclaw itself as 0755 root:root when mounting
// /root/.openclaw/config. That let non-root users read the config file yet
// still fail on the first state write:
//
//	Error: EACCES: permission denied, mkdir '/root/.openclaw/agents'
//
// The correct runtime contract is therefore both:
//   - /root tmpfs, so non-root users can traverse into ~/.openclaw
//   - ~/.openclaw tmpfs, so the state root itself is writable
//
// This spike builds a minimal openclaw stub image whose Clawfile sets
// USER 1000:1000 and whose entrypoint asserts (before exec'ing the gateway)
// that every component of /root/.openclaw/config is statable, the config file
// is readable, and ~/.openclaw/agents is writable. If either half regresses,
// the entrypoint exits non-zero before the gateway starts, the container fails
// to come up, and this test fails with a clear message.
//
// Requires: Docker available locally.
// Run with: go test -tags spike -run TestSpikeOpenClawNonRootHomeReachable ./cmd/claw/...
func TestSpikeOpenClawNonRootHomeReachable(t *testing.T) {
	if err := exec.Command("docker", "info").Run(); err != nil {
		t.Skipf("docker not available: %v", err)
	}

	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	repoRoot := filepath.Join(filepath.Dir(thisFile), "..", "..")

	fixtureDir := filepath.Join(repoRoot, "testdata", "openclaw-stub-nonroot")
	if _, err := os.Stat(filepath.Join(fixtureDir, "Clawfile")); err != nil {
		t.Fatalf("non-root openclaw stub fixture missing: %v", err)
	}

	imageTag := fmt.Sprintf("claw-spike-openclaw-nonroot:%d", time.Now().UnixNano())
	spikeBuildImage(t, fixtureDir, imageTag, "Clawfile")
	t.Cleanup(func() {
		_, _ = exec.Command("docker", "image", "rm", "-f", imageTag).CombinedOutput()
	})

	spikeEnsureRepoInfraImages(t, repoRoot, infraComponentClawdash)

	workDir := t.TempDir()
	agentsDir := filepath.Join(workDir, "agents")
	if err := os.MkdirAll(agentsDir, 0o755); err != nil {
		t.Fatalf("create agents dir: %v", err)
	}
	agentPath := filepath.Join(agentsDir, "AGENTS.md")
	if err := os.WriteFile(agentPath, []byte("# Non-Root Spike Agent\n\nProve the canonical home is reachable.\n"), 0o644); err != nil {
		t.Fatalf("write AGENTS.md: %v", err)
	}

	podPath := filepath.Join(workDir, "claw-pod.yml")
	podYAML := fmt.Sprintf(`x-claw:
  pod: openclaw-nonroot-home-spike

services:
  nonroot:
    image: %s
    x-claw:
      agent: ./agents/AGENTS.md
`, imageTag)
	if err := os.WriteFile(podPath, []byte(podYAML), 0o644); err != nil {
		t.Fatalf("write pod file: %v", err)
	}

	prevDetach := composeUpDetach
	composeUpDetach = true
	defer func() { composeUpDetach = prevDetach }()
	t.Setenv("CLAWDASH_ADDR", ":"+spikeFreePort(t))

	if err := runComposeUp(podPath); err != nil {
		t.Fatalf("runComposeUp failed: %v", err)
	}

	composePath := filepath.Join(workDir, "compose.generated.yml")
	t.Cleanup(func() {
		cmd := exec.Command("docker", "compose", "-f", composePath, "down", "--volumes", "--remove-orphans")
		_, _ = cmd.CombinedOutput()
	})

	idOut, err := exec.Command("docker", "compose", "-f", composePath, "ps", "-q", "nonroot").Output()
	if err != nil {
		t.Fatalf("docker compose ps nonroot: %v", err)
	}
	containerID := strings.TrimSpace(string(idOut))
	if containerID == "" {
		t.Fatal("nonroot service has no container ID")
	}

	// The bug under test crash-loops on a ~10–30s cadence (mkdir → exit →
	// restart). Wait long enough that any crash loop would visibly tick at
	// least twice, then assert the container is still running with restart
	// count zero.
	spikeWaitRunning(t, containerID, 30*time.Second)
	spikeWaitHealthy(t, containerID, 60*time.Second)

	// Give the on-failure restart policy room to trigger if the entrypoint
	// is dying. 45s is comfortably more than two crash cycles observed in
	// production (~15s each).
	time.Sleep(45 * time.Second)

	stateOut, err := exec.Command("docker", "inspect",
		"--format", "{{.RestartCount}} {{.State.Status}} {{.State.ExitCode}} {{.State.Error}}",
		containerID).Output()
	if err != nil {
		t.Fatalf("docker inspect after settle: %v", err)
	}
	state := strings.TrimSpace(string(stateOut))
	fields := strings.Fields(state)
	if len(fields) < 3 {
		t.Fatalf("unexpected docker inspect output: %q", state)
	}
	restartCount := fields[0]
	status := fields[1]
	exitCode := fields[2]

	if status != "running" {
		logs, _ := exec.Command("docker", "logs", "--tail", "60", containerID).CombinedOutput()
		t.Fatalf("non-root openclaw container is not running: status=%s exit=%s. The driver tmpfs layout is unreachable from a non-root USER. State: %s\nLogs:\n%s", status, exitCode, state, logs)
	}
	if restartCount != "0" {
		logs, _ := exec.Command("docker", "logs", "--tail", "60", containerID).CombinedOutput()
		t.Fatalf("non-root openclaw container restarted %s times — the entrypoint is failing to reach /root/.openclaw/config. Logs:\n%s", restartCount, logs)
	}

	// Sanity check: the container's runtime user really is non-root. If a
	// future change accidentally pinned USER root in the fixture, this spike
	// would silently start passing for the wrong reason.
	uidOut, err := exec.Command("docker", "exec", containerID, "id", "-u").Output()
	if err != nil {
		t.Fatalf("docker exec id -u: %v", err)
	}
	uid := strings.TrimSpace(string(uidOut))
	if uid == "0" {
		t.Fatalf("regression fixture is running as root (uid=0); the non-root regression has no teeth. Check testdata/openclaw-stub-nonroot/Clawfile USER directive.")
	}

	// And the openclaw config file is reachable from that uid — proves the
	// /root tmpfs traversal works end-to-end.
	if out, err := exec.Command("docker", "exec", containerID, "cat", "/root/.openclaw/config/openclaw.json").CombinedOutput(); err != nil {
		t.Fatalf("uid %s cannot read /root/.openclaw/config/openclaw.json after container is up: %v\n%s", uid, err, out)
	}
	if out, err := exec.Command("docker", "exec", containerID, "mkdir", "-p", "/root/.openclaw/agents/post-start-probe").CombinedOutput(); err != nil {
		t.Fatalf("uid %s cannot create ~/.openclaw/agents after container is up: %v\n%s", uid, err, out)
	}
}
