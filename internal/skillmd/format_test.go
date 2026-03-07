package skillmd

import (
	"strings"
	"testing"
)

func TestFormatIncludesFrontMatterAndBody(t *testing.T) {
	out := Format("handle-discord", "Identity details for Discord.", "# Discord Handle")

	if !strings.Contains(out, "---\nname: \"handle-discord\"\ndescription: \"Identity details for Discord.\"\n---") {
		t.Fatalf("expected skill frontmatter, got %q", out)
	}
	if !strings.Contains(out, "# Discord Handle") {
		t.Fatalf("expected markdown body, got %q", out)
	}
}

func TestFormatDefaultsMissingMetadata(t *testing.T) {
	out := Format("", "", "body")

	if !strings.Contains(out, "name: \"unnamed-skill\"") {
		t.Fatalf("expected default name, got %q", out)
	}
	if !strings.Contains(out, "description: \"Generated skill.\"") {
		t.Fatalf("expected default description, got %q", out)
	}
}
