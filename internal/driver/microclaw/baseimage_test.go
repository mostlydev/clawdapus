package microclaw

import (
	"strings"
	"testing"

	"github.com/mostlydev/clawdapus/internal/driver"
)

func TestBaseImageProvider(t *testing.T) {
	d := &Driver{}

	var _ driver.BaseImageProvider = d

	tag, dockerfile := d.BaseImage()

	if tag != "microclaw:latest" {
		t.Fatalf("expected tag microclaw:latest, got %q", tag)
	}
	if !strings.HasPrefix(dockerfile, "FROM ghcr.io/microclaw/microclaw:latest") {
		t.Fatal("Dockerfile should start from the official microclaw image")
	}
	if !strings.Contains(dockerfile, "apt-get install -y --no-install-recommends procps") {
		t.Fatal("Dockerfile should install procps for the pgrep-based healthcheck")
	}
	if !strings.Contains(dockerfile, "mkdir -p /app/config /claw-data") {
		t.Fatal("Dockerfile should create the config mount parent path")
	}
}
