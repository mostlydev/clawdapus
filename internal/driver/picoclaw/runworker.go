package picoclaw

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/mostlydev/clawdapus/internal/cllama"
	"github.com/mostlydev/clawdapus/internal/driver"
	"github.com/mostlydev/clawdapus/internal/driver/shared"
)

// The per-run contract is pinned against PicoClaw v0.2.7 one-shot mode:
// `picoclaw agent -m <task> -s agent:main:<key>` exits 0 on success and 1 on
// provider/config failure, and prints the answer on stdout after "\n🦞 ".
const (
	runHomeDir      = "/tmp/home"
	runPicoclawHome = runHomeDir + "/.picoclaw"
	runWorkspaceDir = runHomeDir + "/workspace"
	runResultMarker = "\n🦞 "
	// runInputLimit keeps each interpolated env value under Linux's 128 KiB
	// MAX_ARG_STRLEN.
	runInputLimit = 120 << 10
)

// runDisabledTools lists PicoClaw native tools that can act outside the
// conversation. A per-run worker's business tools are cllama-managed only.
var runDisabledTools = []string{
	"append_file", "edit_file", "exec", "find_skills", "install_skill",
	"spawn", "subagent", "web_fetch", "write_file",
}

// runWrapper writes the config and contract from env into tmpfs, removes the
// config from the environment, then execs PicoClaw. Task and session arrive as
// positional arguments and are never evaluated by the shell.
const runWrapper = `umask 077 && mkdir -p "$PICOCLAW_HOME" "$CLAW_RUN_WORKSPACE" && ` +
	`printf '%s' "$CLAW_RUN_CONFIG" > "$PICOCLAW_CONFIG" && ` +
	`printf '%s' "$CLAW_RUN_AGENTS" > "$CLAW_RUN_WORKSPACE/AGENTS.md" && ` +
	`unset CLAW_RUN_CONFIG CLAW_RUN_AGENTS && ` +
	`exec picoclaw agent -m "$1" -s "$2"`

func (d *Driver) ValidateRunWorker(rc *driver.ResolvedClaw) error {
	if rc.AgentHostPath == "" {
		return fmt.Errorf("picoclaw driver: no agent host path specified (no contract, no start)")
	}
	if _, err := os.Stat(rc.AgentHostPath); err != nil {
		return fmt.Errorf("picoclaw driver: agent file %q not found: %w", rc.AgentHostPath, err)
	}
	if len(rc.Cllama) == 0 {
		return fmt.Errorf("picoclaw driver: per-run workers require cllama")
	}
	modelRef, err := shared.PrimaryModelRef(rc.Models)
	if err != nil {
		return fmt.Errorf("picoclaw driver: %w", err)
	}
	if _, _, ok := shared.SplitModelRef(modelRef); !ok {
		return fmt.Errorf("picoclaw driver: invalid MODEL primary %q (expected provider/model)", modelRef)
	}
	if len(rc.Handles) > 0 {
		return fmt.Errorf("picoclaw driver: per-run workers do not support HANDLE")
	}
	for _, cmd := range rc.Configures {
		if _, _, err := shared.ParseConfigSetCommand(cmd, "picoclaw"); err != nil {
			return fmt.Errorf("picoclaw driver: unsupported CONFIGURE command %q: %w", cmd, err)
		}
	}
	for slot := range rc.ReasoningEffort {
		if _, ok := rc.Models[slot]; !ok {
			return fmt.Errorf("picoclaw driver: x-claw.reasoning-effort names model slot %q, which has no MODEL", slot)
		}
	}
	for slot := range rc.ProviderPreferences {
		ref, ok := rc.Models[slot]
		if !ok {
			return fmt.Errorf("picoclaw driver: x-claw.provider-preferences names model slot %q, which has no MODEL", slot)
		}
		if provider, _, _ := shared.SplitModelRef(strings.TrimSpace(ref)); provider != "openrouter" {
			return fmt.Errorf("picoclaw driver: x-claw.provider-preferences applies only to openrouter/* models; slot %q is %q", slot, ref)
		}
	}
	return nil
}

