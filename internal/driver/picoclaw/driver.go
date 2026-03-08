package picoclaw

import (
	"bytes"
	"context"
	"encoding/json"
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

const (
	healthURL = "http://localhost:18790/health"
	readyURL  = "http://localhost:18790/ready"
)

type Driver struct{}

func init() {
	driver.Register("picoclaw", &Driver{})
}

func (d *Driver) Validate(rc *driver.ResolvedClaw) error {
	if rc.AgentHostPath == "" {
		return fmt.Errorf("picoclaw driver: no agent host path specified (no contract, no start)")
	}
	if _, err := os.Stat(rc.AgentHostPath); err != nil {
		return fmt.Errorf("picoclaw driver: agent file %q not found: %w", rc.AgentHostPath, err)
	}

	modelRef, err := primaryModelRef(rc.Models)
	if err != nil {
		return err
	}
	provider, _, ok := shared.SplitModelRef(modelRef)
	if !ok {
		return fmt.Errorf("picoclaw driver: invalid MODEL primary %q (expected provider/model)", modelRef)
	}

	for _, cmd := range rc.Configures {
		if _, _, err := parseConfigSetCommand(cmd); err != nil {
			return fmt.Errorf("picoclaw driver: unsupported CONFIGURE command %q: %w", cmd, err)
		}
	}

	enabledChannels := 0
	for rawPlatform := range rc.Handles {
		platform := normalizePlatform(rawPlatform)
		if !isSupportedPlatform(platform) {
			fmt.Printf("[claw] warning: picoclaw driver has no HANDLE validation for platform %q; skipping\n", rawPlatform)
			continue
		}

		enabledChannels++
		tokenVar := shared.PlatformTokenVar(platform)
		if tokenVar == "" {
			return fmt.Errorf("picoclaw driver: unsupported token mapping for HANDLE %q", rawPlatform)
		}
		if shared.ResolveEnvTokenFromMap(rc.Environment, tokenVar) == "" {
			return fmt.Errorf("picoclaw driver: HANDLE %s requires %s in service environment", platform, tokenVar)
		}
	}

	if enabledChannels == 0 {
		return fmt.Errorf("picoclaw driver: no channels enabled (add at least one supported HANDLE: %s)", strings.Join(supportedPlatforms, ", "))
	}

	if len(rc.Cllama) == 0 {
		llmProvider := shared.NormalizeProvider(provider)
		if !shared.ProviderAllowsEmptyAPIKey(llmProvider) {
			if key := shared.ResolveProviderAPIKey(llmProvider, rc.Environment); key == "" {
				expected := strings.Join(shared.ExpectedProviderKeys(llmProvider), ", ")
				return fmt.Errorf("picoclaw driver: no API key found for provider %q (checked: %s)", llmProvider, expected)
			}
		}
	}

	return nil
}

func (d *Driver) Materialize(rc *driver.ResolvedClaw, opts driver.MaterializeOpts) (*driver.MaterializeResult, error) {
	configData, err := GenerateConfig(rc)
	if err != nil {
		return nil, fmt.Errorf("picoclaw driver: config generation failed: %w", err)
	}

	homeDir := filepath.Join(opts.RuntimeDir, "picoclaw-home")
	if err := os.MkdirAll(homeDir, 0o700); err != nil {
		return nil, fmt.Errorf("picoclaw driver: create picoclaw home dir: %w", err)
	}
	configPath := filepath.Join(homeDir, "config.json")
	if err := os.WriteFile(configPath, configData, 0o644); err != nil {
		return nil, fmt.Errorf("picoclaw driver: write config.json: %w", err)
	}

	workspaceDir := filepath.Join(homeDir, "workspace")
	if err := os.MkdirAll(workspaceDir, 0o700); err != nil {
		return nil, fmt.Errorf("picoclaw driver: create workspace dir: %w", err)
	}

	agentContent, err := os.ReadFile(rc.AgentHostPath)
	if err != nil {
		return nil, fmt.Errorf("picoclaw driver: read agent contract: %w", err)
	}
	podName := opts.PodName
	if podName == "" {
		podName = rc.ServiceName
	}
	clawdapusMD := shared.GenerateClawdapusMD(rc, podName)
	seededAgents := strings.TrimSpace(string(agentContent)) + "\n\n---\n\n" + strings.TrimSpace(clawdapusMD) + "\n"
	seedPath := filepath.Join(workspaceDir, "AGENTS.md")
	if err := os.WriteFile(seedPath, []byte(seededAgents), 0o644); err != nil {
		return nil, fmt.Errorf("picoclaw driver: write seeded AGENTS.md: %w", err)
	}

	if len(rc.Invocations) > 0 {
		cronPath := filepath.Join(workspaceDir, "cron", "jobs.json")
		if err := os.MkdirAll(filepath.Dir(cronPath), 0o700); err != nil {
			return nil, fmt.Errorf("picoclaw driver: create cron dir: %w", err)
		}
		cronJSON, err := generateCronJobsJSON(rc.Invocations)
		if err != nil {
			return nil, fmt.Errorf("picoclaw driver: generate cron jobs: %w", err)
		}
		if err := os.WriteFile(cronPath, cronJSON, 0o644); err != nil {
			return nil, fmt.Errorf("picoclaw driver: write cron jobs: %w", err)
		}
	}

	mounts := []driver.Mount{
		{
			HostPath:      homeDir,
			ContainerPath: picoclawHomeDir,
			ReadOnly:      false,
		},
	}
	if rc.PersonaHostPath != "" {
		mounts = append(mounts, driver.Mount{
			HostPath:      rc.PersonaHostPath,
			ContainerPath: picoclawWorkspaceDir + "/persona",
			ReadOnly:      false,
		})
	}

	return &driver.MaterializeResult{
		Mounts:      mounts,
		Tmpfs:       []string{"/tmp"},
		ReadOnly:    true,
		Restart:     "on-failure",
		SkillDir:    picoclawWorkspaceDir + "/skills",
		SkillLayout: "directory",
		Healthcheck: &driver.Healthcheck{
			Test:     []string{"CMD-SHELL", "curl -fsS " + healthURL + " >/dev/null || exit 1"},
			Interval: "30s",
			Timeout:  "10s",
			Retries:  3,
		},
		Environment: map[string]string{
			"CLAW_MANAGED":     "true",
			"CLAW_PERSONA_DIR": picoclawWorkspaceDir + "/persona",
			"PICOCLAW_HOME":    picoclawHomeDir,
			"PICOCLAW_CONFIG":  picoclawHomeDir + "/config.json",
		},
	}, nil
}

func (d *Driver) PostApply(rc *driver.ResolvedClaw, opts driver.PostApplyOpts) error {
	if opts.ContainerID == "" {
		return fmt.Errorf("picoclaw driver: post-apply check failed: no container ID")
	}

	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return fmt.Errorf("picoclaw driver: post-apply failed to create docker client: %w", err)
	}
	defer cli.Close()

	inspectCtx, cancelInspect := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancelInspect()

	info, err := cli.ContainerInspect(inspectCtx, opts.ContainerID)
	if err != nil {
		return fmt.Errorf("picoclaw driver: post-apply container inspect failed: %w", err)
	}
	if info.State == nil || !info.State.Running {
		status := "unknown"
		if info.State != nil && info.State.Status != "" {
			status = info.State.Status
		}
		return fmt.Errorf("picoclaw driver: post-apply check failed: container is not running (status: %s)", status)
	}
	return nil
}

