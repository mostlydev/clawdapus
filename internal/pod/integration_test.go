//go:build integration

package pod

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mostlydev/clawdapus/internal/driver"
	_ "github.com/mostlydev/clawdapus/internal/driver/openclaw"
	"github.com/mostlydev/clawdapus/internal/inspect"
	"github.com/mostlydev/clawdapus/internal/runtime"
)

func TestComposeUpSmoke(t *testing.T) {
	// Requires: claw build -t claw-openclaw-example examples/openclaw
	// Run: go test -tags=integration ./internal/pod/ -run TestComposeUpSmoke -v

	imageRef := "claw-openclaw-example"

	info, err := inspect.Inspect(imageRef)
	if err != nil {
		t.Skipf("image %q not available: %v (run 'claw build' first)", imageRef, err)
	}
	if info.ClawType == "" {
		t.Fatalf("image %q has no claw.type label", imageRef)
	}

	// Resolve contract
	exampleDir, _ := filepath.Abs("../../examples/openclaw")
	contract, err := runtime.ResolveContract(exampleDir, info.Agent)
	if err != nil {
		t.Fatalf("contract resolution failed: %v", err)
	}

	// Resolve driver
	d, err := driver.Lookup(info.ClawType)
	if err != nil {
		t.Fatalf("driver lookup failed: %v", err)
	}

	// Validate
	rc := &driver.ResolvedClaw{
		ServiceName:   "gateway",
		ImageRef:      imageRef,
		ClawType:      info.ClawType,
		Agent:         info.Agent,
		AgentHostPath: contract.HostPath,
		Models:        info.Models,
		Configures:    info.Configures,
		Privileges:    info.Privileges,
		Count:         1,
	}
	if err := d.Validate(rc); err != nil {
		t.Fatalf("validation failed: %v", err)
	}

	// Materialize
	runtimeDir := t.TempDir()
	result, err := d.Materialize(rc, driver.MaterializeOpts{RuntimeDir: runtimeDir})
	if err != nil {
		t.Fatalf("materialization failed: %v", err)
	}

	if !result.ReadOnly {
		t.Error("expected ReadOnly=true")
	}
	if result.Restart != "on-failure" {
		t.Errorf("expected restart=on-failure, got %q", result.Restart)
	}

	// Check config file was written
	configPath := filepath.Join(runtimeDir, "openclaw.json")
	if _, err := os.Stat(configPath); err != nil {
		t.Fatalf("config file not written: %v", err)
	}

	// Emit compose
	p := &Pod{
		Name: "smoke-test",
		Services: map[string]*Service{
			"gateway": {
				Image: imageRef,
				Claw: &ClawBlock{
					Agent: "./AGENTS.md",
					Count: 1,
				},
			},
		},
	}
	results := map[string]*driver.MaterializeResult{"gateway": result}
	output, err := EmitCompose(p, results)
	if err != nil {
		t.Fatalf("compose emission failed: %v", err)
	}

	if output == "" {
		t.Fatal("empty compose output")
	}
	t.Logf("Generated compose:\n%s", output)
}
