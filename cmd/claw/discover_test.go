package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/mostlydev/clawdapus/internal/describe"
	"github.com/mostlydev/clawdapus/internal/pod"
)

func TestSelectMCPStdioServices(t *testing.T) {
	p := &pod.Pod{Services: map[string]*pod.Service{
		"agent": {Claw: &pod.ClawBlock{Agent: "./AGENTS.md"}},
		"echo": {
			Claw: &pod.ClawBlock{MCPStdio: &pod.MCPStdioBlock{Command: "node"}},
		},
	}}

	names, err := selectMCPStdioServices(p, nil)
	if err != nil {
		t.Fatalf("selectMCPStdioServices: %v", err)
	}
	if len(names) != 1 || names[0] != "echo" {
		t.Fatalf("unexpected services: %v", names)
	}

	if _, err := selectMCPStdioServices(p, []string{"agent"}); err == nil || !strings.Contains(err.Error(), "not an x-claw.mcp-stdio") {
		t.Fatalf("expected non-stdio service rejection, got %v", err)
	}
}

func TestDiscoveryContainerEnvExpandsServiceEnvAndMCPConfig(t *testing.T) {
	podDir := t.TempDir()
	if err := writeRuntimeFile(filepath.Join(podDir, ".env"), []byte("SECRET=from-dotenv\n"), 0o644); err != nil {
		t.Fatalf("write .env: %v", err)
	}

	svc := &pod.Service{
		Environment: map[string]string{
			"API_TOKEN":           "${SECRET}",
			"CLAW_MCP_STDIO_PATH": "/custom-mcp",
		},
		Claw: &pod.ClawBlock{MCPStdio: &pod.MCPStdioBlock{
			Command: "node",
			Args:    []string{"/srv/server.js"},
		}},
	}

	env, path, err := discoveryContainerEnv(podDir, svc)
	if err != nil {
		t.Fatalf("discoveryContainerEnv: %v", err)
	}
	if env["API_TOKEN"] != "from-dotenv" {
		t.Fatalf("expected expanded API_TOKEN, got %q", env["API_TOKEN"])
	}
	if env["CLAW_MCP_STDIO_COMMAND"] != "node" {
		t.Fatalf("expected command env, got %q", env["CLAW_MCP_STDIO_COMMAND"])
	}
	if env["CLAW_MCP_STDIO_ARGS"] != `["/srv/server.js"]` {
		t.Fatalf("expected args JSON, got %q", env["CLAW_MCP_STDIO_ARGS"])
	}
	if path != "/custom-mcp" {
		t.Fatalf("expected custom path, got %q", path)
	}
}

func TestDiscoveryVolumeArgsResolvesRelativeBinds(t *testing.T) {
	podDir := t.TempDir()
	volumes, err := discoveryVolumeArgs(podDir, []interface{}{
		"./echo-server:/srv/echo-server:ro",
		map[string]interface{}{
			"type":      "bind",
			"source":    "./fixtures",
			"target":    "/srv/fixtures",
			"read_only": true,
		},
	})
	if err != nil {
		t.Fatalf("discoveryVolumeArgs: %v", err)
	}
	want0 := filepath.Join(podDir, "echo-server") + ":/srv/echo-server:ro"
	want1 := filepath.Join(podDir, "fixtures") + ":/srv/fixtures:ro"
	if volumes[0] != want0 || volumes[1] != want1 {
		t.Fatalf("unexpected volumes:\n%q\n%q", volumes[0], volumes[1])
	}
}

func TestRedactedDockerArgsHidesEnvValues(t *testing.T) {
	got := redactedDockerArgs([]string{"run", "-e", "PERPLEXITY_API_KEY=secret", "--env=TOKEN=also-secret", "image"})
	if strings.Contains(got, "secret") {
		t.Fatalf("expected env values to be redacted, got %q", got)
	}
	if !strings.Contains(got, "PERPLEXITY_API_KEY=<redacted>") || !strings.Contains(got, "--env=TOKEN=<redacted>") {
		t.Fatalf("unexpected redacted args: %q", got)
	}
}

func TestDiscoveredSnapshotMissingOrStale(t *testing.T) {
	podDir := t.TempDir()
	svc := &pod.Service{
		Image: "wrapper:v1",
		Claw: &pod.ClawBlock{MCPStdio: &pod.MCPStdioBlock{
			Command: "node",
			Args:    []string{"/srv/server.js"},
		}},
	}

	stale, err := discoveredSnapshotMissingOrStale(podDir, "echo", svc)
	if err != nil {
		t.Fatalf("missing snapshot check: %v", err)
	}
	if !stale {
		t.Fatal("missing snapshot should be stale")
	}

	descriptor := &describe.ServiceDescriptor{
		Version:     2,
		Description: "Echo",
		MCP:         &describe.MCPDescriptor{Transport: "streamable_http", Path: "/mcp"},
		Tools: []describe.ToolDescriptor{{
			Name:        "echo",
			Description: "Echo text.",
			InputSchema: map[string]interface{}{"type": "object"},
		}},
		XClawDiscovery: &describe.DiscoveryMetadata{
			Command:      "node",
			Args:         []string{"/srv/server.js"},
			WrapperImage: "wrapper:v1",
		},
	}
	if err := writeDescriptorSnapshot(discoveredSnapshotPath(podDir, "echo"), descriptor); err != nil {
		t.Fatalf("write snapshot: %v", err)
	}

	stale, err = discoveredSnapshotMissingOrStale(podDir, "echo", svc)
	if err != nil {
		t.Fatalf("matching snapshot check: %v", err)
	}
	if stale {
		t.Fatal("matching snapshot should not be stale")
	}

	svc.Claw.MCPStdio.Args = []string{"/srv/other.js"}
	stale, err = discoveredSnapshotMissingOrStale(podDir, "echo", svc)
	if err != nil {
		t.Fatalf("changed snapshot check: %v", err)
	}
	if !stale {
		t.Fatal("changed command args should mark snapshot stale")
	}
}

func TestDiscoverMissingOrStaleSkipsExplicitDescribeFile(t *testing.T) {
	podDir := t.TempDir()
	explicitPath := filepath.Join(podDir, "echo.claw-describe.json")
	if err := writeRuntimeFile(explicitPath, []byte(`{"version":2,"mcp":{"path":"/mcp"},"tools":[{"name":"echo","description":"Echo","inputSchema":{"type":"object"}}]}`), 0o644); err != nil {
		t.Fatalf("write descriptor: %v", err)
	}
	p := &pod.Pod{Services: map[string]*pod.Service{
		"echo": {
			Image: "wrapper:v1",
			Claw: &pod.ClawBlock{
				DescribeFile: "./echo.claw-describe.json",
				MCPStdio:     &pod.MCPStdioBlock{Command: "node"},
			},
		},
	}}

	results, err := discoverMCPStdioServices(nil, podDir, p, nil, discoverSelectionMissingOrStale)
	if err != nil {
		t.Fatalf("discoverMCPStdioServices: %v", err)
	}
	if len(results) != 1 || !results[0].Skipped || results[0].Path != explicitPath {
		t.Fatalf("expected explicit descriptor skip, got %+v", results)
	}
}
