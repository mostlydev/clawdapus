package build

import (
	"os"
	"path/filepath"
	"reflect"
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

func TestGenerateAcceptsNanobotType(t *testing.T) {
	dir := t.TempDir()
	clawfilePath := filepath.Join(dir, "Clawfile")

	input := `FROM alpine:latest

CLAW_TYPE nanobot
AGENT AGENTS.md
MODEL primary openrouter/anthropic/claude-sonnet-4
`
	if err := os.WriteFile(clawfilePath, []byte(input), 0o644); err != nil {
		t.Fatal(err)
	}

	generatedPath, err := Generate(clawfilePath)
	if err != nil {
		t.Fatalf("expected nanobot CLAW_TYPE to be accepted, got error: %v", err)
	}

	content, err := os.ReadFile(generatedPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), `LABEL claw.type="nanobot"`) {
		t.Fatal("missing claw.type=nanobot label in generated output")
	}
}

func TestGenerateAcceptsPicoclawType(t *testing.T) {
	dir := t.TempDir()
	clawfilePath := filepath.Join(dir, "Clawfile")

	input := `FROM alpine:latest

CLAW_TYPE picoclaw
AGENT AGENTS.md
MODEL primary openrouter/anthropic/claude-sonnet-4
`
	if err := os.WriteFile(clawfilePath, []byte(input), 0o644); err != nil {
		t.Fatal(err)
	}

	generatedPath, err := Generate(clawfilePath)
	if err != nil {
		t.Fatalf("expected picoclaw CLAW_TYPE to be accepted, got error: %v", err)
	}

	content, err := os.ReadFile(generatedPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), `LABEL claw.type="picoclaw"`) {
		t.Fatal("missing claw.type=picoclaw label in generated output")
	}
}

func TestGenerateAcceptsHermesType(t *testing.T) {
	dir := t.TempDir()
	clawfilePath := filepath.Join(dir, "Clawfile")

	input := `FROM alpine:latest

CLAW_TYPE hermes
AGENT AGENTS.md
MODEL primary openrouter/anthropic/claude-sonnet-4
`
	if err := os.WriteFile(clawfilePath, []byte(input), 0o644); err != nil {
		t.Fatal(err)
	}

	generatedPath, err := Generate(clawfilePath)
	if err != nil {
		t.Fatalf("expected hermes CLAW_TYPE to be accepted, got error: %v", err)
	}

	content, err := os.ReadFile(generatedPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), `LABEL claw.type="hermes"`) {
		t.Fatal("missing claw.type=hermes label in generated output")
	}
}

func TestGenerateBuildsMatchingDriverBaseImageWhenMissing(t *testing.T) {
	dir := t.TempDir()
	clawfilePath := filepath.Join(dir, "Clawfile")

	input := `FROM hermes:latest

CLAW_TYPE hermes
AGENT AGENTS.md
`
	if err := os.WriteFile(clawfilePath, []byte(input), 0o644); err != nil {
		t.Fatal(err)
	}

	generatedPath, args, baseDockerfile := runGenerateWithFakeDocker(t, clawfilePath)

	if generatedPath != filepath.Join(dir, "Dockerfile.generated") {
		t.Fatalf("expected generated Dockerfile path, got %q", generatedPath)
	}
	if len(args) != 4 {
		t.Fatalf("expected docker build invocation with 4 args, got %v", args)
	}
	if !reflect.DeepEqual(args[:3], []string{"build", "-t", "hermes:latest"}) {
		t.Fatalf("unexpected docker build args prefix: %v", args)
	}
	if baseDockerfile == "" {
		t.Fatal("expected base image Dockerfile content to be captured")
	}
	if !strings.Contains(baseDockerfile, "https://github.com/NousResearch/hermes-agent.git") {
		t.Fatal("expected captured base Dockerfile to use the Hermes upstream repository")
	}
}

