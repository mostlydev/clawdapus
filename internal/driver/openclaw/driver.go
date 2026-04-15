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

const openclawHomeDir = "/root/.openclaw"
const openclawConfigDir = openclawHomeDir + "/config"
const openclawConfigPath = openclawConfigDir + "/openclaw.json"
const openclawCronDir = openclawHomeDir + "/cron"
const openclawWorkspaceTmpfs = "/claw:mode=1777,uid=0,gid=0"

// openclawStateTmpfs mounts the writable tmpfs at /root, NOT at /root/.openclaw.
// Most upstream openclaw base images (e.g. ghcr.io/openclaw/openclaw) ship a non-root
// runtime USER such as `node`, while leaving /root at its baked-in mode 0700 root:root
// from the image layer. A tmpfs at /root/.openclaw alone is unreachable for that user
// because traversing into /root requires execute on the parent, and the read-only image
// layer cannot be chmod'd at runtime. Mounting the tmpfs one level higher overlays /root
// with a fresh mode-1777 tmpfs that any user can traverse, while Docker still creates
// /root/.openclaw on top of it as the bind-mount target for the config directory. This
// keeps the canonical ~/.openclaw layout intact for both root- and non-root images.
const openclawStateTmpfs = "/root:mode=1777,uid=0,gid=0"

