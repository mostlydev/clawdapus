package pod

import (
	"strings"
	"testing"
)

const podWithStringHandle = `
x-claw:
  pod: test-pod

services:
  bot:
    image: openclaw:latest
    x-claw:
      agent: ./AGENTS.md
      handles:
        discord: "123456789"
`

const podWithMapHandle = `
x-claw:
  pod: test-pod

services:
  bot:
    image: openclaw:latest
    x-claw:
      agent: ./AGENTS.md
      handles:
        discord:
          id: "123456789"
          username: "crypto-bot"
          guilds:
            - id: "111222333"
              name: "Crypto Ops HQ"
              channels:
                - id: "987654321"
                  name: "bot-commands"
                - id: "555666777"
                  name: "crypto-alerts"
`

const podWithMultiplePlatforms = `
x-claw:
  pod: test-pod

services:
  bot:
    image: openclaw:latest
    x-claw:
      agent: ./AGENTS.md
      handles:
        discord: "111111"
        slack: "U222222"
        telegram: "333333"
`

const podWithNumericHandle = `
x-claw:
  pod: test-pod

services:
  bot:
    image: openclaw:latest
    x-claw:
      agent: ./AGENTS.md
      handles:
        discord: 123456789
`

const podWithNoHandles = `
x-claw:
  pod: test-pod

services:
  bot:
    image: openclaw:latest
    x-claw:
      agent: ./AGENTS.md
`

func TestParseHandlesStringShorthand(t *testing.T) {
	p, err := Parse(strings.NewReader(podWithStringHandle))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	bot := p.Services["bot"]
	if bot == nil || bot.Claw == nil {
		t.Fatal("expected bot service with claw block")
	}

	if len(bot.Claw.Handles) != 1 {
		t.Fatalf("expected 1 handle, got %d", len(bot.Claw.Handles))
	}

	info := bot.Claw.Handles["discord"]
	if info == nil {
		t.Fatal("expected discord handle")
	}
	if info.ID != "123456789" {
		t.Errorf("expected ID '123456789', got %q", info.ID)
	}
	if info.Username != "" {
		t.Errorf("expected empty username for string shorthand, got %q", info.Username)
	}
	if len(info.Guilds) != 0 {
		t.Errorf("expected no guilds for string shorthand, got %d", len(info.Guilds))
	}
}

func TestParseHandlesMapFormFull(t *testing.T) {
	p, err := Parse(strings.NewReader(podWithMapHandle))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	bot := p.Services["bot"]
	info := bot.Claw.Handles["discord"]
	if info == nil {
		t.Fatal("expected discord handle")
	}

	if info.ID != "123456789" {
		t.Errorf("expected ID '123456789', got %q", info.ID)
	}
	if info.Username != "crypto-bot" {
		t.Errorf("expected username 'crypto-bot', got %q", info.Username)
	}
	if len(info.Guilds) != 1 {
		t.Fatalf("expected 1 guild, got %d", len(info.Guilds))
	}

	guild := info.Guilds[0]
	if guild.ID != "111222333" {
		t.Errorf("expected guild ID '111222333', got %q", guild.ID)
	}
	if guild.Name != "Crypto Ops HQ" {
		t.Errorf("expected guild name 'Crypto Ops HQ', got %q", guild.Name)
	}
	if len(guild.Channels) != 2 {
		t.Fatalf("expected 2 channels, got %d", len(guild.Channels))
	}

	if guild.Channels[0].ID != "987654321" {
		t.Errorf("expected first channel ID '987654321', got %q", guild.Channels[0].ID)
	}
	if guild.Channels[0].Name != "bot-commands" {
		t.Errorf("expected first channel name 'bot-commands', got %q", guild.Channels[0].Name)
	}
	if guild.Channels[1].ID != "555666777" {
		t.Errorf("expected second channel ID '555666777', got %q", guild.Channels[1].ID)
	}
	if guild.Channels[1].Name != "crypto-alerts" {
		t.Errorf("expected second channel name 'crypto-alerts', got %q", guild.Channels[1].Name)
	}
}

func TestParseHandlesMultiplePlatforms(t *testing.T) {
	p, err := Parse(strings.NewReader(podWithMultiplePlatforms))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	bot := p.Services["bot"]
	if len(bot.Claw.Handles) != 3 {
		t.Fatalf("expected 3 handles, got %d", len(bot.Claw.Handles))
	}

	if bot.Claw.Handles["discord"].ID != "111111" {
		t.Errorf("unexpected discord ID: %q", bot.Claw.Handles["discord"].ID)
	}
	if bot.Claw.Handles["slack"].ID != "U222222" {
		t.Errorf("unexpected slack ID: %q", bot.Claw.Handles["slack"].ID)
	}
	if bot.Claw.Handles["telegram"].ID != "333333" {
		t.Errorf("unexpected telegram ID: %q", bot.Claw.Handles["telegram"].ID)
	}
}

