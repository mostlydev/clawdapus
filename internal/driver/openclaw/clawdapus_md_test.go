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
	// Service surface references skill file
	if !strings.Contains(md, "skills/surface-fleet-master.md") {
		t.Error("service surface should reference its companion skill file")
	}
	// Channel surface should describe token and reference its skill file
	if !strings.Contains(md, "DISCORD_BOT_TOKEN") {
		t.Error("expected token env var hint for channel surface")
	}
	if !strings.Contains(md, "skills/surface-discord.md") {
		t.Error("expected channel skill in Skills index")
	}
	// Volume surfaces do NOT get skill references
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

func TestClawdapusMDMultipleProxies(t *testing.T) {
	rc := &driver.ResolvedClaw{
		ServiceName: "tiverton",
		ClawType:    "openclaw",
		Cllama:      []string{"passthrough", "policy"},
	}
	md := GenerateClawdapusMD(rc, "test-pod")
	if !strings.Contains(md, "passthrough -> policy") {
		t.Error("expected chain description")
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
			{Name: "team-conventions.md", HostPath: "/tmp/skills/team-conventions.md"},
		},
	}

	md := GenerateClawdapusMD(rc, "test-pod")

	if !strings.Contains(md, "skills/custom-workflow.md") {
		t.Error("expected custom-workflow.md in skills section")
	}
	if !strings.Contains(md, "skills/team-conventions.md") {
		t.Error("expected team-conventions.md in skills section")
	}
	if strings.Contains(md, "No skills available") {
		t.Error("should not say no skills when explicit skills are present")
	}
}

