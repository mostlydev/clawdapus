package inspect

import "testing"

func TestParseLabelsExtractsClawLabels(t *testing.T) {
	raw := map[string]string{
		"claw.type":              "openclaw",
		"claw.agent.file":        "AGENTS.md",
		"claw.model.primary":     "openrouter/anthropic/claude-sonnet-4",
		"claw.surface.1":         "service://fleet-master",
		"claw.surface.0":         "channel://discord",
		"claw.privilege.worker":  "root",
		"claw.privilege.runtime": "claw-user",
		"claw.configure.0":       "openclaw config set agents.defaults.heartbeat.every 30m",
		"claw.configure.1":       "openclaw config set agents.defaults.heartbeat.target none",
		"claw.skill.0":          "./skills/custom-workflow.md",
		"claw.skill.1":          "./skills/team-conventions.md",
		"maintainer":             "someone",
	}

	info := ParseLabels(raw)

	if info.ClawType != "openclaw" {
		t.Fatalf("expected openclaw claw type, got %q", info.ClawType)
	}
	if info.Agent != "AGENTS.md" {
		t.Fatalf("expected AGENTS.md, got %q", info.Agent)
	}
	if info.Models["primary"] != "openrouter/anthropic/claude-sonnet-4" {
		t.Fatalf("unexpected model map: %#v", info.Models)
	}
	if len(info.Surfaces) != 2 {
		t.Fatalf("expected 2 surfaces, got %d", len(info.Surfaces))
	}
	if info.Surfaces[0] != "channel://discord" || info.Surfaces[1] != "service://fleet-master" {
		t.Fatalf("expected sorted surfaces, got %#v", info.Surfaces)
	}
	if len(info.Configures) != 2 {
		t.Fatalf("expected 2 configures, got %d", len(info.Configures))
	}
	if info.Configures[0] != "openclaw config set agents.defaults.heartbeat.every 30m" {
		t.Fatalf("expected configure[0] to be heartbeat.every, got %q", info.Configures[0])
	}
	if info.Configures[1] != "openclaw config set agents.defaults.heartbeat.target none" {
		t.Fatalf("expected configure[1] to be heartbeat.target, got %q", info.Configures[1])
	}
	if len(info.Skills) != 2 {
		t.Fatalf("expected 2 skills, got %d", len(info.Skills))
	}
	if info.Skills[0] != "./skills/custom-workflow.md" {
		t.Fatalf("expected skill[0] to be custom-workflow, got %q", info.Skills[0])
	}
	if info.Skills[1] != "./skills/team-conventions.md" {
		t.Fatalf("expected skill[1] to be team-conventions, got %q", info.Skills[1])
	}
	if info.Privileges["worker"] != "root" {
		t.Fatalf("expected worker privilege root, got %q", info.Privileges["worker"])
	}
	if info.Privileges["runtime"] != "claw-user" {
		t.Fatalf("expected runtime privilege claw-user, got %q", info.Privileges["runtime"])
	}
}

func TestParseLabelsIgnoresNonClawLabels(t *testing.T) {
	raw := map[string]string{
		"org.opencontainers.image.title": "something",
		"maintainer":                     "someone",
	}

	info := ParseLabels(raw)
	if info.ClawType != "" {
		t.Fatalf("expected empty claw type, got %q", info.ClawType)
	}
}
