package clawfile

import (
	"strings"
	"testing"
)

const testClawfile = `FROM node:24-bookworm-slim

CLAW_TYPE openclaw
AGENT AGENTS.md

MODEL primary openrouter/anthropic/claude-sonnet-4

CONFIGURE openclaw config set agents.defaults.heartbeat.every 30m
CONFIGURE openclaw config set agents.defaults.heartbeat.target none

INVOKE 0,30 * * * * heartbeat

TRACK apt pip npm

SURFACE channel://discord
SURFACE service://fleet-master

PRIVILEGE worker root
PRIVILEGE runtime claw-user

RUN apt-get update && apt-get install -y bash ca-certificates cron curl git jq tini
RUN npm install -g openclaw@2026.2.9
WORKDIR /workspace
`

func TestParseExtractsCoreConfig(t *testing.T) {
	result, err := Parse(strings.NewReader(testClawfile))
	if err != nil {
		t.Fatal(err)
	}

	if result.Config.ClawType != "openclaw" {
		t.Fatalf("expected openclaw claw type, got %q", result.Config.ClawType)
	}
	if result.Config.Agent != "AGENTS.md" {
		t.Fatalf("expected AGENTS.md, got %q", result.Config.Agent)
	}

	model, ok := result.Config.Models["primary"]
	if !ok {
		t.Fatal("expected primary model to be parsed")
	}
	if model != "openrouter/anthropic/claude-sonnet-4" {
		t.Fatalf("unexpected model value: %q", model)
	}
}

func TestParseExtractsLists(t *testing.T) {
	result, err := Parse(strings.NewReader(testClawfile))
	if err != nil {
		t.Fatal(err)
	}

	if len(result.Config.Configures) != 2 {
		t.Fatalf("expected 2 configures, got %d", len(result.Config.Configures))
	}
	if len(result.Config.Invocations) != 1 {
		t.Fatalf("expected 1 invocation, got %d", len(result.Config.Invocations))
	}
	if len(result.Config.Tracks) != 3 {
		t.Fatalf("expected 3 tracks, got %d", len(result.Config.Tracks))
	}
	if len(result.Config.Surfaces) != 2 {
		t.Fatalf("expected 2 surfaces, got %d", len(result.Config.Surfaces))
	}
	if len(result.Config.Privileges) != 2 {
		t.Fatalf("expected 2 privileges, got %d", len(result.Config.Privileges))
	}
}

func TestParsePreservesDockerInstructions(t *testing.T) {
	result, err := Parse(strings.NewReader(testClawfile))
	if err != nil {
		t.Fatal(err)
	}

	if len(result.DockerNodes) != 4 {
		t.Fatalf("expected 4 docker instructions, got %d", len(result.DockerNodes))
	}
}

func TestParseFailsUnknownClawDirective(t *testing.T) {
	_, err := Parse(strings.NewReader("FROM alpine\nCLAW_UNKNOWN foo\nRUN echo hi\n"))
	if err == nil {
		t.Fatal("expected parse to fail for unknown CLAW_* directive")
	}
	if !strings.Contains(err.Error(), "unknown Claw directive") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseFailsDuplicateSingleton(t *testing.T) {
	_, err := Parse(strings.NewReader("FROM alpine\nCLAW_TYPE one\nCLAW_TYPE two\n"))
	if err == nil {
		t.Fatal("expected duplicate CLAW_TYPE to fail")
	}
}
