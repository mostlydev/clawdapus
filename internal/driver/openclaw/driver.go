package openclaw

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/docker/docker/client"
	"github.com/mostlydev/clawdapus/internal/driver"
)

type Driver struct{}

func init() {
	driver.Register("openclaw", &Driver{})
}

func (d *Driver) Validate(rc *driver.ResolvedClaw) error {
	if rc.AgentHostPath == "" {
		return fmt.Errorf("openclaw driver: no agent host path specified (no contract, no start)")
	}
	if _, err := os.Stat(rc.AgentHostPath); err != nil {
		return fmt.Errorf("openclaw driver: agent file %q not found: %w (no contract, no start)", rc.AgentHostPath, err)
	}
	return nil
}

func (d *Driver) Materialize(rc *driver.ResolvedClaw, opts driver.MaterializeOpts) (*driver.MaterializeResult, error) {
	configData, err := GenerateConfig(rc)
	if err != nil {
		return nil, fmt.Errorf("openclaw driver: config generation failed: %w", err)
	}

	configPath := filepath.Join(opts.RuntimeDir, "openclaw.json")
	if err := os.WriteFile(configPath, configData, 0644); err != nil {
		return nil, fmt.Errorf("openclaw driver: failed to write config: %w", err)
	}

	mounts := []driver.Mount{
		{
			HostPath:      configPath,
			ContainerPath: "/app/config/openclaw.json",
			ReadOnly:      true,
		},
		{
			HostPath:      rc.AgentHostPath,
			ContainerPath: "/claw/" + rc.Agent,
			ReadOnly:      true,
		},
	}

	return &driver.MaterializeResult{
		Mounts:  mounts,
		Tmpfs:   []string{"/tmp", "/app/data"},
		ReadOnly: true,
		Restart:  "on-failure",
		Healthcheck: &driver.Healthcheck{
			Test:     []string{"CMD", "openclaw", "health", "--json"},
			Interval: "30s",
			Timeout:  "10s",
			Retries:  3,
		},
		Environment: map[string]string{
			"CLAW_MANAGED": "true",
		},
	}, nil
}

func (d *Driver) PostApply(rc *driver.ResolvedClaw, opts driver.PostApplyOpts) error {
	if opts.ContainerID == "" {
		return fmt.Errorf("openclaw driver: post-apply check failed: no container ID")
	}

	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return fmt.Errorf("openclaw driver: post-apply failed to create docker client: %w", err)
	}
	defer cli.Close()

	info, err := cli.ContainerInspect(context.Background(), opts.ContainerID)
	if err != nil {
		return fmt.Errorf("openclaw driver: post-apply container inspect failed: %w", err)
	}

	if !info.State.Running {
		return fmt.Errorf("openclaw driver: post-apply check failed: container %s is not running (status: %s)", opts.ContainerID[:12], info.State.Status)
	}

	return nil
}

func (d *Driver) HealthProbe(ref driver.ContainerRef) (*driver.Health, error) {
	return &driver.Health{OK: true, Detail: "probe wired, awaiting container exec"}, nil
}
