package nullclaw

import (
	"strings"
	"testing"

	"github.com/mostlydev/clawdapus/internal/driver"
)

func TestBaseImageProvider(t *testing.T) {
	d := &Driver{}

	var _ driver.BaseImageProvider = d

	tag, dockerfile := d.BaseImage()

	if tag != "nullclaw:latest" {
		t.Fatalf("expected tag nullclaw:latest, got %q", tag)
	}
	if !strings.HasPrefix(dockerfile, "FROM ghcr.io/nullclaw/nullclaw:latest") {
		t.Fatal("Dockerfile should alias the official nullclaw image")
	}
	if !strings.Contains(dockerfile, "https://github.com/nullclaw/nullclaw") {
		t.Fatal("Dockerfile should point at the nullclaw upstream source")
	}
}
