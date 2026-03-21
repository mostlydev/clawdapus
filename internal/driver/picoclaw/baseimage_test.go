package picoclaw

import (
	"strings"
	"testing"

	"github.com/mostlydev/clawdapus/internal/driver"
)

func TestBaseImageProvider(t *testing.T) {
	d := &Driver{}

	var _ driver.BaseImageProvider = d

	tag, dockerfile := d.BaseImage()

	if tag != "picoclaw:latest" {
		t.Fatalf("expected tag picoclaw:latest, got %q", tag)
	}
	if !strings.HasPrefix(dockerfile, "FROM docker.io/sipeed/picoclaw:latest") {
		t.Fatal("Dockerfile should alias the official picoclaw image")
	}
	if !strings.Contains(dockerfile, "https://github.com/sipeed/picoclaw") {
		t.Fatal("Dockerfile should point at the picoclaw upstream source")
	}
}
