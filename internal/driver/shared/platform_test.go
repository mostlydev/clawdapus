package shared

import "testing"

func TestPlatformTokenVar(t *testing.T) {
	tests := []struct{ platform, want string }{
		{"discord", "DISCORD_BOT_TOKEN"},
		{"dingtalk", "DINGTALK_BOT_TOKEN"},
		{"feishu", "FEISHU_BOT_TOKEN"},
		{"line", "LINE_BOT_TOKEN"},
		{"maixcam", "MAIXCAM_BOT_TOKEN"},
		{"matrix", "MATRIX_BOT_TOKEN"},
		{"onebot", "ONEBOT_BOT_TOKEN"},
		{"pico", "PICO_BOT_TOKEN"},
		{"qq", "QQ_BOT_TOKEN"},
		{"slack", "SLACK_BOT_TOKEN"},
		{"telegram", "TELEGRAM_BOT_TOKEN"},
		{"wecom", "WECOM_BOT_TOKEN"},
		{"wecom_app", "WECOM_APP_BOT_TOKEN"},
		{"whatsapp", "WHATSAPP_BOT_TOKEN"},
		{"Discord", "DISCORD_BOT_TOKEN"},
		{"SLACK", "SLACK_BOT_TOKEN"},
		{"unknown", ""},
		{"", ""},
	}
	for _, tt := range tests {
		if got := PlatformTokenVar(tt.platform); got != tt.want {
			t.Errorf("PlatformTokenVar(%q) = %q, want %q", tt.platform, got, tt.want)
		}
	}
}
