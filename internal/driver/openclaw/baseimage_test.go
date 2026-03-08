package openclaw

import (
	"strings"
	"testing"

	"github.com/mostlydev/clawdapus/internal/driver"
)

func TestBaseImageProvider(t *testing.T) {
	d := &Driver{}

	// Verify it implements the interface.
	var _ driver.BaseImageProvider = d

	tag, dockerfile := d.BaseImage()

	if tag != "openclaw:latest" {
		t.Fatalf("expected tag openclaw:latest, got %q", tag)
	}
	if !strings.HasPrefix(dockerfile, "FROM node:22-slim") {
		t.Fatal("Dockerfile should start with FROM node:22-slim")
	}
	if !strings.Contains(dockerfile, "openclaw.ai/install.sh") {
		t.Fatal("Dockerfile should install openclaw")
	}
	if !strings.Contains(dockerfile, "/usr/local/bin/openclaw-entrypoint.sh") {
		t.Fatal("Dockerfile should install the entrypoint outside /claw")
	}
	if strings.Contains(dockerfile, `ENTRYPOINT ["/usr/bin/tini", "--", "/claw/entrypoint.sh"]`) {
		t.Fatal("Dockerfile should not place the runtime entrypoint under /claw")
	}
	if !strings.Contains(dockerfile, "ENTRYPOINT") {
		t.Fatal("Dockerfile should have ENTRYPOINT directive")
	}
}
