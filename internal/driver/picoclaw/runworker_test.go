package picoclaw

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mostlydev/clawdapus/internal/driver"
)

func newRunWorkerRC(t *testing.T) *driver.ResolvedClaw {
	t.Helper()
	agent := filepath.Join(t.TempDir(), "AGENTS.md")
	if err := os.WriteFile(agent, []byte("# Worker\nDo the task."), 0o644); err != nil {
		t.Fatal(err)
	}
	return &driver.ResolvedClaw{
		ServiceName:   "worker",
		ClawType:      "picoclaw",
		AgentHostPath: agent,
		Models:        map[string]string{"primary": "anthropic/claude-sonnet-5-5", "fallback": "openai/gpt-5.5"},
		Cllama:        []string{"passthrough"},
	}
}

func TestValidateRunWorkerDoesNotRequireChannels(t *testing.T) {
	rc := newRunWorkerRC(t)
	d := &Driver{}
	if err := d.Validate(rc); err == nil || !strings.Contains(err.Error(), "no channels") {
		t.Fatalf("long-lived Validate must still require a channel, got %v", err)
	}
	if err := d.ValidateRunWorker(rc); err != nil {
		t.Fatalf("ValidateRunWorker: %v", err)
	}
}

func TestValidateRunWorkerRejectsMissingCllamaAndHandles(t *testing.T) {
	d := &Driver{}
	rc := newRunWorkerRC(t)
	rc.Cllama = nil
	if err := d.ValidateRunWorker(rc); err == nil || !strings.Contains(err.Error(), "require cllama") {
		t.Fatalf("expected cllama requirement, got %v", err)
	}
	rc = newRunWorkerRC(t)
	rc.Handles = map[string]*driver.HandleInfo{"discord": {ID: "1"}}
	if err := d.ValidateRunWorker(rc); err == nil || !strings.Contains(err.Error(), "HANDLE") {
		t.Fatalf("expected HANDLE rejection, got %v", err)
	}
}

func TestCompileRunWorkerPinsOneShotContract(t *testing.T) {
	rc := newRunWorkerRC(t)
	rc.Configures = []string{"picoclaw config set tools.exec.enabled true", "picoclaw config set agents.defaults.max_tokens 4096"}
	spec, err := (&Driver{}).CompileRunWorker(rc, driver.RunWorkerOpts{PodName: "pod"})
	if err != nil {
		t.Fatalf("CompileRunWorker: %v", err)
	}
	if spec.Entrypoint[0] != "/bin/sh" || !strings.Contains(spec.Entrypoint[2], `exec picoclaw agent -m "$1" -s "$2"`) {
		t.Fatalf("unexpected entrypoint %q", spec.Entrypoint)
	}
	if strings.Join(spec.Command, " ") != "${CLAW_RUN_TASK} ${CLAW_RUN_SESSION}" {
		t.Fatalf("command must only reference interpolated inputs, got %q", spec.Command)
	}
	if spec.Inputs["CLAW_RUN_TASK"] != driver.RunPlaceholderTask {
		t.Fatalf("task input = %q", spec.Inputs["CLAW_RUN_TASK"])
	}
	if spec.Inputs["CLAW_RUN_SESSION"] != "agent:main:"+driver.RunPlaceholderSession {
		t.Fatalf("session input must use the agent:<id>:<rest> shape, got %q", spec.Inputs["CLAW_RUN_SESSION"])
	}
	if spec.Result.Source != "stdout" || spec.Result.Marker != "\n🦞 " {
		t.Fatalf("unexpected result contract %+v", spec.Result)
	}
	if len(spec.Tmpfs) != 1 || !strings.HasPrefix(spec.Tmpfs[0], "/tmp:") {
		t.Fatalf("tmpfs = %q", spec.Tmpfs)
	}
	for k, v := range spec.Environment {
		if strings.Contains(v, driver.RunPlaceholderCllamaToken) {
			t.Fatalf("static env %s must not carry the token", k)
		}
	}
	if !strings.Contains(spec.Inputs["CLAW_RUN_AGENTS"], "Do the task.") {
		t.Fatalf("contract missing from agents input")
	}

	var cfg map[string]interface{}
	if err := json.Unmarshal([]byte(spec.Inputs["CLAW_RUN_CONFIG"]), &cfg); err != nil {
		t.Fatalf("config input is not JSON: %v", err)
	}
	if cfg["version"] != float64(3) {
		t.Fatalf("config must be v3, got %v", cfg["version"])
	}
	if _, ok := cfg["channels"]; ok {
		t.Fatalf("per-run config must not enable channels")
	}
	models := cfg["model_list"].([]interface{})
	if len(models) != 2 {
		t.Fatalf("expected primary and fallback models, got %d", len(models))
	}
	primary := models[0].(map[string]interface{})
	if primary["model_name"] != "primary" || primary["model"] != "openai/anthropic/claude-sonnet-5-5" {
		t.Fatalf("unexpected primary %v", primary)
	}
	if _, legacy := primary["api_key"]; legacy {
		t.Fatalf("v3 config must not use api_key: PicoClaw then sends no auth header")
	}
	keys := primary["api_keys"].([]interface{})
	if len(keys) != 1 || keys[0] != driver.RunPlaceholderCllamaToken {
		t.Fatalf("api_keys = %v", keys)
	}
	tools := cfg["tools"].(map[string]interface{})
	for _, name := range runDisabledTools {
		entry := tools[name].(map[string]interface{})
		if entry["enabled"] != false {
			t.Fatalf("tool %s must be disabled even when CONFIGURE enables it", name)
		}
	}
	defaults := cfg["agents"].(map[string]interface{})["defaults"].(map[string]interface{})
	if defaults["max_tokens"] != float64(4096) {
		t.Fatalf("non-security CONFIGURE settings must survive, got %v", defaults["max_tokens"])
	}
}
