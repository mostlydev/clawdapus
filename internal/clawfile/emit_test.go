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
	if !strings.Contains(output, "claw-user heartbeat") {
		t.Fatal("expected INVOKE cron line to use PRIVILEGE runtime user")
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
	if !strings.Contains(output, `LABEL claw.skill.0="./skills/custom-workflow.md"`) {
		t.Fatal("missing claw.skill.0 label")
	}
	if !strings.Contains(output, `LABEL claw.skill.1="./skills/team-conventions.md"`) {
		t.Fatal("missing claw.skill.1 label")
	}
	if !strings.Contains(output, "RUN apt-get update") {
		t.Fatal("missing passthrough RUN instruction")
	}
	if !strings.Contains(output, "WORKDIR /workspace") {
		t.Fatal("missing passthrough WORKDIR instruction")
	}
	if strings.Index(output, "RUN apt-get update") > strings.Index(output, "/etc/cron.d/claw") {
		t.Fatal("expected generated infra lines to be injected after user RUN instructions")
	}

	for _, rawDirective := range []string{
		"CLAW_TYPE ",
		"AGENT ",
		"MODEL ",
		"CONFIGURE ",
		"INVOKE ",
		"TRACK ",
		"SURFACE ",
		"SKILL ",
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

func TestEmitInjectsGeneratedLinesAtEndOfFinalStage(t *testing.T) {
	input := `FROM alpine:3.20 AS base
RUN echo base

FROM alpine:3.20
CLAW_TYPE openclaw
AGENT AGENTS.md
INVOKE */5 * * * * heartbeat
PRIVILEGE runtime claw-user
RUN echo final
`
	parsed, err := Parse(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}

	output, err := Emit(parsed)
	if err != nil {
		t.Fatal(err)
	}

	idxFinalRun := strings.LastIndex(output, "RUN echo final")
	idxCron := strings.LastIndex(output, "/etc/cron.d/claw")
	if idxFinalRun == -1 || idxCron == -1 {
		t.Fatalf("expected both final RUN and cron generation in output:\n%s", output)
	}
	if idxCron < idxFinalRun {
		t.Fatalf("expected generated infra lines after final stage instructions:\n%s", output)
	}
}
