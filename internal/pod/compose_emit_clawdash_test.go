package pod

import (
	"strings"
	"testing"

	"github.com/mostlydev/clawdapus/internal/driver"
	"gopkg.in/yaml.v3"
)

func TestEmitComposeInjectsClawdashDashboard(t *testing.T) {
	p := &Pod{
		Name: "ops-pod",
		Services: map[string]*Service{
			"bot": {
				Image: "ghcr.io/example/bot:latest",
				Claw:  &ClawBlock{},
			},
		},
		Clawdash: &ClawdashConfig{
			Image:              "ghcr.io/mostlydev/clawdash:latest",
			Addr:               ":8082",
			ManifestHostPath:   "/tmp/.claw-runtime/pod-manifest.json",
			DockerSockHostPath: "/var/run/docker.sock",
			CllamaCostsURL:     "http://localhost:8181",
			PodName:            "ops-pod",
		},
	}
	results := map[string]*driver.MaterializeResult{
		"bot": {ReadOnly: true, Restart: "on-failure"},
	}

	out, err := EmitCompose(p, results)
	if err != nil {
		t.Fatalf("EmitCompose returned error: %v", err)
	}

	var cf struct {
		Services map[string]struct {
			Ports       []string          `yaml:"ports"`
			ReadOnly    bool              `yaml:"read_only"`
			Tmpfs       []string          `yaml:"tmpfs"`
			Volumes     []string          `yaml:"volumes"`
			Environment map[string]string `yaml:"environment"`
			Labels      map[string]string `yaml:"labels"`
			Networks    []string          `yaml:"networks"`
			Healthcheck struct {
				Test []string `yaml:"test"`
			} `yaml:"healthcheck"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal([]byte(out), &cf); err != nil {
		t.Fatalf("parse compose yaml: %v", err)
	}

	clawdashSvc, ok := cf.Services["clawdash"]
	if !ok {
		t.Fatal("expected clawdash service in output")
	}
	if !clawdashSvc.ReadOnly {
		t.Fatal("expected clawdash read_only: true")
	}
	if len(clawdashSvc.Ports) != 1 || clawdashSvc.Ports[0] != "8082:8082" {
		t.Fatalf("expected clawdash port 8082:8082, got %v", clawdashSvc.Ports)
	}
	if len(clawdashSvc.Tmpfs) != 1 || clawdashSvc.Tmpfs[0] != "/tmp" {
		t.Fatalf("expected clawdash tmpfs [/tmp], got %v", clawdashSvc.Tmpfs)
	}
	if len(clawdashSvc.Volumes) != 2 {
		t.Fatalf("expected 2 clawdash mounts, got %v", clawdashSvc.Volumes)
	}
	if !strings.Contains(strings.Join(clawdashSvc.Volumes, ","), "/claw/pod-manifest.json:ro") {
		t.Fatalf("expected pod-manifest mount, got %v", clawdashSvc.Volumes)
	}
	if !strings.Contains(strings.Join(clawdashSvc.Volumes, ","), "/var/run/docker.sock:ro") {
		t.Fatalf("expected docker sock read-only mount, got %v", clawdashSvc.Volumes)
	}
	if clawdashSvc.Environment["CLAWDASH_MANIFEST"] != "/claw/pod-manifest.json" {
		t.Fatalf("expected CLAWDASH_MANIFEST env, got %v", clawdashSvc.Environment["CLAWDASH_MANIFEST"])
	}
	if clawdashSvc.Environment["CLAWDASH_CLLAMA_COSTS_URL"] != "http://localhost:8181" {
		t.Fatalf("expected CLAWDASH_CLLAMA_COSTS_URL env, got %v", clawdashSvc.Environment["CLAWDASH_CLLAMA_COSTS_URL"])
	}
	if clawdashSvc.Labels["claw.role"] != "dashboard" {
		t.Fatalf("expected claw.role=dashboard, got %q", clawdashSvc.Labels["claw.role"])
	}
	if len(clawdashSvc.Networks) != 1 || clawdashSvc.Networks[0] != "claw-internal" {
		t.Fatalf("expected clawdash on claw-internal network, got %v", clawdashSvc.Networks)
	}
	if len(clawdashSvc.Healthcheck.Test) < 2 || clawdashSvc.Healthcheck.Test[1] != "/clawdash" {
		t.Fatalf("expected clawdash healthcheck command, got %v", clawdashSvc.Healthcheck.Test)
	}
}

func TestEmitComposeRejectsClawdashWithoutManifestPath(t *testing.T) {
	p := &Pod{
		Name: "ops-pod",
		Services: map[string]*Service{
			"bot": {
				Image: "ghcr.io/example/bot:latest",
				Claw:  &ClawBlock{},
			},
		},
		Clawdash: &ClawdashConfig{
			Image:            "ghcr.io/mostlydev/clawdash:latest",
			PodName:          "ops-pod",
			ManifestHostPath: "",
		},
	}
	results := map[string]*driver.MaterializeResult{
		"bot": {ReadOnly: true, Restart: "on-failure"},
	}

	_, err := EmitCompose(p, results)
	if err == nil {
		t.Fatal("expected error when clawdash manifest host path is empty")
	}
	if !strings.Contains(err.Error(), "manifest host path") {
		t.Fatalf("unexpected error: %v", err)
	}
}
