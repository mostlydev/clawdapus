package nanoclaw

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/docker/docker/client"
	"github.com/mostlydev/clawdapus/internal/driver"
	"github.com/mostlydev/clawdapus/internal/driver/shared"
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
	podName := opts.PodName
	if podName == "" {
		podName = rc.ServiceName
	}

	clawdapusMd := shared.GenerateClawdapusMD(rc, podName)
	clawdapusPath := filepath.Join(opts.RuntimeDir, "CLAWDAPUS.md")
	if err := os.WriteFile(clawdapusPath, []byte(clawdapusMd), 0644); err != nil {
		return nil, fmt.Errorf("nanoclaw driver: write CLAWDAPUS.md: %w", err)
	}

	mounts := []driver.Mount{
		{HostPath: rc.AgentHostPath, ContainerPath: "/workspace/AGENTS.md", ReadOnly: true},
		{HostPath: clawdapusPath, ContainerPath: "/workspace/CLAWDAPUS.md", ReadOnly: true},
		{HostPath: "/var/run/docker.sock", ContainerPath: "/var/run/docker.sock", ReadOnly: false},
	}

	env := map[string]string{"CLAW_MANAGED": "true"}
	if len(rc.Cllama) > 0 {
		firstProxy := fmt.Sprintf("http://cllama-%s:8080/v1", rc.Cllama[0])
		env["ANTHROPIC_BASE_URL"] = firstProxy
		if rc.CllamaToken != "" {
			env["ANTHROPIC_API_KEY"] = rc.CllamaToken
		}
	}

	return &driver.MaterializeResult{
		Mounts:      mounts,
		Tmpfs:       []string{"/tmp"},
		ReadOnly:    false,
		Restart:     "on-failure",
		SkillDir:    "/home/node/.claude/skills",
		SkillLayout: "directory",
		Healthcheck: &driver.Healthcheck{
			Test:     []string{"CMD-SHELL", "pgrep -f 'node.*index' > /dev/null"},
			Interval: "30s",
			Timeout:  "10s",
			Retries:  3,
		},
		Environment: env,
	}, nil
}

func (d *Driver) PostApply(rc *driver.ResolvedClaw, opts driver.PostApplyOpts) error {
	if opts.ContainerID == "" {
		return fmt.Errorf("nanoclaw driver: post-apply check failed: no container ID")
	}
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return fmt.Errorf("nanoclaw driver: post-apply docker client: %w", err)
	}
	defer cli.Close()
	info, err := cli.ContainerInspect(context.Background(), opts.ContainerID)
	if err != nil {
		return fmt.Errorf("nanoclaw driver: post-apply inspect: %w", err)
	}
	if !info.State.Running {
		cid := opts.ContainerID
		if len(cid) > 12 {
			cid = cid[:12]
		}
		return fmt.Errorf("nanoclaw driver: container %s not running (status: %s)", cid, info.State.Status)
	}
	return nil
}

func (d *Driver) HealthProbe(ref driver.ContainerRef) (*driver.Health, error) {
	if ref.ContainerID == "" {
		return &driver.Health{OK: false, Detail: "no container ID"}, nil
	}
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("nanoclaw driver: health docker client: %w", err)
	}
	defer cli.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	info, err := cli.ContainerInspect(ctx, ref.ContainerID)
	if err != nil {
		return &driver.Health{OK: false, Detail: fmt.Sprintf("inspect: %v", err)}, nil
	}
	if info.State == nil || !info.State.Running {
		status := "unknown"
		if info.State != nil {
			status = info.State.Status
		}
		return &driver.Health{OK: false, Detail: fmt.Sprintf("not running (%s)", status)}, nil
	}
	return &driver.Health{OK: true, Detail: "container running"}, nil
}
