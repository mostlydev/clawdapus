package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/mostlydev/clawdapus/internal/driver"
	"github.com/mostlydev/clawdapus/internal/pod"
)

func TestBuildPodManifestIncludesResolvedState(t *testing.T) {
	p := &pod.Pod{
		Name: "fleet",
		Services: map[string]*pod.Service{
			"bot": {
				Image: "bot:latest",
				Claw:  &pod.ClawBlock{Count: 2},
			},
			"redis": {
				Image: "redis:7",
			},
		},
	}

	resolved := map[string]*driver.ResolvedClaw{
		"bot": {
			ServiceName: "bot",
			ImageRef:    "bot:latest",
			ClawType:    "openclaw",
			Agent:       "AGENTS.md",
			Models: map[string]string{
				"primary": "anthropic/claude-sonnet-4-20250514",
			},
			Count: 2,
			Handles: map[string]*driver.HandleInfo{
				"discord": {ID: "123", Username: "fleet-bot"},
			},
			PeerHandles: map[string]map[string]*driver.HandleInfo{
				"analyst": {
					"discord": {ID: "456", Username: "analyst-bot"},
				},
			},
			Surfaces: []driver.ResolvedSurface{
				{Scheme: "channel", Target: "discord"},
				{Scheme: "service", Target: "redis", Ports: []string{"6379"}},
			},
			Skills: []driver.ResolvedSkill{
				{Name: "risk-limits.md", HostPath: "/host/risk-limits.md"},
			},
			Invocations: []driver.Invocation{
				{Schedule: "0 * * * *", Message: "status pulse", Name: "status", To: "123"},
			},
			Cllama: []string{"passthrough"},
		},
	}
	proxies := []pod.CllamaProxyConfig{
		{ProxyType: "passthrough", Image: "ghcr.io/mostlydev/cllama-passthrough:latest"},
	}

	got := buildPodManifest(p, resolved, proxies)
	if got.PodName != "fleet" {
		t.Fatalf("expected podName=fleet, got %q", got.PodName)
	}
	if len(got.Services) != 2 {
		t.Fatalf("expected 2 services, got %d", len(got.Services))
	}

	botSvc := got.Services["bot"]
	if botSvc.ClawType != "openclaw" {
		t.Fatalf("expected claw type openclaw, got %q", botSvc.ClawType)
	}
	if botSvc.Count != 2 {
		t.Fatalf("expected count 2, got %d", botSvc.Count)
	}
	if len(botSvc.Skills) != 1 || botSvc.Skills[0] != "risk-limits.md" {
		t.Fatalf("expected skill name-only serialization, got %v", botSvc.Skills)
	}
	if len(botSvc.Cllama) != 1 || botSvc.Cllama[0] != "passthrough" {
		t.Fatalf("expected cllama passthrough, got %v", botSvc.Cllama)
	}

	redisSvc := got.Services["redis"]
	if redisSvc.ClawType != "" {
		t.Fatalf("expected non-claw service clawType empty, got %q", redisSvc.ClawType)
	}
	if redisSvc.Count != 1 {
		t.Fatalf("expected non-claw count 1, got %d", redisSvc.Count)
	}

	if len(got.Proxies) != 1 {
		t.Fatalf("expected 1 proxy, got %d", len(got.Proxies))
	}
	if got.Proxies[0].ServiceName != "cllama-passthrough" {
		t.Fatalf("expected proxy service cllama-passthrough, got %q", got.Proxies[0].ServiceName)
	}
}

func TestWritePodManifestWritesJSONFile(t *testing.T) {
	dir := t.TempDir()
	p := &pod.Pod{
		Name: "test-pod",
		Services: map[string]*pod.Service{
			"bot": {Image: "bot:latest"},
		},
	}

	path, err := writePodManifest(dir, p, nil, nil)
	if err != nil {
		t.Fatalf("writePodManifest returned error: %v", err)
	}
	if path != filepath.Join(dir, "pod-manifest.json") {
		t.Fatalf("unexpected manifest path %q", path)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var decoded map[string]interface{}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("manifest is not valid json: %v", err)
	}
	if decoded["podName"] != "test-pod" {
		t.Fatalf("expected podName=test-pod, got %v", decoded["podName"])
	}
}
