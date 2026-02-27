package shared

import "strings"

// PlatformTokenVar returns the conventional env var name for a platform's bot token.
func PlatformTokenVar(platform string) string {
	switch strings.ToLower(platform) {
	case "discord":
		return "DISCORD_BOT_TOKEN"
	case "slack":
		return "SLACK_BOT_TOKEN"
	case "telegram":
		return "TELEGRAM_BOT_TOKEN"
	default:
		return ""
	}
}
