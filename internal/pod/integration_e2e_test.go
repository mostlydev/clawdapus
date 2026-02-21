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
	"gopkg.in/yaml.v3"
)

const stubImage = "claw-integration-test"

const phase3MultiClawPodYAML = `x-claw:
  pod: research-pod

services:
  researcher:
    image: claw-integration-test
    x-claw:
      agent: ./agents/RESEARCHER.md
      surfaces:
        - "volume://research-cache read-write"

  analyst:
    image: claw-integration-test
    x-claw:
      agent: ./agents/ANALYST.md
      surfaces:
        - "volume://research-cache read-only"
`

const phase3ResearcherAgent = `# Researcher Agent Contract

You are a researcher agent. Your job is to gather and organize information.
`

const phase3AnalystAgent = `# Analyst Agent Contract

You are an analyst agent. Your job is to read research data and produce insights.
`

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

func writePhase3MultiClawFixture(t *testing.T, workDir string) {
	t.Helper()

	files := map[string]string{
		"claw-pod.yml":         phase3MultiClawPodYAML,
		"agents/RESEARCHER.md": phase3ResearcherAgent,
		"agents/ANALYST.md":    phase3AnalystAgent,
	}

	if err := os.MkdirAll(filepath.Join(workDir, "agents"), 0755); err != nil {
		t.Fatalf("create agents dir: %v", err)
	}

	for name, body := range files {
		if err := os.WriteFile(filepath.Join(workDir, name), []byte(body), 0644); err != nil {
			t.Fatalf("write fixture %s: %v", name, err)
		}
	}
}

