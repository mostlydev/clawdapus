package nullclaw

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
)

type Driver struct{}

func init() {
	driver.Register("nullclaw", &Driver{})
}

func (d *Driver) Validate(rc *driver.ResolvedClaw) error {
	if rc.AgentHostPath == "" {
		return fmt.Errorf("nullclaw driver: no agent host path specified (no contract, no start)")
	}
	if _, err := os.Stat(rc.AgentHostPath); err != nil {
		return fmt.Errorf("nullclaw driver: agent file %q not found: %w", rc.AgentHostPath, err)
	}

	for _, cmd := range rc.Configures {
		if _, _, err := parseConfigSetCommand(cmd); err != nil {
			return fmt.Errorf("nullclaw driver: unsupported CONFIGURE command %q: %w", cmd, err)
		}
	}

	for platform := range rc.Handles {
		switch strings.ToLower(platform) {
		case "discord":
			if resolveEnvTokenFromMap(rc.Environment, "DISCORD_BOT_TOKEN") == "" {
				return fmt.Errorf("nullclaw driver: HANDLE discord requires DISCORD_BOT_TOKEN in service environment")
			}
		case "telegram":
			if resolveEnvTokenFromMap(rc.Environment, "TELEGRAM_BOT_TOKEN") == "" {
				return fmt.Errorf("nullclaw driver: HANDLE telegram requires TELEGRAM_BOT_TOKEN in service environment")
			}
		case "slack":
			if resolveEnvTokenFromMap(rc.Environment, "SLACK_BOT_TOKEN") == "" {
				return fmt.Errorf("nullclaw driver: HANDLE slack requires SLACK_BOT_TOKEN in service environment")
			}
		default:
			fmt.Printf("[claw] warning: nullclaw driver has no HANDLE validation for platform %q; skipping\n", platform)
		}
	}

	return nil
}

func (d *Driver) Materialize(rc *driver.ResolvedClaw, opts driver.MaterializeOpts) (*driver.MaterializeResult, error) {
	configData, err := GenerateConfig(rc)
	if err != nil {
		return nil, fmt.Errorf("nullclaw driver: config generation failed: %w", err)
	}

	homeDir := filepath.Join(opts.RuntimeDir, "nullclaw-home")
	if err := os.MkdirAll(homeDir, 0o700); err != nil {
		return nil, fmt.Errorf("nullclaw driver: create nullclaw home dir: %w", err)
	}
	configPath := filepath.Join(homeDir, "config.json")
	if err := os.WriteFile(configPath, configData, 0o644); err != nil {
		return nil, fmt.Errorf("nullclaw driver: write config.json: %w", err)
	}

	podName := opts.PodName
	if podName == "" {
		podName = rc.ServiceName
	}
	clawdapusPath := filepath.Join(opts.RuntimeDir, "CLAWDAPUS.md")
	clawdapusMD := shared.GenerateClawdapusMD(rc, podName)
	if err := os.WriteFile(clawdapusPath, []byte(clawdapusMD), 0o644); err != nil {
		return nil, fmt.Errorf("nullclaw driver: write CLAWDAPUS.md: %w", err)
	}

	return &driver.MaterializeResult{
		Mounts: []driver.Mount{
			{
				HostPath:      homeDir,
				ContainerPath: "/root/.nullclaw",
				ReadOnly:      false,
			},
			{
				// Upstream image sets HOME=/nullclaw-data; mount both for compatibility.
				HostPath:      homeDir,
				ContainerPath: "/nullclaw-data/.nullclaw",
				ReadOnly:      false,
			},
			{
				HostPath:      rc.AgentHostPath,
				ContainerPath: "/claw/AGENTS.md",
				ReadOnly:      true,
			},
			{
				HostPath:      clawdapusPath,
				ContainerPath: "/claw/CLAWDAPUS.md",
				ReadOnly:      true,
			},
		},
		Tmpfs:       []string{"/tmp"},
		ReadOnly:    true,
		Restart:     "on-failure",
		SkillDir:    "/claw/skills",
		SkillLayout: "",
		Healthcheck: &driver.Healthcheck{
			Test:     []string{"CMD-SHELL", "curl -fsS http://localhost:3000/health >/dev/null || exit 1"},
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
		return fmt.Errorf("nullclaw driver: post-apply check failed: no container ID")
	}

	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return fmt.Errorf("nullclaw driver: post-apply failed to create docker client: %w", err)
	}
	defer cli.Close()

	inspectCtx, cancelInspect := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancelInspect()
	info, err := cli.ContainerInspect(inspectCtx, opts.ContainerID)
	if err != nil {
		return fmt.Errorf("nullclaw driver: post-apply container inspect failed: %w", err)
	}
	if info.State == nil || !info.State.Running {
		status := "unknown"
		if info.State != nil && info.State.Status != "" {
			status = info.State.Status
		}
		return fmt.Errorf("nullclaw driver: post-apply check failed: container is not running (status: %s)", status)
	}

	if len(rc.Invocations) == 0 {
		return nil
	}

	existing, err := listExistingCronJobs(cli, opts.ContainerID)
	if err != nil {
		return fmt.Errorf("nullclaw driver: post-apply failed to list cron jobs: %w", err)
	}

	for _, inv := range rc.Invocations {
		if strings.TrimSpace(inv.Name) != "" {
			fmt.Printf("[claw] warning: nullclaw driver: INVOKE name %q is not supported by nullclaw cron CLI; ignoring\n", inv.Name)
		}
		if strings.TrimSpace(inv.To) != "" {
			fmt.Printf("[claw] warning: nullclaw driver: INVOKE to=%q is not supported by nullclaw cron CLI; ignoring\n", inv.To)
		}

		command, err := buildInvocationCommand(inv.Message)
		if err != nil {
			return fmt.Errorf("nullclaw driver: post-apply invalid INVOKE message: %w", err)
		}
		key := cronEntryKey(inv.Schedule, command)
		if _, exists := existing[key]; exists {
			fmt.Printf("[claw] nullclaw: cron already exists (schedule: %s)\n", inv.Schedule)
			continue
		}

		args := buildCronAddArgs(inv.Schedule, command)
		execCtx, cancelExec := context.WithTimeout(context.Background(), 20*time.Second)
		stdout, stderr, exitCode, execErr := execInContainer(execCtx, cli, opts.ContainerID, args)
		cancelExec()
		if execErr != nil {
			return fmt.Errorf("nullclaw driver: post-apply failed to add cron job (schedule: %s): %w", inv.Schedule, execErr)
		}
		if exitCode != 0 {
			detail := strings.TrimSpace(stderr)
			if detail == "" {
				detail = strings.TrimSpace(stdout)
			}
			if detail == "" {
				detail = "no output"
			}
			return fmt.Errorf("nullclaw driver: post-apply cron add failed (schedule: %s, exit: %d): %s", inv.Schedule, exitCode, detail)
		}

		existing[key] = struct{}{}
		fmt.Printf("[claw] nullclaw: registered cron job (schedule: %s)\n", inv.Schedule)
	}

	return nil
}

