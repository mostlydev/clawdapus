package openclaw

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/client"
	"github.com/mostlydev/clawdapus/internal/driver"
	"github.com/mostlydev/clawdapus/internal/health"
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

	// Generate CLAWDAPUS.md — infrastructure context for the agent
	podName := opts.PodName
	if podName == "" {
		podName = rc.ServiceName
	}
	clawdapusMd := GenerateClawdapusMD(rc, podName)
	clawdapusPath := filepath.Join(opts.RuntimeDir, "CLAWDAPUS.md")
	if err := os.WriteFile(clawdapusPath, []byte(clawdapusMd), 0644); err != nil {
		return nil, fmt.Errorf("openclaw driver: failed to write CLAWDAPUS.md: %w", err)
	}

	mounts = append(mounts, driver.Mount{
		HostPath:      clawdapusPath,
		ContainerPath: "/claw/CLAWDAPUS.md",
		ReadOnly:      true,
	})

	return &driver.MaterializeResult{
		Mounts:  mounts,
		Tmpfs:   []string{"/tmp", "/run", "/app/data", "/root/.openclaw"},
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
		cid := opts.ContainerID
		if len(cid) > 12 {
			cid = cid[:12]
		}
		return fmt.Errorf("openclaw driver: post-apply check failed: container %s is not running (status: %s)", cid, info.State.Status)
	}

	return nil
}

func (d *Driver) HealthProbe(ref driver.ContainerRef) (*driver.Health, error) {
	if ref.ContainerID == "" {
		return &driver.Health{OK: false, Detail: "no container ID"}, nil
	}

	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("openclaw driver: health probe failed to create docker client: %w", err)
	}
	defer cli.Close()

	ctx := context.Background()

	execCfg := types.ExecConfig{
		Cmd:          []string{"openclaw", "health", "--json"},
		AttachStdout: true,
		AttachStderr: true,
	}
	execID, err := cli.ContainerExecCreate(ctx, ref.ContainerID, execCfg)
	if err != nil {
		return &driver.Health{OK: false, Detail: fmt.Sprintf("exec create failed: %v", err)}, nil
	}

	resp, err := cli.ContainerExecAttach(ctx, execID.ID, types.ExecStartCheck{})
	if err != nil {
		return &driver.Health{OK: false, Detail: fmt.Sprintf("exec attach failed: %v", err)}, nil
	}
	defer resp.Close()

	var buf bytes.Buffer
	if _, err := io.Copy(&buf, resp.Reader); err != nil {
		return &driver.Health{OK: false, Detail: fmt.Sprintf("exec read failed: %v", err)}, nil
	}

	result, err := health.ParseHealthJSON(buf.Bytes())
	if err != nil {
		return &driver.Health{OK: false, Detail: fmt.Sprintf("parse failed: %v", err)}, nil
	}

	return &driver.Health{OK: result.OK, Detail: result.Detail}, nil
}
