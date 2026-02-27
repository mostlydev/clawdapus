package shared

import (
	"strings"
	"testing"

	"github.com/mostlydev/clawdapus/internal/driver"
)

func TestGenerateChannelSkillSimple(t *testing.T) {
	surface := driver.ResolvedSurface{
		Scheme: "channel",
		Target: "discord",
	}
	skill := GenerateChannelSkill(surface)
	if !strings.Contains(skill, "Discord") {
		t.Error("expected platform name Discord in skill")
	}
	if !strings.Contains(skill, "DISCORD_BOT_TOKEN") {
		t.Error("expected token env var hint in skill")
	}
	if !strings.Contains(skill, "Usage") {
		t.Error("expected Usage section in skill")
	}
	// Must not reference OpenClaw specifically
	if strings.Contains(skill, "OpenClaw") {
		t.Error("shared channel skill must not reference OpenClaw")
	}
}

func TestGenerateChannelSkillWithDM(t *testing.T) {
	surface := driver.ResolvedSurface{
		Scheme: "channel",
		Target: "discord",
		ChannelConfig: &driver.ChannelConfig{
			DM: driver.ChannelDMConfig{
				Enabled: true,
				Policy:  "allowlist",
			},
		},
	}
	skill := GenerateChannelSkill(surface)
	if !strings.Contains(skill, "Direct Messages") {
		t.Error("expected DM section when DM config present")
	}
	if !strings.Contains(skill, "allowlist") {
		t.Error("expected DM policy in skill")
	}
}

func TestGenerateChannelSkillWithGuilds(t *testing.T) {
	surface := driver.ResolvedSurface{
		Scheme: "channel",
		Target: "discord",
		ChannelConfig: &driver.ChannelConfig{
			Guilds: map[string]driver.ChannelGuildConfig{
				"1465489501551067136": {
					Policy:         "allowlist",
					RequireMention: true,
				},
			},
		},
	}
	skill := GenerateChannelSkill(surface)
	if !strings.Contains(skill, "Guild Access") {
		t.Error("expected Guild Access section when guilds present")
	}
	if !strings.Contains(skill, "1465489501551067136") {
		t.Error("expected guild ID in skill")
	}
}

func TestGenerateChannelSkillSlack(t *testing.T) {
	surface := driver.ResolvedSurface{Scheme: "channel", Target: "slack"}
	skill := GenerateChannelSkill(surface)
	if !strings.Contains(skill, "Slack") {
		t.Error("expected Slack in skill")
	}
	if !strings.Contains(skill, "SLACK_BOT_TOKEN") {
		t.Error("expected SLACK_BOT_TOKEN hint")
	}
}

func TestGenerateChannelSkillEmptyTargetDoesNotPanic(t *testing.T) {
	surface := driver.ResolvedSurface{Scheme: "channel", Target: ""}
	skill := GenerateChannelSkill(surface)
	if !strings.Contains(skill, "Unknown Channel Surface") {
		t.Errorf("expected fallback platform title, got: %s", skill)
	}
}
