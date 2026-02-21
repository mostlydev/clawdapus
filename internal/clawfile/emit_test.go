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
	if strings.Contains(output, "/etc/cron.d/claw") {
		t.Fatal("INVOKE must not write to /etc/cron.d — it should be a label")
	}
	if !strings.Contains(output, `claw.invoke.0=`) {
		t.Fatal("missing claw.invoke.0 label for INVOKE directive")
	}
	if !strings.Contains(output, "heartbeat") {
		t.Fatal("missing heartbeat invocation payload in label")
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
	if strings.Index(output, "RUN apt-get update") > strings.Index(output, "claw.invoke.0=") {
		t.Fatal("expected generated label lines to be injected after user RUN instructions")
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

func TestEmitHandleLabel(t *testing.T) {
	result, err := Parse(strings.NewReader("FROM alpine\nCLAW_TYPE openclaw\nHANDLE discord\n"))
	if err != nil {
		t.Fatal(err)
	}

	output, err := Emit(result)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(output, `LABEL claw.handle.discord="true"`) {
		t.Errorf("expected claw.handle.discord label in output, got:\n%s", output)
	}
}

func TestEmitMultipleHandleLabels(t *testing.T) {
	result, err := Parse(strings.NewReader("FROM alpine\nCLAW_TYPE openclaw\nHANDLE discord\nHANDLE slack\n"))
	if err != nil {
		t.Fatal(err)
	}

	output, err := Emit(result)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(output, `LABEL claw.handle.discord="true"`) {
		t.Error("expected claw.handle.discord label in output")
	}
	if !strings.Contains(output, `LABEL claw.handle.slack="true"`) {
		t.Error("expected claw.handle.slack label in output")
	}
}

func TestEmitHandleRawDirectiveNotLeaked(t *testing.T) {
	result, err := Parse(strings.NewReader("FROM alpine\nCLAW_TYPE openclaw\nHANDLE discord\n"))
	if err != nil {
		t.Fatal(err)
	}

	output, err := Emit(result)
	if err != nil {
		t.Fatal(err)
	}

	for _, line := range strings.Split(output, "\n") {
		if strings.HasPrefix(line, "HANDLE ") {
			t.Fatalf("raw HANDLE directive leaked into output: %q", line)
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
	idxLabel := strings.LastIndex(output, "claw.invoke.0=")
	if idxFinalRun == -1 || idxLabel == -1 {
		t.Fatalf("expected both final RUN and invoke label in output:\n%s", output)
	}
	if idxLabel < idxFinalRun {
		t.Fatalf("expected generated label lines after final stage instructions:\n%s", output)
	}
	if strings.Contains(output, "/etc/cron.d/claw") {
		t.Fatal("INVOKE must not write to /etc/cron.d — it should be a label")
	}
}

func TestEmitInvokeAsLabels(t *testing.T) {
	input := "FROM alpine\nCLAW_TYPE openclaw\nINVOKE 15 8 * * 1-5 Pre-market synthesis\n"
	parsed, err := Parse(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	output, err := Emit(parsed)
	if err != nil {
		t.Fatal(err)
	}
	// Tab-encoded: schedule\tcommand — strconv.Quote renders the tab as \t
	if !strings.Contains(output, `claw.invoke.0="15 8 * * 1-5\tPre-market synthesis"`) {
		t.Errorf("expected claw.invoke.0 label with tab-encoded value, got:\n%s", output)
	}
	if strings.Contains(output, "/etc/cron.d") {
		t.Error("INVOKE must not write to /etc/cron.d")
	}
}

func TestEmitMultipleInvokeOrdering(t *testing.T) {
	input := "FROM alpine\nCLAW_TYPE openclaw\nINVOKE 15 8 * * 1-5 Pre-market\nINVOKE */30 * * * * Heartbeat\n"
	parsed, err := Parse(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	output, err := Emit(parsed)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, `claw.invoke.0="15 8 * * 1-5\tPre-market"`) {
		t.Errorf("expected claw.invoke.0 for first INVOKE, got:\n%s", output)
	}
	if !strings.Contains(output, `claw.invoke.1="*/30 * * * *\tHeartbeat"`) {
		t.Errorf("expected claw.invoke.1 for second INVOKE, got:\n%s", output)
	}
	if strings.Index(output, "claw.invoke.0=") > strings.Index(output, "claw.invoke.1=") {
		t.Error("expected claw.invoke.0 before claw.invoke.1")
	}
}
