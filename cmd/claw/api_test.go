package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestCallClawAPIComposeBuildsExecCommand(t *testing.T) {
	composePath := writeComposeFixture(t, `
services:
  claw-api:
    image: ghcr.io/mostlydev/claw-api:latest
`)

	prev := runClawAPIComposeCommand
	var gotArgs []string
	runClawAPIComposeCommand = func(args ...string) ([]byte, error) {
		gotArgs = append([]string(nil), args...)
		return []byte("{\"ok\":true}\n"), nil
	}
	defer func() { runClawAPIComposeCommand = prev }()

	out, err := callClawAPICompose(composePath, "", "get", "/schedule", nil)
	if err != nil {
		t.Fatalf("callClawAPICompose: %v", err)
	}
	if string(out) != "{\"ok\":true}\n" {
		t.Fatalf("unexpected output: %q", string(out))
	}
	want := []string{
		"compose", "-f", composePath,
		"exec", "-T", "claw-api",
		"/claw-api",
		"-request-method", "GET",
		"-request-path", "/schedule",
		"-request-principal", "claw-scheduler",
	}
	if !reflect.DeepEqual(gotArgs, want) {
		t.Fatalf("unexpected args:\n got: %#v\nwant: %#v", gotArgs, want)
	}
}

func TestCallClawAPIComposeMarshalsBody(t *testing.T) {
	composePath := writeComposeFixture(t, `
services:
  claw-api:
    image: ghcr.io/mostlydev/claw-api:latest
`)

	prev := runClawAPIComposeCommand
	var gotArgs []string
	runClawAPIComposeCommand = func(args ...string) ([]byte, error) {
		gotArgs = append([]string(nil), args...)
		return []byte("{}\n"), nil
	}
	defer func() { runClawAPIComposeCommand = prev }()

	_, err := callClawAPICompose(composePath, "ops-admin", "POST", "/schedule/westin/fire", map[string]any{"bypass_when": true})
	if err != nil {
		t.Fatalf("callClawAPICompose: %v", err)
	}
	joined := strings.Join(gotArgs, " ")
	if !strings.Contains(joined, `-request-principal ops-admin`) {
		t.Fatalf("expected principal in args, got %#v", gotArgs)
	}
	if !strings.Contains(joined, `-request-body {"bypass_when":true}`) {
		t.Fatalf("expected request body in args, got %#v", gotArgs)
	}
}

func TestCallClawAPIComposeRejectsMissingClawAPIService(t *testing.T) {
	composePath := writeComposeFixture(t, `
services:
  analyst:
    image: ghcr.io/mostlydev/openclaw:latest
`)

	_, err := callClawAPICompose(composePath, "", "GET", "/schedule", nil)
	if err == nil || !strings.Contains(err.Error(), "pod does not include claw-api") {
		t.Fatalf("expected missing claw-api error, got %v", err)
	}
}

func TestAPICmdRegistered(t *testing.T) {
	for _, cmd := range rootCmd.Commands() {
		if cmd.Name() == "api" {
			return
		}
	}
	t.Fatal("expected 'api' command to be registered on rootCmd")
}

func TestAPIScheduleClearSkipNextCmdRegistered(t *testing.T) {
	for _, cmd := range apiScheduleCmd.Commands() {
		if cmd.Name() == "clear-skip-next" {
			return
		}
	}
	t.Fatal("expected 'clear-skip-next' command under 'claw api schedule'")
}

func writeComposeFixture(t *testing.T, raw string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "compose.generated.yml")
	if err := os.WriteFile(path, []byte(strings.TrimSpace(raw)+"\n"), 0o644); err != nil {
		t.Fatalf("write compose fixture: %v", err)
	}
	return path
}
