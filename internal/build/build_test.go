package build

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mostlydev/clawdapus/internal/driver/hermes"
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

func TestGenerateLeavesPublishedHermesBaseForDockerPullWhenMissing(t *testing.T) {
	dir := t.TempDir()
	clawfilePath := filepath.Join(dir, "Clawfile")

	input := `FROM ` + hermes.BaseImageTag + `

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
	if len(args) != 0 {
		t.Fatalf("expected no auto-build for published Hermes base image, got %v", args)
	}
	if baseDockerfile != "" {
		t.Fatal("expected no captured base Dockerfile when Hermes base auto-build is disabled")
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

func TestGenerateReturnsMissingRunnerBaseErrorWhenRunnerBaseMissing(t *testing.T) {
	dir := t.TempDir()
	clawfilePath := filepath.Join(dir, "Clawfile")

	input := `FROM openclaw:latest

CLAW_TYPE openclaw
AGENT AGENTS.md
`
	if err := os.WriteFile(clawfilePath, []byte(input), 0o644); err != nil {
		t.Fatal(err)
	}

	fakeDir := t.TempDir()
	binDir := filepath.Join(fakeDir, "bin")
	argsFile := filepath.Join(fakeDir, "docker-args.txt")
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
    exit 0
fi
exit 0
`
	if err := os.WriteFile(dockerPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DOCKER_ARGS_FILE", argsFile)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	_, err := Generate(clawfilePath)
	if err == nil {
		t.Fatal("expected Generate to fail when runner base is missing")
	}

	var missing *MissingRunnerBaseError
	if !errors.As(err, &missing) {
		t.Fatalf("expected MissingRunnerBaseError, got %v", err)
	}
	if missing.ImageRef != "openclaw:latest" {
		t.Fatalf("expected missing image ref openclaw:latest, got %q", missing.ImageRef)
	}
	if _, statErr := os.Stat(argsFile); !os.IsNotExist(statErr) {
		t.Fatalf("expected no docker build invocation, got stat err %v", statErr)
	}
}

func TestGenerateRewritesRunnerFromUsingLocalProvenance(t *testing.T) {
	dir := t.TempDir()
	clawfilePath := filepath.Join(dir, "Clawfile")

	input := `FROM openclaw:latest

CLAW_TYPE openclaw
AGENT AGENTS.md
`
	if err := os.WriteFile(clawfilePath, []byte(input), 0o644); err != nil {
		t.Fatal(err)
	}

	fakeDir := t.TempDir()
	binDir := filepath.Join(fakeDir, "bin")
	dockerPath := filepath.Join(binDir, "docker")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}

	script := `#!/bin/sh
if [ "$1" = "image" ] && [ "$2" = "inspect" ]; then
    cat <<'EOF'
[{"Id":"sha256:abc123def4567890","RepoTags":["openclaw:latest","openclaw:built-20260415-abc123def456"]}]
EOF
    exit 0
fi
exit 0
`
	if err := os.WriteFile(dockerPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	generatedPath, err := Generate(clawfilePath)
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	content, err := os.ReadFile(generatedPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(content)
	if !strings.Contains(text, "FROM openclaw:built-20260415-abc123def456") {
		t.Fatalf("expected rewritten FROM with versioned local tag, got:\n%s", text)
	}
	if !strings.Contains(text, `LABEL claw.runner.driver="openclaw"`) {
		t.Fatalf("expected claw.runner.driver label, got:\n%s", text)
	}
	if !strings.Contains(text, `LABEL claw.runner.image-id="sha256:abc123def4567890"`) {
		t.Fatalf("expected claw.runner.image-id label, got:\n%s", text)
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

type fakeRunnerProvider struct {
	tag        string
	dockerfile string
}

func (f fakeRunnerProvider) BaseImage() (string, string) {
	return f.tag, f.dockerfile
}

func (f fakeRunnerProvider) RunnerAlias() string {
	return strings.TrimSuffix(f.tag, ":latest")
}

func TestRefreshRunnerBaseUsesPullAndNoCache(t *testing.T) {
	dir := t.TempDir()
	binDir := filepath.Join(dir, "bin")
	argsFile := filepath.Join(dir, "docker-calls.txt")
	dockerPath := filepath.Join(binDir, "docker")

	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	script := `#!/bin/sh
if [ "$1" = "build" ]; then
    printf 'build %s\n' "$*" >> "$DOCKER_ARGS_FILE"
    exit 0
fi
if [ "$1" = "image" ] && [ "$2" = "inspect" ]; then
    cat <<'EOF'
[{"Id":"sha256:abc123def4567890","RepoTags":["openclaw:latest"]}]
EOF
    exit 0
fi
if [ "$1" = "tag" ]; then
    printf 'tag %s\n' "$*" >> "$DOCKER_ARGS_FILE"
    exit 0
fi
exit 0
`
	if err := os.WriteFile(dockerPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	t.Setenv("DOCKER_ARGS_FILE", argsFile)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	result, err := RefreshRunnerBase("openclaw", fakeRunnerProvider{
		tag:        "openclaw:latest",
		dockerfile: "FROM busybox\n",
	})
	if err != nil {
		t.Fatalf("RefreshRunnerBase returned error: %v", err)
	}
	if result.DriverName != "openclaw" {
		t.Fatalf("expected driver name openclaw, got %q", result.DriverName)
	}
	if result.ImageRef != "openclaw:latest" {
		t.Fatalf("expected image ref openclaw:latest, got %q", result.ImageRef)
	}
	if !strings.HasPrefix(result.BuiltRef, "openclaw:built-") {
		t.Fatalf("expected built ref to use fallback built tag, got %q", result.BuiltRef)
	}
	if result.ImageID != "sha256:abc123def4567890" {
		t.Fatalf("expected image id to be captured, got %q", result.ImageID)
	}

	data, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("read fake docker args: %v", err)
	}
	text := strings.TrimSpace(string(data))
	if !strings.Contains(text, "build build --pull --no-cache -t openclaw:claw-refresh-") {
		t.Fatalf("expected docker build with --pull --no-cache, got %q", text)
	}
	if !strings.Contains(text, "tag tag openclaw:claw-refresh-") || !strings.Contains(text, " openclaw:built-") {
		t.Fatalf("expected docker tag call for built tag, got %q", text)
	}
	if !strings.Contains(text, " openclaw:latest") {
		t.Fatalf("expected docker tag call for latest tag, got %q", text)
	}
}

func TestRefreshRunnerBaseReportsPreviousVersionedRef(t *testing.T) {
	dir := t.TempDir()
	binDir := filepath.Join(dir, "bin")
	dockerPath := filepath.Join(binDir, "docker")

	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	script := `#!/bin/sh
if [ "$1" = "build" ]; then
    exit 0
fi
if [ "$1" = "image" ] && [ "$2" = "inspect" ]; then
    case "$3" in
        openclaw:latest)
            cat <<'EOF'
[{"Id":"sha256:old123def4567890","RepoTags":["openclaw:latest","openclaw:built-20260401-old123def456"]}]
EOF
            ;;
        *)
            cat <<'EOF'
[{"Id":"sha256:new123def4567890","RepoTags":["openclaw:claw-refresh-test"]}]
EOF
            ;;
    esac
    exit 0
fi
if [ "$1" = "tag" ]; then
    exit 0
fi
if [ "$1" = "image" ] && [ "$2" = "rm" ]; then
    exit 0
fi
exit 0
`
	if err := os.WriteFile(dockerPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	result, err := RefreshRunnerBase("openclaw", fakeRunnerProvider{
		tag:        "openclaw:latest",
		dockerfile: "FROM busybox\n",
	})
	if err != nil {
		t.Fatalf("RefreshRunnerBase returned error: %v", err)
	}
	if result.PreviousRef != "openclaw:built-20260401-old123def456" {
		t.Fatalf("expected previous ref, got %q", result.PreviousRef)
	}
	if result.PreviousTag != "built-20260401-old123def456" {
		t.Fatalf("expected previous tag, got %q", result.PreviousTag)
	}
}

func TestFallbackRunnerVersionTagAvoidsSameDayCollisions(t *testing.T) {
	first := fallbackRunnerVersionTag("sha256:aaaaaaaaaaaabbbbbbbbbbbb")
	second := fallbackRunnerVersionTag("sha256:bbbbbbbbbbbbcccccccccccc")
	if first == second {
		t.Fatalf("expected image-id suffix to avoid same-day collisions, got %q", first)
	}
	if !strings.HasPrefix(first, "built-") || !strings.HasSuffix(first, "-aaaaaaaaaaaa") {
		t.Fatalf("unexpected first fallback tag %q", first)
	}
	if !strings.HasPrefix(second, "built-") || !strings.HasSuffix(second, "-bbbbbbbbbbbb") {
		t.Fatalf("unexpected second fallback tag %q", second)
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