func TestGenerateSkipsAutoBuildWhenFROMDoesNotMatchDeclaredBaseImage(t *testing.T) {
	dir := t.TempDir()
	clawfilePath := filepath.Join(dir, "Clawfile")

	input := `FROM docker.io/sipeed/picoclaw:latest

CLAW_TYPE picoclaw
AGENT AGENTS.md
`
	if err := os.WriteFile(clawfilePath, []byte(input), 0o644); err != nil {
		t.Fatal(err)
	}

	generatedPath, args, baseDockerfile := runGenerateWithFakeDocker(t, clawfilePath)

	if generatedPath != filepath.Join(dir, "Dockerfile.generated") {
		t.Fatalf("expected generated Dockerfile path, got %q", generatedPath)
	}
	if len(args) != 0 {
		t.Fatalf("expected no auto-build when FROM does not match provider tag, got %v", args)
	}
	if baseDockerfile != "" {
		t.Fatal("expected no base image Dockerfile capture when auto-build is skipped")
	}
}

func TestBuildFromGeneratedUsesExplicitContext(t *testing.T) {
	dir := t.TempDir()
	generatedPath := filepath.Join(dir, "agents", "shared", "Dockerfile.generated")
	contextDir := filepath.Join(dir, "repo-root")
	if err := os.MkdirAll(filepath.Dir(generatedPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(contextDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(generatedPath, []byte("FROM scratch\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	gotArgs := runBuildWithFakeDocker(t, generatedPath, "example:latest", contextDir)
	want := []string{"build", "-f", generatedPath, "-t", "example:latest", contextDir}
	if !reflect.DeepEqual(gotArgs, want) {
		t.Fatalf("expected docker args %v, got %v", want, gotArgs)
	}
}

func TestBuildFromGeneratedDefaultsContextToGeneratedDir(t *testing.T) {
	dir := t.TempDir()
	generatedPath := filepath.Join(dir, "agents", "shared", "Dockerfile.generated")
	if err := os.MkdirAll(filepath.Dir(generatedPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(generatedPath, []byte("FROM scratch\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	gotArgs := runBuildWithFakeDocker(t, generatedPath, "", "")
	want := []string{"build", "-f", generatedPath, filepath.Dir(generatedPath)}
	if !reflect.DeepEqual(gotArgs, want) {
		t.Fatalf("expected docker args %v, got %v", want, gotArgs)
	}
}

func runBuildWithFakeDocker(t *testing.T, generatedPath, tag, contextDir string) []string {
	t.Helper()

	dir := t.TempDir()
	binDir := filepath.Join(dir, "bin")
	argsFile := filepath.Join(dir, "docker-args.txt")
	dockerPath := filepath.Join(binDir, "docker")

	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dockerPath, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$DOCKER_ARGS_FILE\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	t.Setenv("DOCKER_ARGS_FILE", argsFile)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	if err := BuildFromGenerated(generatedPath, tag, contextDir); err != nil {
		t.Fatalf("BuildFromGenerated returned error: %v", err)
	}

	data, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("read fake docker args: %v", err)
	}
	return strings.Split(strings.TrimSpace(string(data)), "\n")
}

func runGenerateWithFakeDocker(t *testing.T, clawfilePath string) (string, []string, string) {
	t.Helper()

	dir := t.TempDir()
	binDir := filepath.Join(dir, "bin")
	argsFile := filepath.Join(dir, "docker-args.txt")
	dockerfileCopy := filepath.Join(dir, "base.Dockerfile")
	dockerPath := filepath.Join(binDir, "docker")

	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}

	script := `#!/bin/sh
if [ "$1" = "image" ] && [ "$2" = "inspect" ]; then
    exit 1
fi

if [ "$1" = "build" ]; then
    printf '%s\n' "$@" > "$DOCKER_ARGS_FILE"
    cp "$4/Dockerfile" "$BASE_DOCKERFILE_COPY"
    exit 0
fi

exit 0
`
	if err := os.WriteFile(dockerPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	t.Setenv("DOCKER_ARGS_FILE", argsFile)
	t.Setenv("BASE_DOCKERFILE_COPY", dockerfileCopy)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	generatedPath, err := Generate(clawfilePath)
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	var args []string
	if data, err := os.ReadFile(argsFile); err == nil {
		args = strings.Split(strings.TrimSpace(string(data)), "\n")
	}

	var dockerfile string
	if data, err := os.ReadFile(dockerfileCopy); err == nil {
		dockerfile = string(data)
	}

	return generatedPath, args, dockerfile
}
