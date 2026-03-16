package hermes

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/docker/docker/client"
	"github.com/mostlydev/clawdapus/internal/driver"
	"github.com/mostlydev/clawdapus/internal/driver/shared"
)

type Driver struct{}

func init() {
	driver.Register("hermes", &Driver{})
}

func (d *Driver) Validate(rc *driver.ResolvedClaw) error {
	if rc.AgentHostPath == "" {
		return fmt.Errorf("hermes driver: no agent host path specified (no contract, no start)")
	}
	if _, err := os.Stat(rc.AgentHostPath); err != nil {
		return fmt.Errorf("hermes driver: agent file %q not found: %w", rc.AgentHostPath, err)
	}

	for _, cmd := range rc.Configures {
		if _, _, err := shared.ParseConfigSetCommand(cmd, "hermes"); err != nil {
			return fmt.Errorf("hermes driver: unsupported CONFIGURE command %q: %w", cmd, err)
		}
	}

	supported := 0
	for rawPlatform := range rc.Handles {
		switch platform := strings.ToLower(strings.TrimSpace(rawPlatform)); platform {
		case "discord":
			supported++
			if shared.ResolveEnvTokenFromMap(rc.Environment, "DISCORD_BOT_TOKEN") == "" {
				return fmt.Errorf("hermes driver: HANDLE discord requires DISCORD_BOT_TOKEN in service environment")
			}
		case "telegram":
			supported++
			if shared.ResolveEnvTokenFromMap(rc.Environment, "TELEGRAM_BOT_TOKEN") == "" {
				return fmt.Errorf("hermes driver: HANDLE telegram requires TELEGRAM_BOT_TOKEN in service environment")
			}
		case "slack":
			supported++
			if shared.ResolveEnvTokenFromMap(rc.Environment, "SLACK_BOT_TOKEN") == "" ||
				shared.ResolveEnvTokenFromMap(rc.Environment, "SLACK_APP_TOKEN") == "" {
				return fmt.Errorf("hermes driver: HANDLE slack requires SLACK_BOT_TOKEN and SLACK_APP_TOKEN in service environment")
			}
		default:
			return fmt.Errorf(
				"hermes driver: HANDLE %q is not supported in Hermes v1 (supported: %s)",
				rawPlatform,
				strings.Join(supportedPlatforms, ", "),
			)
		}
	}
	if supported == 0 {
		return fmt.Errorf("hermes driver: no supported HANDLE platforms enabled (add at least one of: %s)", strings.Join(supportedPlatforms, ", "))
	}

	for i, inv := range rc.Invocations {
		if !shared.IsFiveFieldCron(inv.Schedule) {
			return fmt.Errorf("hermes driver: INVOKE %d has invalid cron expression %q (expected 5 fields)", i+1, inv.Schedule)
		}
	}

	if _, err := resolveModelConfig(rc); err != nil {
		return err
	}
	return nil
}

