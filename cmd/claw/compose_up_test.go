package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mostlydev/clawdapus/internal/driver"
)

func TestComposeUpRejectsFileFlagAndPositionalTogether(t *testing.T) {
	prev := composePodFile
	composePodFile = "a.yml"
	defer func() { composePodFile = prev }()

	err := composeUpCmd.RunE(composeUpCmd, []string{"b.yml"})
	if err == nil {
		t.Fatal("expected conflict error when both --file and positional pod file are set")
	}
	if !strings.Contains(err.Error(), "pod file specified twice") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestMergeResolvedSkills(t *testing.T) {
	imageSkills := []driver.ResolvedSkill{
		{Name: "agent.md", HostPath: "/host/image/agent.md"},
		{Name: "shared.md", HostPath: "/host/image/shared.md"},
	}
	podSkills := []driver.ResolvedSkill{
		{Name: "shared.md", HostPath: "/host/pod/shared.md"},
		{Name: "pod.md", HostPath: "/host/pod/pod.md"},
	}

	merged := mergeResolvedSkills(imageSkills, podSkills)
	if len(merged) != 3 {
		t.Fatalf("expected 3 skills, got %d", len(merged))
	}
	if merged[1].HostPath != "/host/pod/shared.md" {
		t.Fatalf("expected pod override for shared.md, got %q", merged[1].HostPath)
	}
	if merged[2].Name != "pod.md" {
		t.Fatalf("expected pod-level-only skill to be appended, got %q", merged[2].Name)
	}
}

func TestResolveSkillEmitWritesFile(t *testing.T) {
	tmpDir := t.TempDir()

	prevExtractor := extractServiceSkillFromImage
	prevWriter := writeRuntimeFile
	extractServiceSkillFromImage = func(_, _ string) ([]byte, error) {
		return []byte("# emitted\n"), nil
	}
	writeRuntimeFile = func(path string, data []byte, perm os.FileMode) error {
		return prevWriter(path, data, perm)
	}
	defer func() {
		extractServiceSkillFromImage = prevExtractor
		writeRuntimeFile = prevWriter
	}()

	skill, err := resolveSkillEmit("gateway", tmpDir, "claw/openclaw:latest", "/app/SKILL.md")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if skill == nil {
		t.Fatal("expected emitted skill resolution")
	}
	if skill.Name != "SKILL.md" {
		t.Errorf("expected SKILL.md, got %q", skill.Name)
	}
	if !strings.HasSuffix(skill.HostPath, filepath.Join("skills", "SKILL.md")) {
		t.Errorf("expected emitted skill in skills dir, got %q", skill.HostPath)
	}

	got, err := os.ReadFile(skill.HostPath)
	if err != nil {
		t.Fatalf("read emitted skill file: %v", err)
	}
	if string(got) != "# emitted\n" {
		t.Errorf("unexpected emitted skill content: %q", string(got))
	}
}

func TestResolveSkillEmitRejectsInvalidPath(t *testing.T) {
	tmpDir := t.TempDir()

	_, err := resolveSkillEmit("gateway", tmpDir, "claw/openclaw:latest", "/")
	if err == nil {
		t.Fatal("expected invalid emitted skill path error")
	}
}

func TestResolveSkillEmitFallsBackOnExtractionError(t *testing.T) {
	tmpDir := t.TempDir()

	prevExtractor := extractServiceSkillFromImage
	extractServiceSkillFromImage = func(_, _ string) ([]byte, error) {
		return nil, fmt.Errorf("image not found")
	}
	defer func() { extractServiceSkillFromImage = prevExtractor }()

	// Should return nil, nil — pod startup continues with fallback skill
	skill, err := resolveSkillEmit("gateway", tmpDir, "claw/openclaw:latest", "/app/SKILL.md")
	if err != nil {
		t.Fatalf("expected warn+fallback (nil error), got: %v", err)
	}
	if skill != nil {
		t.Errorf("expected nil skill on extraction failure, got %+v", skill)
	}
}

func TestMergedPortsDeduplication(t *testing.T) {
	expose := []string{"80", "443"}
	ports := []string{"443", "8080"}

	merged := mergedPorts(expose, ports)
	if len(merged) != 3 {
		t.Fatalf("expected 3 merged ports, got %d: %v", len(merged), merged)
	}
	seen := map[string]bool{}
	for _, p := range merged {
		if seen[p] {
			t.Errorf("duplicate port %q in merged result", p)
		}
		seen[p] = true
	}
}

func TestMergedPortsExposeOnly(t *testing.T) {
	merged := mergedPorts([]string{"80"}, nil)
	if len(merged) != 1 || merged[0] != "80" {
		t.Errorf("expected [80], got %v", merged)
	}
}

func TestMergedPortsPortsOnly(t *testing.T) {
	merged := mergedPorts(nil, []string{"443"})
	if len(merged) != 1 || merged[0] != "443" {
		t.Errorf("expected [443], got %v", merged)
	}
}

func TestResolveServiceGeneratedSkills(t *testing.T) {
	tmpDir := t.TempDir()
	surfaces := []driver.ResolvedSurface{
		{
			Scheme: "service",
			Target: "api-server",
			Ports:  []string{"8080"},
		},
		{
			Scheme: "service",
			Target: "db",
		},
		{
			Scheme: "channel",
			Target: "discord",
		},
	}

	skills, err := resolveServiceGeneratedSkills(tmpDir, surfaces)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(skills) != 2 {
		t.Fatalf("expected 2 generated skills, got %d", len(skills))
	}

	if skills[0].Name != "surface-api-server.md" && skills[1].Name != "surface-api-server.md" {
		t.Fatalf("expected generated skill for api-server, got %v", []string{skills[0].Name, skills[1].Name})
	}
	if skills[0].Name != "surface-db.md" && skills[1].Name != "surface-db.md" {
		t.Fatalf("expected generated skill for db, got %v", []string{skills[0].Name, skills[1].Name})
	}
}

func testInvokeHandles() map[string]*driver.HandleInfo {
	return map[string]*driver.HandleInfo{
		"discord": {
			ID: "bot-discord",
			Guilds: []driver.GuildInfo{
				{
					ID: "d-guild-1",
					Channels: []driver.ChannelInfo{
						{ID: "d-alerts-1", Name: "alerts"},
						{ID: "d-trading-floor", Name: "trading-floor"},
					},
				},
				{
					ID: "d-guild-2",
					Channels: []driver.ChannelInfo{
						{ID: "d-alerts-2", Name: "alerts"},
					},
				},
			},
		},
		"slack": {
			ID: "bot-slack",
			Guilds: []driver.GuildInfo{
				{
					ID: "s-workspace-1",
					Channels: []driver.ChannelInfo{
						{ID: "s-alerts", Name: "alerts"},
						{ID: "s-infra", Name: "infra"},
					},
				},
			},
		},
		"telegram": {
			ID: "bot-telegram",
			Guilds: []driver.GuildInfo{
				{
					ID: "tg-1",
					Channels: []driver.ChannelInfo{
						{ID: "-100777", Name: "ops"},
					},
				},
			},
		},
	}
}

func TestResolveInvocationTargetByName(t *testing.T) {
	got := resolveInvocationTarget(testInvokeHandles(), "infra")
	if got.To != "s-infra" {
		t.Fatalf("expected infra to resolve to s-infra, got %q", got.To)
	}
	if got.Warning != "" {
		t.Fatalf("expected no warning for unique name lookup, got %q", got.Warning)
	}
}

func TestResolveInvocationTargetByID(t *testing.T) {
	got := resolveInvocationTarget(testInvokeHandles(), "s-infra")
	if got.To != "s-infra" {
		t.Fatalf("expected raw channel ID to be preserved, got %q", got.To)
	}
	if got.Warning != "" {
		t.Fatalf("expected no warning for ID lookup, got %q", got.Warning)
	}
}

func TestResolveInvocationTargetExplicitPlatformName(t *testing.T) {
	got := resolveInvocationTarget(testInvokeHandles(), "discord:trading-floor")
	if got.To != "d-trading-floor" {
		t.Fatalf("expected discord:trading-floor -> d-trading-floor, got %q", got.To)
	}
	if got.Warning != "" {
		t.Fatalf("expected no warning for explicit unique platform target, got %q", got.Warning)
	}
}

func TestResolveInvocationTargetExplicitPlatformID(t *testing.T) {
	got := resolveInvocationTarget(testInvokeHandles(), "telegram:-100777")
	if got.To != "-100777" {
		t.Fatalf("expected explicit telegram ID to be preserved, got %q", got.To)
	}
	if got.Warning != "" {
		t.Fatalf("expected no warning for explicit ID, got %q", got.Warning)
	}
}

func TestResolveInvocationTargetUnknownTargetFallsBackToRaw(t *testing.T) {
	got := resolveInvocationTarget(testInvokeHandles(), "C123RAW")
	if got.To != "C123RAW" {
		t.Fatalf("expected unknown target to pass through, got %q", got.To)
	}
	if got.Warning != "" {
		t.Fatalf("expected no warning for raw fallback, got %q", got.Warning)
	}
}

func TestResolveInvocationTargetUnknownPlatformFallsBackToScopedRaw(t *testing.T) {
	got := resolveInvocationTarget(testInvokeHandles(), "mattermost:town-square")
	if got.To != "town-square" {
		t.Fatalf("expected unknown platform target to pass through scoped value, got %q", got.To)
	}
	if got.Warning != "" {
		t.Fatalf("expected no warning for unknown platform fallback, got %q", got.Warning)
	}
}

func TestResolveInvocationTargetNoHandlesStillSupportsPlatformPrefix(t *testing.T) {
	got := resolveInvocationTarget(nil, "telegram:-100999")
	if got.To != "-100999" {
		t.Fatalf("expected explicit target with no handles to preserve scoped value, got %q", got.To)
	}
	if got.Warning != "" {
		t.Fatalf("expected no warning with empty handles map, got %q", got.Warning)
	}
}

func TestResolveInvocationTargetAmbiguousAcrossPlatforms(t *testing.T) {
	got := resolveInvocationTarget(testInvokeHandles(), "alerts")
	if got.To != "alerts" {
		t.Fatalf("expected ambiguous target to keep raw value, got %q", got.To)
	}
	if !strings.Contains(got.Warning, "ambiguous") {
		t.Fatalf("expected ambiguity warning, got %q", got.Warning)
	}
	if !strings.Contains(got.Warning, "platform:target") {
		t.Fatalf("expected platform disambiguation hint, got %q", got.Warning)
	}
}

func TestResolveInvocationTargetAmbiguousWithinPlatform(t *testing.T) {
	got := resolveInvocationTarget(testInvokeHandles(), "discord:alerts")
	if got.To != "alerts" {
		t.Fatalf("expected ambiguous platform-scoped target to keep raw value, got %q", got.To)
	}
	if !strings.Contains(got.Warning, "ambiguous") {
		t.Fatalf("expected ambiguity warning, got %q", got.Warning)
	}
	if !strings.Contains(got.Warning, "channel ID") {
		t.Fatalf("expected channel ID disambiguation hint, got %q", got.Warning)
	}
}

func TestResolveCllama(t *testing.T) {
	tests := []struct {
		name  string
		image []string
		pod   []string
		want  []string
	}{
		{
			name:  "pod overrides image",
			image: []string{"passthrough"},
			pod:   []string{"passthrough", "policy"},
			want:  []string{"passthrough", "policy"},
		},
		{
			name:  "image fallback",
			image: []string{"passthrough"},
			pod:   nil,
			want:  []string{"passthrough"},
		},
		{
			name:  "both empty",
			image: nil,
			pod:   nil,
			want:  nil,
		},
		{
			name:  "pod only",
			image: nil,
			pod:   []string{"passthrough"},
			want:  []string{"passthrough"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveCllama(tt.image, tt.pod)
			if len(got) != len(tt.want) {
				t.Fatalf("resolveCllama(%v, %v) length=%d, want %d", tt.image, tt.pod, len(got), len(tt.want))
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("resolveCllama(%v, %v) = %v, want %v", tt.image, tt.pod, got, tt.want)
				}
			}
		})
	}
}

