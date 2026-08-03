package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestCallClawAPIComposeBuildsExecCommand(t *testing.T) {
	composePath := writeComposeFixture(t, `
services:
  claw-api:
    image: ghcr.io/mostlydev/claw-api:latest
`)

	prev := runClawAPIComposeCommand
	var gotArgs []string
	var gotTimeout time.Duration
	runClawAPIComposeCommand = func(timeout time.Duration, args ...string) ([]byte, error) {
		gotTimeout = timeout
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
	if gotTimeout != defaultAPIExecTimeout {
		t.Fatalf("expected default exec timeout %v, got %v", defaultAPIExecTimeout, gotTimeout)
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
	runClawAPIComposeCommand = func(_ time.Duration, args ...string) ([]byte, error) {
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

func TestCallClawAPIComposeGivesScheduleFireAFullWakeRequestBudget(t *testing.T) {
	composePath := writeComposeFixture(t, `
services:
  claw-api:
    image: ghcr.io/mostlydev/claw-api:latest
`)

	prev := runClawAPIComposeCommand
	var gotArgs []string
	var gotTimeout time.Duration
	runClawAPIComposeCommand = func(timeout time.Duration, args ...string) ([]byte, error) {
		gotTimeout = timeout
		gotArgs = append([]string(nil), args...)
		return []byte("{}\n"), nil
	}
	defer func() { runClawAPIComposeCommand = prev }()

	if _, err := callClawAPICompose(composePath, "ops-admin", "POST", "/schedule/westin/fire", nil); err != nil {
		t.Fatalf("callClawAPICompose: %v", err)
	}
	joined := strings.Join(gotArgs, " ")
	if !strings.Contains(joined, "-request-timeout 2m5s") {
		t.Fatalf("expected schedule fire to cover the two-minute runner wake budget, got %#v", gotArgs)
	}
	if gotTimeout != 2*time.Minute+10*time.Second {
		t.Fatalf("expected outer transport to outlive the request budget, got %v", gotTimeout)
	}
}

func TestCallClawAPIComposeHonorsExplicitExecTimeoutForScheduleFire(t *testing.T) {
	composePath := writeComposeFixture(t, `
services:
  claw-api:
    image: ghcr.io/mostlydev/claw-api:latest
`)

	previousTimeout := apiExecTimeout
	apiExecTimeout = 45 * time.Second
	defer func() { apiExecTimeout = previousTimeout }()
	prev := runClawAPIComposeCommand
	var gotTimeout time.Duration
	runClawAPIComposeCommand = func(timeout time.Duration, _ ...string) ([]byte, error) {
		gotTimeout = timeout
		return []byte("{}\n"), nil
	}
	defer func() { runClawAPIComposeCommand = prev }()

	if _, err := callClawAPICompose(composePath, "ops-admin", "POST", "/schedule/westin/fire", nil); err != nil {
		t.Fatalf("callClawAPICompose: %v", err)
	}
	if gotTimeout != 45*time.Second {
		t.Fatalf("expected explicit exec timeout to win, got %v", gotTimeout)
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