func (d *Driver) HealthProbe(ref driver.ContainerRef) (*driver.Health, error) {
	if ref.ContainerID == "" {
		return &driver.Health{OK: false, Detail: "no container ID"}, nil
	}

	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("nullclaw driver: health probe failed to create docker client: %w", err)
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
	return &driver.Health{OK: true, Detail: "container running"}, nil
}

func buildCronAddArgs(expression, command string) []string {
	return []string{"nullclaw", "cron", "add", expression, command}
}

func buildInvocationCommand(message string) (string, error) {
	trimmed := strings.TrimSpace(message)
	if trimmed == "" {
		return "", fmt.Errorf("empty invocation message")
	}
	return "nullclaw agent -m " + shellQuote(trimmed), nil
}

func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}

func cronEntryKey(expression, command string) string {
	return strings.TrimSpace(expression) + "\x1f" + strings.TrimSpace(command)
}

func parseCronListOutput(text string) map[string]struct{} {
	out := make(map[string]struct{})
	lines := strings.Split(text, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !strings.Contains(trimmed, " | ") || !strings.Contains(trimmed, "cmd:") {
			continue
		}
		parts := strings.Split(trimmed, "|")
		if len(parts) < 2 {
			continue
		}
		expr := strings.TrimSpace(parts[1])
		cmdIdx := strings.LastIndex(trimmed, "cmd:")
		if cmdIdx < 0 {
			continue
		}
		cmd := strings.TrimSpace(trimmed[cmdIdx+len("cmd:"):])
		if expr == "" || cmd == "" {
			continue
		}
		out[cronEntryKey(expr, cmd)] = struct{}{}
	}
	return out
}

func listExistingCronJobs(cli *client.Client, containerID string) (map[string]struct{}, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	stdout, stderr, exitCode, err := execInContainer(ctx, cli, containerID, []string{"nullclaw", "cron", "list"})
	if err != nil {
		return nil, err
	}
	if exitCode != 0 {
		detail := strings.TrimSpace(stderr)
		if detail == "" {
			detail = strings.TrimSpace(stdout)
		}
		if detail == "" {
			detail = "no output"
		}
		return nil, fmt.Errorf("cron list failed (exit: %d): %s", exitCode, detail)
	}
	return parseCronListOutput(stdout + "\n" + stderr), nil
}

func execInContainer(ctx context.Context, cli *client.Client, containerID string, cmd []string) (string, string, int, error) {
	execCfg := types.ExecConfig{
		Cmd:          cmd,
		AttachStdout: true,
		AttachStderr: true,
	}
	execID, err := cli.ContainerExecCreate(ctx, containerID, execCfg)
	if err != nil {
		return "", "", -1, fmt.Errorf("exec create failed: %w", err)
	}

	resp, err := cli.ContainerExecAttach(ctx, execID.ID, types.ExecStartCheck{})
	if err != nil {
		return "", "", -1, fmt.Errorf("exec attach failed: %w", err)
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
			return "", "", -1, fmt.Errorf("exec read failed: %w", copyErr)
		}
	case <-ctx.Done():
		resp.Close()
		return "", "", -1, fmt.Errorf("exec timed out")
	}

	execInspect, err := cli.ContainerExecInspect(ctx, execID.ID)
	if err != nil {
		return "", "", -1, fmt.Errorf("exec inspect failed: %w", err)
	}
	return stdoutBuf.String(), stderrBuf.String(), execInspect.ExitCode, nil
}
