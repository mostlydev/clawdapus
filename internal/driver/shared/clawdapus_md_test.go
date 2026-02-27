package shared

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
	if !strings.Contains(md, "research-pod") {
		t.Error("expected pod name")
	}
	if !strings.Contains(md, "researcher") {
		t.Error("expected service name")
	}
	if !strings.Contains(md, "DISCORD_BOT_TOKEN") {
		t.Error("expected token env var hint for channel surface")
	}
	if !strings.Contains(md, "skills/surface-fleet-master.md") {
		t.Error("service surface should reference its companion skill file")
	}
}

func TestGenerateClawdapusMDNoSurfaces(t *testing.T) {
	rc := &driver.ResolvedClaw{ServiceName: "worker", ClawType: "openclaw"}
	md := GenerateClawdapusMD(rc, "test-pod")

	if !strings.Contains(md, "No surfaces") {
		t.Error("expected 'No surfaces' message")
	}
}

func TestClawdapusMDIncludesProxySection(t *testing.T) {
	rc := &driver.ResolvedClaw{
		ServiceName: "tiverton",
		ClawType:    "openclaw",
		Cllama:      []string{"passthrough"},
	}
	md := GenerateClawdapusMD(rc, "test-pod")
	if !strings.Contains(md, "## LLM Proxy") {
		t.Error("expected LLM Proxy section")
	}
	if !strings.Contains(md, "cllama-passthrough:8080") {
		t.Error("expected proxy endpoint with type name")
	}
}

func TestClawdapusMDNoProxyWhenNoCllama(t *testing.T) {
	rc := &driver.ResolvedClaw{ServiceName: "tiverton", ClawType: "openclaw"}
	md := GenerateClawdapusMD(rc, "test-pod")
	if strings.Contains(md, "## LLM Proxy") {
		t.Error("should not include proxy section")
	}
}

func TestGenerateClawdapusMDListsExplicitSkills(t *testing.T) {
	rc := &driver.ResolvedClaw{
		ServiceName: "worker",
		ClawType:    "openclaw",
		Skills: []driver.ResolvedSkill{
			{Name: "custom-workflow.md", HostPath: "/tmp/skills/custom-workflow.md"},
		},
	}
	md := GenerateClawdapusMD(rc, "test-pod")
	if !strings.Contains(md, "skills/custom-workflow.md") {
		t.Error("expected custom-workflow.md in skills section")
	}
}

func TestGenerateClawdapusMDHandlesSection(t *testing.T) {
	rc := &driver.ResolvedClaw{
		ServiceName: "bot",
		ClawType:    "openclaw",
		Handles: map[string]*driver.HandleInfo{
			"discord": {
				ID:       "123456789",
				Username: "crypto-bot",
				Guilds: []driver.GuildInfo{
					{ID: "111222333", Name: "Crypto Ops HQ"},
				},
			},
		},
	}
	md := GenerateClawdapusMD(rc, "test-pod")
	if !strings.Contains(md, "## Handles") {
		t.Error("expected Handles section header")
	}
	if !strings.Contains(md, "### discord") {
		t.Error("expected discord platform header")
	}
	if !strings.Contains(md, "123456789") {
		t.Error("expected handle ID")
	}
}
