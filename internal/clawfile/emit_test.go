package clawfile

import (
	"strings"
	"testing"
)

func TestEmitProducesValidDockerfile(t *testing.T) {
	parsed, err := Parse(strings.NewReader(testClawfile))
	if err != nil {
		t.Fatal(err)
	}

	output, err := Emit(parsed)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.HasPrefix(output, "FROM node:24-bookworm-slim") {
		t.Fatalf("expected output to start with FROM, got %q", output)
	}
	if !strings.Contains(output, `LABEL claw.type="openclaw"`) {
		t.Fatal("missing claw.type label")
	}
	if !strings.Contains(output, `LABEL claw.agent.file="AGENTS.md"`) {
		t.Fatal("missing claw.agent.file label")
	}
	if !strings.Contains(output, `LABEL claw.model.primary="openrouter/anthropic/claude-sonnet-4"`) {
		t.Fatal("missing claw.model.primary label")
	}
	if !strings.Contains(output, `LABEL claw.surface.0="channel://discord"`) {
		t.Fatal("missing claw.surface.0 label")
	}
	if !strings.Contains(output, `LABEL claw.surface.1="service://fleet-master"`) {
		t.Fatal("missing claw.surface.1 label")
	}
	if !strings.Contains(output, "/etc/cron.d/claw") {
		t.Fatal("missing cron file generation")
	}
	if !strings.Contains(output, "heartbeat") {
		t.Fatal("missing heartbeat invocation")
	}
	if !strings.Contains(output, `LABEL claw.configure.0="openclaw config set agents.defaults.heartbeat.every 30m"`) {
		t.Fatal("missing claw.configure.0 label")
	}
	if !strings.Contains(output, `LABEL claw.configure.1="openclaw config set agents.defaults.heartbeat.target none"`) {
		t.Fatal("missing claw.configure.1 label")
	}
	if !strings.Contains(output, "RUN apt-get update") {
		t.Fatal("missing passthrough RUN instruction")
	}
	if !strings.Contains(output, "WORKDIR /workspace") {
		t.Fatal("missing passthrough WORKDIR instruction")
	}

	for _, rawDirective := range []string{
		"CLAW_TYPE ",
		"AGENT ",
		"MODEL ",
		"CONFIGURE ",
		"INVOKE ",
		"TRACK ",
		"SURFACE ",
		"PRIVILEGE ",
	} {
		for _, line := range strings.Split(output, "\n") {
			if strings.HasPrefix(line, rawDirective) {
				t.Fatalf("raw directive leaked into output: %q", line)
			}
		}
	}
}

func TestEmitIsDeterministic(t *testing.T) {
	parsed, err := Parse(strings.NewReader(testClawfile))
	if err != nil {
		t.Fatal(err)
	}

	a, err := Emit(parsed)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Emit(parsed)
	if err != nil {
		t.Fatal(err)
	}

	if a != b {
		t.Fatal("expected deterministic emit output")
	}
}
