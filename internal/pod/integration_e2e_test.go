//go:build integration

package pod

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/docker/docker/client"
	"github.com/mostlydev/clawdapus/internal/driver"
	_ "github.com/mostlydev/clawdapus/internal/driver/openclaw"
	"github.com/mostlydev/clawdapus/internal/inspect"
	"github.com/mostlydev/clawdapus/internal/runtime"
)

const stubImage = "claw-integration-test"

func requireDocker(t *testing.T) {
	t.Helper()
	if err := exec.Command("docker", "info").Run(); err != nil {
		t.Skipf("docker not available: %v", err)
	}
}

func buildStubImage(t *testing.T) {
	t.Helper()
	clawBin := findClawBinary(t)
	fixtureDir, _ := filepath.Abs("../../testdata/openclaw-stub")
	cmd := exec.Command(clawBin, "build", "-t", stubImage, fixtureDir)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("failed to build stub image: %v", err)
	}
}

func findClawBinary(t *testing.T) string {
	t.Helper()
	// Try bin/claw from repo root
	bin, _ := filepath.Abs("../../bin/claw")
	if _, err := os.Stat(bin); err == nil {
		return bin
	}
	t.Fatal("claw binary not found at bin/claw — run 'go build -o bin/claw ./cmd/claw' first")
	return ""
}

func copyFixture(t *testing.T, workDir string) {
	t.Helper()
	fixtureDir, _ := filepath.Abs("../../testdata/openclaw-stub")

	for _, name := range []string{"claw-pod.yml", "AGENTS.md"} {
		data, err := os.ReadFile(filepath.Join(fixtureDir, name))
		if err != nil {
			t.Fatalf("read fixture %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(workDir, name), data, 0644); err != nil {
			t.Fatalf("write fixture %s: %v", name, err)
		}
	}
}

func TestE2EBuildAndInspect(t *testing.T) {
	requireDocker(t)
	buildStubImage(t)

	info, err := inspect.Inspect(stubImage)
	if err != nil {
		t.Fatalf("inspect failed: %v", err)
	}

	if info.ClawType != "openclaw" {
		t.Errorf("expected ClawType=openclaw, got %q", info.ClawType)
	}
	if info.Agent != "AGENTS.md" {
		t.Errorf("expected Agent=AGENTS.md, got %q", info.Agent)
	}
	if model, ok := info.Models["primary"]; !ok || model != "test/stub-model" {
		t.Errorf("expected model primary=test/stub-model, got %v", info.Models)
	}
}

func TestE2EComposeLifecycle(t *testing.T) {
	requireDocker(t)
	buildStubImage(t)

	workDir := t.TempDir()
	copyFixture(t, workDir)

	// Parse pod
	podFile := filepath.Join(workDir, "claw-pod.yml")
	f, err := os.Open(podFile)
	if err != nil {
		t.Fatalf("open pod file: %v", err)
	}
	p, err := Parse(f)
	f.Close()
	if err != nil {
		t.Fatalf("parse pod: %v", err)
	}

	// Inspect image, resolve contract, materialize
	info, err := inspect.Inspect(stubImage)
	if err != nil {
		t.Fatalf("inspect: %v", err)
	}

	contract, err := runtime.ResolveContract(workDir, info.Agent)
	if err != nil {
		t.Fatalf("resolve contract: %v", err)
	}

	d, err := driver.Lookup(info.ClawType)
	if err != nil {
		t.Fatalf("lookup driver: %v", err)
	}

	rc := &driver.ResolvedClaw{
		ServiceName:   "gateway",
		ImageRef:      stubImage,
		ClawType:      info.ClawType,
		Agent:         info.Agent,
		AgentHostPath: contract.HostPath,
		Models:        info.Models,
		Configures:    info.Configures,
		Privileges:    info.Privileges,
		Count:         1,
	}
	if err := d.Validate(rc); err != nil {
		t.Fatalf("validate: %v", err)
	}

	runtimeDir := filepath.Join(workDir, ".claw-runtime", "gateway")
	if err := os.MkdirAll(runtimeDir, 0755); err != nil {
		t.Fatalf("mkdir runtime: %v", err)
	}

	result, err := d.Materialize(rc, driver.MaterializeOpts{RuntimeDir: runtimeDir})
	if err != nil {
		t.Fatalf("materialize: %v", err)
	}

	// Emit compose
	results := map[string]*driver.MaterializeResult{"gateway": result}
	output, err := EmitCompose(p, results)
	if err != nil {
		t.Fatalf("emit compose: %v", err)
	}

	composePath := filepath.Join(workDir, "compose.generated.yml")
	if err := os.WriteFile(composePath, []byte(output), 0644); err != nil {
		t.Fatalf("write compose: %v", err)
	}

	// Verify compose content
	if !strings.Contains(output, "read_only: true") {
		t.Error("compose output missing read_only: true")
	}
	if !strings.Contains(output, "claw.pod: integration-test") {
		t.Error("compose output missing claw.pod label")
	}
	if !strings.Contains(output, "claw-internal") {
		t.Error("compose output missing claw-internal network")
	}
	if !strings.Contains(output, "internal: true") {
		t.Error("compose output missing internal: true for claw-internal network")
	}

	// Docker compose up -d
	upCmd := exec.Command("docker", "compose", "-f", composePath, "up", "-d")
	upCmd.Stdout = os.Stderr
	upCmd.Stderr = os.Stderr
	if err := upCmd.Run(); err != nil {
		t.Fatalf("docker compose up: %v", err)
	}

	t.Cleanup(func() {
		downCmd := exec.Command("docker", "compose", "-f", composePath, "down", "--timeout", "5")
		downCmd.Stdout = os.Stderr
		downCmd.Stderr = os.Stderr
		downCmd.Run()
	})

	// Poll for container running state
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		t.Fatalf("docker client: %v", err)
	}
	defer cli.Close()

	containerID := resolveContainerIDForTest(t, composePath, "gateway")

	deadline := time.Now().Add(15 * time.Second)
	for {
		cinfo, err := cli.ContainerInspect(context.Background(), containerID)
		if err == nil && cinfo.State.Running {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("container did not become running within 15s")
		}
		time.Sleep(500 * time.Millisecond)
	}

	// Exec openclaw health --json inside container
	execResult, err := exec.Command("docker", "exec", containerID, "openclaw", "health", "--json").Output()
	if err != nil {
		t.Fatalf("exec health check: %v", err)
	}

	var healthResp map[string]interface{}
	if err := json.Unmarshal(execResult, &healthResp); err != nil {
		t.Fatalf("parse health response: %v (raw: %s)", err, execResult)
	}
	if healthResp["status"] != "ok" {
		t.Errorf("expected status=ok, got %v", healthResp["status"])
	}
}