func TestParseHandlesNumericIDCoerced(t *testing.T) {
	p, err := Parse(strings.NewReader(podWithNumericHandle))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	bot := p.Services["bot"]
	info := bot.Claw.Handles["discord"]
	if info == nil {
		t.Fatal("expected discord handle")
	}
	if info.ID != "123456789" {
		t.Errorf("expected numeric ID coerced to string '123456789', got %q", info.ID)
	}
}

func TestParseHandlesAbsentMeansNil(t *testing.T) {
	p, err := Parse(strings.NewReader(podWithNoHandles))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	bot := p.Services["bot"]
	if bot.Claw.Handles != nil {
		t.Errorf("expected nil handles when not declared, got %v", bot.Claw.Handles)
	}
}

func TestParseHandlesMapFormIDOnly(t *testing.T) {
	yaml := `
x-claw:
  pod: test-pod
services:
  bot:
    image: openclaw:latest
    x-claw:
      agent: ./AGENTS.md
      handles:
        slack:
          id: "U012345"
`
	p, err := Parse(strings.NewReader(yaml))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	info := p.Services["bot"].Claw.Handles["slack"]
	if info == nil {
		t.Fatal("expected slack handle")
	}
	if info.ID != "U012345" {
		t.Errorf("expected ID 'U012345', got %q", info.ID)
	}
	if info.Username != "" {
		t.Errorf("expected empty username, got %q", info.Username)
	}
}

func TestParseHandlesMapFormGuildChannelStringShorthand(t *testing.T) {
	yaml := `
x-claw:
  pod: test-pod
services:
  bot:
    image: openclaw:latest
    x-claw:
      agent: ./AGENTS.md
      handles:
        discord:
          id: "123"
          guilds:
            - id: "999"
              channels:
                - "chan-111"
                - "chan-222"
`
	p, err := Parse(strings.NewReader(yaml))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	info := p.Services["bot"].Claw.Handles["discord"]
	if info == nil {
		t.Fatal("expected discord handle")
	}
	guild := info.Guilds[0]
	if len(guild.Channels) != 2 {
		t.Fatalf("expected 2 channels from string shorthand, got %d", len(guild.Channels))
	}
	if guild.Channels[0].ID != "chan-111" {
		t.Errorf("expected channel ID 'chan-111', got %q", guild.Channels[0].ID)
	}
	if guild.Channels[0].Name != "" {
		t.Errorf("expected empty channel name for string shorthand, got %q", guild.Channels[0].Name)
	}
}

func TestParseHandlesErrorOnEmptyMapID(t *testing.T) {
	yaml := `
x-claw:
  pod: test-pod
services:
  bot:
    image: openclaw:latest
    x-claw:
      agent: ./AGENTS.md
      handles:
        discord:
          username: "no-id-here"
`
	_, err := Parse(strings.NewReader(yaml))
	if err == nil {
		t.Fatal("expected error for handle map with no id")
	}
	if !strings.Contains(err.Error(), "id") {
		t.Errorf("expected error mentioning 'id', got: %v", err)
	}
}

func TestParseHandlesErrorOnInvalidType(t *testing.T) {
	yaml := `
x-claw:
  pod: test-pod
services:
  bot:
    image: openclaw:latest
    x-claw:
      agent: ./AGENTS.md
      handles:
        discord:
          - "this is a list not a map"
`
	_, err := Parse(strings.NewReader(yaml))
	if err == nil {
		t.Fatal("expected error for invalid handle value type")
	}
}

func TestParseHandlesPlatformKeyNormalized(t *testing.T) {
	yaml := `
x-claw:
  pod: test-pod
services:
  bot:
    image: openclaw:latest
    x-claw:
      agent: ./AGENTS.md
      handles:
        Discord: "123456789"
`
	p, err := Parse(strings.NewReader(yaml))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	bot := p.Services["bot"]
	// Key should be lowercased "discord", not "Discord"
	if _, ok := bot.Claw.Handles["Discord"]; ok {
		t.Error("expected platform key to be normalized to lowercase, found 'Discord'")
	}
	info := bot.Claw.Handles["discord"]
	if info == nil {
		t.Fatal("expected normalized platform key 'discord'")
	}
	if info.ID != "123456789" {
		t.Errorf("expected ID '123456789', got %q", info.ID)
	}
}

func TestParseHandlesGuildMissingIDErrors(t *testing.T) {
	yaml := `
x-claw:
  pod: test-pod
services:
  bot:
    image: openclaw:latest
    x-claw:
      agent: ./AGENTS.md
      handles:
        discord:
          id: "123"
          guilds:
            - name: "Missing ID Guild"
`
	_, err := Parse(strings.NewReader(yaml))
	if err == nil {
		t.Fatal("expected error for guild with no id")
	}
	if !strings.Contains(err.Error(), "id") {
		t.Errorf("expected error mentioning 'id', got: %v", err)
	}
}
