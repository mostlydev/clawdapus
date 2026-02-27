package build

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerateWritesDockerfile(t *testing.T) {
	dir := t.TempDir()
	clawfilePath := filepath.Join(dir, "Clawfile")

	input := `FROM alpine:latest

CLAW_TYPE openclaw
AGENT CONTRACT.md

RUN echo hello
`
	if err := os.WriteFile(clawfilePath, []byte(input), 0o644); err != nil {
		t.Fatal(err)
	}

	generatedPath, err := Generate(clawfilePath)
	if err != nil {
		t.Fatal(err)
	}

	expectedPath := filepath.Join(dir, "Dockerfile.generated")
	if generatedPath != expectedPath {
		t.Fatalf("expected generated path %s, got %s", expectedPath, generatedPath)
	}

	content, err := os.ReadFile(generatedPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(content) == 0 {
		t.Fatal("expected generated Dockerfile to be non-empty")
	}

	text := string(content)
	if !strings.Contains(text, "FROM alpine:latest") {
		t.Fatal("missing FROM instruction in generated output")
	}
	if !strings.Contains(text, `LABEL claw.type="openclaw"`) {
		t.Fatal("missing claw.type label in generated output")
	}
}

func TestGenerateRejectsUnknownClawType(t *testing.T) {
	dir := t.TempDir()
	clawfilePath := filepath.Join(dir, "Clawfile")

	input := `FROM alpine:latest

CLAW_TYPE unknown-runner
AGENT CONTRACT.md
`
	if err := os.WriteFile(clawfilePath, []byte(input), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := Generate(clawfilePath)
	if err == nil {
		t.Fatal("expected Generate to fail for unknown CLAW_TYPE")
	}
	if !strings.Contains(err.Error(), "unknown CLAW_TYPE") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestGenerateAcceptsMicroclawType(t *testing.T) {
	dir := t.TempDir()
	clawfilePath := filepath.Join(dir, "Clawfile")

	input := `FROM alpine:latest

CLAW_TYPE microclaw
AGENT AGENTS.md
MODEL primary anthropic/claude-sonnet-4
`
	if err := os.WriteFile(clawfilePath, []byte(input), 0o644); err != nil {
		t.Fatal(err)
	}

	generatedPath, err := Generate(clawfilePath)
	if err != nil {
		t.Fatalf("expected microclaw CLAW_TYPE to be accepted, got error: %v", err)
	}

	content, err := os.ReadFile(generatedPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), `LABEL claw.type="microclaw"`) {
		t.Fatal("missing claw.type=microclaw label in generated output")
	}
}
