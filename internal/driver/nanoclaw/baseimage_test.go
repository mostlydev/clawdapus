package nanoclaw

import (
	"strings"
	"testing"

	"github.com/mostlydev/clawdapus/internal/driver"
)

func TestBaseImageProvider(t *testing.T) {
	d := &Driver{}

	var _ driver.BaseImageProvider = d

	tag, dockerfile := d.BaseImage()

	if tag != "nanoclaw-orchestrator:latest" {
		t.Fatalf("expected tag nanoclaw-orchestrator:latest, got %q", tag)
	}
	if !strings.HasPrefix(dockerfile, "FROM node:22-bookworm-slim AS builder") {
		t.Fatal("Dockerfile should use a builder stage")
	}
	if !strings.Contains(dockerfile, "https://github.com/qwibitai/nanoclaw.git") {
		t.Fatal("Dockerfile should clone the nanoclaw upstream repository")
	}
	if !strings.Contains(dockerfile, "COPY --from=docker:27-cli /usr/local/bin/docker /usr/local/bin/docker") {
		t.Fatal("Dockerfile should copy in the Docker CLI")
	}
	if !strings.Contains(dockerfile, "npm install -g @anthropic-ai/claude-code") {
		t.Fatal("Dockerfile should install the Claude Code CLI")
	}
	if !strings.Contains(dockerfile, `ENTRYPOINT ["/usr/bin/tini", "--", "node", "/workspace/dist/index.js"]`) {
		t.Fatal("Dockerfile should start the nanoclaw orchestrator")
	}
}
