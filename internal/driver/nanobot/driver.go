package nanobot

import (
	"context"
	"encoding/json"
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
	driver.Register("nanobot", &Driver{})
}

func (d *Driver) Validate(rc *driver.ResolvedClaw) error {
	if rc.AgentHostPath == "" {
		return fmt.Errorf("nanobot driver: no agent host path specified (no contract, no start)")
	}
	if _, err := os.Stat(rc.AgentHostPath); err != nil {
		return fmt.Errorf("nanobot driver: agent file %q not found: %w", rc.AgentHostPath, err)
	}

	modelRef, err := primaryModelRef(rc.Models)
	if err != nil {
		return err
	}
	provider, _, ok := shared.SplitModelRef(modelRef)
	if !ok {
		return fmt.Errorf("nanobot driver: invalid MODEL primary %q (expected provider/model)", modelRef)
	}

	for _, cmd := range rc.Configures {
		if _, _, err := parseConfigSetCommand(cmd); err != nil {
			return fmt.Errorf("nanobot driver: unsupported CONFIGURE command %q: %w", cmd, err)
		}
	}

	for platform := range rc.Handles {
		switch strings.ToLower(platform) {
		case "discord":
			if shared.ResolveEnvTokenFromMap(rc.Environment, "DISCORD_BOT_TOKEN") == "" {
				return fmt.Errorf("nanobot driver: HANDLE discord requires DISCORD_BOT_TOKEN in service environment")
			}
		case "telegram":
			if shared.ResolveEnvTokenFromMap(rc.Environment, "TELEGRAM_BOT_TOKEN") == "" {
				return fmt.Errorf("nanobot driver: HANDLE telegram requires TELEGRAM_BOT_TOKEN in service environment")
			}
		case "slack":
			if shared.ResolveEnvTokenFromMap(rc.Environment, "SLACK_BOT_TOKEN") == "" {
				return fmt.Errorf("nanobot driver: HANDLE slack requires SLACK_BOT_TOKEN in service environment")
			}
		default:
			fmt.Printf("[claw] warning: nanobot driver has no HANDLE validation for platform %q; skipping\n", platform)
		}
	}

	if len(rc.Cllama) == 0 {
		llmProvider := shared.NormalizeProvider(provider)
		if !shared.ProviderAllowsEmptyAPIKey(llmProvider) {
			if key := shared.ResolveProviderAPIKey(llmProvider, rc.Environment); key == "" {
				expected := strings.Join(shared.ExpectedProviderKeys(llmProvider), ", ")
				return fmt.Errorf("nanobot driver: no API key found for provider %q (checked: %s)", llmProvider, expected)
			}
		}
	}

	return nil
}

func (d *Driver) Materialize(rc *driver.ResolvedClaw, opts driver.MaterializeOpts) (*driver.MaterializeResult, error) {
	configData, err := GenerateConfig(rc)
	if err != nil {
		return nil, fmt.Errorf("nanobot driver: config generation failed: %w", err)
	}

	homeDir := filepath.Join(opts.RuntimeDir, "nanobot-home")
	if err := os.MkdirAll(homeDir, 0o700); err != nil {
		return nil, fmt.Errorf("nanobot driver: create nanobot home dir: %w", err)
	}
	configPath := filepath.Join(homeDir, "config.json")
	if err := os.WriteFile(configPath, configData, 0o644); err != nil {
		return nil, fmt.Errorf("nanobot driver: write config.json: %w", err)
	}

	workspaceDir := filepath.Join(homeDir, "workspace")
	if err := os.MkdirAll(workspaceDir, 0o700); err != nil {
		return nil, fmt.Errorf("nanobot driver: create workspace dir: %w", err)
	}

	agentContent, err := os.ReadFile(rc.AgentHostPath)
	if err != nil {
		return nil, fmt.Errorf("nanobot driver: read agent contract: %w", err)
	}
	podName := opts.PodName
	if podName == "" {
		podName = rc.ServiceName
	}
	clawdapusMD := shared.GenerateClawdapusMD(rc, podName)
	seededAgents := strings.TrimSpace(string(agentContent)) + "\n\n---\n\n" + strings.TrimSpace(clawdapusMD) + "\n"
	seedPath := filepath.Join(workspaceDir, "AGENTS.md")
	if err := os.WriteFile(seedPath, []byte(seededAgents), 0o644); err != nil {
		return nil, fmt.Errorf("nanobot driver: write seeded AGENTS.md: %w", err)
	}

	if len(rc.Invocations) > 0 {
		cronPath := filepath.Join(homeDir, "cron", "jobs.json")
		if err := os.MkdirAll(filepath.Dir(cronPath), 0o700); err != nil {
			return nil, fmt.Errorf("nanobot driver: create cron dir: %w", err)
		}
		cronJSON, err := generateCronJobsJSON(rc.Invocations)
		if err != nil {
			return nil, fmt.Errorf("nanobot driver: generate cron jobs: %w", err)
		}
		if err := os.WriteFile(cronPath, cronJSON, 0o644); err != nil {
			return nil, fmt.Errorf("nanobot driver: write cron jobs: %w", err)
		}
	}

	mounts := []driver.Mount{
		{
			HostPath:      homeDir,
			ContainerPath: "/root/.nanobot",
			ReadOnly:      false,
		},
	}
	if rc.PersonaHostPath != "" {
		mounts = append(mounts, driver.Mount{
			HostPath:      rc.PersonaHostPath,
			ContainerPath: "/root/.nanobot/workspace/persona",
			ReadOnly:      false,
		})
	}

	return &driver.MaterializeResult{
		Mounts:      mounts,
		Tmpfs:       []string{"/tmp"},
		ReadOnly:    true,
		Restart:     "on-failure",
		SkillDir:    "/root/.nanobot/workspace/skills",
		SkillLayout: "directory",
		Healthcheck: &driver.Healthcheck{
			Test:     []string{"CMD-SHELL", "pgrep -f 'nanobot gateway' > /dev/null"},
			Interval: "30s",
			Timeout:  "10s",
			Retries:  3,
		},
		Environment: map[string]string{
			"CLAW_MANAGED":     "true",
			"CLAW_PERSONA_DIR": "/root/.nanobot/workspace/persona",
		},
	}, nil
}