// openclawHomeTmpfs keeps ~/.openclaw itself writable after the /root overlay is in place.
// With only the /root tmpfs plus a bind mount at /root/.openclaw/config, Docker creates the
// intermediate /root/.openclaw directory as 0755 root:root. Non-root runtime users can then
// read the mounted config file but still fail on the first state write
// (`mkdir ~/.openclaw/agents`). Mounting a second tmpfs at ~/.openclaw fixes that while
// preserving the canonical upstream path layout and config bind mount.
const openclawHomeTmpfs = openclawHomeDir + ":mode=1777,uid=0,gid=0"

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
	memoryDir, err := shared.PreparePortableMemory(shared.ResolveStateDir(opts.RuntimeDir, opts.StateDir), opts.RuntimeDir)
	if err != nil {
		return nil, fmt.Errorf("openclaw driver: prepare portable memory: %w", err)
	}

	// Write config into its own subdirectory and bind-mount the whole directory.
	// openclaw performs atomic writes by creating a temp file alongside the config
	// (openclaw.json.<n>.<uuid>.tmp → rename). The directory must be writable for
	// that pattern to work; a read-only single-file bind-mount causes EROFS.
	configDir := filepath.Join(opts.RuntimeDir, "config")
	if err := os.MkdirAll(configDir, 0777); err != nil {
		return nil, fmt.Errorf("openclaw driver: create config dir: %w", err)
	}
	if err := os.Chmod(configDir, 0o777); err != nil {
		return nil, fmt.Errorf("openclaw driver: chmod config dir: %w", err)
	}
	configPath := filepath.Join(configDir, "openclaw.json")
	if err := os.WriteFile(configPath, configData, 0o666); err != nil {
		return nil, fmt.Errorf("openclaw driver: failed to write config: %w", err)
	}
	if err := os.Chmod(configPath, 0o666); err != nil {
		return nil, fmt.Errorf("openclaw driver: chmod config file: %w", err)
	}

	cronDir := filepath.Join(opts.RuntimeDir, "cron")
	if err := os.MkdirAll(cronDir, 0777); err != nil {
		return nil, fmt.Errorf("openclaw driver: create cron dir: %w", err)
	}
	if err := os.Chmod(cronDir, 0o777); err != nil {
		return nil, fmt.Errorf("openclaw driver: chmod cron dir: %w", err)
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

	agentMountPath, err := materializeEffectiveAgentContract(opts.RuntimeDir, rc.AgentHostPath, clawdapusMd)
	if err != nil {
		return nil, err
	}

	mounts := []driver.Mount{
		{
			// Bind-mount the directory (not the file) so openclaw can write temp files
			// alongside the config during atomic save operations.
			// This lives under the canonical ~/.openclaw home on purpose: state and config
			// share the upstream layout, but OPENCLAW_CONFIG_PATH still keeps config access
			// explicit rather than inferred from OPENCLAW_STATE_DIR.
			HostPath:      configDir,
			ContainerPath: openclawConfigDir,
			ReadOnly:      false,
		},
		{
			// Cron definitions and run logs live under ~/.openclaw/cron in current
			// OpenClaw builds. Mount the whole directory so the runner can atomically
			// rewrite jobs.json and append run history under cron/runs/.
			HostPath:      cronDir,
			ContainerPath: openclawCronDir,
			ReadOnly:      false,
		},
		{
			// Always mount as AGENTS.md so openclaw finds it at workspace root (/claw/AGENTS.md).
			HostPath:      agentMountPath,
			ContainerPath: "/claw/AGENTS.md",
			ReadOnly:      true,
		},
		{
			HostPath:      memoryDir,
			ContainerPath: shared.PortableMemoryDir,
			ReadOnly:      false,
		},
	}
	if rc.PersonaHostPath != "" {
		mounts = append(mounts, driver.Mount{
			HostPath:      rc.PersonaHostPath,
			ContainerPath: "/claw/persona",
			ReadOnly:      false,
		})
	}

	// Generate jobs.json if there are scheduled invocations.
	// OpenClaw resolves its cron store under ~/.openclaw/cron/jobs.json, separate
	// from the config directory. Keep the cron dir writable because the runtime
	// rewrites jobs atomically and appends run history below cron/runs/.
	if len(rc.Invocations) > 0 {
		jobsData, err := GenerateJobsJSON(rc)
		if err != nil {
			return nil, fmt.Errorf("openclaw driver: generate jobs.json: %w", err)
		}
		jobsPath := filepath.Join(cronDir, "jobs.json")
		if err := os.WriteFile(jobsPath, jobsData, 0o666); err != nil {
			return nil, fmt.Errorf("openclaw driver: write jobs.json: %w", err)
		}
		if err := os.Chmod(jobsPath, 0o666); err != nil {
			return nil, fmt.Errorf("openclaw driver: chmod jobs.json: %w", err)
		}
	}

	mounts = append(mounts, driver.Mount{
		HostPath:      clawdapusPath,
		ContainerPath: "/claw/CLAWDAPUS.md",
		ReadOnly:      true,
	})

	env := map[string]string{
		"CLAW_MANAGED":           "true",
		shared.PortableMemoryEnv: shared.PortableMemoryDir,
		"OPENCLAW_CONFIG_PATH":   openclawConfigPath,
		"OPENCLAW_STATE_DIR":     openclawHomeDir,
		// Keep HOME aligned with the canonical writable openclaw home so upstream plugins
		// and any os.UserHomeDir()/~ resolution land on the same tmpfs-backed path.
		"HOME": "/root",
	}
	if rc.PersonaHostPath != "" {
		env["CLAW_PERSONA_DIR"] = "/claw/persona"
	}

	return &driver.MaterializeResult{
		Mounts: mounts,
		Tmpfs: []string{
			// /claw is the runner workspace. Keep it writable so agent turns can persist
			// workspace artifacts like SOUL.md while read-only file mounts (AGENTS.md,
			// CLAWDAPUS.md, skills) still layer on top of the tmpfs.
			openclawWorkspaceTmpfs,
			"/tmp",
			"/run",
			// Tmpfs at /root (not /root/.openclaw) overlays the read-only image layer
			// with a writable mode-1777 directory so non-root runtime users can traverse
			// into ~/.openclaw regardless of the image-baked /root permissions.
			openclawStateTmpfs,
			// Tmpfs at ~/.openclaw keeps the canonical state root writable after Docker
			// creates the nested config bind mount at ~/.openclaw/config.
			openclawHomeTmpfs,
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
		Environment: env,
	}, nil
}

func materializeEffectiveAgentContract(runtimeDir, agentHostPath, clawdapusMd string) (string, error) {
	agentData, err := os.ReadFile(agentHostPath)
	if err != nil {
		return "", fmt.Errorf("openclaw driver: read agent contract: %w", err)
	}

	var combined strings.Builder
	combined.WriteString(strings.TrimRight(string(agentData), "\n"))

	if trimmed := strings.TrimSpace(clawdapusMd); trimmed != "" {
		combined.WriteString("\n\n")
		combined.WriteString("--- BEGIN: infrastructure_context (guide) ---\n\n")
		combined.WriteString("This infrastructure context was generated by Clawdapus.\n")
		combined.WriteString("Treat it as authoritative runtime environment data for this pod.\n")
		combined.WriteString("It is also mounted separately at `/claw/CLAWDAPUS.md`.\n\n")
		combined.WriteString(strings.TrimRight(clawdapusMd, "\n"))
		combined.WriteString("\n\n")
		combined.WriteString("--- END: infrastructure_context (guide) ---")
	}

	combined.WriteString("\n")

	effectivePath := filepath.Join(runtimeDir, "AGENTS.effective.md")
	if err := os.WriteFile(effectivePath, []byte(combined.String()), 0644); err != nil {
		return "", fmt.Errorf("openclaw driver: write effective AGENTS.md: %w", err)
	}
	return effectivePath, nil
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
