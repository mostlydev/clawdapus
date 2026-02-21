package openclaw

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
	if strings.Contains(out, "## Memberships") {
		t.Error("expected no memberships section when no guilds")
	}
}

func TestGenerateHandleSkillWithUsername(t *testing.T) {
	info := &driver.HandleInfo{ID: "123456789", Username: "crypto-bot"}
	out := GenerateHandleSkill("discord", info)

	if !strings.Contains(out, "crypto-bot") {
		t.Error("expected username in output")
	}
	if !strings.Contains(out, "username `crypto-bot`") {
		t.Error("expected username in usage section")
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
					{ID: "555666777", Name: "crypto-alerts"},
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
	if !strings.Contains(out, "Crypto Ops HQ") {
		t.Error("expected guild name")
	}
	if !strings.Contains(out, "987654321") {
		t.Error("expected channel ID")
	}
	if !strings.Contains(out, "#bot-commands") {
		t.Error("expected channel name with # prefix")
	}
	if !strings.Contains(out, "#crypto-alerts") {
		t.Error("expected second channel name")
	}
}

func TestGenerateHandleSkillGuildWithoutName(t *testing.T) {
	info := &driver.HandleInfo{
		ID: "123",
		Guilds: []driver.GuildInfo{
			{ID: "999"},
		},
	}
	out := GenerateHandleSkill("discord", info)

	if !strings.Contains(out, "999") {
		t.Error("expected guild ID")
	}
	// No em dash when name is empty
	if strings.Contains(out, "999 —") {
		t.Error("expected no em dash when guild name is empty")
	}
}

func TestGenerateHandleSkillChannelWithoutName(t *testing.T) {
	info := &driver.HandleInfo{
		ID: "123",
		Guilds: []driver.GuildInfo{
			{
				ID: "999",
				Channels: []driver.ChannelInfo{
					{ID: "chan-111"},
				},
			},
		},
	}
	out := GenerateHandleSkill("discord", info)

	if !strings.Contains(out, "`chan-111`") {
		t.Error("expected channel ID in backticks")
	}
	// No # prefix when channel name is empty
	if strings.Contains(out, "(#)") {
		t.Error("expected no empty parenthesized name")
	}
}

func TestGenerateHandleSkillSlack(t *testing.T) {
	info := &driver.HandleInfo{ID: "U012345", Username: "bot-user"}
	out := GenerateHandleSkill("slack", info)

	if !strings.Contains(out, "# Slack Handle") {
		t.Error("expected Slack title")
	}
	if !strings.Contains(out, "Slack") {
		t.Error("expected platform name in description")
	}
}

func TestGenerateHandleSkillTelegram(t *testing.T) {
	info := &driver.HandleInfo{ID: "987654321"}
	out := GenerateHandleSkill("telegram", info)

	if !strings.Contains(out, "# Telegram Handle") {
		t.Error("expected Telegram title")
	}
}
