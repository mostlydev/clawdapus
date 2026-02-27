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
		"claw.skill.0":           "./skills/custom-workflow.md",
		"claw.skill.1":           "./skills/team-conventions.md",
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

func TestParseLabelsExtractsSkillEmit(t *testing.T) {
	raw := map[string]string{
		"claw.type":       "openclaw",
		"claw.skill.emit": "/app/SKILL.md",
		"claw.skill.0":    "./skills/custom-workflow.md",
	}

	info := ParseLabels(raw)

	if info.SkillEmit != "/app/SKILL.md" {
		t.Fatalf("expected SkillEmit=/app/SKILL.md, got %q", info.SkillEmit)
	}
	// skill.emit should not appear in the Skills slice
	if len(info.Skills) != 1 {
		t.Fatalf("expected 1 skill, got %d: %v", len(info.Skills), info.Skills)
	}
	if info.Skills[0] != "./skills/custom-workflow.md" {
		t.Fatalf("expected skill[0] to be custom-workflow, got %q", info.Skills[0])
	}
}

func TestParseLabelsExtractsHandles(t *testing.T) {
	raw := map[string]string{
		"claw.type":           "openclaw",
		"claw.handle.discord": "true",
	}

	info := ParseLabels(raw)

	if len(info.Handles) != 1 {
		t.Fatalf("expected 1 handle, got %d: %v", len(info.Handles), info.Handles)
	}
	if info.Handles[0] != "discord" {
		t.Errorf("expected handle 'discord', got %q", info.Handles[0])
	}
}

func TestParseLabelsHandlesSortedAlphabetically(t *testing.T) {
	raw := map[string]string{
		"claw.type":            "openclaw",
		"claw.handle.slack":    "true",
		"claw.handle.discord":  "true",
		"claw.handle.telegram": "true",
	}

	info := ParseLabels(raw)

	if len(info.Handles) != 3 {
		t.Fatalf("expected 3 handles, got %d: %v", len(info.Handles), info.Handles)
	}
	if info.Handles[0] != "discord" {
		t.Errorf("expected sorted first handle 'discord', got %q", info.Handles[0])
	}
	if info.Handles[1] != "slack" {
		t.Errorf("expected sorted second handle 'slack', got %q", info.Handles[1])
	}
	if info.Handles[2] != "telegram" {
		t.Errorf("expected sorted third handle 'telegram', got %q", info.Handles[2])
	}
}

func TestParseLabelsIgnoresFalseyHandleValues(t *testing.T) {
	raw := map[string]string{
		"claw.type":            "openclaw",
		"claw.handle.discord":  "false",
		"claw.handle.slack":    "0",
		"claw.handle.telegram": "yes",
		"claw.handle.matrix":   "true",
	}

	info := ParseLabels(raw)

	if len(info.Handles) != 1 {
		t.Fatalf("expected only 1 truthy handle, got %d: %v", len(info.Handles), info.Handles)
	}
	if info.Handles[0] != "matrix" {
		t.Errorf("expected handle 'matrix' (only truthy one), got %q", info.Handles[0])
	}
}

func TestParseLabelsAcceptsOneAsTruthy(t *testing.T) {
	raw := map[string]string{
		"claw.type":           "openclaw",
		"claw.handle.discord": "1",
	}

	info := ParseLabels(raw)

	if len(info.Handles) != 1 {
		t.Fatalf("expected 1 handle, got %d: %v", len(info.Handles), info.Handles)
	}
	if info.Handles[0] != "discord" {
		t.Errorf("expected handle 'discord', got %q", info.Handles[0])
	}
}

func TestParseLabelsNoHandlesMeansEmpty(t *testing.T) {
	raw := map[string]string{
		"claw.type": "openclaw",
	}

	info := ParseLabels(raw)

	if info.Handles == nil {
		t.Fatal("expected non-nil Handles slice")
	}
	if len(info.Handles) != 0 {
		t.Errorf("expected 0 handles, got %d: %v", len(info.Handles), info.Handles)
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

func TestParseLabelsInvocations(t *testing.T) {
	raw := map[string]string{
		"claw.invoke.0": "15 8 * * 1-5\tPre-market synthesis",
		"claw.invoke.1": "*/5 * * * *\tNews poll",
	}
	info := ParseLabels(raw)

	if len(info.Invocations) != 2 {
		t.Fatalf("expected 2 invocations, got %d", len(info.Invocations))
	}
	if info.Invocations[0].Schedule != "15 8 * * 1-5" {
		t.Errorf("expected invocations[0].Schedule=%q, got %q", "15 8 * * 1-5", info.Invocations[0].Schedule)
	}
	if info.Invocations[0].Command != "Pre-market synthesis" {
		t.Errorf("expected invocations[0].Command=%q, got %q", "Pre-market synthesis", info.Invocations[0].Command)
	}
	if info.Invocations[1].Schedule != "*/5 * * * *" {
		t.Errorf("expected invocations[1].Schedule=%q, got %q", "*/5 * * * *", info.Invocations[1].Schedule)
	}
	if info.Invocations[1].Command != "News poll" {
		t.Errorf("expected invocations[1].Command=%q, got %q", "News poll", info.Invocations[1].Command)
	}
}

func TestParseLabelsCllamaIndexedOrdering(t *testing.T) {
	raw := map[string]string{
		"claw.cllama.1": "policy",
		"claw.cllama.0": "passthrough",
	}
	info := ParseLabels(raw)
	if len(info.Cllama) != 2 {
		t.Fatalf("expected 2 cllama entries, got %d", len(info.Cllama))
	}
	if info.Cllama[0] != "passthrough" || info.Cllama[1] != "policy" {
		t.Fatalf("unexpected cllama ordering: %v", info.Cllama)
	}
}

func TestParseLabelsCllamaLegacyFallback(t *testing.T) {
	raw := map[string]string{
		"claw.cllama.default": "passthrough",
	}
	info := ParseLabels(raw)
	if len(info.Cllama) != 1 {
		t.Fatalf("expected 1 cllama entry, got %d", len(info.Cllama))
	}
	if info.Cllama[0] != "passthrough" {
		t.Fatalf("expected legacy cllama passthrough, got %v", info.Cllama)
	}
}
