package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	schedulepkg "github.com/mostlydev/clawdapus/internal/schedule"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

var (
	apiPrincipalName string
	apiExecTimeout   time.Duration

	schedulePauseUntil      string
	schedulePauseReason     string
	scheduleFireBypassWhen  bool
	scheduleFireBypassPause bool

	runClawAPIComposeCommand = runClawAPIComposeCommandDefault
)

type composeServiceIndex struct {
	Services map[string]map[string]any `yaml:"services"`
}

const (
	httpMethodGet         = "GET"
	httpMethodPost        = "POST"
	clawAPIServiceName    = "claw-api"
	defaultAPIExecTimeout = 15 * time.Second
)

type clawAPIRequestBudget struct {
	requestTimeout time.Duration
	execTimeout    time.Duration
}

var apiCmd = &cobra.Command{
	Use:   "api",
	Short: "Call the in-pod governance API through docker compose exec",
	Long: "Calls claw-api from the host by tunneling through `docker compose exec` into the in-pod claw-api container.\n\n" +
		"Security model: Docker access is pod-admin access. The `--principal` flag selects which in-container principal to use from claw-api's principals.json; it is not a host-side access boundary.",
}

var apiScheduleCmd = &cobra.Command{
	Use:   "schedule",
	Short: "Inspect and control scheduled invocations through claw-api",
}

var apiScheduleListCmd = &cobra.Command{
	Use:   "list",
	Short: "List scheduled invocations and current state",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runScheduleRequest(cmd.OutOrStdout(), httpMethodGet, "/schedule", nil)
	},
}

var apiScheduleGetCmd = &cobra.Command{
	Use:   "get <id>",
	Short: "Show one scheduled invocation",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runScheduleRequest(cmd.OutOrStdout(), httpMethodGet, "/schedule/"+strings.TrimSpace(args[0]), nil)
	},
}

var apiSchedulePauseCmd = &cobra.Command{
	Use:   "pause <id>",
	Short: "Pause one scheduled invocation",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		body := map[string]any{}
		if until := strings.TrimSpace(schedulePauseUntil); until != "" {
			body["until"] = until
		}
		if reason := strings.TrimSpace(schedulePauseReason); reason != "" {
			body["reason"] = reason
		}
		return runScheduleRequest(cmd.OutOrStdout(), httpMethodPost, "/schedule/"+strings.TrimSpace(args[0])+"/pause", body)
	},
}

var apiScheduleResumeCmd = &cobra.Command{
	Use:   "resume <id>",
	Short: "Resume one scheduled invocation",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runScheduleRequest(cmd.OutOrStdout(), httpMethodPost, "/schedule/"+strings.TrimSpace(args[0])+"/resume", nil)
	},
}

var apiScheduleSkipNextCmd = &cobra.Command{
	Use:   "skip-next <id>",
	Short: "Skip the next scheduled fire for one invocation",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runScheduleRequest(cmd.OutOrStdout(), httpMethodPost, "/schedule/"+strings.TrimSpace(args[0])+"/skip-next", nil)
	},
}

var apiScheduleClearSkipNextCmd = &cobra.Command{
	Use:   "clear-skip-next <id>",
	Short: "Clear a pending skip-next flag for one invocation",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runScheduleRequest(cmd.OutOrStdout(), httpMethodPost, "/schedule/"+strings.TrimSpace(args[0])+"/clear-skip-next", nil)
	},
}

var apiScheduleFireCmd = &cobra.Command{
	Use:   "fire <id>",
	Short: "Trigger an immediate fire for one invocation",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		body := map[string]any{}
		if scheduleFireBypassWhen {
			body["bypass_when"] = true
		}
		if scheduleFireBypassPause {
			body["bypass_pause"] = true
		}
		return runScheduleRequest(cmd.OutOrStdout(), httpMethodPost, "/schedule/"+strings.TrimSpace(args[0])+"/fire", body)
	},
}

func runScheduleRequest(w io.Writer, method, requestPath string, body any) error {
	generatedPath, err := resolveComposeGeneratedPath()
	if err != nil {
		return err
	}
	out, err := callClawAPICompose(generatedPath, apiPrincipalName, method, requestPath, body)
	if err != nil {
		return err
	}
	if w == nil {
		w = os.Stdout
	}
	_, err = w.Write(out)
	return err
}

