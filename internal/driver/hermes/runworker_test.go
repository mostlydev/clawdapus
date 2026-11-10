package hermes

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mostlydev/clawdapus/internal/driver"
)

func runWorkerClaw(t *testing.T) *driver.ResolvedClaw {
	t.Helper()
	agent := filepath.Join(t.TempDir(), "AGENTS.md")
	if err := os.WriteFile(agent, []byte("# Contract\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return &driver.ResolvedClaw{
		ServiceName:   "worker",
		ClawType:      "hermes",
		AgentHostPath: agent,
		Cllama:        []string{"passthrough"},
		Models:        map[string]string{"primary": "anthropic/claude-sonnet-5-5"},
		Configures:    []string{"hermes config set model.base_url http://elsewhere/v1"},
	}
}

func TestCompileRunWorkerTaskHome(t *testing.T) {
	d := &Driver{}
	rc := runWorkerClaw(t)
	spec, err := d.CompileRunWorker(rc, driver.RunWorkerOpts{Home: &driver.RunHomeSpec{Mount: "/home/task"}, MaxResultBytes: 4096})
	if err != nil {
		t.Fatal(err)
	}
	if spec.Home == nil || spec.Gate == nil || spec.Result.Header != driver.RunResultHeaderJSONLine || spec.Result.Marker != RunResultMarker {
		t.Fatalf("spec lacks the task-home contract: %+v", spec)
	}
	if spec.Inputs["CLAW_RUN_TOKEN"] != driver.RunPlaceholderCllamaToken || spec.Inputs["CLAW_RUN_TASK"] != driver.RunPlaceholderTask {
		t.Fatalf("inputs: %v", spec.Inputs)
	}
	config := spec.Inputs["CLAW_RUN_CONFIG"]
	if strings.Contains(config, driver.RunPlaceholderCllamaToken) || strings.Contains(config, "api_key:") {
		t.Fatalf("config carries a credential:\n%s", config)
	}
	if !strings.Contains(config, "base_url: http://gate:8080/v1") || !strings.Contains(config, "key_env: CLAW_MODEL_KEY") || strings.Contains(config, "elsewhere") {
		t.Fatalf("CONFIGURE rerouted the model:\n%s", config)
	}
	for k, v := range spec.Environment {
		if strings.Contains(v, driver.RunPlaceholderCllamaToken) {
			t.Fatalf("static env %s carries the token placeholder", k)
		}
	}
	if got := spec.Gate.Command; len(got) != 3 || got[0] != "cllama-passthrough" && !strings.HasPrefix(got[0], "cllama") || got[1] != "8080" {
		t.Fatalf("gate destination: %v", got)
	}
	if !strings.Contains(spec.Inputs["CLAW_RUN_AGENTS"], "/home/task/.claw-run/question.md") {
		t.Fatal("contract does not explain the question file")
	}
	if !d.SupportsRunProfile(true) || d.SupportsRunProfile(false) {
		t.Fatal("hermes must compile only the task-home profile")
	}
}

func TestCompileRunWorkerRequiresHome(t *testing.T) {
	d := &Driver{}
	if _, err := d.CompileRunWorker(runWorkerClaw(t), driver.RunWorkerOpts{MaxResultBytes: 1}); err == nil || !strings.Contains(err.Error(), "x-claw.run.home") {
		t.Fatalf("got %v", err)
	}
	rc := runWorkerClaw(t)
	rc.Handles = map[string]*driver.HandleInfo{"discord": {}}
	if err := d.ValidateRunWorker(rc); err == nil || !strings.Contains(err.Error(), "HANDLE") {
		t.Fatalf("got %v", err)
	}
}
