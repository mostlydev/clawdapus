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

// TestSpikeComposeUpRotatesRuntimeDirSafely covers the live redeploy path that
// broke Tiverton: calling claw up twice on an already-running pod must not
// poison bind-mounted runtime artifacts by deleting .claw-runtime in place.
//
// The regression shape was:
//   - first claw up started the pod normally
//   - second claw up removed .claw-runtime while containers still held mounts
//   - docker recreated missing bind sources as root-owned directories
//   - later materialization failed on chmod/write, and /claw/AGENTS.md became a directory
//
// This spike proves the fix end-to-end by running the same non-root OpenClaw
// pod through runComposeUp twice without an intervening down, then asserting:
//   - the service container was force-recreated onto the new runtime tree
//   - host runtime artifacts remain regular files after the second deploy
//   - in-container /claw/AGENTS.md and /claw/CLAWDAPUS.md are regular files
//   - the non-root user can still create ~/.openclaw/agents after the redeploy
func TestSpikeComposeUpRotatesRuntimeDirSafely(t *testing.T) {
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

	imageTag := fmt.Sprintf("claw-spike-openclaw-runtime-rotation:%d", time.Now().UnixNano())
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
	if err := os.WriteFile(agentPath, []byte("# Runtime Rotation Spike Agent\n\nProve repeated claw up leaves runtime mounts intact.\n"), 0o644); err != nil {
		t.Fatalf("write AGENTS.md: %v", err)
	}

	podPath := filepath.Join(workDir, "claw-pod.yml")
	podYAML := fmt.Sprintf(`x-claw:
  pod: openclaw-runtime-rotation-spike

services:
  rotating:
    image: %s
    x-claw:
      agent: ./agents/AGENTS.md
`, imageTag)
	if err := os.WriteFile(podPath, []byte(podYAML), 0o644); err != nil {
		t.Fatalf("write pod file: %v", err)
	}

	generatedPath := filepath.Join(workDir, "compose.generated.yml")
	runtimeDir := filepath.Join(workDir, ".claw-runtime")

	prevDetach := composeUpDetach
	composeUpDetach = true
	defer func() { composeUpDetach = prevDetach }()
	t.Setenv("CLAWDASH_ADDR", ":"+spikeFreePort(t))

	const composeProject = "openclaw-runtime-rotation-spike"
	spikeCleanupProject(composeProject, generatedPath)
	t.Cleanup(func() {
		spikeCleanupProject(composeProject, generatedPath)
	})

	if err := runComposeUp(podPath); err != nil {
		t.Fatalf("first runComposeUp failed: %v", err)
	}

	firstContainerID := rollcallResolveContainerID(t, generatedPath, "rotating")
	spikeWaitRunning(t, firstContainerID, 30*time.Second)
	spikeWaitHealthy(t, firstContainerID, 60*time.Second)
	assertRuntimeArtifactsAreFiles(t, runtimeDir, "rotating")

	if err := runComposeUp(podPath); err != nil {
		rotationLogContainer(t, firstContainerID)
		t.Fatalf("second runComposeUp failed: %v", err)
	}

	secondContainerID := rollcallResolveContainerID(t, generatedPath, "rotating")
	if secondContainerID == firstContainerID {
		t.Fatalf("expected rotating service to be recreated on second claw up; container id stayed %s", firstContainerID)
	}
	spikeWaitRunning(t, secondContainerID, 30*time.Second)
	spikeWaitHealthy(t, secondContainerID, 60*time.Second)

	assertRuntimeArtifactsAreFiles(t, runtimeDir, "rotating")
	assertContainerArtifactsAreFiles(t, secondContainerID)
	assertNoPreviousRuntimeDirs(t, workDir)
}

func assertRuntimeArtifactsAreFiles(t *testing.T, runtimeDir, serviceName string) {
	t.Helper()

	for _, rel := range []string{
		filepath.Join(serviceName, "AGENTS.effective.md"),
		filepath.Join(serviceName, "CLAWDAPUS.md"),
		filepath.Join(serviceName, "config", "openclaw.json"),
	} {
		path := filepath.Join(runtimeDir, rel)
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat %s: %v", path, err)
		}
		if !info.Mode().IsRegular() {
			t.Fatalf("expected %s to be a regular file, got mode %s", path, info.Mode())
		}
	}
}

func assertContainerArtifactsAreFiles(t *testing.T, containerID string) {
	t.Helper()

	cmd := exec.Command(
		"docker", "exec", containerID,
		"sh", "-lc",
		"test -f /claw/AGENTS.md && test -f /claw/CLAWDAPUS.md && test -f /root/.openclaw/config/openclaw.json && mkdir -p /root/.openclaw/agents/rotation-probe",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		rotationLogContainer(t, containerID)
		t.Fatalf("container runtime artifacts are not healthy after redeploy: %v\n%s", err, strings.TrimSpace(string(out)))
	}
}

func assertNoPreviousRuntimeDirs(t *testing.T, workDir string) {
	t.Helper()

	matches, err := filepath.Glob(filepath.Join(workDir, ".claw-runtime.previous-*"))
	if err != nil {
		t.Fatalf("glob previous runtime dirs: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("expected previous runtime dirs to be cleaned after successful redeploy, found %v", matches)
	}
}

func rotationLogContainer(t *testing.T, containerID string) {
	t.Helper()

	if strings.TrimSpace(containerID) == "" {
		return
	}
	out, err := exec.Command("docker", "logs", "--tail", "80", containerID).CombinedOutput()
	if err != nil {
		t.Logf("docker logs %s failed: %v", containerID, err)
		return
	}
	t.Logf("=== %s logs ===\n%s", containerID, strings.TrimSpace(string(out)))
}
