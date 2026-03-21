package nanobot

import (
	"strings"
	"testing"

	"github.com/mostlydev/clawdapus/internal/driver"
)

func TestBaseImageProvider(t *testing.T) {
	d := &Driver{}

	var _ driver.BaseImageProvider = d

	tag, dockerfile := d.BaseImage()

	if tag != "nanobot:latest" {
		t.Fatalf("expected tag nanobot:latest, got %q", tag)
	}
	if !strings.HasPrefix(dockerfile, "FROM ghcr.io/astral-sh/uv:python3.12-bookworm-slim") {
		t.Fatal("Dockerfile should start from the uv Python base image")
	}
	if !strings.Contains(dockerfile, "uv pip install --system --no-cache nanobot-ai") {
		t.Fatal("Dockerfile should install nanobot-ai")
	}
	if !strings.Contains(dockerfile, `CMD ["gateway"]`) {
		t.Fatal("Dockerfile should run nanobot gateway")
	}
}
