package skillmd

import (
	"fmt"
	"strconv"
	"strings"
)

// Format wraps markdown body content in the SKILL.md frontmatter expected by
// directory-style skill loaders.
func Format(name, description, body string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "unnamed-skill"
	}

	description = strings.TrimSpace(description)
	if description == "" {
		description = "Generated skill."
	}

	body = strings.TrimSpace(body)

	var b strings.Builder
	b.WriteString("---\n")
	b.WriteString(fmt.Sprintf("name: %s\n", strconv.Quote(name)))
	b.WriteString(fmt.Sprintf("description: %s\n", strconv.Quote(description)))
	b.WriteString("---\n")
	if body != "" {
		b.WriteString("\n")
		b.WriteString(body)
		b.WriteString("\n")
	}

	return b.String()
}