func (d *Driver) CompileRunWorker(rc *driver.ResolvedClaw, opts driver.RunWorkerOpts) (*driver.RunWorkerSpec, error) {
	if err := d.ValidateRunWorker(rc); err != nil {
		return nil, err
	}
	configData, err := generateRunConfig(rc)
	if err != nil {
		return nil, err
	}
	agentContent, err := os.ReadFile(rc.AgentHostPath)
	if err != nil {
		return nil, fmt.Errorf("picoclaw driver: read agent contract: %w", err)
	}
	podName := opts.PodName
	if podName == "" {
		podName = rc.ServiceName
	}
	agents := strings.TrimSpace(string(agentContent)) + "\n\n---\n\n" + strings.TrimSpace(shared.GenerateClawdapusMD(rc, podName)) + "\n"
	inputs := map[string]string{
		"CLAW_RUN_TASK":    driver.RunPlaceholderTask,
		"CLAW_RUN_SESSION": "agent:main:" + driver.RunPlaceholderSession,
		"CLAW_RUN_CONFIG":  string(configData),
		"CLAW_RUN_AGENTS":  agents,
	}
	for name, value := range inputs {
		if len(value) > runInputLimit {
			return nil, fmt.Errorf("picoclaw driver: per-run input %s is %d bytes (limit %d)", name, len(value), runInputLimit)
		}
	}
	return &driver.RunWorkerSpec{
		Entrypoint: []string{"/bin/sh", "-c", runWrapper, "claw-run"},
		Command:    []string{"${CLAW_RUN_TASK}", "${CLAW_RUN_SESSION}"},
		Environment: map[string]string{
			"CLAW_MANAGED":       "true",
			"CLAW_RUN_WORKSPACE": runWorkspaceDir,
			"HOME":               runHomeDir,
			"NO_COLOR":           "1",
			"PICOCLAW_CONFIG":    runPicoclawHome + "/config.json",
			"PICOCLAW_HOME":      runPicoclawHome,
			"PICOCLAW_LOG_FILE":  "/tmp/picoclaw.log",
		},
		Inputs: inputs,
		Tmpfs:  []string{"/tmp:mode=1777,size=64m"},
		Result: driver.RunResultContract{Source: "stdout", Marker: runResultMarker},
	}, nil
}

// generateRunConfig emits a PicoClaw v3 config. Without "version":3 PicoClaw
// migrates the file on every start, and a v3 config only authenticates through
// api_keys. Security keys are asserted after CONFIGURE so an image directive
// cannot re-enable native tools or reroute models.
func generateRunConfig(rc *driver.ResolvedClaw) ([]byte, error) {
	config := make(map[string]interface{})
	for _, cmd := range rc.Configures {
		path, value, err := shared.ParseConfigSetCommand(cmd, "picoclaw")
		if err != nil {
			return nil, fmt.Errorf("picoclaw driver: per-run config: %w", err)
		}
		if err := shared.SetPath(config, path, value); err != nil {
			return nil, fmt.Errorf("picoclaw driver: per-run config: %w", err)
		}
	}
	delete(config, "channels")
	delete(config, "gateway")
	config["version"] = 3
	if err := shared.SetPath(config, "agents.defaults.model_name", "primary"); err != nil {
		return nil, fmt.Errorf("picoclaw driver: per-run config: %w", err)
	}
	if err := shared.SetPath(config, "agents.defaults.workspace", runWorkspaceDir); err != nil {
		return nil, fmt.Errorf("picoclaw driver: per-run config: %w", err)
	}
	proxy := cllama.ProxyBaseURL(rc.Cllama[0])
	slots := sortedModelSlots(rc.Models)
	models := make([]map[string]interface{}, 0, len(slots))
	for _, slot := range slots {
		ref := strings.TrimSpace(rc.Models[slot])
		if _, _, ok := shared.SplitModelRef(ref); !ok {
			return nil, fmt.Errorf("picoclaw driver: invalid MODEL %s %q (expected provider/model)", slot, ref)
		}
		entry := map[string]interface{}{
			"model_name": slot,
			"model":      "openai/" + ref,
			"api_base":   proxy,
			"api_keys":   []string{driver.RunPlaceholderCllamaToken},
		}
		// PicoClaw copies extra_body over the whole request body, so it
		// carries only the validated effort and OpenRouter preferences, and
		// never comes from CONFIGURE.
		extra := map[string]interface{}{}
		if effort := rc.ReasoningEffort[slot]; effort != "" {
			extra["reasoning_effort"] = effort
		}
		if prefs := rc.ProviderPreferences[slot]; prefs != nil {
			extra["provider"] = prefs.Object()
		}
		if len(extra) > 0 {
			entry["extra_body"] = extra
		}
		models = append(models, entry)
	}
	config["model_list"] = models
	tools, _ := config["tools"].(map[string]interface{})
	if tools == nil {
		tools = make(map[string]interface{})
	}
	for _, name := range runDisabledTools {
		tools[name] = map[string]interface{}{"enabled": false}
	}
	config["tools"] = tools
	return json.Marshal(config)
}