func parsePhase3Materialization(t *testing.T, podFile string) (map[string]*driver.MaterializeResult, *Pod, []byte, func()) {
	t.Helper()

	podDir := filepath.Dir(podFile)

	f, err := os.Open(podFile)
	if err != nil {
		t.Fatalf("open pod file: %v", err)
	}
	pod, err := Parse(f)
	f.Close()
	if err != nil {
		t.Fatalf("parse pod file: %v", err)
	}

	info, err := inspect.Inspect(stubImage)
	if err != nil {
		t.Fatalf("inspect image: %v", err)
	}

	d, err := driver.Lookup(info.ClawType)
	if err != nil {
		t.Fatalf("lookup driver: %v", err)
	}

	results := make(map[string]*driver.MaterializeResult, len(pod.Services))

	for name, svc := range pod.Services {
		if svc.Claw == nil {
			t.Fatalf("expected claw-managed service %q in phase 3 fixture", name)
		}

		contract, err := runtime.ResolveContract(podDir, svc.Claw.Agent)
		if err != nil {
			t.Fatalf("resolve contract for %s: %v", name, err)
		}

		var surfaces []driver.ResolvedSurface
		for _, raw := range svc.Claw.Surfaces {
			s, err := ParseSurface(raw)
			if err != nil {
				t.Fatalf("parse surface %q for %q: %v", raw, name, err)
			}
			surfaces = append(surfaces, s)
		}

		rc := &driver.ResolvedClaw{
			ServiceName:   name,
			ImageRef:      svc.Image,
			ClawType:      info.ClawType,
			Agent:         filepath.Base(svc.Claw.Agent),
			AgentHostPath: contract.HostPath,
			Models:        info.Models,
			Configures:    info.Configures,
			Privileges:    info.Privileges,
			Count:         svc.Claw.Count,
			Environment:   svc.Environment,
			Surfaces:      surfaces,
		}

		if err := d.Validate(rc); err != nil {
			t.Fatalf("validate %s: %v", name, err)
		}

		svcRuntimeDir := filepath.Join(podDir, ".claw-runtime", name)
		if err := os.MkdirAll(svcRuntimeDir, 0700); err != nil {
			t.Fatalf("create runtime dir %s: %v", name, err)
		}

		result, err := d.Materialize(rc, driver.MaterializeOpts{RuntimeDir: svcRuntimeDir, PodName: pod.Name})
		if err != nil {
			t.Fatalf("materialize %s: %v", name, err)
		}
		results[name] = result

		clawdapusPath := filepath.Join(svcRuntimeDir, "CLAWDAPUS.md")
		clawdapus, err := os.ReadFile(clawdapusPath)
		if err != nil {
			t.Fatalf("read CLAWDAPUS for %s: %v", name, err)
		}
		if !strings.Contains(string(clawdapus), "# CLAWDAPUS.md") {
			t.Fatalf("missing CLAWDAPUS header for %s", name)
		}
		if !strings.Contains(string(clawdapus), "- **Pod:** research-pod") {
			t.Fatalf("missing pod identity for %s", name)
		}
		if !strings.Contains(string(clawdapus), "- **Service:** "+name) {
			t.Fatalf("missing service identity for %s", name)
		}
		if !strings.Contains(string(clawdapus), "research-cache") {
			t.Fatalf("missing research-cache surface for %s", name)
		}
		if strings.Contains(string(clawdapus), "## Skills") {
			// no-op, confirm generated format exists
		}
	}

	compose, err := EmitCompose(pod, results)
	if err != nil {
		t.Fatalf("emit compose: %v", err)
	}

	cleanup := func() {
		_ = os.RemoveAll(filepath.Join(podDir, ".claw-runtime"))
	}

	return results, pod, []byte(compose), cleanup
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

func TestE2EPhase3MultiClawContextAndHooks(t *testing.T) {
	requireDocker(t)
	buildStubImage(t)

	workDir := t.TempDir()
	writePhase3MultiClawFixture(t, workDir)
	podFile := filepath.Join(workDir, "claw-pod.yml")

	results, p, composeData, cleanup := parsePhase3Materialization(t, podFile)
	defer cleanup()

	var cf struct {
		Services map[string]struct {
			Volumes  []string `yaml:"volumes"`
			Networks []string `yaml:"networks"`
		} `yaml:"services"`
		Volumes  map[string]interface{} `yaml:"volumes"`
		Networks map[string]struct {
			Internal bool `yaml:"internal"`
		} `yaml:"networks"`
	}
	if err := yaml.Unmarshal(composeData, &cf); err != nil {
		t.Fatalf("parse compose output: %v", err)
	}

	if _, ok := cf.Volumes["research-cache"]; !ok {
		t.Fatal("expected top-level research-cache volume declaration")
	}

	researcher, ok := cf.Services["researcher"]
	if !ok {
		t.Fatal("expected researcher service in compose output")
	}
	foundResearcherRW := false
	for _, v := range researcher.Volumes {
		if v == "research-cache:/mnt/research-cache:rw" {
			foundResearcherRW = true
		}
	}
	if !foundResearcherRW {
		t.Fatalf("expected researcher rw volume mount, got %v", researcher.Volumes)
	}

	analyst, ok := cf.Services["analyst"]
	if !ok {
		t.Fatal("expected analyst service in compose output")
	}
	foundAnalystRO := false
	for _, v := range analyst.Volumes {
		if v == "research-cache:/mnt/research-cache:ro" {
			foundAnalystRO = true
		}
	}
	if !foundAnalystRO {
		t.Fatalf("expected analyst ro volume mount, got %v", analyst.Volumes)
	}

	for _, name := range []string{"researcher", "analyst"} {
		service, ok := cf.Services[name]
		if !ok || len(service.Networks) == 0 || service.Networks[0] != "claw-internal" {
			t.Fatalf("expected %s on claw-internal network, got %v", name, service.Networks)
		}
	}
	if net, ok := cf.Networks["claw-internal"]; !ok || !net.Internal {
		t.Fatal("expected claw-internal network internal=true")
	}

	if _, ok := results["researcher"]; !ok {
		t.Fatal("expected materialize result for researcher")
	}
	if _, ok := results["analyst"]; !ok {
		t.Fatal("expected materialize result for analyst")
	}

	for _, svc := range []string{"researcher", "analyst"} {
		cfgPath := filepath.Join(workDir, ".claw-runtime", svc, "openclaw.json")
		cfgData, err := os.ReadFile(cfgPath)
		if err != nil {
			t.Fatalf("read openclaw config for %s: %v", svc, err)
		}
		var cfg map[string]interface{}
		if err := json.Unmarshal(cfgData, &cfg); err != nil {
			t.Fatalf("unmarshal openclaw config for %s: %v", svc, err)
		}
		hooks, ok := cfg["hooks"].(map[string]interface{})
		if !ok {
			t.Fatalf("expected hooks for %s", svc)
		}
		bef, ok := hooks["bootstrap-extra-files"].(map[string]interface{})
		if !ok {
			t.Fatalf("expected bootstrap-extra-files for %s", svc)
		}
		paths, ok := bef["paths"].([]interface{})
		if !ok || len(paths) == 0 {
			t.Fatalf("expected bootstrap-extra-files.paths for %s", svc)
		}
		if paths[0] != "CLAWDAPUS.md" {
			t.Fatalf("expected CLAWDAPUS.md in bootstrap paths for %s, got %v", svc, paths)
		}
	}

	mdPath := filepath.Join(workDir, ".claw-runtime", "researcher", "CLAWDAPUS.md")
	md := mustReadFile(t, mdPath)
	if !strings.Contains(string(md), "read-write") {
		t.Fatal("expected researcher CLAWDAPUS.md to include read-write access")
	}
	md = mustReadFile(t, filepath.Join(workDir, ".claw-runtime", "analyst", "CLAWDAPUS.md"))
	if !strings.Contains(string(md), "read-only") {
		t.Fatal("expected analyst CLAWDAPUS.md to include read-only access")
	}

	if !strings.Contains(p.Name, "research-pod") {
		t.Fatalf("expected parsed pod name research-pod, got %q", p.Name)
	}
}

func TestE2EPhase3MultiClawComposeLifecycle(t *testing.T) {
	requireDocker(t)
	buildStubImage(t)

	workDir := t.TempDir()
	writePhase3MultiClawFixture(t, workDir)
	podFile := filepath.Join(workDir, "claw-pod.yml")
	_, p, composeData, cleanup := parsePhase3Materialization(t, podFile)
	defer cleanup()

	composePath := filepath.Join(workDir, "compose.generated.yml")
	if err := os.WriteFile(composePath, composeData, 0644); err != nil {
		t.Fatalf("write compose file: %v", err)
	}

	// Fail-closed defaults apply to both Claw services.
	out := string(composeData)
	if !strings.Contains(out, "restart: on-failure") {
		t.Fatal("expected on-failure restart policy for all claw services")
	}
	if !strings.Contains(out, "read_only: true") {
		t.Fatal("expected read_only: true for all claw services")
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

	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		t.Fatalf("docker client: %v", err)
	}
	defer cli.Close()

	for _, svc := range []string{"researcher", "analyst"} {
		containerID := resolveContainerIDForTest(t, composePath, svc)
		deadline := time.Now().Add(20 * time.Second)
		for {
			cinfo, err := cli.ContainerInspect(context.Background(), containerID)
			if err == nil && cinfo.State != nil && cinfo.State.Running {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("container %s did not become running", containerID)
			}
			time.Sleep(500 * time.Millisecond)
		}

		clawdapus, err := os.ReadFile(filepath.Join(workDir, ".claw-runtime", svc, "CLAWDAPUS.md"))
		if err != nil {
			t.Fatalf("read clawdapus for %s: %v", svc, err)
		}
		if !strings.Contains(string(clawdapus), "- **Service:** "+svc) {
			t.Fatalf("CLAWDAPUS.md missing service %s identity", svc)
		}
		if !strings.Contains(string(clawdapus), "- **Pod:** research-pod") {
			t.Fatalf("CLAWDAPUS.md missing pod identity for %s", svc)
		}
		if svc == "researcher" && !strings.Contains(string(clawdapus), "read-write") {
			t.Fatalf("expected researcher read-write surface in CLAWDAPUS.md")
		}
		if svc == "analyst" && !strings.Contains(string(clawdapus), "read-only") {
			t.Fatalf("expected analyst read-only surface in CLAWDAPUS.md")
		}
	}
	if p == nil || p.Name != "research-pod" {
		t.Fatalf("expected parsed pod name research-pod, got %+v", p)
	}
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file %s: %v", path, err)
	}
	return data
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