func TestE2EPostApplyVerifiesRunning(t *testing.T) {
	requireDocker(t)
	buildStubImage(t)

	workDir := t.TempDir()
	copyFixture(t, workDir)

	// Minimal setup: parse, inspect, materialize, emit, up
	podFile := filepath.Join(workDir, "claw-pod.yml")
	f, _ := os.Open(podFile)
	p, _ := Parse(f)
	f.Close()

	info, _ := inspect.Inspect(stubImage)
	contract, _ := runtime.ResolveContract(workDir, info.Agent)
	d, _ := driver.Lookup(info.ClawType)

	rc := &driver.ResolvedClaw{
		ServiceName:   "gateway",
		ImageRef:      stubImage,
		ClawType:      info.ClawType,
		Agent:         info.Agent,
		AgentHostPath: contract.HostPath,
		Models:        info.Models,
		Configures:    info.Configures,
		Privileges:    info.Privileges,
		Count:         1,
	}

	runtimeDir := filepath.Join(workDir, ".claw-runtime", "gateway")
	os.MkdirAll(runtimeDir, 0755)
	result, _ := d.Materialize(rc, driver.MaterializeOpts{RuntimeDir: runtimeDir})

	results := map[string]*driver.MaterializeResult{"gateway": result}
	output, _ := EmitCompose(p, results)
	composePath := filepath.Join(workDir, "compose.generated.yml")
	os.WriteFile(composePath, []byte(output), 0644)

	upCmd := exec.Command("docker", "compose", "-f", composePath, "up", "-d")
	upCmd.Stdout = os.Stderr
	upCmd.Stderr = os.Stderr
	if err := upCmd.Run(); err != nil {
		t.Fatalf("docker compose up: %v", err)
	}

	t.Cleanup(func() {
		downCmd := exec.Command("docker", "compose", "-f", composePath, "down", "--timeout", "5")
		downCmd.Stdout = os.Stderr
		downCmd.Stderr = os.Stderr
		downCmd.Run()
	})

	containerID := resolveContainerIDForTest(t, composePath, "gateway")

	// Wait for running
	deadline := time.Now().Add(15 * time.Second)
	for {
		cli, _ := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
		cinfo, err := cli.ContainerInspect(context.Background(), containerID)
		cli.Close()
		if err == nil && cinfo.State.Running {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("container did not start within 15s")
		}
		time.Sleep(500 * time.Millisecond)
	}

	// PostApply should succeed on running container
	if err := d.PostApply(rc, driver.PostApplyOpts{ContainerID: containerID}); err != nil {
		t.Fatalf("PostApply on running container should succeed: %v", err)
	}

	// Stop container
	stopCmd := exec.Command("docker", "stop", containerID)
	stopCmd.Run()

	// PostApply should fail on stopped container
	if err := d.PostApply(rc, driver.PostApplyOpts{ContainerID: containerID}); err == nil {
		t.Fatal("PostApply on stopped container should fail")
	}
}

