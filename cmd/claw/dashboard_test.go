package main

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mostlydev/clawdapus/internal/pod"
)

func TestLoadDashboardSettings(t *testing.T) {
	for _, tc := range []struct {
		name, bind, enabled, addr, wantBind string
		wantEnabled                         bool
		wantErr                             bool
	}{
		{name: "loopback", bind: "127.0.0.1", enabled: "true", addr: ":8082", wantBind: "127.0.0.1", wantEnabled: true},
		{name: "IPv6", bind: "[::1]", enabled: "true", addr: ":8082", wantBind: "::1", wantEnabled: true},
		{name: "disabled", enabled: "false", addr: "ignored"},
		{name: "bad bool", enabled: "yes", wantErr: true},
		{name: "bad bind", bind: "localhost", enabled: "true", wantErr: true},
		{name: "container loopback", enabled: "true", addr: "127.0.0.1:8082", wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("CLAW_DASHBOARD_BIND", tc.bind)
			t.Setenv("CLAWDASH_ENABLED", tc.enabled)
			t.Setenv("CLAWDASH_ADDR", tc.addr)
			got, err := loadDashboardSettings()
			if (err != nil) != tc.wantErr {
				t.Fatalf("settings=%+v err=%v", got, err)
			}
			if err == nil && (got.Bind != tc.wantBind || got.Enabled != tc.wantEnabled) {
				t.Fatalf("settings=%+v", got)
			}
		})
	}
}

func TestDisabledClawdashHasNoGeneratedPrincipal(t *testing.T) {
	for _, disabled := range []bool{false, true} {
		dir := t.TempDir()
		p := &pod.Pod{Name: "test", Services: map[string]*pod.Service{}, DisableClawdash: disabled,
			ClawAPI: &pod.ClawAPIConfig{Addr: ":8080", PrincipalsHostPath: filepath.Join(dir, "claw-api", "principals.json")}}
		if _, err := prepareClawAPIRuntime(dir, p, nil); err != nil {
			t.Fatal(err)
		}
		raw, err := os.ReadFile(p.ClawAPI.PrincipalsHostPath)
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.Contains(string(raw), "claw-dashboard"); got == disabled {
			t.Fatalf("disabled=%v generated dashboard principal=%v", disabled, got)
		}
	}
}

func TestRemoveDisabledClawdashUsesPriorComposeOnly(t *testing.T) {
	previous := []byte("name: old-project\nservices:\n  clawdash:\n    labels:\n      claw.role: dashboard\n      claw.service: clawdash\n")
	dir := t.TempDir()
	oldCommand := runComposeDockerCommand
	t.Cleanup(func() { runComposeDockerCommand = oldCommand })
	var snapshot string
	calls := 0
	runComposeDockerCommand = func(args ...string) error {
		calls++
		if len(args) != 7 || args[0] != "compose" || args[1] != "-f" || !reflect.DeepEqual(args[3:], []string{"rm", "--stop", "--force", "clawdash"}) {
			t.Fatalf("unexpected removal arguments: %v", args)
		}
		snapshot = args[2]
		info, err := os.Stat(snapshot)
		if err != nil || info.Mode().Perm() != 0o600 || filepath.Dir(snapshot) != dir {
			t.Fatalf("snapshot not private/in original project dir: %v %v", info, err)
		}
		raw, err := os.ReadFile(snapshot)
		if err != nil || string(raw) != string(previous) {
			t.Fatalf("removal lost prior project: %s %v", raw, err)
		}
		return nil
	}
	generated := filepath.Join(dir, "compose.generated.yml")
	if err := removeDisabledClawdash(generated, previous); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(snapshot); !os.IsNotExist(err) {
		t.Fatalf("private snapshot not removed: %v", err)
	}
	if err := removeDisabledClawdash(generated, []byte("services: {}")); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("unexpected lifecycle calls: %d", calls)
	}
	if err := removeDisabledClawdash(generated, []byte("services:\n  clawdash: {}")); err == nil {
		t.Fatal("unlabelled service was accepted for removal")
	}
	runComposeDockerCommand = func(args ...string) error {
		snapshot = args[2]
		return errors.New("compose failed")
	}
	if err := removeDisabledClawdash(generated, previous); err == nil || !strings.Contains(err.Error(), "claw compose rm --stop --force clawdash") {
		t.Fatalf("missing actionable Compose removal failure: %v", err)
	}
	if _, err := os.Stat(snapshot); !os.IsNotExist(err) {
		t.Fatalf("private snapshot remains after failed removal: %v", err)
	}
}

func TestFailedDashboardRemovalPreservesComposeAndRestoresRuntime(t *testing.T) {
	dir := t.TempDir()
	runtimeDir := filepath.Join(dir, ".claw-runtime")
	if err := os.Mkdir(runtimeDir, 0o700); err != nil {
		t.Fatal(err)
	}
	contract := filepath.Join(runtimeDir, "contract")
	if err := os.WriteFile(contract, []byte("old context"), 0o600); err != nil {
		t.Fatal(err)
	}
	stage, err := prepareRuntimeDir(runtimeDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(contract, []byte("new context"), 0o600); err != nil {
		t.Fatal(err)
	}
	previous := []byte("services:\n  clawdash:\n    labels: {claw.role: dashboard, claw.service: clawdash}\n")
	generated := filepath.Join(dir, "compose.generated.yml")
	if err := os.WriteFile(generated, previous, 0o600); err != nil {
		t.Fatal(err)
	}
	oldCommand := runComposeDockerCommand
	t.Cleanup(func() { runComposeDockerCommand = oldCommand })
	runComposeDockerCommand = func(args ...string) error {
		if len(args) != 7 || args[3] != "rm" {
			t.Fatalf("unexpected apply after failed removal: %v", args)
		}
		return errors.New("old compose cannot load")
	}
	applied, err := applyComposeDefinition(generated, []byte("new compose"), 0o600, previous, true, true)
	if err == nil || applied {
		t.Fatalf("failed removal must permit runtime rollback: applied=%v err=%v", applied, err)
	}
	// This is the rollback decision consumed by runComposeUp's deferred handler.
	if err := stage.Rollback(); err != nil {
		t.Fatal(err)
	}
	if raw, err := os.ReadFile(contract); err != nil || string(raw) != "old context" {
		t.Fatalf("old runtime not restored: %q %v", raw, err)
	}
	if raw, err := os.ReadFile(generated); err != nil || string(raw) != string(previous) {
		t.Fatalf("old Compose not preserved: %q %v", raw, err)
	}
}

func TestCleanupInterruptedDashboardSnapshots(t *testing.T) {
	dir := t.TempDir()
	stale := filepath.Join(dir, ".claw-previous-interrupted.yml")
	if err := os.WriteFile(stale, []byte("private old config"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, ".claw-previous-unrelated.yml")
	if err := os.Symlink(stale, link); err != nil {
		t.Fatal(err)
	}
	if err := cleanupDashboardSnapshots(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("interrupted snapshot remains: %v", err)
	}
	if _, err := os.Lstat(link); err != nil {
		t.Fatalf("cleanup removed a symlink: %v", err)
	}
}
