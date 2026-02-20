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

CLAW_TYPE generic
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
	if !strings.Contains(text, `LABEL claw.type="generic"`) {
		t.Fatal("missing claw.type label in generated output")
	}
}
