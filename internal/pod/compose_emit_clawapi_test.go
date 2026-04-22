package pod

import (
	"strings"
	"testing"

	"github.com/mostlydev/clawdapus/internal/driver"
	"gopkg.in/yaml.v3"
)

func TestEmitComposeInjectsClawAPI(t *testing.T) {
	p := &Pod{
		Name: "ops-pod",
		Services: map[string]*Service{
			"octopus": {
				Image: "ghcr.io/example/octopus:latest",
				Claw: &ClawBlock{
					Surfaces: []driver.ResolvedSurface{
						{Scheme: "service", Target: "claw-api"},
					},
				},
			},
		},
		ClawAPI: &ClawAPIConfig{
			Image:              "ghcr.io/mostlydev/claw-api:latest",
			Addr:               ":8080",
			ManifestHostPath:   "/tmp/.claw-runtime/pod-manifest.json",
			PrincipalsHostPath: "/tmp/.claw-runtime/claw-api/principals.json",
			DockerSockHostPath: "/var/run/docker.sock",
			PodName:            "ops-pod",
		},
	}
	results := map[string]*driver.MaterializeResult{
		"octopus": {ReadOnly: true, Restart: "on-failure"},
	}

	out, err := EmitCompose(p, results)
	if err != nil {
		t.Fatalf("EmitCompose returned error: %v", err)
	}

	var cf struct {
		Services map[string]struct {
			Expose      []string          `yaml:"expose"`
			ReadOnly    bool              `yaml:"read_only"`
			Tmpfs       []string          `yaml:"tmpfs"`
			Volumes     []string          `yaml:"volumes"`
			Environment map[string]string `yaml:"environment"`
			Labels      map[string]string `yaml:"labels"`
			Networks    []string          `yaml:"networks"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal([]byte(out), &cf); err != nil {
		t.Fatalf("parse compose yaml: %v", err)
	}

	clawAPISvc, ok := cf.Services["claw-api"]
	if !ok {
		t.Fatal("expected claw-api service in output")
	}
	// rootfs is read-only; the governance bind mount is writable independently.
	if !clawAPISvc.ReadOnly {
		t.Fatal("expected claw-api read_only: true")
	}
	if len(clawAPISvc.Expose) != 1 || clawAPISvc.Expose[0] != "8080" {
		t.Fatalf("expected claw-api expose 8080, got %v", clawAPISvc.Expose)
	}
	if len(clawAPISvc.Tmpfs) != 1 || clawAPISvc.Tmpfs[0] != "/tmp" {
		t.Fatalf("expected claw-api tmpfs [/tmp], got %v", clawAPISvc.Tmpfs)
	}
	// No GovernanceHostPath set → 3 volumes (manifest, principals, docker socket).
	if len(clawAPISvc.Volumes) != 3 {
		t.Fatalf("expected 3 claw-api mounts, got %v", clawAPISvc.Volumes)
	}
	if !strings.Contains(strings.Join(clawAPISvc.Volumes, ","), "/claw/principals.json:ro") {
		t.Fatalf("expected principals mount, got %v", clawAPISvc.Volumes)
	}
	if clawAPISvc.Environment["CLAW_API_MANIFEST"] != "/claw/pod-manifest.json" {
		t.Fatalf("expected CLAW_API_MANIFEST env, got %v", clawAPISvc.Environment["CLAW_API_MANIFEST"])
	}
	if clawAPISvc.Environment["CLAW_GOVERNANCE_DIR"] != "/claw-governance" {
		t.Fatalf("expected CLAW_GOVERNANCE_DIR env, got %v", clawAPISvc.Environment["CLAW_GOVERNANCE_DIR"])
	}
	if clawAPISvc.Labels["claw.role"] != "governance-api" {
		t.Fatalf("expected claw.role=governance-api, got %q", clawAPISvc.Labels["claw.role"])
	}
	if len(clawAPISvc.Networks) != 1 || clawAPISvc.Networks[0] != "claw-internal" {
		t.Fatalf("expected claw-api on claw-internal network, got %v", clawAPISvc.Networks)
	}
}

func TestEmitComposeInjectsClawAPIScheduleManifestWhenConfigured(t *testing.T) {
	p := &Pod{
		Name: "ops-pod",
		Services: map[string]*Service{
			"octopus": {Image: "ghcr.io/example/octopus:latest", Claw: &ClawBlock{}},
		},
		ClawAPI: &ClawAPIConfig{
			Image:              "ghcr.io/mostlydev/claw-api:latest",
			Addr:               ":8080",
			ManifestHostPath:   "/tmp/.claw-runtime/pod-manifest.json",
			ScheduleHostPath:   "/tmp/.claw-runtime/schedule.json",
			PrincipalsHostPath: "/tmp/.claw-runtime/claw-api/principals.json",
			DockerSockHostPath: "/var/run/docker.sock",
			PodName:            "ops-pod",
		},
	}

	out, err := EmitCompose(p, map[string]*driver.MaterializeResult{
		"octopus": {ReadOnly: true, Restart: "on-failure"},
	})
	if err != nil {
		t.Fatalf("EmitCompose returned error: %v", err)
	}

	var cf struct {
		Services map[string]struct {
			Volumes     []string          `yaml:"volumes"`
			Environment map[string]string `yaml:"environment"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal([]byte(out), &cf); err != nil {
		t.Fatalf("parse compose yaml: %v", err)
	}

	clawAPISvc := cf.Services["claw-api"]
	if !strings.Contains(strings.Join(clawAPISvc.Volumes, ","), "/claw/schedule.json:ro") {
		t.Fatalf("expected schedule manifest mount, got %v", clawAPISvc.Volumes)
	}
	if clawAPISvc.Environment["CLAW_API_SCHEDULE_MANIFEST"] != "/claw/schedule.json" {
		t.Fatalf("expected schedule manifest env, got %v", clawAPISvc.Environment["CLAW_API_SCHEDULE_MANIFEST"])
	}
}

func TestEmitComposeInjectsClawAPIContextMountAndCllamaCredentials(t *testing.T) {
	p := &Pod{
		Name: "ops-pod",
		Services: map[string]*Service{
			"octopus": {Image: "ghcr.io/example/octopus:latest", Claw: &ClawBlock{}},
		},
		ClawAPI: &ClawAPIConfig{
			Image:              "ghcr.io/mostlydev/claw-api:latest",
			Addr:               ":8080",
			ManifestHostPath:   "/tmp/.claw-runtime/pod-manifest.json",
			PrincipalsHostPath: "/tmp/.claw-runtime/claw-api/principals.json",
			DockerSockHostPath: "/var/run/docker.sock",
			ContextHostDir:     "/tmp/.claw-runtime/context",
			CllamaAPIURL:       "http://cllama:8081",
			CllamaAPIToken:     "ui-token",
			PodName:            "ops-pod",
		},
	}

	out, err := EmitCompose(p, map[string]*driver.MaterializeResult{
		"octopus": {ReadOnly: true, Restart: "on-failure"},
	})
	if err != nil {
		t.Fatalf("EmitCompose returned error: %v", err)
	}

	var cf struct {
		Services map[string]struct {
			Volumes     []string          `yaml:"volumes"`
			Environment map[string]string `yaml:"environment"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal([]byte(out), &cf); err != nil {
		t.Fatalf("parse compose yaml: %v", err)
	}

	clawAPISvc := cf.Services["claw-api"]
	joinedVolumes := strings.Join(clawAPISvc.Volumes, ",")
	if !strings.Contains(joinedVolumes, "/tmp/.claw-runtime/context:/claw/context:ro") {
		t.Fatalf("expected context mount, got %v", clawAPISvc.Volumes)
	}
	if clawAPISvc.Environment["CLAW_CONTEXT_ROOT"] != "/claw/context" {
		t.Fatalf("expected CLAW_CONTEXT_ROOT env, got %v", clawAPISvc.Environment["CLAW_CONTEXT_ROOT"])
	}
	if clawAPISvc.Environment["CLAW_CLLAMA_API_URL"] != "http://cllama:8081" {
		t.Fatalf("expected CLAW_CLLAMA_API_URL env, got %v", clawAPISvc.Environment["CLAW_CLLAMA_API_URL"])
	}
	if clawAPISvc.Environment["CLAW_CLLAMA_API_TOKEN"] != "ui-token" {
		t.Fatalf("expected CLAW_CLLAMA_API_TOKEN env, got %v", clawAPISvc.Environment["CLAW_CLLAMA_API_TOKEN"])
	}
}