func TestE2EHealthProbe(t *testing.T) {
	requireDocker(t)
	buildStubImage(t)

	workDir := t.TempDir()
	copyFixture(t, workDir)

	// Minimal setup: parse, inspect, materialize, emit, up
	podFile := filepath.Join(workDir, "claw-pod.yml")
	f, _ := os.Open(podFile)
	p, _ := Parse(f)
	f.Close()

	info, _ := inspect.Inspect(stubImage)
	contract, _ := runtime.ResolveContract(workDir, info.Agent)
	d, _ := driver.Lookup(info.ClawType)

	rc := &driver.ResolvedClaw{
		ServiceName:   "gateway",
		ImageRef:      stubImage,
		ClawType:      info.ClawType,
		Agent:         info.Agent,
		AgentHostPath: contract.HostPath,
		Models:        info.Models,
		Configures:    info.Configures,
		Privileges:    info.Privileges,
		Count:         1,
	}

	runtimeDir := filepath.Join(workDir, ".claw-runtime", "gateway")
	os.MkdirAll(runtimeDir, 0755)
	result, _ := d.Materialize(rc, driver.MaterializeOpts{RuntimeDir: runtimeDir})

	results := map[string]*driver.MaterializeResult{"gateway": result}
	output, _ := EmitCompose(p, results)
	composePath := filepath.Join(workDir, "compose.generated.yml")
	os.WriteFile(composePath, []byte(output), 0644)

	// Verify network isolation in compose output
	if !strings.Contains(output, "claw-internal") {
		t.Error("compose output missing claw-internal network")
	}
	if !strings.Contains(output, "internal: true") {
		t.Error("compose output missing internal: true for claw-internal network")
	}

	upCmd := exec.Command("docker", "compose", "-f", composePath, "up", "-d")
	upCmd.Stdout = os.Stderr
	upCmd.Stderr = os.Stderr
	if err := upCmd.Run(); err != nil {
		t.Fatalf("docker compose up: %v", err)
	}

	t.Cleanup(func() {
		downCmd := exec.Command("docker", "compose", "-f", composePath, "down", "--timeout", "5")
		downCmd.Stdout = os.Stderr
		downCmd.Stderr = os.Stderr
		downCmd.Run()
	})

	containerID := resolveContainerIDForTest(t, composePath, "gateway")

	// Wait for running
	deadline := time.Now().Add(15 * time.Second)
	for {
		cli, _ := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
		cinfo, err := cli.ContainerInspect(context.Background(), containerID)
		cli.Close()
		if err == nil && cinfo.State.Running {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("container did not start within 15s")
		}
		time.Sleep(500 * time.Millisecond)
	}

	// HealthProbe should succeed on running container
	h, err := d.HealthProbe(driver.ContainerRef{
		ContainerID: containerID,
		ServiceName: "gateway",
	})
	if err != nil {
		t.Fatalf("HealthProbe returned error: %v", err)
	}
	if !h.OK {
		t.Errorf("expected HealthProbe OK=true, got false (detail: %s)", h.Detail)
	}
	if !strings.Contains(h.Detail, "healthy") {
		t.Errorf("expected Detail to contain 'healthy', got %q", h.Detail)
	}
}

func resolveContainerIDForTest(t *testing.T, composePath, service string) string {
	t.Helper()
	out, err := exec.Command("docker", "compose", "-f", composePath, "ps", "-q", service).Output()
	if err != nil {
		t.Fatalf("resolve container ID for %s: %v", service, err)
	}
	id := strings.TrimSpace(string(out))
	if id == "" {
		t.Fatalf("no container ID found for service %s", service)
	}
	// Handle multiple lines (take first)
	if idx := strings.Index(id, "\n"); idx > 0 {
		id = id[:idx]
	}
	return id
}

func init() {
	// Ensure fmt is used
	_ = fmt.Sprintf
}
