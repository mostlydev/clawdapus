package openclaw

import (
	"fmt"
	"strings"

	"github.com/mostlydev/clawdapus/internal/driver"
)

// GenerateServiceSkill produces a fallback markdown skill file for a service
// surface, including hostname, ports, and network info.
func GenerateServiceSkill(surface driver.ResolvedSurface) string {
	var b strings.Builder

	b.WriteString(fmt.Sprintf("# %s (service surface)\n\n", surface.Target))
	b.WriteString("## Connection\n")
	b.WriteString(fmt.Sprintf("- **Hostname:** %s\n", surface.Target))
	b.WriteString("- **Network:** claw-internal (pod-internal, no external access)\n")
	if len(surface.Ports) > 0 {
		b.WriteString(fmt.Sprintf("- **Ports:** %s\n", strings.Join(surface.Ports, ", ")))
	}
	b.WriteString("\n## Usage\n")
	b.WriteString("This service is available to you within the pod network.\n")
	b.WriteString(fmt.Sprintf("Use the hostname `%s` to connect.\n", surface.Target))
	b.WriteString("Credentials, if required, are provided via environment variables.\n")

	return b.String()
}
