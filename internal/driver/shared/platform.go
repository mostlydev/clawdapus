package shared

import "strings"

// PlatformTokenVar returns the conventional env var name for a platform's bot token.
func PlatformTokenVar(platform string) string {
	switch strings.ToLower(platform) {
	case "discord":
		return "DISCORD_BOT_TOKEN"
	case "dingtalk":
		return "DINGTALK_BOT_TOKEN"
	case "feishu":
		return "FEISHU_BOT_TOKEN"
	case "line":
		return "LINE_BOT_TOKEN"
	case "maixcam":
		return "MAIXCAM_BOT_TOKEN"
	case "matrix":
		return "MATRIX_BOT_TOKEN"
	case "onebot":
		return "ONEBOT_BOT_TOKEN"
	case "pico":
		return "PICO_BOT_TOKEN"
	case "qq":
		return "QQ_BOT_TOKEN"
	case "slack":
		return "SLACK_BOT_TOKEN"
	case "telegram":
		return "TELEGRAM_BOT_TOKEN"
	case "wecom":
		return "WECOM_BOT_TOKEN"
	case "wecom_app":
		return "WECOM_APP_BOT_TOKEN"
	case "whatsapp":
		return "WHATSAPP_BOT_TOKEN"
	default:
		return ""
	}
}
