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

SKILL ./skills/custom-workflow.md
SKILL ./skills/team-conventions.md

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
	if len(result.Config.Skills) != 2 {
		t.Fatalf("expected 2 skills, got %d", len(result.Config.Skills))
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

func TestParseRequiresClawType(t *testing.T) {
	_, err := Parse(strings.NewReader("FROM alpine\nAGENT AGENTS.md\n"))
	if err == nil {
		t.Fatal("expected missing CLAW_TYPE to fail")
	}
	if !strings.Contains(err.Error(), "missing required CLAW_TYPE directive") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseRejectsInvalidSurfaceAccessMode(t *testing.T) {
	_, err := Parse(strings.NewReader("FROM alpine\nCLAW_TYPE openclaw\nSURFACE volume://cache read-only-ish\n"))
	if err == nil {
		t.Fatal("expected invalid surface access mode to fail")
	}
	if !strings.Contains(err.Error(), "invalid") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseRejectsSurfaceAccessModeForChannel(t *testing.T) {
	_, err := Parse(strings.NewReader("FROM alpine\nCLAW_TYPE openclaw\nSURFACE channel://discord read-only\n"))
	if err == nil {
		t.Fatal("expected channel surface with access mode to fail")
	}
}

func TestParseExtractsSkills(t *testing.T) {
	input := `FROM alpine
CLAW_TYPE openclaw
SKILL ./skills/custom-workflow.md
SKILL ./skills/team-conventions.md
`
	result, err := Parse(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Config.Skills) != 2 {
		t.Fatalf("expected 2 skills, got %d", len(result.Config.Skills))
	}
	if result.Config.Skills[0] != "./skills/custom-workflow.md" {
		t.Errorf("expected first skill path, got %q", result.Config.Skills[0])
	}
	if result.Config.Skills[1] != "./skills/team-conventions.md" {
		t.Errorf("expected second skill path, got %q", result.Config.Skills[1])
	}
}

func TestParseRejectsEmptySkill(t *testing.T) {
	_, err := Parse(strings.NewReader("FROM alpine\nCLAW_TYPE openclaw\nSKILL\n"))
	if err == nil {
		t.Fatal("expected SKILL with no argument to fail")
	}
}

func TestParseRejectsInvalidInvokeSchedule(t *testing.T) {
	_, err := Parse(strings.NewReader("FROM alpine\nCLAW_TYPE openclaw\nINVOKE 99 * * * * heartbeat\n"))
	if err == nil {
		t.Fatal("expected invalid cron schedule to fail")
	}
	if !strings.Contains(err.Error(), "invalid minute field") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseHandleDirective(t *testing.T) {
	result, err := Parse(strings.NewReader("FROM alpine\nCLAW_TYPE openclaw\nHANDLE discord\n"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Config.Handles) != 1 {
		t.Fatalf("expected 1 handle, got %d", len(result.Config.Handles))
	}
	if result.Config.Handles[0] != "discord" {
		t.Errorf("expected handle 'discord', got %q", result.Config.Handles[0])
	}
}

func TestParseHandleLowercases(t *testing.T) {
	result, err := Parse(strings.NewReader("FROM alpine\nCLAW_TYPE openclaw\nHANDLE Discord\n"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Config.Handles[0] != "discord" {
		t.Errorf("expected lowercased 'discord', got %q", result.Config.Handles[0])
	}
}

func TestParseMultipleHandleDirectives(t *testing.T) {
	result, err := Parse(strings.NewReader("FROM alpine\nCLAW_TYPE openclaw\nHANDLE discord\nHANDLE slack\n"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Config.Handles) != 2 {
		t.Fatalf("expected 2 handles, got %d: %v", len(result.Config.Handles), result.Config.Handles)
	}
	if result.Config.Handles[0] != "discord" {
		t.Errorf("expected first handle 'discord', got %q", result.Config.Handles[0])
	}
	if result.Config.Handles[1] != "slack" {
		t.Errorf("expected second handle 'slack', got %q", result.Config.Handles[1])
	}
}

func TestParseHandleRequiresArgument(t *testing.T) {
	_, err := Parse(strings.NewReader("FROM alpine\nCLAW_TYPE openclaw\nHANDLE\n"))
	if err == nil {
		t.Fatal("expected HANDLE with no argument to fail")
	}
	if !strings.Contains(err.Error(), "HANDLE requires exactly one platform name") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseHandleRejectsMultipleTokens(t *testing.T) {
	_, err := Parse(strings.NewReader("FROM alpine\nCLAW_TYPE openclaw\nHANDLE discord extra\n"))
	if err == nil {
		t.Fatal("expected HANDLE with extra tokens to fail")
	}
	if !strings.Contains(err.Error(), "HANDLE requires exactly one platform name") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseHandleDuplicateErrors(t *testing.T) {
	_, err := Parse(strings.NewReader("FROM alpine\nCLAW_TYPE openclaw\nHANDLE discord\nHANDLE discord\n"))
	if err == nil {
		t.Fatal("expected duplicate HANDLE to fail")
	}
	if !strings.Contains(err.Error(), "duplicate HANDLE") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseHandleNotPresentMeansEmpty(t *testing.T) {
	result, err := Parse(strings.NewReader("FROM alpine\nCLAW_TYPE openclaw\n"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Config.Handles == nil {
		t.Fatal("expected non-nil Handles slice (empty, not nil)")
	}
	if len(result.Config.Handles) != 0 {
		t.Errorf("expected 0 handles, got %d", len(result.Config.Handles))
	}
}
