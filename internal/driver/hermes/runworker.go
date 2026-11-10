package hermes

import (
	_ "embed"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/mostlydev/clawdapus/internal/cllama"
	"github.com/mostlydev/clawdapus/internal/driver"
	"github.com/mostlydev/clawdapus/internal/driver/shared"
	"gopkg.in/yaml.v3"
)

// The task-home per-run contract (ADR-027) is pinned against hermes-base
// v2026.6.19: `hermes chat -Q -q <task> [--resume <id>]` prints
// `session_id: <id>` on stderr, exits non-zero on failure, and stores the
// conversation in $HERMES_HOME/state.db.

//go:embed runwrap/wrapper.py
var runWrapper string

//go:embed runwrap/gate.py
var runGate string

//go:embed runwrap/meter.py
var runMeter string

const (
	// RunResultMarker starts the wrapper's result frame on stdout.
	RunResultMarker = "\x1eclaw-run-result\n"

	runGatePort     = "8080"
	runProviderName = "claw"
	// runKeyEnv names the environment variable the wrapper gives Hermes the
	// run's cllama token in. It must match wrapper.py.
	runKeyEnv      = "CLAW_MODEL_KEY"
	runToolsets    = "terminal,file,memory,skills,todo"
	runInputLimit  = 120 << 10
	runTmpfs       = "/tmp:mode=1777,size=256m"
	runQuestionRel = ".claw-run/question.md"
)

// SupportsRunProfile reports which per-run profiles Hermes compiles: only
// the task-home profile. A stateless one-shot Hermes worker is not supported.
func (d *Driver) SupportsRunProfile(taskHome bool) bool { return taskHome }

// ValidateRunWorker is separate from Validate, which requires a channel
// handle that a per-run worker never has.
func (d *Driver) ValidateRunWorker(rc *driver.ResolvedClaw) error {
	if len(rc.ProviderPreferences) > 0 {
		return fmt.Errorf("hermes driver: task-home profile does not project x-claw.provider-preferences yet")
	}
	if len(rc.ReasoningEffort) > 0 {
		return fmt.Errorf("hermes driver: task-home profile does not project x-claw.reasoning-effort yet")
	}
	if rc.AgentHostPath == "" {
		return fmt.Errorf("hermes driver: no agent host path specified (no contract, no start)")
	}
	if _, err := os.Stat(rc.AgentHostPath); err != nil {
		return fmt.Errorf("hermes driver: agent file %q not found: %w", rc.AgentHostPath, err)
	}
	if len(rc.Cllama) == 0 {
		return fmt.Errorf("hermes driver: per-run workers require cllama")
	}
	modelRef, err := shared.PrimaryModelRef(rc.Models)
	if err != nil {
		return fmt.Errorf("hermes driver: %w", err)
	}
	if _, _, ok := shared.SplitModelRef(modelRef); !ok {
		return fmt.Errorf("hermes driver: invalid MODEL primary %q (expected provider/model)", modelRef)
	}
	if len(rc.Handles) > 0 {
		return fmt.Errorf("hermes driver: per-run workers do not support HANDLE")
	}
	for _, cmd := range rc.Configures {
		if _, _, err := shared.ParseConfigSetCommand(cmd, "hermes"); err != nil {
			return fmt.Errorf("hermes driver: unsupported CONFIGURE command %q: %w", cmd, err)
		}
	}
	return nil
}

