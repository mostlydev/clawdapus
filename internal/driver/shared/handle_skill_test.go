package shared

import (
	"strings"
	"testing"

	"github.com/mostlydev/clawdapus/internal/driver"
)

func TestGenerateHandleSkillIDOnly(t *testing.T) {
	info := &driver.HandleInfo{ID: "123456789"}
	out := GenerateHandleSkill("discord", info)

	if !strings.Contains(out, "# Discord Handle") {
		t.Error("expected title with capitalized platform name")
	}
	if !strings.Contains(out, "123456789") {
		t.Error("expected handle ID")
	}
	if strings.Contains(out, "**Username:**") {
		t.Error("expected no username line when username is empty")
	}
}

func TestGenerateHandleSkillWithGuilds(t *testing.T) {
	info := &driver.HandleInfo{
		ID:       "123456789",
		Username: "crypto-bot",
		Guilds: []driver.GuildInfo{
			{
				ID:   "111222333",
				Name: "Crypto Ops HQ",
				Channels: []driver.ChannelInfo{
					{ID: "987654321", Name: "bot-commands"},
				},
			},
		},
	}
	out := GenerateHandleSkill("discord", info)

	if !strings.Contains(out, "## Memberships") {
		t.Error("expected Memberships section")
	}
	if !strings.Contains(out, "111222333") {
		t.Error("expected guild ID")
	}
	if !strings.Contains(out, "#bot-commands") {
		t.Error("expected channel name with # prefix")
	}
}