func callClawAPICompose(composePath, principalName, method, requestPath string, body any) ([]byte, error) {
	if err := ensureComposeService(composePath, clawAPIServiceName); err != nil {
		return nil, err
	}
	args := []string{
		"compose", "-f", composePath,
		"exec", "-T", clawAPIServiceName,
		"/claw-api",
		"-request-method", strings.ToUpper(strings.TrimSpace(method)),
		"-request-path", strings.TrimSpace(requestPath),
		"-request-principal", defaultAPIPrincipal(principalName),
	}
	budget := clawAPIRequestBudgetFor(method, requestPath)
	if budget.requestTimeout > 0 {
		args = append(args, "-request-timeout", budget.requestTimeout.String())
	}
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal request body: %w", err)
		}
		if string(raw) != "null" && string(raw) != "{}" {
			args = append(args, "-request-body", string(raw))
		}
	}
	execTimeout := budget.execTimeout
	if apiExecTimeout > 0 {
		execTimeout = apiExecTimeout
	}
	out, err := runClawAPIComposeCommand(execTimeout, args...)
	if err != nil {
		return nil, formatComposeOutputError("docker compose exec "+clawAPIServiceName, err, out)
	}
	return out, nil
}

func clawAPIRequestBudgetFor(method, requestPath string) clawAPIRequestBudget {
	budget := clawAPIRequestBudget{execTimeout: defaultAPIExecTimeout}
	if !strings.EqualFold(strings.TrimSpace(method), httpMethodPost) {
		return budget
	}
	path := strings.SplitN(strings.TrimSpace(requestPath), "?", 2)[0]
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) == 3 && parts[0] == "schedule" && parts[1] != "" && parts[2] == "fire" {
		budget.requestTimeout = schedulepkg.ManualFireRequestTimeout
		budget.execTimeout = schedulepkg.ManualFireTransportTimeout
	}
	return budget
}

func defaultAPIPrincipal(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "claw-scheduler"
	}
	return name
}

func ensureComposeService(composePath, service string) error {
	raw, err := os.ReadFile(composePath)
	if err != nil {
		return fmt.Errorf("read compose file: %w", err)
	}
	var parsed composeServiceIndex
	if err := yaml.Unmarshal(raw, &parsed); err != nil {
		return fmt.Errorf("parse compose file: %w", err)
	}
	if _, ok := parsed.Services[service]; ok {
		return nil
	}
	if service == clawAPIServiceName {
		return fmt.Errorf("pod does not include %s (run 'claw up' with x-claw.master or pod-level x-claw.invoke)", clawAPIServiceName)
	}
	return fmt.Errorf("service %q not found in compose.generated.yml", service)
}

func runClawAPIComposeCommandDefault(timeout time.Duration, args ...string) ([]byte, error) {
	if timeout <= 0 {
		timeout = defaultAPIExecTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", args...)
	out, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		return out, fmt.Errorf("timed out after %s", timeout)
	}
	return out, err
}

func init() {
	apiCmd.PersistentFlags().StringVar(&apiPrincipalName, "principal", "claw-scheduler", "Principal name inside claw-api principals.json to use for the request; not a host-side access boundary")
	apiCmd.PersistentFlags().DurationVar(&apiExecTimeout, "exec-timeout", 0, "Maximum time to wait for the docker compose exec transport (0 selects an operation-specific default)")

	apiSchedulePauseCmd.Flags().StringVar(&schedulePauseUntil, "until", "", "Pause until this RFC3339 timestamp instead of indefinitely")
	apiSchedulePauseCmd.Flags().StringVar(&schedulePauseReason, "reason", "", "Optional operator reason recorded with the pause")
	apiScheduleFireCmd.Flags().BoolVar(&scheduleFireBypassWhen, "bypass-when", false, "Fire even if the calendar gate is closed")
	apiScheduleFireCmd.Flags().BoolVar(&scheduleFireBypassPause, "bypass-pause", false, "Fire even if the invocation is paused")

	apiScheduleCmd.AddCommand(
		apiScheduleListCmd,
		apiScheduleGetCmd,
		apiSchedulePauseCmd,
		apiScheduleResumeCmd,
		apiScheduleSkipNextCmd,
		apiScheduleClearSkipNextCmd,
		apiScheduleFireCmd,
	)
	apiCmd.AddCommand(apiScheduleCmd)
	rootCmd.AddCommand(apiCmd)
}