func (d *Driver) CompileRunWorker(rc *driver.ResolvedClaw, opts driver.RunWorkerOpts) (*driver.RunWorkerSpec, error) {
	if err := d.ValidateRunWorker(rc); err != nil {
		return nil, err
	}
	if opts.Home == nil || strings.TrimSpace(opts.Home.Mount) == "" {
		return nil, fmt.Errorf("hermes driver: per-run workers require x-claw.run.home")
	}
	if opts.MaxResultBytes <= 0 {
		return nil, fmt.Errorf("hermes driver: per-run worker needs a result limit")
	}
	home := opts.Home.Mount
	maxBytes := opts.Home.MaxBytes
	if maxBytes <= 0 {
		return nil, fmt.Errorf("hermes driver: task-home worker requires a positive storage watchdog threshold")
	}
	workspace := home + "/workspace"

	// The worker reaches cllama only through its run's gate, so the model
	// base URL names the gate and the gate names cllama.
	proxy, err := url.Parse(cllama.ProxyBaseURL(rc.Cllama[0]))
	if err != nil || proxy.Hostname() == "" || proxy.Port() == "" {
		return nil, fmt.Errorf("hermes driver: cannot derive the cllama address")
	}
	gateURL := "http://" + driver.GateServiceName + ":" + runGatePort + proxy.Path

	configData, err := generateRunConfig(rc, gateURL, workspace)
	if err != nil {
		return nil, err
	}
	agentContent, err := os.ReadFile(rc.AgentHostPath)
	if err != nil {
		return nil, fmt.Errorf("hermes driver: read agent contract: %w", err)
	}
	podName := opts.PodName
	if podName == "" {
		podName = rc.ServiceName
	}
	agents := strings.TrimSpace(string(agentContent)) + "\n\n---\n\n" +
		strings.TrimSpace(shared.GenerateClawdapusMD(rc, podName)) + "\n\n---\n\n" +
		runProtocol(home)
	inputs := map[string]string{
		"CLAW_RUN_TASK":   driver.RunPlaceholderTask,
		"CLAW_RUN_TOKEN":  driver.RunPlaceholderCllamaToken,
		"CLAW_RUN_CONFIG": string(configData),
		"CLAW_RUN_AGENTS": agents,
		"CLAW_RUN_SOUL":   managedDefaultAgentIdentity + "\n",
	}
	for name, value := range inputs {
		if len(value) > runInputLimit {
			return nil, fmt.Errorf("hermes driver: per-run input %s is %d bytes (limit %d)", name, len(value), runInputLimit)
		}
	}
	return &driver.RunWorkerSpec{
		Entrypoint: []string{"python3", "-I", "-S", "-c", runWrapper},
		Command:    []string{"${CLAW_RUN_TASK}"},
		Environment: map[string]string{
			"CLAW_MANAGED":                "true",
			"CLAW_RUN_HOME":               home,
			"CLAW_RUN_MAX_RESULT_BYTES":   strconv.Itoa(opts.MaxResultBytes),
			"CLAW_RUN_TOOLSETS":           runToolsets,
			"HOME":                        home,
			"HERMES_HOME":                 home + "/hermes",
			"HERMES_YOLO_MODE":            "1",
			"HERMES_ACCEPT_HOOKS":         "1",
			hermesDefaultAgentIdentityEnv: managedDefaultAgentIdentity,
			"NO_COLOR":                    "1",
			"NO_PROXY":                    driver.GateServiceName,
			"PYTHONDONTWRITEBYTECODE":     "1",
			"TERMINAL_CWD":                workspace,
			"TERMINAL_ENV":                "local",
			"XDG_STATE_HOME":              hermesDefaultXDGStateHome,
		},
		Inputs: inputs,
		Tmpfs:  []string{runTmpfs},
		Result: driver.RunResultContract{Source: "stdout", Marker: RunResultMarker, Header: driver.RunResultHeaderJSONLine},
		Home:   &driver.RunHomeSpec{Mount: home, MaxBytes: maxBytes},
		Meter: &driver.RunGateSpec{
			Entrypoint:  []string{"python3", "-I", "-S", "-c", runMeter},
			Command:     []string{home, strconv.FormatInt(maxBytes, 10)},
			Environment: map[string]string{"PYTHONDONTWRITEBYTECODE": "1"},
		},
		Gate: &driver.RunGateSpec{
			Entrypoint: []string{"python3", "-c", runGate},
			Command:    []string{proxy.Hostname(), proxy.Port(), runGatePort},
			Environment: map[string]string{
				"PYTHONDONTWRITEBYTECODE": "1",
			},
		},
	}, nil
}

// runProtocol tells the worker how a task-home run begins and ends.
func runProtocol(home string) string {
	return "## Task home\n\n" +
		"You are working on one task across several runs. `" + home + "` is yours alone and persists between runs: " +
		"your files are in `" + home + "/workspace`, and your earlier conversation is resumed for you. " +
		"Nothing outside `" + home + "` is kept.\n\n" +
		"Each run is one turn. Your final message is returned as the run's result.\n\n" +
		"To hand a file to the person who gave you the task, put it in `" + home + "/deliverables/`, directly in that " +
		"directory with a plain name. Files there of up to 512 KiB can be collected by them; nothing else in your home can.\n\n" +
		"If you cannot continue without an answer from a person, write the question to `" + home + "/" + runQuestionRel + "` " +
		"and end your turn. The answer arrives as your next message in a later run. " +
		"Do not write that file for any other reason.\n"
}

// generateRunConfig emits the non-secret Hermes config for a run. The run's
// token is not in it: the wrapper passes it to Hermes through the process
// environment only. Managed keys are asserted after CONFIGURE so an image
// directive cannot reroute the model or the terminal.
func generateRunConfig(rc *driver.ResolvedClaw, gateURL, workspace string) ([]byte, error) {
	modelRef, err := shared.PrimaryModelRef(rc.Models)
	if err != nil {
		return nil, fmt.Errorf("hermes driver: %w", err)
	}
	config := map[string]any{}
	for _, cmd := range rc.Configures {
		path, value, err := shared.ParseConfigSetCommand(cmd, "hermes")
		if err != nil {
			return nil, fmt.Errorf("hermes driver: per-run config: %w", err)
		}
		if err := shared.SetPath(config, path, value); err != nil {
			return nil, fmt.Errorf("hermes driver: per-run config: %w", err)
		}
	}
	for _, key := range []string{"platforms", "platform_toolsets", "cron", "custom_providers"} {
		delete(config, key)
	}
	// Hermes sends OPENAI_API_KEY only to openai.com hosts. A named custom
	// provider with key_env reads the run token from the process environment
	// instead, so the token is never written into config.
	config["custom_providers"] = []any{map[string]any{"name": runProviderName, "base_url": gateURL, "key_env": runKeyEnv}}
	config["model"] = map[string]any{"default": modelRef, "provider": runProviderName}
	config["approvals"] = map[string]any{"mode": "off"}
	config["terminal"] = map[string]any{"backend": "local", "cwd": workspace, "timeout": 180}
	config["display"] = map[string]any{"memory_notifications": "off"}
	return yaml.Marshal(config)
}
