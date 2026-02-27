package nanoclaw

import (
	"fmt"
	"os"

	"github.com/mostlydev/clawdapus/internal/driver"
)

// Driver implements the Clawdapus driver interface for NanoClaw —
// a lightweight agent runtime using the Claude Agent SDK.
type Driver struct{}

func init() {
	driver.Register("nanoclaw", &Driver{})
}

func (d *Driver) Validate(rc *driver.ResolvedClaw) error {
	if rc.AgentHostPath == "" {
		return fmt.Errorf("nanoclaw driver: no agent host path specified (no contract, no start)")
	}
	if _, err := os.Stat(rc.AgentHostPath); err != nil {
		return fmt.Errorf("nanoclaw driver: agent file %q not found: %w", rc.AgentHostPath, err)
	}
	if rc.Privileges == nil || rc.Privileges["docker-socket"] != "true" {
		return fmt.Errorf("nanoclaw driver: requires PRIVILEGE docker-socket (nanoclaw spawns agent containers via Docker)")
	}
	if len(rc.Invocations) > 0 {
		fmt.Printf("[claw] warning: nanoclaw driver: INVOKE scheduling not supported; ignoring %d invocations\n", len(rc.Invocations))
	}
	return nil
}

func (d *Driver) Materialize(rc *driver.ResolvedClaw, opts driver.MaterializeOpts) (*driver.MaterializeResult, error) {
	return nil, fmt.Errorf("nanoclaw driver: Materialize not yet implemented")
}

func (d *Driver) PostApply(rc *driver.ResolvedClaw, opts driver.PostApplyOpts) error {
	return fmt.Errorf("nanoclaw driver: PostApply not yet implemented")
}

func (d *Driver) HealthProbe(ref driver.ContainerRef) (*driver.Health, error) {
	return nil, fmt.Errorf("nanoclaw driver: HealthProbe not yet implemented")
}
