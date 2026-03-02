package pod

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mostlydev/clawdapus/internal/driver"
	_ "github.com/mostlydev/clawdapus/internal/driver/nanobot"
	_ "github.com/mostlydev/clawdapus/internal/driver/picoclaw"
	"gopkg.in/yaml.v3"
)

func TestNanobotAndPicoclawMaterializeAndCompose(t *testing.T) {
	workDir := t.TempDir()
	agentsDir := filepath.Join(workDir, "agents")
	if err := os.MkdirAll(agentsDir, 0o755); err != nil {
		t.Fatalf("create agents dir: %v", err)
	}

	nanobotAgent := filepath.Join(agentsDir, "NANOBOT.md")
	if err := os.WriteFile(nanobotAgent, []byte("# Nanobot\n\nYou are nanobot."), 0o644); err != nil {
		t.Fatalf("write nanobot contract: %v", err)
	}
	picoclawAgent := filepath.Join(agentsDir, "PICOCLAW.md")
	if err := os.WriteFile(picoclawAgent, []byte("# PicoClaw\n\nYou are picoclaw."), 0o644); err != nil {
		t.Fatalf("write picoclaw contract: %v", err)
	}

	nanobotDriver, err := driver.Lookup("nanobot")
	if err != nil {
		t.Fatalf("lookup nanobot driver: %v", err)
	}
	picoclawDriver, err := driver.Lookup("picoclaw")
	if err != nil {
		t.Fatalf("lookup picoclaw driver: %v", err)
	}

	rcNanobot := &driver.ResolvedClaw{
		ServiceName:   "nanobot",
		ClawType:      "nanobot",
		AgentHostPath: nanobotAgent,
		Models: map[string]string{
			"primary": "openrouter/anthropic/claude-sonnet-4",
		},
		Handles: map[string]*driver.HandleInfo{
			"discord": {ID: "111"},
		},
		Environment: map[string]string{
			"OPENROUTER_API_KEY": "or-key",
			"DISCORD_BOT_TOKEN":  "discord-token-a",
		},
		Invocations: []driver.Invocation{
			{Schedule: "*/10 * * * *", Message: "nanobot heartbeat", Name: "nanobot-heartbeat"},
		},
	}
	if err := nanobotDriver.Validate(rcNanobot); err != nil {
		t.Fatalf("validate nanobot: %v", err)
	}

	nanobotRuntimeDir := filepath.Join(workDir, ".claw-runtime", "nanobot")
	nanobotResult, err := nanobotDriver.Materialize(rcNanobot, driver.MaterializeOpts{
		RuntimeDir: nanobotRuntimeDir,
		PodName:    "newtypes-pod",
	})
	if err != nil {
		t.Fatalf("materialize nanobot: %v", err)
	}

	rcPicoclaw := &driver.ResolvedClaw{
		ServiceName:   "picoclaw",
		ClawType:      "picoclaw",
		AgentHostPath: picoclawAgent,
		Models: map[string]string{
			"primary": "openrouter/anthropic/claude-sonnet-4",
		},
		Handles: map[string]*driver.HandleInfo{
			"discord": {ID: "222"},
		},
		Environment: map[string]string{
			"OPENROUTER_API_KEY": "or-key",
			"DISCORD_BOT_TOKEN":  "discord-token-b",
		},
		Invocations: []driver.Invocation{
			{Schedule: "*/15 * * * *", Message: "picoclaw heartbeat", Name: "picoclaw-heartbeat"},
		},
	}
	if err := picoclawDriver.Validate(rcPicoclaw); err != nil {
		t.Fatalf("validate picoclaw: %v", err)
	}

	picoclawRuntimeDir := filepath.Join(workDir, ".claw-runtime", "picoclaw")
	picoclawResult, err := picoclawDriver.Materialize(rcPicoclaw, driver.MaterializeOpts{
		RuntimeDir: picoclawRuntimeDir,
		PodName:    "newtypes-pod",
	})
	if err != nil {
		t.Fatalf("materialize picoclaw: %v", err)
	}

	for _, path := range []string{
		filepath.Join(nanobotRuntimeDir, "nanobot-home", "config.json"),
		filepath.Join(nanobotRuntimeDir, "nanobot-home", "workspace", "AGENTS.md"),
		filepath.Join(nanobotRuntimeDir, "nanobot-home", "cron", "jobs.json"),
		filepath.Join(picoclawRuntimeDir, "picoclaw-home", "config.json"),
		filepath.Join(picoclawRuntimeDir, "picoclaw-home", "workspace", "AGENTS.md"),
		filepath.Join(picoclawRuntimeDir, "picoclaw-home", "workspace", "cron", "jobs.json"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected materialized file %s: %v", path, err)
		}
	}

	picoclawCfgRaw, err := os.ReadFile(filepath.Join(picoclawRuntimeDir, "picoclaw-home", "config.json"))
	if err != nil {
		t.Fatalf("read picoclaw config: %v", err)
	}
	var picoclawCfg map[string]interface{}
	if err := json.Unmarshal(picoclawCfgRaw, &picoclawCfg); err != nil {
		t.Fatalf("parse picoclaw config json: %v", err)
	}
	modelList, ok := picoclawCfg["model_list"].([]interface{})
	if !ok || len(modelList) == 0 {
		t.Fatalf("expected non-empty picoclaw model_list, got %#v", picoclawCfg["model_list"])
	}

	p := &Pod{
		Name: "newtypes-pod",
		Services: map[string]*Service{
			"nanobot": {
				Image: "nanobot-example:latest",
				Claw: &ClawBlock{
					Agent: "./agents/NANOBOT.md",
				},
			},
			"picoclaw": {
				Image: "picoclaw-example:latest",
				Claw: &ClawBlock{
					Agent: "./agents/PICOCLAW.md",
				},
			},
		},
	}
	results := map[string]*driver.MaterializeResult{
		"nanobot":  nanobotResult,
		"picoclaw": picoclawResult,
	}

	out, err := EmitCompose(p, results)
	if err != nil {
		t.Fatalf("emit compose: %v", err)
	}

	var cf struct {
		Services map[string]struct {
			ReadOnly    bool              `yaml:"read_only"`
			Environment map[string]string `yaml:"environment"`
			Volumes     []string          `yaml:"volumes"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal([]byte(out), &cf); err != nil {
		t.Fatalf("parse compose output: %v", err)
	}

	nanoSvc, ok := cf.Services["nanobot"]
	if !ok {
		t.Fatal("expected nanobot service in compose output")
	}
	if !nanoSvc.ReadOnly {
		t.Fatal("expected nanobot read_only=true")
	}
	if nanoSvc.Environment["CLAW_MANAGED"] != "true" {
		t.Fatalf("expected nanobot CLAW_MANAGED=true, got %q", nanoSvc.Environment["CLAW_MANAGED"])
	}
	if !containsSuffix(nanoSvc.Volumes, ":/root/.nanobot:rw") {
		t.Fatalf("expected nanobot /root/.nanobot bind mount, got %v", nanoSvc.Volumes)
	}

	picoSvc, ok := cf.Services["picoclaw"]
	if !ok {
		t.Fatal("expected picoclaw service in compose output")
	}
	if !picoSvc.ReadOnly {
		t.Fatal("expected picoclaw read_only=true")
	}
	if picoSvc.Environment["PICOCLAW_HOME"] != "/home/picoclaw/.picoclaw" {
		t.Fatalf("expected PICOCLAW_HOME=/home/picoclaw/.picoclaw, got %q", picoSvc.Environment["PICOCLAW_HOME"])
	}
	if picoSvc.Environment["PICOCLAW_CONFIG"] != "/home/picoclaw/.picoclaw/config.json" {
		t.Fatalf("expected PICOCLAW_CONFIG=/home/picoclaw/.picoclaw/config.json, got %q", picoSvc.Environment["PICOCLAW_CONFIG"])
	}
	if !containsSuffix(picoSvc.Volumes, ":/home/picoclaw/.picoclaw:rw") {
		t.Fatalf("expected picoclaw /home/picoclaw/.picoclaw bind mount, got %v", picoSvc.Volumes)
	}
}

func containsSuffix(values []string, suffix string) bool {
	for _, value := range values {
		if strings.HasSuffix(value, suffix) {
			return true
		}
	}
	return false
}