func TestDetectCllama(t *testing.T) {
	claws := map[string]*driver.ResolvedClaw{
		"bot-a": {Cllama: nil},
		"bot-b": {Cllama: []string{"passthrough"}},
		"bot-c": {Cllama: []string{"passthrough", "policy"}},
	}
	enabled, agents := detectCllama(claws)
	if !enabled {
		t.Error("expected cllama enabled")
	}
	if len(agents) != 2 || agents[0] != "bot-b" || agents[1] != "bot-c" {
		t.Errorf("expected [bot-b bot-c], got %v", agents)
	}
}

func TestCollectProxyTypes(t *testing.T) {
	claws := map[string]*driver.ResolvedClaw{
		"bot-a": {Cllama: []string{"passthrough"}},
		"bot-b": {Cllama: []string{"passthrough", "policy"}},
	}
	types := collectProxyTypes(claws)
	if len(types) != 2 || types[0] != "passthrough" || types[1] != "policy" {
		t.Errorf("expected [passthrough policy], got %v", types)
	}
}

func TestStripLLMKeys(t *testing.T) {
	env := map[string]string{
		"OPENAI_API_KEY":    "sk-real",
		"ANTHROPIC_API_KEY": "sk-ant",
		"DISCORD_BOT_TOKEN": "keep",
		"LOG_LEVEL":         "info",
	}
	stripLLMKeys(env)
	if _, ok := env["OPENAI_API_KEY"]; ok {
		t.Error("should strip OPENAI_API_KEY")
	}
	if _, ok := env["ANTHROPIC_API_KEY"]; ok {
		t.Error("should strip ANTHROPIC_API_KEY")
	}
	if env["DISCORD_BOT_TOKEN"] != "keep" {
		t.Error("should keep non-LLM keys")
	}
}

func TestIsProviderKey(t *testing.T) {
	tests := []struct {
		key  string
		want bool
	}{
		{"OPENAI_API_KEY", true},
		{"ANTHROPIC_API_KEY", true},
		{"OPENROUTER_API_KEY", true},
		{"PROVIDER_API_KEY_CUSTOM", true},
		{"DISCORD_BOT_TOKEN", false},
		{"LOG_LEVEL", false},
	}
	for _, tt := range tests {
		if got := isProviderKey(tt.key); got != tt.want {
			t.Errorf("isProviderKey(%q) = %v, want %v", tt.key, got, tt.want)
		}
	}
}