func (d *Driver) HealthProbe(ref driver.ContainerRef) (*driver.Health, error) {
	if ref.ContainerID == "" {
		return &driver.Health{OK: false, Detail: "no container ID"}, nil
	}

	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("picoclaw driver: health probe failed to create docker client: %w", err)
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

	healthStatus, healthDetail, err := probeStatusEndpoint(cli, ref.ContainerID, healthURL)
	if err != nil {
		return &driver.Health{OK: false, Detail: fmt.Sprintf("health endpoint probe failed: %v", err)}, nil
	}
	if healthStatus != "ok" {
		detail := healthDetail
		if detail == "" {
			detail = "no detail"
		}
		return &driver.Health{OK: false, Detail: fmt.Sprintf("/health returned status=%s: %s", healthStatus, detail)}, nil
	}

	readyStatus, readyDetail, readyErr := probeStatusEndpoint(cli, ref.ContainerID, readyURL)
	if readyErr == nil {
		if readyStatus != "ok" {
			detail := readyDetail
			if detail == "" {
				detail = "no detail"
			}
			return &driver.Health{OK: false, Detail: fmt.Sprintf("/health ok, /ready returned status=%s: %s", readyStatus, detail)}, nil
		}
		if readyDetail != "" {
			return &driver.Health{OK: true, Detail: fmt.Sprintf("/health ok, /ready ok: %s", readyDetail)}, nil
		}
		return &driver.Health{OK: true, Detail: "/health ok, /ready ok"}, nil
	}

	if healthDetail != "" {
		return &driver.Health{OK: true, Detail: fmt.Sprintf("/health ok: %s", healthDetail)}, nil
	}
	return &driver.Health{OK: true, Detail: "/health ok"}, nil
}

