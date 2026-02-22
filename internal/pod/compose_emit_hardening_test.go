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
				Claw:  &ClawBlock{},
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

func TestEmitComposeHostSurfaceReadOnly(t *testing.T) {
	p := &Pod{
		Name: "host-pod",
		Services: map[string]*Service{
			"worker": {
				Image: "ghcr.io/example/worker:v1",
				Claw: &ClawBlock{
					Surfaces: []driver.ResolvedSurface{{Scheme: "host", Target: "/data/workspace", AccessMode: "read-only"}},
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

	if !strings.Contains(out, "/data/workspace:/data/workspace:ro") {
		t.Fatal("expected host bind mount /data/workspace:/data/workspace:ro")
	}
}

func TestEmitComposeVolumeSurfaceReadOnly(t *testing.T) {
	p := &Pod{
		Name: "surface-pod",
		Services: map[string]*Service{
			"worker": {
				Image: "ghcr.io/example/worker:v1",
				Claw: &ClawBlock{
					Surfaces: []driver.ResolvedSurface{{Scheme: "volume", Target: "shared-cache", AccessMode: "read-only"}},
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

func TestEmitComposeClawInternalNetwork(t *testing.T) {
	p := &Pod{
		Name: "net-pod",
		Services: map[string]*Service{
			"bot": {
				Image: "ghcr.io/example/bot:v1",
				Claw:  &ClawBlock{},
			},
		},
	}

	results := map[string]*driver.MaterializeResult{
		"bot": {
			ReadOnly: true,
			Restart:  "on-failure",
		},
	}

	out, err := EmitCompose(p, results)
	if err != nil {
		t.Fatalf("EmitCompose returned error: %v", err)
	}

	if !strings.Contains(out, "claw-internal") {
		t.Fatal("expected claw-internal network in output")
	}
	// claw-internal is intentionally NOT internal: true — agents need internet access for LLMs, Discord, etc.
	if strings.Contains(out, "internal: true") {
		t.Fatal("claw-internal must not set internal: true — agents need internet access")
	}
	// Service should be on the claw-internal network
	if !strings.Contains(out, "networks:") {
		t.Fatal("expected networks section in output")
	}
}

func TestEmitComposeNonClawServiceNoNetwork(t *testing.T) {
	p := &Pod{
		Name: "mixed-pod",
		Services: map[string]*Service{
			"bot": {
				Image: "ghcr.io/example/bot:v1",
				Claw:  &ClawBlock{},
			},
			"redis": {
				Image: "redis:7",
				// No Claw block — non-claw service
			},
		},
	}

	results := map[string]*driver.MaterializeResult{
		"bot": {
			ReadOnly: true,
			Restart:  "on-failure",
		},
	}

	out, err := EmitCompose(p, results)
	if err != nil {
		t.Fatalf("EmitCompose returned error: %v", err)
	}

	// The bot service should have claw-internal network
	if !strings.Contains(out, "claw-internal") {
		t.Fatal("expected claw-internal network for claw service")
	}

	// Parse output to check redis doesn't have networks
	lines := strings.Split(out, "\n")
	inRedis := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "redis:" {
			inRedis = true
			continue
		}
		// Next top-level service key
		if inRedis && !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") && trimmed != "" {
			inRedis = false
		}
		if inRedis && strings.Contains(trimmed, "claw-internal") {
			t.Fatal("non-claw service redis should not be on claw-internal network")
		}
	}
}

func TestEmitComposeTmpfsIncludesOpenClawHome(t *testing.T) {
	p := &Pod{
		Name: "tmpfs-pod",
		Services: map[string]*Service{
			"gateway": {
				Image: "claw-openclaw-example",
				Claw:  &ClawBlock{},
			},
		},
	}

	results := map[string]*driver.MaterializeResult{
		"gateway": {
			ReadOnly: true,
			Restart:  "on-failure",
			Tmpfs:    []string{"/tmp", "/run", "/app/data", "/root/.openclaw"},
		},
	}

	out, err := EmitCompose(p, results)
	if err != nil {
		t.Fatalf("EmitCompose returned error: %v", err)
	}

	// All tmpfs paths must appear in output — missing any causes container
	// startup failure with read_only: true (ENOENT on mkdir)
	for _, path := range []string{"/tmp", "/run", "/app/data", "/root/.openclaw"} {
		if !strings.Contains(out, path) {
			t.Errorf("compose output missing tmpfs %q — container will crash with read_only: true", path)
		}
	}
}

func TestEmitComposeVolumeSurfaceOpaqueURI(t *testing.T) {
	p := &Pod{
		Name: "opaque-pod",
		Services: map[string]*Service{
			"worker": {
				Image: "ghcr.io/example/worker:v1",
				Claw: &ClawBlock{
					Surfaces: []driver.ResolvedSurface{{Scheme: "volume", Target: "shared-cache", AccessMode: "read-write"}},
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

func TestEmitComposeRejectsInvalidSurfaceAccessMode(t *testing.T) {
	p := &Pod{
		Name: "invalid-access-pod",
		Services: map[string]*Service{
			"worker": {
				Image: "ghcr.io/example/worker:v1",
				Claw: &ClawBlock{
					Surfaces: []driver.ResolvedSurface{{Scheme: "volume", Target: "shared-cache", AccessMode: "read-only-ish"}},
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

	_, err := EmitCompose(p, results)
	if err == nil {
		t.Fatal("expected invalid surface access mode to fail")
	}
	if !strings.Contains(err.Error(), "unsupported access mode") {
		t.Fatalf("unexpected error: %v", err)
	}
}
