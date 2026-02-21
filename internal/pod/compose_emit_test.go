package pod

import (
	"strings"
	"testing"

	"github.com/mostlydev/clawdapus/internal/driver"
)

func TestEmitComposeBasicService(t *testing.T) {
	p := &Pod{
		Name: "test-pod",
		Services: map[string]*Service{
			"bot": {
				Image: "ghcr.io/example/bot:latest",
				Environment: map[string]string{
					"LOG_LEVEL": "info",
					"MODE":      "default",
				},
			},
		},
	}

	results := map[string]*driver.MaterializeResult{
		"bot": {
			ReadOnly: true,
			Restart:  "on-failure:5",
			Tmpfs:    []string{"/tmp", "/run"},
			Environment: map[string]string{
				"MODE":       "enforced",
				"DRIVER_VAR": "injected",
			},
			Healthcheck: &driver.Healthcheck{
				Test:     []string{"CMD", "curl", "-f", "http://localhost:8080/health"},
				Interval: "30s",
				Timeout:  "10s",
				Retries:  3,
			},
		},
	}

	out, err := EmitCompose(p, results)
	if err != nil {
		t.Fatalf("EmitCompose returned error: %v", err)
	}

	// read_only hardening
	if !strings.Contains(out, "read_only: true") {
		t.Error("expected read_only: true in output")
	}

	// tmpfs paths
	if !strings.Contains(out, "/tmp") {
		t.Error("expected /tmp tmpfs in output")
	}
	if !strings.Contains(out, "/run") {
		t.Error("expected /run tmpfs in output")
	}

	// restart policy
	if !strings.Contains(out, "on-failure:5") {
		t.Error("expected restart on-failure:5 in output")
	}

	// healthcheck
	if !strings.Contains(out, "interval: 30s") {
		t.Error("expected healthcheck interval in output")
	}
	if !strings.Contains(out, "retries: 3") {
		t.Error("expected healthcheck retries in output")
	}

	// driver env wins on conflict: MODE should be "enforced", not "default"
	if !strings.Contains(out, "enforced") {
		t.Error("expected driver env to override pod env for MODE")
	}
	if !strings.Contains(out, "DRIVER_VAR") {
		t.Error("expected DRIVER_VAR in environment")
	}

	// pod env preserved for non-conflicting keys
	if !strings.Contains(out, "LOG_LEVEL") {
		t.Error("expected LOG_LEVEL from pod env in output")
	}

	// labels
	if !strings.Contains(out, "claw.pod: test-pod") {
		t.Error("expected claw.pod label in output")
	}
	if !strings.Contains(out, "claw.service: bot") {
		t.Error("expected claw.service label in output")
	}
}

func TestEmitComposeExpandsCount(t *testing.T) {
	p := &Pod{
		Name: "scale-pod",
		Services: map[string]*Service{
			"worker": {
				Image: "ghcr.io/example/worker:v1",
				Claw: &ClawBlock{
					Count: 3,
				},
			},
		},
	}

	results := map[string]*driver.MaterializeResult{
		"worker": {
			ReadOnly: true,
			Restart:  "on-failure",
		},
	}

	out, err := EmitCompose(p, results)
	if err != nil {
		t.Fatalf("EmitCompose returned error: %v", err)
	}

	// Should have ordinal-named services
	if !strings.Contains(out, "worker-0:") {
		t.Error("expected worker-0 service in output")
	}
	if !strings.Contains(out, "worker-1:") {
		t.Error("expected worker-1 service in output")
	}
	if !strings.Contains(out, "worker-2:") {
		t.Error("expected worker-2 service in output")
	}

	// Bare "worker:" without ordinal should NOT appear as a service key.
	// We check that "worker:" only appears in ordinal form or label context.
	lines := strings.Split(out, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		// A top-level service key would be "worker:" with no ordinal suffix
		if trimmed == "worker:" {
			t.Error("bare 'worker:' should not appear; expected ordinal-named services only")
		}
	}

	// Ordinal labels
	if !strings.Contains(out, "claw.ordinal: \"0\"") {
		t.Error("expected claw.ordinal label for ordinal 0")
	}
	if !strings.Contains(out, "claw.ordinal: \"2\"") {
		t.Error("expected claw.ordinal label for ordinal 2")
	}
}

func TestEmitComposeVolumeSurface(t *testing.T) {
	p := &Pod{
		Name: "vol-pod",
		Services: map[string]*Service{
			"processor": {
				Image: "ghcr.io/example/proc:v1",
				Claw: &ClawBlock{
					Surfaces: []string{
						"volume://shared-cache read-write",
					},
				},
			},
		},
	}

	results := map[string]*driver.MaterializeResult{
		"processor": {
			ReadOnly: true,
			Restart:  "on-failure",
		},
	}

	out, err := EmitCompose(p, results)
	if err != nil {
		t.Fatalf("EmitCompose returned error: %v", err)
	}

	// Top-level volumes section should declare shared-cache
	if !strings.Contains(out, "volumes:") {
		t.Error("expected top-level volumes section in output")
	}
	if !strings.Contains(out, "shared-cache") {
		t.Error("expected shared-cache volume in output")
	}

	// Volume mount on the service
	if !strings.Contains(out, "shared-cache:/mnt/shared-cache:rw") {
		t.Error("expected volume mount shared-cache:/mnt/shared-cache:rw in output")
	}
}

func TestEmitComposeIsDeterministic(t *testing.T) {
	p := &Pod{
		Name: "det-pod",
		Services: map[string]*Service{
			"zulu": {
				Image: "ghcr.io/example/zulu:v1",
			},
			"alpha": {
				Image: "ghcr.io/example/alpha:v1",
			},
		},
	}

	results := map[string]*driver.MaterializeResult{
		"zulu": {
			ReadOnly: true,
			Restart:  "on-failure",
		},
		"alpha": {
			ReadOnly: true,
			Restart:  "on-failure",
		},
	}

	out1, err := EmitCompose(p, results)
	if err != nil {
		t.Fatalf("first EmitCompose returned error: %v", err)
	}

	out2, err := EmitCompose(p, results)
	if err != nil {
		t.Fatalf("second EmitCompose returned error: %v", err)
	}

	if out1 != out2 {
		t.Errorf("output is not deterministic:\n--- first ---\n%s\n--- second ---\n%s", out1, out2)
	}

	// alpha should appear before zulu due to sorting
	alphaIdx := strings.Index(out1, "alpha:")
	zuluIdx := strings.Index(out1, "zulu:")
	if alphaIdx == -1 || zuluIdx == -1 {
		t.Fatal("expected both alpha and zulu services in output")
	}
	if alphaIdx > zuluIdx {
		t.Error("expected alpha to appear before zulu (sorted order)")
	}
}
