package shared

import "testing"

func TestPlatformTokenVar(t *testing.T) {
	tests := []struct{ platform, want string }{
		{"discord", "DISCORD_BOT_TOKEN"},
		{"slack", "SLACK_BOT_TOKEN"},
		{"telegram", "TELEGRAM_BOT_TOKEN"},
		{"Discord", "DISCORD_BOT_TOKEN"},
		{"SLACK", "SLACK_BOT_TOKEN"},
		{"whatsapp", ""},
		{"unknown", ""},
		{"", ""},
	}
	for _, tt := range tests {
		if got := PlatformTokenVar(tt.platform); got != tt.want {
			t.Errorf("PlatformTokenVar(%q) = %q, want %q", tt.platform, got, tt.want)
		}
	}
}