type probeResponse struct {
	Status  string `json:"status"`
	Detail  string `json:"detail,omitempty"`
	Message string `json:"message,omitempty"`
}

func parseProbeResponse(data string) (status string, detail string, err error) {
	trimmed := strings.TrimSpace(data)
	idx := strings.Index(trimmed, "{")
	if idx < 0 {
		return "", "", fmt.Errorf("no JSON object found in response")
	}

	var parsed probeResponse
	if err := json.Unmarshal([]byte(trimmed[idx:]), &parsed); err != nil {
		return "", "", fmt.Errorf("invalid JSON response: %w", err)
	}

	status = strings.ToLower(strings.TrimSpace(parsed.Status))
	detail = strings.TrimSpace(parsed.Detail)
	if detail == "" {
		detail = strings.TrimSpace(parsed.Message)
	}

	if status == "" {
		return "", detail, fmt.Errorf("response missing status field")
	}

	return status, detail, nil
}

func probeStatusEndpoint(cli *client.Client, containerID, url string) (status string, detail string, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	stdout, stderr, exitCode, execErr := execInContainer(ctx, cli, containerID, []string{"curl", "-fsS", url})
	if execErr != nil {
		return "", "", execErr
	}
	if exitCode != 0 {
		out := strings.TrimSpace(stderr)
		if out == "" {
			out = strings.TrimSpace(stdout)
		}
		if out == "" {
			out = "no output"
		}
		return "", "", fmt.Errorf("curl %s failed (exit: %d): %s", url, exitCode, out)
	}

	status, detail, parseErr := parseProbeResponse(stdout)
	if parseErr != nil {
		out := strings.TrimSpace(stdout)
		if out == "" {
			out = strings.TrimSpace(stderr)
		}
		return "", "", fmt.Errorf("parse %s response: %w (output: %s)", url, parseErr, out)
	}
	return status, detail, nil
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

type picoclawCronStore struct {
	Version int               `json:"version"`
	Jobs    []picoclawCronJob `json:"jobs"`
}

type picoclawCronJob struct {
	Name     string                `json:"name"`
	Schedule picoclawCronSchedule  `json:"schedule"`
	Payload  picoclawCronPayload   `json:"payload"`
	State    picoclawCronJobStatus `json:"state"`
}

type picoclawCronSchedule struct {
	Kind       string `json:"kind"`
	Expression string `json:"expression"`
}

type picoclawCronPayload struct {
	Kind    string `json:"kind"`
	Message string `json:"message"`
	To      string `json:"to,omitempty"`
}

type picoclawCronJobStatus struct {
	Enabled bool `json:"enabled"`
}

func generateCronJobsJSON(invocations []driver.Invocation) ([]byte, error) {
	store := picoclawCronStore{
		Version: 1,
		Jobs:    make([]picoclawCronJob, 0, len(invocations)),
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

		store.Jobs = append(store.Jobs, picoclawCronJob{
			Name: name,
			Schedule: picoclawCronSchedule{
				Kind:       "cron",
				Expression: expr,
			},
			Payload: picoclawCronPayload{
				Kind:    "agent_turn",
				Message: message,
				To:      strings.TrimSpace(inv.To),
			},
			State: picoclawCronJobStatus{Enabled: true},
		})
	}

	return json.MarshalIndent(store, "", "  ")
}

func isFiveFieldCron(expr string) bool {
	return len(strings.Fields(strings.TrimSpace(expr))) == 5
}
