package hermes

import (
	"strings"
	"testing"

	"github.com/mostlydev/clawdapus/internal/driver"
)

func TestBaseImageProvider(t *testing.T) {
	d := &Driver{}

	var _ driver.BaseImageProvider = d

	tag, dockerfile := d.BaseImage()

	if tag != "hermes:latest" {
		t.Fatalf("expected tag hermes:latest, got %q", tag)
	}
	if !strings.HasPrefix(dockerfile, "FROM ghcr.io/astral-sh/uv:python3.11-bookworm-slim") {
		t.Fatal("Dockerfile should start from the uv Python base image")
	}
	if !strings.Contains(dockerfile, "https://github.com/NousResearch/hermes-agent.git") {
		t.Fatal("Dockerfile should clone the correct Hermes upstream repository")
	}
	if !strings.Contains(dockerfile, `"/opt/hermes-agent[messaging,cron]"`) {
		t.Fatal("Dockerfile should install Hermes with messaging and cron extras")
	}
	if !strings.Contains(dockerfile, `CMD ["gateway", "start"]`) {
		t.Fatal("Dockerfile should start the Hermes gateway")
	}
}
