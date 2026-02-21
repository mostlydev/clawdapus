package openclaw

import (
	"strings"
	"testing"

	"github.com/mostlydev/clawdapus/internal/driver"
)

func TestGenerateClawdapusMD(t *testing.T) {
	rc := &driver.ResolvedClaw{
		ServiceName: "researcher",
		ClawType:    "openclaw",
		Surfaces: []driver.ResolvedSurface{
			{Scheme: "volume", Target: "research-cache", AccessMode: "read-write"},
			{Scheme: "channel", Target: "discord", AccessMode: ""},
			{Scheme: "service", Target: "fleet-master", AccessMode: ""},
		},
	}

	md := GenerateClawdapusMD(rc, "research-pod")

	if !strings.Contains(md, "# CLAWDAPUS.md") {
		t.Error("expected CLAWDAPUS.md header")
	}
	// Identity section
	if !strings.Contains(md, "research-pod") {
		t.Error("expected pod name")
	}
	if !strings.Contains(md, "researcher") {
		t.Error("expected service name")
	}
	if !strings.Contains(md, "openclaw") {
		t.Error("expected claw type")
	}
	// Surfaces
	if !strings.Contains(md, "research-cache") {
		t.Error("expected research-cache surface")
	}
	if !strings.Contains(md, "/mnt/research-cache") {
		t.Error("expected mount path for volume surface")
	}
	if !strings.Contains(md, "read-write") {
		t.Error("expected read-write access mode")
	}
	if !strings.Contains(md, "discord") {
		t.Error("expected discord channel surface")
	}
	// Service and channel surfaces reference skills
	if !strings.Contains(md, "skills/surface-fleet-master.md") {
		t.Error("expected skill reference for service surface")
	}
	if !strings.Contains(md, "skills/surface-discord.md") {
		t.Error("expected skill reference for channel surface")
	}
	// Volume surfaces should NOT reference skills
	if strings.Contains(md, "skills/surface-research-cache.md") {
		t.Error("volume surface should not have skill reference")
	}
}

func TestGenerateClawdapusMDNoSurfaces(t *testing.T) {
	rc := &driver.ResolvedClaw{
		ServiceName: "worker",
		ClawType:    "openclaw",
	}

	md := GenerateClawdapusMD(rc, "test-pod")

	if !strings.Contains(md, "# CLAWDAPUS.md") {
		t.Error("expected header")
	}
	if !strings.Contains(md, "No surfaces") {
		t.Error("expected 'No surfaces' message")
	}
	if !strings.Contains(md, "worker") {
		t.Error("expected service name in identity")
	}
}

func TestGenerateClawdapusMDVolumeReadOnly(t *testing.T) {
	rc := &driver.ResolvedClaw{
		ServiceName: "analyst",
		ClawType:    "openclaw",
		Surfaces: []driver.ResolvedSurface{
			{Scheme: "volume", Target: "shared-data", AccessMode: "read-only"},
		},
	}

	md := GenerateClawdapusMD(rc, "research-pod")

	if !strings.Contains(md, "read-only") {
		t.Error("expected read-only access mode")
	}
	if !strings.Contains(md, "/mnt/shared-data") {
		t.Error("expected mount path")
	}
}
