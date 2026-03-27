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
			{Scheme: "host", Target: "/var/shared/reports", AccessMode: "read-write"},
			{Scheme: "channel", Target: "discord", AccessMode: ""},
			{
				Scheme:     "service",
				Target:     "fleet-master",
				AccessMode: "",
				SkillName:  "fleet-manual.md",
				ServiceInfo: &driver.ServiceSurfaceInfo{
					Description: "Governance API",
					AuthType:    "bearer",
					AuthEnv:     "FLEET_TOKEN",
					Endpoints: []driver.ServiceEndpoint{
						{Method: "GET", Path: "/fleet/status", Description: "Service health"},
					},
				},
			},
		},
		Feeds: []driver.ResolvedFeed{
			{Name: "market-context", Source: "trading-api", Path: "/context", TTL: 180, Description: "Market summary"},
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
	if !strings.Contains(md, "/var/shared/reports") {
		t.Error("expected host surface mount path")
	}
	if !strings.Contains(md, "skills/fleet-manual.md") {
		t.Error("service surface should reference its companion skill file")
	}
	if !strings.Contains(md, "Governance API") {
		t.Error("expected inline service description")
	}
	if !strings.Contains(md, "GET /fleet/status") {
		t.Error("expected endpoint list")
	}
	if !strings.Contains(md, "market-context") {
		t.Error("expected feeds section entry")
	}
}

func TestGenerateClawdapusMDNoSurfaces(t *testing.T) {
	rc := &driver.ResolvedClaw{ServiceName: "worker", ClawType: "openclaw"}
	md := GenerateClawdapusMD(rc, "test-pod")

	if !strings.Contains(md, "No surfaces") {
		t.Error("expected 'No surfaces' message")
	}
}

func TestGenerateClawdapusMDMemorySection(t *testing.T) {
	rc := &driver.ResolvedClaw{ServiceName: "worker", ClawType: "openclaw"}
	md := GenerateClawdapusMD(rc, "test-pod")

	if !strings.Contains(md, "## Memory") {
		t.Fatal("expected Memory section")
	}
	if !strings.Contains(md, PortableMemoryDir) {
		t.Fatal("expected portable memory mount path")
	}
	if !strings.Contains(md, PortableMemoryEnv) {
		t.Fatal("expected portable memory env var")
	}
	if !strings.Contains(md, "/claw/MEMORY.md") {
		t.Fatal("expected openclaw native memory alias")
	}
}

func TestGenerateClawdapusMDHermesMemoryAlias(t *testing.T) {
	rc := &driver.ResolvedClaw{ServiceName: "worker", ClawType: "hermes"}
	md := GenerateClawdapusMD(rc, "test-pod")

	if !strings.Contains(md, "~/.hermes/memories/") {
		t.Fatal("expected Hermes native memory alias")
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
	if !strings.Contains(md, "cllama:8080") {
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

func TestGenerateClawdapusMDPersonaSection(t *testing.T) {
	rc := &driver.ResolvedClaw{
		ServiceName:     "bot",
		ClawType:        "openclaw",
		Persona:         "ghcr.io/mostlydev/personas/allen:latest",
		PersonaHostPath: "/tmp/runtime/persona",
	}
	md := GenerateClawdapusMD(rc, "test-pod")
	if !strings.Contains(md, "Persona Ref") {
		t.Fatal("expected persona ref in identity section")
	}
	if !strings.Contains(md, "## Persona") {
		t.Fatal("expected persona section")
	}
	if !strings.Contains(md, "`persona/`") {
		t.Fatal("expected persona mount path")
	}
}

func TestGenerateClawdapusMDPeerHandles(t *testing.T) {
	rc := &driver.ResolvedClaw{
		ServiceName: "researcher",
		ClawType:    "openclaw",
		PeerHandles: map[string]map[string]*driver.HandleInfo{
			"trader": {
				"discord": {ID: "111", Username: "trader-bot"},
			},
			"auditor": {
				"discord":  {ID: "222", Username: "auditor-bot"},
				"telegram": {ID: "333"},
			},
		},
	}
	md := GenerateClawdapusMD(rc, "test-pod")

	if !strings.Contains(md, "## Peer Handles") {
		t.Fatal("expected Peer Handles section")
	}
	if !strings.Contains(md, "### auditor") {
		t.Error("expected auditor peer heading")
	}
	if !strings.Contains(md, "### trader") {
		t.Error("expected trader peer heading")
	}
	if !strings.Contains(md, "<@111>") {
		t.Error("expected trader discord mention syntax")
	}
	if !strings.Contains(md, "trader-bot") {
		t.Error("expected trader username")
	}
	if !strings.Contains(md, "333") {
		t.Error("expected auditor telegram ID")
	}
}

func TestGenerateClawdapusMDNoPeerHandlesWhenEmpty(t *testing.T) {
	rc := &driver.ResolvedClaw{ServiceName: "solo", ClawType: "openclaw"}
	md := GenerateClawdapusMD(rc, "test-pod")
	if strings.Contains(md, "Peer Handles") {
		t.Error("should not include Peer Handles section when no peers")
	}
}

func TestGenerateClawdapusMDIncludesContextComposition(t *testing.T) {
	rc := &driver.ResolvedClaw{
		ServiceName: "bot",
		ClawType:    "openclaw",
		Includes: []driver.ResolvedInclude{
			{
				ID:          "risk_limits",
				Mode:        "enforce",
				Description: "Hard constraints",
				HostPath:    "/workspace/governance/risk-limits.md",
			},
			{
				ID:          "strategy_notes",
				Mode:        "reference",
				Description: "Desk playbook",
				HostPath:    "/workspace/playbooks/strategy.md",
				SkillName:   "include-strategy_notes.md",
			},
		},
	}

	md := GenerateClawdapusMD(rc, "test-pod")
	if !strings.Contains(md, "## Included Context") {
		t.Fatal("expected Included Context section")
	}
	if !strings.Contains(md, "risk_limits (enforce)") {
		t.Fatal("expected enforce include heading")
	}
	if !strings.Contains(md, "inlined into `/claw/AGENTS.md`") {
		t.Fatal("expected enforce include to note inline contract placement")
	}
	if !strings.Contains(md, "skills/include-strategy_notes.md") {
		t.Fatal("expected reference include skill mount")
	}
}
