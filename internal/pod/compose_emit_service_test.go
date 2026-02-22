package pod

import (
	"testing"

	"github.com/mostlydev/clawdapus/internal/driver"
	"gopkg.in/yaml.v3"
)

func TestEmitComposeServiceSurfaceTargetGetsNetwork(t *testing.T) {
	p := &Pod{
		Name: "svc-pod",
		Services: map[string]*Service{
			"api-server": {
				Image:  "nginx:alpine",
				Expose: []string{"80"},
			},
			"researcher": {
				Image: "claw-openclaw-example",
				Claw: &ClawBlock{
					Surfaces: []driver.ResolvedSurface{
						{Scheme: "service", Target: "api-server"},
					},
				},
			},
		},
	}

	results := map[string]*driver.MaterializeResult{
		"researcher": {
			ReadOnly: true,
			Restart:  "on-failure",
		},
	}

	out, err := EmitCompose(p, results)
	if err != nil {
		t.Fatalf("EmitCompose returned error: %v", err)
	}

	var cf struct {
		Services map[string]struct {
			Networks []string `yaml:"networks"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal([]byte(out), &cf); err != nil {
		t.Fatalf("failed to parse compose output: %v", err)
	}

	// Claw service should be on claw-internal
	researcher, ok := cf.Services["researcher"]
	if !ok {
		t.Fatal("expected researcher service in output")
	}
	if len(researcher.Networks) == 0 || researcher.Networks[0] != "claw-internal" {
		t.Errorf("expected researcher on claw-internal, got %v", researcher.Networks)
	}

	// Non-claw service targeted by service:// should also be on claw-internal
	apiServer, ok := cf.Services["api-server"]
	if !ok {
		t.Fatal("expected api-server service in output")
	}
	if len(apiServer.Networks) == 0 || apiServer.Networks[0] != "claw-internal" {
		t.Errorf("expected api-server on claw-internal (service surface target), got %v", apiServer.Networks)
	}
}

func TestEmitComposeNonTargetServiceNotOnNetwork(t *testing.T) {
	p := &Pod{
		Name: "mixed-pod",
		Services: map[string]*Service{
			"api-server": {
				Image:  "nginx:alpine",
				Expose: []string{"80"},
			},
			"logging": {
				Image: "fluentd:latest",
			},
			"researcher": {
				Image: "claw-openclaw-example",
				Claw: &ClawBlock{
					Surfaces: []driver.ResolvedSurface{
						{Scheme: "service", Target: "api-server"},
					},
				},
			},
		},
	}

	results := map[string]*driver.MaterializeResult{
		"researcher": {
			ReadOnly: true,
			Restart:  "on-failure",
		},
	}

	out, err := EmitCompose(p, results)
	if err != nil {
		t.Fatalf("EmitCompose returned error: %v", err)
	}

	var cf struct {
		Services map[string]struct {
			Networks []string `yaml:"networks"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal([]byte(out), &cf); err != nil {
		t.Fatalf("failed to parse compose output: %v", err)
	}

	// logging is a non-claw service NOT referenced by any service surface
	logging, ok := cf.Services["logging"]
	if !ok {
		t.Fatal("expected logging service in output")
	}
	if len(logging.Networks) > 0 {
		t.Errorf("expected logging service NOT on any network, got %v", logging.Networks)
	}

	// api-server IS a target, so it should be on claw-internal
	apiServer := cf.Services["api-server"]
	if len(apiServer.Networks) == 0 || apiServer.Networks[0] != "claw-internal" {
		t.Errorf("expected api-server on claw-internal, got %v", apiServer.Networks)
	}
}
