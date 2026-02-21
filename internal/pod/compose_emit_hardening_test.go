package pod

import (
	"strings"
	"testing"

	"github.com/mostlydev/clawdapus/internal/driver"
)

func TestEmitComposeAppliesFailClosedDefaultsWithoutDriverResult(t *testing.T) {
	p := &Pod{
		Name: "defaults-pod",
		Services: map[string]*Service{
			"bot": {
				Image: "ghcr.io/example/bot:v1",
			},
		},
	}

	out, err := EmitCompose(p, map[string]*driver.MaterializeResult{})
	if err != nil {
		t.Fatalf("EmitCompose returned error: %v", err)
	}

	if !strings.Contains(out, "read_only: true") {
		t.Fatal("expected fail-closed read_only: true when driver result is absent")
	}
	if !strings.Contains(out, "restart: on-failure") {
		t.Fatal("expected fail-closed restart: on-failure when driver result is absent")
	}
}

func TestEmitComposeVolumeSurfaceReadOnly(t *testing.T) {
	p := &Pod{
		Name: "surface-pod",
		Services: map[string]*Service{
			"worker": {
				Image: "ghcr.io/example/worker:v1",
				Claw: &ClawBlock{
					Surfaces: []string{"volume://shared-cache read-only"},
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

	if !strings.Contains(out, "volumes:") {
		t.Fatal("expected top-level volumes declaration")
	}
	if !strings.Contains(out, "shared-cache:/mnt/shared-cache:ro") {
		t.Fatal("expected read-only shared-cache mount")
	}
}

func TestEmitComposeVolumeSurfaceOpaqueURI(t *testing.T) {
	p := &Pod{
		Name: "opaque-pod",
		Services: map[string]*Service{
			"worker": {
				Image: "ghcr.io/example/worker:v1",
				Claw: &ClawBlock{
					Surfaces: []string{"volume:shared-cache read-write"},
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

	if !strings.Contains(out, "shared-cache:/mnt/shared-cache:rw") {
		t.Fatal("expected opaque volume URI to resolve to shared-cache mount")
	}
}
