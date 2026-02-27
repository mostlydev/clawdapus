package nanoclaw

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mostlydev/clawdapus/internal/driver"
)

func TestDriverRegistered(t *testing.T) {
	d, err := driver.Lookup("nanoclaw")
	if err != nil {
		t.Fatalf("nanoclaw driver not registered: %v", err)
	}
	if d == nil {
		t.Fatal("nanoclaw driver is nil")
	}
}

func TestValidateRequiresAgentPath(t *testing.T) {
	d := &Driver{}
	rc := &driver.ResolvedClaw{
		ServiceName: "test",
		Privileges:  map[string]string{"docker-socket": "true"},
	}
	err := d.Validate(rc)
	if err == nil {
		t.Fatal("expected error for missing agent host path")
	}
	if !strings.Contains(err.Error(), "no agent host path") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateRequiresDockerSocket(t *testing.T) {
	tmp := t.TempDir()
	agentPath := filepath.Join(tmp, "AGENTS.md")
	if err := os.WriteFile(agentPath, []byte("test"), 0644); err != nil {
		t.Fatal(err)
	}

	d := &Driver{}
	rc := &driver.ResolvedClaw{
		ServiceName:   "test",
		AgentHostPath: agentPath,
		Privileges:    map[string]string{},
	}
	err := d.Validate(rc)
	if err == nil {
		t.Fatal("expected error for missing docker-socket privilege")
	}
	if !strings.Contains(err.Error(), "PRIVILEGE docker-socket") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateAcceptsValidConfig(t *testing.T) {
	tmp := t.TempDir()
	agentPath := filepath.Join(tmp, "AGENTS.md")
	if err := os.WriteFile(agentPath, []byte("test"), 0644); err != nil {
		t.Fatal(err)
	}

	d := &Driver{}
	rc := &driver.ResolvedClaw{
		ServiceName:   "test",
		AgentHostPath: agentPath,
		Privileges:    map[string]string{"docker-socket": "true"},
	}
	if err := d.Validate(rc); err != nil {
		t.Fatalf("unexpected validation error: %v", err)
	}
}

func TestValidateWarnsOnInvocations(t *testing.T) {
	tmp := t.TempDir()
	agentPath := filepath.Join(tmp, "AGENTS.md")
	if err := os.WriteFile(agentPath, []byte("test"), 0644); err != nil {
		t.Fatal(err)
	}

	d := &Driver{}
	rc := &driver.ResolvedClaw{
		ServiceName:   "test",
		AgentHostPath: agentPath,
		Privileges:    map[string]string{"docker-socket": "true"},
		Invocations:   []driver.Invocation{{Schedule: "0 * * * *", Message: "test"}},
	}
	// Should not error, just warn
	if err := d.Validate(rc); err != nil {
		t.Fatalf("invocations should produce warning, not error: %v", err)
	}
}
