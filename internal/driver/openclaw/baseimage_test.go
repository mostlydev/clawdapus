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
	if !strings.HasPrefix(dockerfile, "FROM node:24.22.0-bookworm-slim@sha256:") {
		t.Fatal("Dockerfile should start with FROM node:24.22.0-bookworm-slim@sha256:")
	}
	if !strings.Contains(dockerfile, "https://registry.npmjs.org/openclaw/-/openclaw-"+UpstreamVersion+".tgz") {
		t.Fatal("Dockerfile should install openclaw")
	}
	if !strings.Contains(dockerfile, "\nUSER root\n") {
		t.Fatal("Dockerfile should explicitly run as root so ~/.openclaw resolves to /root/.openclaw")
	}
	if !strings.Contains(dockerfile, "/usr/local/bin/openclaw-entrypoint.sh") {
		t.Fatal("Dockerfile should install the entrypoint outside /claw")
	}
	if !strings.Contains(dockerfile, `ln -snf "$CLAW_MEMORY_DIR/MEMORY.md" /claw/MEMORY.md`) {
		t.Fatal("Dockerfile should bridge shared memory into /claw/MEMORY.md")
	}
	if !strings.Contains(dockerfile, `ln -snf "$CLAW_MEMORY_DIR/MEMORY.md" /claw/memory.md`) {
		t.Fatal("Dockerfile should bridge shared memory into /claw/memory.md")
	}
	if strings.Contains(dockerfile, `ENTRYPOINT ["/usr/bin/tini", "--", "/claw/entrypoint.sh"]`) {
		t.Fatal("Dockerfile should not place the runtime entrypoint under /claw")
	}
	if !strings.Contains(dockerfile, "ENTRYPOINT") {
		t.Fatal("Dockerfile should have ENTRYPOINT directive")
	}
}