func TestGenerateClawdapusMDServiceSkillInSkillsSection(t *testing.T) {
	rc := &driver.ResolvedClaw{
		ServiceName: "researcher",
		ClawType:    "openclaw",
		Surfaces: []driver.ResolvedSurface{
			{Scheme: "service", Target: "api-server"},
			{Scheme: "volume", Target: "data", AccessMode: "read-write"},
		},
		Skills: []driver.ResolvedSkill{
			{Name: "custom-workflow.md", HostPath: "/tmp/skills/custom-workflow.md"},
		},
	}

	md := GenerateClawdapusMD(rc, "test-pod")

	// Service surface skill in skills section
	if !strings.Contains(md, "skills/surface-api-server.md") {
		t.Error("expected surface-api-server.md in skills section")
	}
	if !strings.Contains(md, "api-server service surface") {
		t.Error("expected service surface description in skills section")
	}
	// Operator skill also present
	if !strings.Contains(md, "skills/custom-workflow.md") {
		t.Error("expected operator skill alongside surface skill")
	}
	// Volume surface should NOT appear in skills section
	if strings.Contains(md, "surface-data.md") {
		t.Error("volume surface should not generate skill reference")
	}
	// Should not say "no skills"
	if strings.Contains(md, "No skills available") {
		t.Error("should not say no skills when skills are present")
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
					{
						ID:   "111222333",
						Name: "Crypto Ops HQ",
						Channels: []driver.ChannelInfo{
							{ID: "987654321", Name: "bot-commands"},
							{ID: "555666777", Name: "crypto-alerts"},
						},
					},
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
	if !strings.Contains(md, "crypto-bot") {
		t.Error("expected username")
	}
	if !strings.Contains(md, "111222333") {
		t.Error("expected guild ID")
	}
	if !strings.Contains(md, "Crypto Ops HQ") {
		t.Error("expected guild name")
	}
	if !strings.Contains(md, "987654321") {
		t.Error("expected channel ID")
	}
	if !strings.Contains(md, "#bot-commands") {
		t.Error("expected channel name with # prefix")
	}
}

func TestGenerateClawdapusMDHandlesSectionSortedAlphabetically(t *testing.T) {
	rc := &driver.ResolvedClaw{
		ServiceName: "bot",
		ClawType:    "openclaw",
		Handles: map[string]*driver.HandleInfo{
			"telegram": {ID: "333"},
			"discord":  {ID: "111"},
			"slack":    {ID: "U222"},
		},
	}

	md := GenerateClawdapusMD(rc, "test-pod")

	discordPos := strings.Index(md, "### discord")
	slackPos := strings.Index(md, "### slack")
	telegramPos := strings.Index(md, "### telegram")

	if discordPos < 0 || slackPos < 0 || telegramPos < 0 {
		t.Fatal("expected all three platform headers")
	}
	if !(discordPos < slackPos && slackPos < telegramPos) {
		t.Error("expected platforms sorted alphabetically: discord < slack < telegram")
	}
}

func TestGenerateClawdapusMDHandlesSectionAbsentWhenNoHandles(t *testing.T) {
	rc := &driver.ResolvedClaw{
		ServiceName: "bot",
		ClawType:    "openclaw",
		Handles:     nil,
	}

	md := GenerateClawdapusMD(rc, "test-pod")

	if strings.Contains(md, "## Handles") {
		t.Error("expected no Handles section when no handles declared")
	}
}

func TestGenerateClawdapusMDHandleWithoutUsername(t *testing.T) {
	rc := &driver.ResolvedClaw{
		ServiceName: "bot",
		ClawType:    "openclaw",
		Handles: map[string]*driver.HandleInfo{
			"slack": {ID: "U012345"},
		},
	}

	md := GenerateClawdapusMD(rc, "test-pod")

	if !strings.Contains(md, "U012345") {
		t.Error("expected Slack handle ID")
	}
	if strings.Contains(md, "**Username:**") {
		t.Error("expected no username line when username is empty")
	}
}

func TestGenerateClawdapusMDHandleGuildWithoutName(t *testing.T) {
	rc := &driver.ResolvedClaw{
		ServiceName: "bot",
		ClawType:    "openclaw",
		Handles: map[string]*driver.HandleInfo{
			"discord": {
				ID: "123",
				Guilds: []driver.GuildInfo{
					{ID: "999"},
				},
			},
		},
	}

	md := GenerateClawdapusMD(rc, "test-pod")

	if !strings.Contains(md, "999") {
		t.Error("expected guild ID")
	}
	// Should not have parentheses for guild name when name is empty
	if strings.Contains(md, "999 (") {
		t.Error("expected no parenthesized name when guild name is empty")
	}
}

func TestGenerateClawdapusMDHandleSkillsInSkillsSection(t *testing.T) {
	rc := &driver.ResolvedClaw{
		ServiceName: "bot",
		ClawType:    "openclaw",
		Handles: map[string]*driver.HandleInfo{
			"discord":  {ID: "111"},
			"telegram": {ID: "333"},
		},
		Skills: []driver.ResolvedSkill{
			{Name: "custom-workflow.md", HostPath: "/tmp/skills/custom-workflow.md"},
			// Auto-generated handle skills that should be filtered from operator section
			{Name: "handle-discord.md", HostPath: "/tmp/.claw-runtime/bot/skills/handle-discord.md"},
			{Name: "handle-telegram.md", HostPath: "/tmp/.claw-runtime/bot/skills/handle-telegram.md"},
		},
	}

	md := GenerateClawdapusMD(rc, "test-pod")

	// Handle skills appear in Skills section
	if !strings.Contains(md, "skills/handle-discord.md") {
		t.Error("expected handle-discord.md in skills section")
	}
	if !strings.Contains(md, "skills/handle-telegram.md") {
		t.Error("expected handle-telegram.md in skills section")
	}
	// Operator skill appears too
	if !strings.Contains(md, "skills/custom-workflow.md") {
		t.Error("expected operator skill in skills section")
	}
	// Handle skills should NOT appear twice (once from rc.Handles, not again from rc.Skills)
	discordCount := strings.Count(md, "handle-discord.md")
	if discordCount != 1 {
		t.Errorf("expected handle-discord.md once in skills section, found %d times", discordCount)
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
