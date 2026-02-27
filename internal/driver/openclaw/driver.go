package openclaw

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
	"github.com/mostlydev/clawdapus/internal/driver"
	"github.com/mostlydev/clawdapus/internal/driver/shared"
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

	// Write config into its own subdirectory and bind-mount the whole directory.
	// openclaw performs atomic writes by creating a temp file alongside the config
	// (openclaw.json.<n>.<uuid>.tmp → rename). The directory must be writable for
	// that pattern to work; a read-only single-file bind-mount causes EROFS.
	configDir := filepath.Join(opts.RuntimeDir, "config")
	if err := os.MkdirAll(configDir, 0700); err != nil {
		return nil, fmt.Errorf("openclaw driver: create config dir: %w", err)
	}
	configPath := filepath.Join(configDir, "openclaw.json")
	if err := os.WriteFile(configPath, configData, 0644); err != nil {
		return nil, fmt.Errorf("openclaw driver: failed to write config: %w", err)
	}

	mounts := []driver.Mount{
		{
			// Bind-mount the directory (not the file) so openclaw can write temp files
			// alongside the config during atomic save operations.
			HostPath:      configDir,
			ContainerPath: "/app/config",
			ReadOnly:      false,
		},
		{
			// Always mount as AGENTS.md so openclaw finds it at workspace root (/claw/AGENTS.md).
			HostPath:      rc.AgentHostPath,
			ContainerPath: "/claw/AGENTS.md",
			ReadOnly:      true,
		},
	}

	// Generate jobs.json if there are scheduled invocations.
	// Mounted read-write: openclaw updates job state (nextRunAtMs, lastRunAtMs, etc.)
	// on every timer tick. Read-only would produce EROFS failures in the scheduler.
	if len(rc.Invocations) > 0 {
		jobsData, err := GenerateJobsJSON(rc)
		if err != nil {
			return nil, fmt.Errorf("openclaw driver: generate jobs.json: %w", err)
		}
		jobsDir := filepath.Join(opts.RuntimeDir, "state", "cron")
		if err := os.MkdirAll(jobsDir, 0700); err != nil {
			return nil, fmt.Errorf("openclaw driver: create jobs dir: %w", err)
		}
		jobsPath := filepath.Join(jobsDir, "jobs.json")
		if err := os.WriteFile(jobsPath, jobsData, 0644); err != nil {
			return nil, fmt.Errorf("openclaw driver: write jobs.json: %w", err)
		}
		mounts = append(mounts, driver.Mount{
			// Bind-mount the directory (not the file) so openclaw can rename temp files
			// alongside jobs.json during atomic save operations (same pattern as openclaw.json).
			HostPath:      jobsDir,
			ContainerPath: "/app/state/cron",
			ReadOnly:      false,
		})
	}

	// Generate CLAWDAPUS.md — infrastructure context for the agent
	podName := opts.PodName
	if podName == "" {
		podName = rc.ServiceName
	}
	clawdapusMd := shared.GenerateClawdapusMD(rc, podName)
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
		Mounts: mounts,
		Tmpfs: []string{
			"/tmp",
			"/run",
			// /app/state covers all openclaw state subdirs (identity, logs, memory, agents, etc.).
			// The jobs.json bind mount layers on top of this tmpfs — Docker applies bind mounts
			// after tmpfs, so /app/state/cron/jobs.json is accessible read-write as expected.
			"/app/state",
		},
		ReadOnly: true,
		Restart:  "on-failure",
		SkillDir: "/claw/skills",
		Healthcheck: &driver.Healthcheck{
			Test:     []string{"CMD", "openclaw", "health", "--json"},
			Interval: "30s",
			Timeout:  "10s",
			Retries:  3,
		},
		Environment: map[string]string{
			"CLAW_MANAGED":         "true",
			"OPENCLAW_CONFIG_PATH": "/app/config/openclaw.json",
			"OPENCLAW_STATE_DIR":   "/app/state",
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

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	info, err := cli.ContainerInspect(ctx, ref.ContainerID)
	if err != nil {
		return &driver.Health{OK: false, Detail: fmt.Sprintf("container inspect failed: %v", err)}, nil
	}
	if info.State == nil || !info.State.Running {
		status := "unknown"
		if info.State != nil && info.State.Status != "" {
			status = info.State.Status
		}
		return &driver.Health{OK: false, Detail: fmt.Sprintf("container is not running (status: %s)", status)}, nil
	}

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

	var stdoutBuf bytes.Buffer
	var stderrBuf bytes.Buffer
	copyDone := make(chan error, 1)
	go func() {
		_, copyErr := stdcopy.StdCopy(&stdoutBuf, &stderrBuf, resp.Reader)
		copyDone <- copyErr
	}()

	select {
	case copyErr := <-copyDone:
		if copyErr != nil {
			return &driver.Health{OK: false, Detail: fmt.Sprintf("exec read failed: %v", copyErr)}, nil
		}
	case <-ctx.Done():
		resp.Close()
		return &driver.Health{OK: false, Detail: "health probe timed out after 15s"}, nil
	}

	execInspect, err := cli.ContainerExecInspect(ctx, execID.ID)
	if err != nil {
		return &driver.Health{OK: false, Detail: fmt.Sprintf("exec inspect failed: %v", err)}, nil
	}
	if execInspect.ExitCode != 0 {
		detail := strings.TrimSpace(stderrBuf.String())
		if detail == "" {
			detail = strings.TrimSpace(stdoutBuf.String())
		}
		if detail == "" {
			detail = "health command failed with no output"
		}
		return &driver.Health{OK: false, Detail: fmt.Sprintf("health command exit code %d: %s", execInspect.ExitCode, detail)}, nil
	}

	result, err := health.ParseHealthJSON(stdoutBuf.Bytes())
	if err != nil {
		detail := fmt.Sprintf("parse failed: %v", err)
		if stderr := strings.TrimSpace(stderrBuf.String()); stderr != "" {
			detail += fmt.Sprintf(" (stderr: %s)", stderr)
		}
		return &driver.Health{OK: false, Detail: detail}, nil
	}

	detail := result.Detail
	if stderr := strings.TrimSpace(stderrBuf.String()); stderr != "" {
		detail += fmt.Sprintf(" (stderr: %s)", stderr)
	}
	return &driver.Health{OK: result.OK, Detail: detail}, nil
}