func (d *Driver) PostApply(rc *driver.ResolvedClaw, opts driver.PostApplyOpts) error {
	if opts.ContainerID == "" {
		return fmt.Errorf("nanobot driver: post-apply check failed: no container ID")
	}

	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return fmt.Errorf("nanobot driver: post-apply failed to create docker client: %w", err)
	}
	defer cli.Close()

	inspectCtx, cancelInspect := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancelInspect()

	info, err := cli.ContainerInspect(inspectCtx, opts.ContainerID)
	if err != nil {
		return fmt.Errorf("nanobot driver: post-apply container inspect failed: %w", err)
	}
	if info.State == nil || !info.State.Running {
		status := "unknown"
		if info.State != nil && info.State.Status != "" {
			status = info.State.Status
		}
		return fmt.Errorf("nanobot driver: post-apply check failed: container is not running (status: %s)", status)
	}
	return nil
}

func (d *Driver) HealthProbe(ref driver.ContainerRef) (*driver.Health, error) {
	if ref.ContainerID == "" {
		return &driver.Health{OK: false, Detail: "no container ID"}, nil
	}

	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("nanobot driver: health probe failed to create docker client: %w", err)
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

type nanobotCronStore struct {
	Version int              `json:"version"`
	Jobs    []nanobotCronJob `json:"jobs"`
}

type nanobotCronJob struct {
	Name     string               `json:"name"`
	Schedule nanobotCronSchedule  `json:"schedule"`
	Payload  nanobotCronPayload   `json:"payload"`
	State    nanobotCronJobStatus `json:"state"`
}

type nanobotCronSchedule struct {
	Kind       string `json:"kind"`
	Expression string `json:"expression"`
}

type nanobotCronPayload struct {
	Kind    string `json:"kind"`
	Message string `json:"message"`
	Deliver bool   `json:"deliver"`
	To      string `json:"to,omitempty"`
}

type nanobotCronJobStatus struct {
	Enabled bool `json:"enabled"`
}

func generateCronJobsJSON(invocations []driver.Invocation) ([]byte, error) {
	store := nanobotCronStore{
		Version: 1,
		Jobs:    make([]nanobotCronJob, 0, len(invocations)),
	}

	for i, inv := range invocations {
		expr := strings.TrimSpace(inv.Schedule)
		if !isFiveFieldCron(expr) {
			return nil, fmt.Errorf("invocation %d has invalid cron expression %q (expected 5 fields)", i+1, inv.Schedule)
		}

		message := strings.TrimSpace(inv.Message)
		if message == "" {
			return nil, fmt.Errorf("invocation %d has empty message", i+1)
		}

		name := strings.TrimSpace(inv.Name)
		if name == "" {
			name = fmt.Sprintf("invoke-%02d", i+1)
		}

		to := strings.TrimSpace(inv.To)
		store.Jobs = append(store.Jobs, nanobotCronJob{
			Name: name,
			Schedule: nanobotCronSchedule{
				Kind:       "cron",
				Expression: expr,
			},
			Payload: nanobotCronPayload{
				Kind:    "agent_turn",
				Message: message,
				Deliver: to != "",
				To:      to,
			},
			State: nanobotCronJobStatus{Enabled: true},
		})
	}

	return json.MarshalIndent(store, "", "  ")
}

func isFiveFieldCron(expr string) bool {
	return len(strings.Fields(strings.TrimSpace(expr))) == 5
}
