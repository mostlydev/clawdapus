package openclaw

import (
	"fmt"
	"strings"

	"github.com/mostlydev/clawdapus/internal/driver"
)

// GenerateHandleSkill produces a skill file describing this claw's identity and
// membership on the given platform. Helps the agent understand how to present
// itself and navigate its guild/channel topology.
func GenerateHandleSkill(platform string, info *driver.HandleInfo) string {
	var b strings.Builder

	title := strings.ToUpper(platform[:1]) + platform[1:]
	b.WriteString(fmt.Sprintf("# %s Handle\n\n", title))
	b.WriteString(fmt.Sprintf("Your identity on %s. Use this information when sending messages, mentioning yourself, or routing responses.\n\n", title))

	b.WriteString("## Identity\n")
	b.WriteString(fmt.Sprintf("- **ID:** %s\n", info.ID))
	if info.Username != "" {
		b.WriteString(fmt.Sprintf("- **Username:** %s\n", info.Username))
	}
	b.WriteString("\n")

	if len(info.Guilds) > 0 {
		b.WriteString("## Memberships\n\n")
		for _, guild := range info.Guilds {
			guildLine := fmt.Sprintf("### %s", guild.ID)
			if guild.Name != "" {
				guildLine += fmt.Sprintf(" — %s", guild.Name)
			}
			b.WriteString(guildLine + "\n")
			if len(guild.Channels) > 0 {
				b.WriteString("\nChannels:\n")
				for _, ch := range guild.Channels {
					chLine := fmt.Sprintf("- `%s`", ch.ID)
					if ch.Name != "" {
						chLine += fmt.Sprintf(" (#%s)", ch.Name)
					}
					b.WriteString(chLine + "\n")
				}
			}
			b.WriteString("\n")
		}
	}

	b.WriteString("## Usage\n")
	b.WriteString(fmt.Sprintf("- When referring to yourself on %s, use your ID `%s`", title, info.ID))
	if info.Username != "" {
		b.WriteString(fmt.Sprintf(" or username `%s`", info.Username))
	}
	b.WriteString(".\n")
	if len(info.Guilds) > 0 {
		b.WriteString("- Your guild memberships and channel access are listed above.\n")
		b.WriteString("- Other agents in this pod know your ID via the `CLAW_HANDLE_*` environment variables.\n")
	}

	return b.String()
}
