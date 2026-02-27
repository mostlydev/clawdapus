package shared

import (
	"fmt"
	"sort"
	"strings"

	"github.com/mostlydev/clawdapus/internal/driver"
)

// GenerateChannelSkill produces a markdown skill file for a channel surface.
// Describes the platform, token env var, routing config, and usage guidance.
func GenerateChannelSkill(surface driver.ResolvedSurface) string {
	var b strings.Builder
	platformTitle := strings.ToUpper(surface.Target[:1]) + surface.Target[1:]

	b.WriteString(fmt.Sprintf("# %s Channel Surface\n\n", platformTitle))
	b.WriteString(fmt.Sprintf("**Platform:** %s\n", platformTitle))
	if tokenVar := PlatformTokenVar(surface.Target); tokenVar != "" {
		b.WriteString(fmt.Sprintf("**Token env var:** `%s`\n", tokenVar))
	}
	b.WriteString("\n")

	if cc := surface.ChannelConfig; cc != nil {
		if len(cc.Guilds) > 0 {
			b.WriteString("## Guild Access\n\n")
			guildIDs := make([]string, 0, len(cc.Guilds))
			for id := range cc.Guilds {
				guildIDs = append(guildIDs, id)
			}
			sort.Strings(guildIDs)
			for _, guildID := range guildIDs {
				g := cc.Guilds[guildID]
				line := fmt.Sprintf("- Guild `%s`", guildID)
				if g.Policy != "" {
					line += fmt.Sprintf(": %s policy", g.Policy)
				}
				if g.RequireMention {
					line += ", mentions required"
				}
				b.WriteString(line + "\n")
			}
			b.WriteString("\n")
		}

		if cc.DM.Enabled || cc.DM.Policy != "" || len(cc.DM.AllowFrom) > 0 {
			b.WriteString("## Direct Messages\n\n")
			line := "- DMs"
			if cc.DM.Enabled {
				line += " enabled"
			}
			if cc.DM.Policy != "" {
				line += fmt.Sprintf(": policy=%s", cc.DM.Policy)
			}
			b.WriteString(line + "\n\n")
		}
	}

	b.WriteString("## Usage\n\n")
	b.WriteString(fmt.Sprintf("Use the %s channel to send messages, receive commands, and interact with users.\n", platformTitle))
	b.WriteString("Messages arrive as agent invocations via your runtime's channel integration.\n")
	if cc := surface.ChannelConfig; cc != nil && (cc.DM.Policy != "" || len(cc.Guilds) > 0) {
		b.WriteString("Only reply to users matching the configured policy.\n")
	}

	return b.String()
}