func (d *Driver) Materialize(rc *driver.ResolvedClaw, opts driver.MaterializeOpts) (*driver.MaterializeResult, error) {
	modelCfg, err := resolveModelConfig(rc)
	if err != nil {
		return nil, fmt.Errorf("hermes driver: %w", err)
	}
	configData, err := GenerateConfig(rc, modelCfg)
	if err != nil {
		return nil, fmt.Errorf("hermes driver: config generation failed: %w", err)
	}
	envData, err := GenerateEnvFile(rc, modelCfg)
	if err != nil {
		return nil, fmt.Errorf("hermes driver: env generation failed: %w", err)
	}

	homeDir := filepath.Join(opts.RuntimeDir, "hermes-home")
	workspaceDir := filepath.Join(opts.RuntimeDir, "workspace")
	for _, dir := range []string{homeDir, workspaceDir, filepath.Join(homeDir, "cron"), filepath.Join(homeDir, "skills")} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, fmt.Errorf("hermes driver: create runtime dir %q: %w", dir, err)
		}
	}

	if err := os.WriteFile(filepath.Join(homeDir, "config.yaml"), configData, 0o644); err != nil {
		return nil, fmt.Errorf("hermes driver: write config.yaml: %w", err)
	}
	if err := os.WriteFile(filepath.Join(homeDir, ".env"), envData, 0o600); err != nil {
		return nil, fmt.Errorf("hermes driver: write .env: %w", err)
	}

	podName := opts.PodName
	if podName == "" {
		podName = rc.ServiceName
	}
	clawdapusMD := shared.GenerateClawdapusMD(rc, podName)
	if err := os.WriteFile(filepath.Join(workspaceDir, "CLAWDAPUS.md"), []byte(clawdapusMD), 0o644); err != nil {
		return nil, fmt.Errorf("hermes driver: write CLAWDAPUS.md: %w", err)
	}
	if _, err := WriteEffectiveAgents(workspaceDir, rc.AgentHostPath, clawdapusMD); err != nil {
		return nil, fmt.Errorf("hermes driver: %w", err)
	}

	if len(rc.Invocations) > 0 {
		jobsData, err := GenerateJobsJSON(rc)
		if err != nil {
			return nil, fmt.Errorf("hermes driver: generate jobs.json: %w", err)
		}
		if err := os.WriteFile(filepath.Join(homeDir, "cron", "jobs.json"), jobsData, 0o644); err != nil {
			return nil, fmt.Errorf("hermes driver: write jobs.json: %w", err)
		}
	}

	mounts := []driver.Mount{
		{
			HostPath:      homeDir,
			ContainerPath: hermesHomeDir,
			ReadOnly:      false,
		},
		{
			HostPath:      workspaceDir,
			ContainerPath: hermesWorkspaceDir,
			ReadOnly:      false,
		},
	}

	env := map[string]string{
		"CLAW_MANAGED":  "true",
		"HERMES_HOME":   hermesHomeDir,
		"MESSAGING_CWD": hermesWorkspaceDir,
		"TERMINAL_CWD":  hermesWorkspaceDir,
	}

	if rc.PersonaHostPath != "" {
		mounts = append(mounts, driver.Mount{
			HostPath:      rc.PersonaHostPath,
			ContainerPath: hermesPersonaDir,
			ReadOnly:      false,
		})
		env["CLAW_PERSONA_DIR"] = hermesPersonaDir
		if err := CopyPersonaSoul(rc.PersonaHostPath, homeDir); err != nil {
			return nil, fmt.Errorf("hermes driver: %w", err)
		}
	}

	return &driver.MaterializeResult{
		Mounts:      mounts,
		Tmpfs:       []string{"/tmp", "/run"},
		ReadOnly:    true,
		Restart:     "on-failure",
		SkillDir:    hermesHomeDir + "/skills",
		SkillLayout: "directory",
		Healthcheck: &driver.Healthcheck{
			Test:     []string{"CMD-SHELL", "hermes gateway status >/dev/null 2>&1 || pgrep -f 'hermes gateway' >/dev/null"},
			Interval: "30s",
			Timeout:  "10s",
			Retries:  3,
		},
		Environment: env,
	}, nil
}

func (d *Driver) PostApply(rc *driver.ResolvedClaw, opts driver.PostApplyOpts) error {
	if opts.ContainerID == "" {
		return fmt.Errorf("hermes driver: post-apply check failed: no container ID")
	}

	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return fmt.Errorf("hermes driver: post-apply failed to create docker client: %w", err)
	}
	defer cli.Close()

	info, err := cli.ContainerInspect(context.Background(), opts.ContainerID)
	if err != nil {
		return fmt.Errorf("hermes driver: post-apply container inspect failed: %w", err)
	}
	if info.State == nil || !info.State.Running {
		status := "unknown"
		if info.State != nil && info.State.Status != "" {
			status = info.State.Status
		}
		return fmt.Errorf("hermes driver: post-apply check failed: container is not running (status: %s)", status)
	}
	return nil
}

func (d *Driver) HealthProbe(ref driver.ContainerRef) (*driver.Health, error) {
	if ref.ContainerID == "" {
		return &driver.Health{OK: false, Detail: "no container ID"}, nil
	}

	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("hermes driver: health probe failed to create docker client: %w", err)
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

	stdout, stderr, exitCode, err := shared.ExecInContainer(
		ctx,
		cli,
		ref.ContainerID,
		[]string{"sh", "-lc", "hermes gateway status >/dev/null 2>&1 || pgrep -f 'hermes gateway' >/dev/null"},
	)
	if err != nil {
		return &driver.Health{OK: false, Detail: err.Error()}, nil
	}
	if exitCode != 0 {
		detail := strings.TrimSpace(stderr)
		if detail == "" {
			detail = strings.TrimSpace(stdout)
		}
		if detail == "" {
			detail = "gateway status probe failed"
		}
		return &driver.Health{OK: false, Detail: detail}, nil
	}

	return &driver.Health{OK: true, Detail: "gateway status ok"}, nil
}
