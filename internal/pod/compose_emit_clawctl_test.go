package pod

import (
	"strings"
	"testing"

	"github.com/mostlydev/clawdapus/internal/driver"
	"gopkg.in/yaml.v3"
)

func TestEmitComposeInjectsClawctlDashboard(t *testing.T) {
	p := &Pod{
		Name: "ops-pod",
		Services: map[string]*Service{
			"bot": {
				Image: "ghcr.io/example/bot:latest",
				Claw:  &ClawBlock{},
			},
		},
		Clawctl: &ClawctlConfig{
			Image:              "ghcr.io/mostlydev/clawctl:latest",
			Addr:               ":8082",
			ManifestHostPath:   "/tmp/.claw-runtime/pod-manifest.json",
			DockerSockHostPath: "/var/run/docker.sock",
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

	clawctlSvc, ok := cf.Services["clawctl"]
	if !ok {
		t.Fatal("expected clawctl service in output")
	}
	if !clawctlSvc.ReadOnly {
		t.Fatal("expected clawctl read_only: true")
	}
	if len(clawctlSvc.Ports) != 1 || clawctlSvc.Ports[0] != "8082:8082" {
		t.Fatalf("expected clawctl port 8082:8082, got %v", clawctlSvc.Ports)
	}
	if len(clawctlSvc.Tmpfs) != 1 || clawctlSvc.Tmpfs[0] != "/tmp" {
		t.Fatalf("expected clawctl tmpfs [/tmp], got %v", clawctlSvc.Tmpfs)
	}
	if len(clawctlSvc.Volumes) != 2 {
		t.Fatalf("expected 2 clawctl mounts, got %v", clawctlSvc.Volumes)
	}
	if !strings.Contains(strings.Join(clawctlSvc.Volumes, ","), "/claw/pod-manifest.json:ro") {
		t.Fatalf("expected pod-manifest mount, got %v", clawctlSvc.Volumes)
	}
	if !strings.Contains(strings.Join(clawctlSvc.Volumes, ","), "/var/run/docker.sock:ro") {
		t.Fatalf("expected docker sock read-only mount, got %v", clawctlSvc.Volumes)
	}
	if clawctlSvc.Environment["CLAWCTL_MANIFEST"] != "/claw/pod-manifest.json" {
		t.Fatalf("expected CLAWCTL_MANIFEST env, got %v", clawctlSvc.Environment["CLAWCTL_MANIFEST"])
	}
	if clawctlSvc.Labels["claw.role"] != "dashboard" {
		t.Fatalf("expected claw.role=dashboard, got %q", clawctlSvc.Labels["claw.role"])
	}
	if len(clawctlSvc.Networks) != 1 || clawctlSvc.Networks[0] != "claw-internal" {
		t.Fatalf("expected clawctl on claw-internal network, got %v", clawctlSvc.Networks)
	}
	if len(clawctlSvc.Healthcheck.Test) < 2 || clawctlSvc.Healthcheck.Test[1] != "/clawctl" {
		t.Fatalf("expected clawctl healthcheck command, got %v", clawctlSvc.Healthcheck.Test)
	}
}

func TestEmitComposeRejectsClawctlWithoutManifestPath(t *testing.T) {
	p := &Pod{
		Name: "ops-pod",
		Services: map[string]*Service{
			"bot": {
				Image: "ghcr.io/example/bot:latest",
				Claw:  &ClawBlock{},
			},
		},
		Clawctl: &ClawctlConfig{
			Image:            "ghcr.io/mostlydev/clawctl:latest",
			PodName:          "ops-pod",
			ManifestHostPath: "",
		},
	}
	results := map[string]*driver.MaterializeResult{
		"bot": {ReadOnly: true, Restart: "on-failure"},
	}

	_, err := EmitCompose(p, results)
	if err == nil {
		t.Fatal("expected error when clawctl manifest host path is empty")
	}
	if !strings.Contains(err.Error(), "manifest host path") {
		t.Fatalf("unexpected error: %v", err)
	}
}
