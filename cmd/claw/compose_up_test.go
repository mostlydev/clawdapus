package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/mostlydev/clawdapus/internal/clawapi"
	"github.com/mostlydev/clawdapus/internal/clawfile"
	"github.com/mostlydev/clawdapus/internal/cllama"
	"github.com/mostlydev/clawdapus/internal/describe"
	"github.com/mostlydev/clawdapus/internal/driver"
	"github.com/mostlydev/clawdapus/internal/driver/openclaw"
	"github.com/mostlydev/clawdapus/internal/inspect"
	"github.com/mostlydev/clawdapus/internal/pod"
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

func TestMergeModelSlots(t *testing.T) {
	tests := []struct {
		name  string
		image map[string]string
		pod   map[string]string
		want  map[string]string
	}{
		{
			name:  "pod overrides image per key",
			image: map[string]string{"primary": "image-primary", "fallback": "image-fallback"},
			pod:   map[string]string{"primary": "pod-primary"},
			want:  map[string]string{"primary": "pod-primary", "fallback": "image-fallback"},
		},
		{
			name:  "image only key preserved",
			image: map[string]string{"fallback": "image-fallback"},
			pod:   map[string]string{"primary": "pod-primary"},
			want:  map[string]string{"primary": "pod-primary", "fallback": "image-fallback"},
		},
		{
			name:  "nil pod returns cloned image",
			image: map[string]string{"primary": "image-primary"},
			pod:   nil,
			want:  map[string]string{"primary": "image-primary"},
		},
		{
			name:  "nil image non nil pod",
			image: nil,
			pod:   map[string]string{"primary": "pod-primary"},
			want:  map[string]string{"primary": "pod-primary"},
		},
		{
			name:  "both nil",
			image: nil,
			pod:   nil,
			want:  nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mergeModelSlots(tt.image, tt.pod)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("mergeModelSlots(%v, %v) = %v, want %v", tt.image, tt.pod, got, tt.want)
			}
		})
	}

	image := map[string]string{"primary": "image-primary"}
	merged := mergeModelSlots(image, map[string]string{"fallback": "pod-fallback"})
	merged["primary"] = "mutated"
	if got := image["primary"]; got != "image-primary" {
		t.Fatalf("expected input image map to remain unchanged, got %q", got)
	}

	const yaml = `
x-claw:
  pod: merge-model-slots
  models-defaults:
    primary: pod-default-primary
    fallback: pod-default-fallback

services:
  suppressor:
    image: suppressor:latest
    x-claw:
      agent: ./AGENTS.md
      models: {}
  null_suppressor:
    image: null-suppressor:latest
    x-claw:
      agent: ./AGENTS.md
      models: null
`

	parsed, err := pod.Parse(strings.NewReader(yaml))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	imageModels := map[string]string{
		"primary":  "image-primary",
		"fallback": "image-fallback",
	}
	for _, serviceName := range []string{"suppressor", "null_suppressor"} {
		service := parsed.Services[serviceName]
		if service == nil || service.Claw == nil {
			t.Fatalf("expected parsed service %q", serviceName)
		}
		got := mergeModelSlots(imageModels, service.Claw.Models)
		if !reflect.DeepEqual(got, imageModels) {
			t.Fatalf("%s: expected image-declared slots to remain after pod-default suppression, got %v", serviceName, got)
		}
	}
}

func TestBuiltinClawAPIDescriptorUsesShortFleetAlertsFeedWindow(t *testing.T) {
	descriptor := builtinClawAPIDescriptor()
	if descriptor == nil {
		t.Fatal("expected builtin claw-api descriptor")
	}
	if len(descriptor.Feeds) != 1 {
		t.Fatalf("expected 1 builtin feed, got %d", len(descriptor.Feeds))
	}
	if got := descriptor.Feeds[0].Name; got != "fleet-alerts" {
		t.Fatalf("feed name = %q; want fleet-alerts", got)
	}
	if got := descriptor.Feeds[0].Path; got != "/fleet/alerts?since=15m" {
		t.Fatalf("feed path = %q; want /fleet/alerts?since=15m", got)
	}
	if got := descriptor.Endpoints[3].Path; got != "/fleet/alerts" {
		t.Fatalf("endpoint path = %q; want /fleet/alerts", got)
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

func TestResolveRuntimePlaceholdersUsesDotEnvForHandleTopology(t *testing.T) {
	tmpDir := t.TempDir()
	dotEnv := strings.Join([]string{
		"BOT_ID=123456789",
		"GUILD_ID=999888777",
		"CHANNEL_ID=111222333",
		"BOT_USERNAME=tiverton",
	}, "\n")
	if err := os.WriteFile(filepath.Join(tmpDir, ".env"), []byte(dotEnv), 0o644); err != nil {
		t.Fatalf("write .env: %v", err)
	}

	p := &pod.Pod{
		Name: "test-pod",
		Services: map[string]*pod.Service{
			"bot": {
				Claw: &pod.ClawBlock{
					Handles: map[string]*driver.HandleInfo{
						"discord": {
							ID:       "${BOT_ID}",
							Username: "${BOT_USERNAME}",
							Guilds: []driver.GuildInfo{{
								ID: "${GUILD_ID}",
								Channels: []driver.ChannelInfo{{
									ID:   "${CHANNEL_ID}",
									Name: "trading-floor",
								}},
							}},
						},
					},
				},
			},
		},
	}

	if err := resolveRuntimePlaceholders(tmpDir, p); err != nil {
		t.Fatalf("resolveRuntimePlaceholders: %v", err)
	}

	handle := p.Services["bot"].Claw.Handles["discord"]
	if handle.ID != "123456789" {
		t.Fatalf("expected expanded handle ID, got %q", handle.ID)
	}
	if handle.Username != "tiverton" {
		t.Fatalf("expected expanded username, got %q", handle.Username)
	}
	if handle.Guilds[0].ID != "999888777" {
		t.Fatalf("expected expanded guild ID, got %q", handle.Guilds[0].ID)
	}
	if handle.Guilds[0].Channels[0].ID != "111222333" {
		t.Fatalf("expected expanded channel ID, got %q", handle.Guilds[0].Channels[0].ID)
	}

	configJSON, err := openclaw.GenerateConfig(&driver.ResolvedClaw{
		ServiceName: "bot",
		Handles:     map[string]*driver.HandleInfo{"discord": handle},
		Models:      map[string]string{},
	})
	if err != nil {
		t.Fatalf("GenerateConfig: %v", err)
	}

	var config map[string]interface{}
	if err := json.Unmarshal(configJSON, &config); err != nil {
		t.Fatalf("unmarshal generated config: %v", err)
	}
	guilds := config["channels"].(map[string]interface{})["discord"].(map[string]interface{})["guilds"].(map[string]interface{})
	if _, ok := guilds["999888777"]; !ok {
		t.Fatalf("expected concrete guild ID key in generated config, got %v", guilds)
	}
	if _, ok := guilds["${GUILD_ID}"]; ok {
		t.Fatalf("did not expect unresolved placeholder key in generated config")
	}
}

func TestResolveRuntimePlaceholdersSupportsLowercaseNamesAndDefaults(t *testing.T) {
	tmpDir := t.TempDir()
	dotEnv := strings.Join([]string{
		"bot_id=123456789",
	}, "\n")
	if err := os.WriteFile(filepath.Join(tmpDir, ".env"), []byte(dotEnv), 0o644); err != nil {
		t.Fatalf("write .env: %v", err)
	}

	p := &pod.Pod{
		Name: "test-pod",
		Services: map[string]*pod.Service{
			"bot": {
				Claw: &pod.ClawBlock{
					Agent: "agents/${bot_name:-default}/AGENTS.md",
					Handles: map[string]*driver.HandleInfo{
						"discord": {
							ID: "${bot_id}",
						},
					},
				},
			},
		},
	}

	if err := resolveRuntimePlaceholders(tmpDir, p); err != nil {
		t.Fatalf("resolveRuntimePlaceholders: %v", err)
	}

	if got := p.Services["bot"].Claw.Agent; got != "agents/default/AGENTS.md" {
		t.Fatalf("expected defaulted agent path, got %q", got)
	}
	if got := p.Services["bot"].Claw.Handles["discord"].ID; got != "123456789" {
		t.Fatalf("expected lowercase env var expansion, got %q", got)
	}
}

func TestResolveRuntimePlaceholdersProvidesRepoRootByDefault(t *testing.T) {
	tmpDir := t.TempDir()

	p := &pod.Pod{
		Name: "test-pod",
		Services: map[string]*pod.Service{
			"bot": {
				Claw: &pod.ClawBlock{
					Surfaces: []driver.ResolvedSurface{{
						Scheme: "host",
						Target: "${REPO_ROOT}/storage/shared",
					}},
				},
			},
		},
	}

	if err := resolveRuntimePlaceholders(tmpDir, p); err != nil {
		t.Fatalf("resolveRuntimePlaceholders: %v", err)
	}

	if got := p.Services["bot"].Claw.Surfaces[0].Target; got != filepath.Join(tmpDir, "storage/shared") {
		t.Fatalf("expected REPO_ROOT to default to pod dir, got %q", got)
	}
}

func TestResolveRuntimePlaceholdersRejectsUnresolvedPlaceholders(t *testing.T) {
	p := &pod.Pod{
		Name: "test-pod",
		Services: map[string]*pod.Service{
			"bot": {
				Claw: &pod.ClawBlock{
					Agent: "agents/${missing_agent}/AGENTS.md",
				},
			},
		},
	}

	err := resolveRuntimePlaceholders(t.TempDir(), p)
	if err == nil {
		t.Fatal("expected unresolved placeholder to fail")
	}
	if !strings.Contains(err.Error(), `unresolved x-claw placeholder "${missing_agent}"`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestResolveRuntimePlaceholdersExpandsDiscordAllowFromHandlesAndServices(t *testing.T) {
	p := &pod.Pod{
		Name: "test-pod",
		Services: map[string]*pod.Service{
			"trading-api": {
				Environment: map[string]string{
					"DISCORD_TRADING_API_BOT_TOKEN": "${DISCORD_TRADING_API_BOT_TOKEN}",
				},
			},
			"tiverton": {
				Claw: &pod.ClawBlock{
					Handles: map[string]*driver.HandleInfo{
						"discord": {
							ID: "111111111111111111",
							Guilds: []driver.GuildInfo{{
								ID: "GUILD1",
							}},
						},
					},
					Surfaces: []driver.ResolvedSurface{
						{
							Scheme: "channel",
							Target: "discord",
							ChannelConfig: &driver.ChannelConfig{
								AllowFromHandles:  true,
								AllowFromServices: []string{"trading-api"},
								Guilds: map[string]driver.ChannelGuildConfig{
									"GUILD1": {
										RequireMention: true,
										Users:          []string{"167037070349434880"},
									},
								},
							},
						},
					},
				},
			},
			"weston": {
				Claw: &pod.ClawBlock{
					Handles: map[string]*driver.HandleInfo{
						"discord": {
							ID: "222222222222222222",
						},
					},
				},
			},
		},
	}

	podDir := t.TempDir()
	dotEnv := filepath.Join(podDir, ".env")
	if err := os.WriteFile(dotEnv, []byte("DISCORD_TRADING_API_BOT_TOKEN=MTIzNDU2Nzg5MDEyMzQ1Njc4.x.y\n"), 0o644); err != nil {
		t.Fatalf("write .env: %v", err)
	}
	if err := resolveRuntimePlaceholders(podDir, p); err != nil {
		t.Fatalf("resolveRuntimePlaceholders: %v", err)
	}

	surface := p.Services["tiverton"].Claw.Surfaces[0]
	users := surface.ChannelConfig.Guilds["GUILD1"].Users
	expected := []string{
		"167037070349434880",
		"111111111111111111",
		"123456789012345678",
		"222222222222222222",
	}
	if !slices.Equal(users, expected) {
		t.Fatalf("expected expanded users %v, got %v", expected, users)
	}

	configJSON, err := openclaw.GenerateConfig(&driver.ResolvedClaw{
		ServiceName: "tiverton",
		Handles:     p.Services["tiverton"].Claw.Handles,
		PeerHandles: map[string]map[string]*driver.HandleInfo{
			"weston": p.Services["weston"].Claw.Handles,
		},
		Models:   map[string]string{},
		Surfaces: p.Services["tiverton"].Claw.Surfaces,
	})
	if err != nil {
		t.Fatalf("GenerateConfig: %v", err)
	}

	var config map[string]interface{}
	if err := json.Unmarshal(configJSON, &config); err != nil {
		t.Fatalf("unmarshal generated config: %v", err)
	}
	guild := config["channels"].(map[string]interface{})["discord"].(map[string]interface{})["guilds"].(map[string]interface{})["GUILD1"].(map[string]interface{})
	gotUsers := guild["users"].([]interface{})
	if len(gotUsers) != len(expected) {
		t.Fatalf("expected %d guild users, got %v", len(expected), gotUsers)
	}
	for i, value := range expected {
		if gotUsers[i] != value {
			t.Fatalf("expected guild users %v, got %v", expected, gotUsers)
		}
	}
}

func TestResolveRuntimePlaceholdersRejectsDiscordAllowFromServicesWithoutBotIdentity(t *testing.T) {
	p := &pod.Pod{
		Name: "test-pod",
		Services: map[string]*pod.Service{
			"api": {Environment: map[string]string{}},
			"tiverton": {
				Claw: &pod.ClawBlock{
					Surfaces: []driver.ResolvedSurface{
						{
							Scheme: "channel",
							Target: "discord",
							ChannelConfig: &driver.ChannelConfig{
								AllowFromServices: []string{"api"},
								Guilds: map[string]driver.ChannelGuildConfig{
									"GUILD1": {},
								},
							},
						},
					},
				},
			},
		},
	}

	err := resolveRuntimePlaceholders(t.TempDir(), p)
	if err == nil {
		t.Fatal("expected allow_from_services without bot identity to fail")
	}
	if !strings.Contains(err.Error(), "has no Discord bot identity") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestReadDotEnvFileParsesQuotedValuesAndComments(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	content := strings.Join([]string{
		`BOT_NAME="tiverton #1" # trailing comment`,
		`HANDLE='westin #ops'`,
		`URL=https://example.com/#frag`,
	}, "\n")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write .env: %v", err)
	}

	env, err := readDotEnvFile(path)
	if err != nil {
		t.Fatalf("readDotEnvFile: %v", err)
	}
	if env["BOT_NAME"] != "tiverton #1" {
		t.Fatalf("expected quoted # to be preserved, got %q", env["BOT_NAME"])
	}
	if env["HANDLE"] != "westin #ops" {
		t.Fatalf("expected single-quoted value, got %q", env["HANDLE"])
	}
	if env["URL"] != "https://example.com/#frag" {
		t.Fatalf("expected inline # without leading space to be preserved, got %q", env["URL"])
	}
}

func TestResolveAgentTimezoneUsesResolvedTZ(t *testing.T) {
	got := resolveAgentTimezone(map[string]string{"TZ": "${BOT_TZ}"}, map[string]string{"BOT_TZ": "America/New_York"})
	if got != "America/New_York" {
		t.Fatalf("expected America/New_York, got %q", got)
	}
}

func TestResolveAgentTimezoneFallsBackToUTCOnInvalidTZ(t *testing.T) {
	got := resolveAgentTimezone(map[string]string{"TZ": "Mars/Olympus"}, map[string]string{})
	if got != "UTC" {
		t.Fatalf("expected UTC fallback, got %q", got)
	}
}

func TestValidateCllamaEnvFilesRejectsProviderKeys(t *testing.T) {
	tmpDir := t.TempDir()
	envPath := filepath.Join(tmpDir, "bot.env")
	if err := os.WriteFile(envPath, []byte("OPENAI_API_KEY=sk-real\nSAFE=yes\n"), 0o644); err != nil {
		t.Fatalf("write env_file: %v", err)
	}

	svc := &pod.Service{
		Compose: map[string]interface{}{
			"env_file": []interface{}{
				map[string]interface{}{
					"path":     "bot.env",
					"required": true,
				},
			},
		},
	}

	err := validateCllamaEnvFiles(tmpDir, "bot", svc)
	if err == nil {
		t.Fatal("expected provider key in env_file to fail")
	}
	if !strings.Contains(err.Error(), `provider key "OPENAI_API_KEY" found in env_file`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestMaterializeContractIncludesBuildsGeneratedContractAndReferenceSkill(t *testing.T) {
	baseDir := t.TempDir()
	runtimeDir := filepath.Join(baseDir, ".claw-runtime", "bot")
	if err := os.MkdirAll(runtimeDir, 0o755); err != nil {
		t.Fatalf("mkdir runtime dir: %v", err)
	}

	agentPath := filepath.Join(baseDir, "AGENTS.md")
	enforcePath := filepath.Join(baseDir, "governance", "risk-limits.md")
	referencePath := filepath.Join(baseDir, "playbooks", "strategy.md")
	if err := os.MkdirAll(filepath.Dir(enforcePath), 0o755); err != nil {
		t.Fatalf("mkdir governance dir: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(referencePath), 0o755); err != nil {
		t.Fatalf("mkdir playbooks dir: %v", err)
	}
	if err := os.WriteFile(agentPath, []byte("# Base Contract\n"), 0o644); err != nil {
		t.Fatalf("write base contract: %v", err)
	}
	if err := os.WriteFile(enforcePath, []byte("No unauthorized trades.\n"), 0o644); err != nil {
		t.Fatalf("write enforce include: %v", err)
	}
	if err := os.WriteFile(referencePath, []byte("# Strategy Notes\n"), 0o644); err != nil {
		t.Fatalf("write reference include: %v", err)
	}

	includes := []pod.IncludeEntry{
		{ID: "risk_limits", File: "./governance/risk-limits.md", Mode: "enforce", Description: "Hard trading rules"},
		{ID: "strategy_notes", File: "./playbooks/strategy.md", Mode: "reference", Description: "Desk playbook"},
	}

	resolved, skills, err := materializeContractIncludes(baseDir, runtimeDir, agentPath, includes)
	if err != nil {
		t.Fatalf("materializeContractIncludes: %v", err)
	}
	if len(resolved) != 2 {
		t.Fatalf("expected 2 resolved includes, got %d", len(resolved))
	}
	if len(skills) != 1 {
		t.Fatalf("expected 1 generated reference skill, got %d", len(skills))
	}
	if skills[0].Name != "include-strategy_notes.md" {
		t.Fatalf("unexpected generated skill name: %q", skills[0].Name)
	}

	generatedPath := filepath.Join(runtimeDir, "AGENTS.generated.md")
	generated, err := os.ReadFile(generatedPath)
	if err != nil {
		t.Fatalf("read generated contract: %v", err)
	}
	text := string(generated)
	if !strings.Contains(text, "# Base Contract") {
		t.Fatalf("expected base contract in generated output")
	}
	if !strings.Contains(text, "--- BEGIN: risk_limits (enforce) ---") {
		t.Fatalf("expected enforce include marker in generated contract:\n%s", text)
	}
	if !strings.Contains(text, "No unauthorized trades.") {
		t.Fatalf("expected enforce include content in generated contract:\n%s", text)
	}

	referenceSkill, err := os.ReadFile(skills[0].HostPath)
	if err != nil {
		t.Fatalf("read generated reference skill: %v", err)
	}
	if string(referenceSkill) != "# Strategy Notes\n" {
		t.Fatalf("unexpected reference skill content: %q", string(referenceSkill))
	}
}

func TestPrepareRuntimeDirStagesPreviousTreeUntilCommit(t *testing.T) {
	tmpDir := t.TempDir()
	runtimeDir := filepath.Join(tmpDir, ".claw-runtime")
	staleDir := filepath.Join(runtimeDir, "nb-roll", "skills", "handle-discord.md")
	if err := os.MkdirAll(staleDir, 0o755); err != nil {
		t.Fatalf("create stale dir: %v", err)
	}
	staleFile := filepath.Join(runtimeDir, "stale.txt")
	if err := os.WriteFile(staleFile, []byte("stale"), 0o644); err != nil {
		t.Fatalf("write stale file: %v", err)
	}

	stage, err := prepareRuntimeDir(runtimeDir)
	if err != nil {
		t.Fatalf("prepare runtime dir: %v", err)
	}

	info, err := os.Stat(runtimeDir)
	if err != nil {
		t.Fatalf("stat runtime dir: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("expected runtime dir to exist as directory")
	}
	if stage.PreviousPath == "" {
		t.Fatal("expected previous runtime dir to be preserved during staging")
	}
	if _, err := os.Stat(staleDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected stale dir to be absent from fresh runtime dir, got err=%v", err)
	}
	if _, err := os.Stat(staleFile); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected stale file to be absent from fresh runtime dir, got err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(stage.PreviousPath, "nb-roll", "skills", "handle-discord.md")); err != nil {
		t.Fatalf("expected staged previous runtime tree to preserve stale dir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(stage.PreviousPath, "stale.txt")); err != nil {
		t.Fatalf("expected staged previous runtime tree to preserve stale file: %v", err)
	}

	if err := stage.Commit(); err != nil {
		t.Fatalf("commit runtime dir: %v", err)
	}
	if _, err := os.Stat(stage.PreviousPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected previous runtime dir to be removed after commit, got err=%v", err)
	}
}

func TestPrepareRuntimeDirRollbackRestoresPreviousTree(t *testing.T) {
	tmpDir := t.TempDir()
	runtimeDir := filepath.Join(tmpDir, ".claw-runtime")
	if err := os.MkdirAll(runtimeDir, 0o755); err != nil {
		t.Fatalf("mkdir runtime dir: %v", err)
	}
	originalPath := filepath.Join(runtimeDir, "AGENTS.generated.md")
	if err := os.WriteFile(originalPath, []byte("old"), 0o644); err != nil {
		t.Fatalf("write original runtime file: %v", err)
	}

	stage, err := prepareRuntimeDir(runtimeDir)
	if err != nil {
		t.Fatalf("prepare runtime dir: %v", err)
	}
	replacementPath := filepath.Join(runtimeDir, "AGENTS.generated.md")
	if err := os.WriteFile(replacementPath, []byte("new"), 0o644); err != nil {
		t.Fatalf("write replacement runtime file: %v", err)
	}

	if err := stage.Rollback(); err != nil {
		t.Fatalf("rollback runtime dir: %v", err)
	}

	data, err := os.ReadFile(originalPath)
	if err != nil {
		t.Fatalf("read restored runtime file: %v", err)
	}
	if string(data) != "old" {
		t.Fatalf("restored runtime file = %q, want %q", string(data), "old")
	}
	if _, err := os.Stat(stage.PreviousPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected previous runtime dir to be consumed by rollback, got err=%v", err)
	}
}

func TestRuntimeConsumerServicesIncludesManagedServicesAndInfra(t *testing.T) {
	services := runtimeConsumerServices(
		map[string]*driver.ResolvedClaw{
			"assistant": {Count: 1},
			"worker":    {Count: 2},
		},
		[]pod.CllamaProxyConfig{{ProxyType: "passthrough"}},
		&pod.ClawAPIConfig{},
		&pod.ClawdashConfig{},
	)

	want := []string{"assistant", "claw-api", "clawdash", "cllama", "worker-0", "worker-1"}
	if !slices.Equal(services, want) {
		t.Fatalf("unexpected runtime consumer services: got %v want %v", services, want)
	}
}

func TestRuntimeConsumerServicesDeduplicatesAndSorts(t *testing.T) {
	services := runtimeConsumerServices(
		map[string]*driver.ResolvedClaw{
			"zeta":  {Count: 1},
			"alpha": nil,
		},
		[]pod.CllamaProxyConfig{{ProxyType: "passthrough"}, {ProxyType: "passthrough"}},
		nil,
		nil,
	)

	want := []string{"alpha", "cllama", "zeta"}
	if !slices.Equal(services, want) {
		t.Fatalf("unexpected runtime consumer services: got %v want %v", services, want)
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

func TestPrepareClawAPIRuntimeWritesPrincipalsAndProjectsAuth(t *testing.T) {
	runtimeDir := t.TempDir()
	p := &pod.Pod{
		Name:   "trading-desk",
		Master: "octopus",
		Services: map[string]*pod.Service{
			"octopus": {
				Environment: map[string]string{},
				Claw:        &pod.ClawBlock{},
			},
		},
		ClawAPI: &pod.ClawAPIConfig{
			Addr:               ":8080",
			PrincipalsHostPath: filepath.Join(runtimeDir, "claw-api", "principals.json"),
		},
	}

	auth, err := prepareClawAPIRuntime(runtimeDir, p, map[string]*driver.ResolvedClaw{
		"octopus": {Count: 1},
	})
	if err != nil {
		t.Fatalf("prepareClawAPIRuntime: %v", err)
	}

	if p.Services["octopus"].Environment["CLAW_API_URL"] != "http://claw-api:8080" {
		t.Fatalf("expected CLAW_API_URL env, got %v", p.Services["octopus"].Environment)
	}
	if token := p.Services["octopus"].Environment["CLAW_API_TOKEN"]; token == "" {
		t.Fatal("expected CLAW_API_TOKEN in master service env")
	}
	if auth["octopus"].Service != "claw-api" || auth["octopus"].Token == "" {
		t.Fatalf("expected projected claw-api auth, got %+v", auth)
	}
	if _, err := os.Stat(p.ClawAPI.PrincipalsHostPath); err != nil {
		t.Fatalf("expected principals file to be written: %v", err)
	}
	raw, err := os.ReadFile(p.ClawAPI.PrincipalsHostPath)
	if err != nil {
		t.Fatalf("read principals: %v", err)
	}
	if !strings.Contains(string(raw), "claw-dashboard") || !strings.Contains(string(raw), clawapi.VerbAgentContext) {
		t.Fatalf("expected dashboard principal with agent context verb, got %s", string(raw))
	}
}

func TestPrepareClawAPIRuntimeUsesPostMergeMasterToken(t *testing.T) {
	// If an explicit principal overrides the master by name, the injected token
	// must be the post-merge one, not the pre-merge auto-generated one.
	runtimeDir := t.TempDir()
	p := &pod.Pod{
		Name:   "ops",
		Master: "octopus",
		Services: map[string]*pod.Service{
			"octopus": {Environment: map[string]string{}, Claw: &pod.ClawBlock{}},
		},
		Principals: []pod.PodPrincipal{
			// Override master by name with read-only verbs.
			{Name: "octopus", Verbs: clawapi.AllReadVerbs, Scope: "pod"},
		},
		ClawAPI: &pod.ClawAPIConfig{
			Addr:               ":8080",
			PrincipalsHostPath: filepath.Join(runtimeDir, "claw-api", "principals.json"),
		},
	}

	auth, err := prepareClawAPIRuntime(runtimeDir, p, map[string]*driver.ResolvedClaw{
		"octopus": {Count: 1},
	})
	if err != nil {
		t.Fatalf("prepareClawAPIRuntime: %v", err)
	}

	injectedToken := p.Services["octopus"].Environment["CLAW_API_TOKEN"]
	if injectedToken == "" {
		t.Fatal("expected CLAW_API_TOKEN in master service env")
	}
	// The cllama auth entry must use the same (post-merge) token.
	if auth["octopus"].Token != injectedToken {
		t.Fatalf("cllama auth token %q != injected token %q (pre-merge token leaked)", auth["octopus"].Token, injectedToken)
	}
	// Verify the token in principals.json matches the injected one.
	raw, err := os.ReadFile(p.ClawAPI.PrincipalsHostPath)
	if err != nil {
		t.Fatalf("read principals: %v", err)
	}
	if !strings.Contains(string(raw), injectedToken) {
		t.Fatalf("injected token %q not found in principals.json", injectedToken)
	}
}

func TestPrepareClawAPIRuntimeWithoutMasterWritesSchedulerPrincipal(t *testing.T) {
	runtimeDir := t.TempDir()
	p := &pod.Pod{
		Name: "ops",
		Services: map[string]*pod.Service{
			"westin": {
				Claw: &pod.ClawBlock{
					Invoke: []pod.InvokeEntry{{
						Schedule: "0 9 * * 1-5",
						Message:  "Open the market.",
					}},
				},
			},
		},
		ClawAPI: &pod.ClawAPIConfig{
			Addr:               ":8080",
			PrincipalsHostPath: filepath.Join(runtimeDir, "claw-api", "principals.json"),
		},
	}

	auth, err := prepareClawAPIRuntime(runtimeDir, p, map[string]*driver.ResolvedClaw{
		"westin": {Count: 1},
	})
	if err != nil {
		t.Fatalf("prepareClawAPIRuntime: %v", err)
	}
	if auth != nil {
		t.Fatalf("expected no cllama auth without master, got %+v", auth)
	}
	if env := p.Services["westin"].Environment; len(env) != 0 {
		t.Fatalf("did not expect token injection without master, got %v", env)
	}
	raw, err := os.ReadFile(p.ClawAPI.PrincipalsHostPath)
	if err != nil {
		t.Fatalf("read principals: %v", err)
	}
	if !strings.Contains(string(raw), "claw-scheduler") {
		t.Fatalf("expected scheduler principal in principals.json, got %s", string(raw))
	}
	if !strings.Contains(string(raw), "claw-dashboard") {
		t.Fatalf("expected dashboard principal in principals.json, got %s", string(raw))
	}
	if !strings.Contains(string(raw), clawapi.VerbScheduleRead) || !strings.Contains(string(raw), clawapi.VerbScheduleControl) {
		t.Fatalf("expected schedule verbs in principals.json, got %s", string(raw))
	}
}

func TestPrepareClawAPIRuntimeWithMasterAndInvokeAlsoWritesSchedulerPrincipal(t *testing.T) {
	runtimeDir := t.TempDir()
	p := &pod.Pod{
		Name:   "ops",
		Master: "octopus",
		Services: map[string]*pod.Service{
			"octopus": {
				Environment: map[string]string{},
				Claw:        &pod.ClawBlock{},
			},
			"westin": {
				Claw: &pod.ClawBlock{
					Invoke: []pod.InvokeEntry{{
						Schedule: "0 9 * * 1-5",
						Message:  "Open the market.",
					}},
				},
			},
		},
		ClawAPI: &pod.ClawAPIConfig{
			Addr:               ":8080",
			PrincipalsHostPath: filepath.Join(runtimeDir, "claw-api", "principals.json"),
		},
	}

	_, err := prepareClawAPIRuntime(runtimeDir, p, map[string]*driver.ResolvedClaw{
		"octopus": {Count: 1},
		"westin":  {Count: 1},
	})
	if err != nil {
		t.Fatalf("prepareClawAPIRuntime: %v", err)
	}

	raw, err := os.ReadFile(p.ClawAPI.PrincipalsHostPath)
	if err != nil {
		t.Fatalf("read principals: %v", err)
	}
	if !strings.Contains(string(raw), "claw-scheduler") {
		t.Fatalf("expected scheduler principal in principals.json, got %s", string(raw))
	}
}

func TestPrepareClawAPIRuntimeWarnsWhenClawAPIImageMayNotSupportPrincipalVerb(t *testing.T) {
	runtimeDir := t.TempDir()
	p := &pod.Pod{
		Name: "ops",
		Services: map[string]*pod.Service{
			"westin": {
				Claw: &pod.ClawBlock{
					Invoke: []pod.InvokeEntry{{
						Schedule: "0 9 * * 1-5",
						Message:  "Open the market.",
					}},
				},
			},
		},
		ClawAPI: &pod.ClawAPIConfig{
			Image:              "ghcr.io/mostlydev/claw-api:v0.4.2",
			Addr:               ":8080",
			PrincipalsHostPath: filepath.Join(runtimeDir, "claw-api", "principals.json"),
		},
	}

	out, err := captureStdout(t, func() error {
		_, err := prepareClawAPIRuntime(runtimeDir, p, map[string]*driver.ResolvedClaw{
			"westin": {Count: 1},
		})
		return err
	})
	if err != nil {
		t.Fatalf("prepareClawAPIRuntime: %v", err)
	}
	if !strings.Contains(out, `ghcr.io/mostlydev/claw-api:v0.4.2`) || !strings.Contains(out, `known minimum v0.6.0`) {
		t.Fatalf("expected skew warning in output, got %q", out)
	}
}

func TestPrepareClawAPIRuntimeRejectsInjectIntoReservedMasterService(t *testing.T) {
	runtimeDir := t.TempDir()
	p := &pod.Pod{
		Name:   "ops",
		Master: "octopus",
		Services: map[string]*pod.Service{
			"octopus":   {Environment: map[string]string{}, Claw: &pod.ClawBlock{}},
			"ci-runner": {Environment: map[string]string{}, Claw: &pod.ClawBlock{}},
		},
		Principals: []pod.PodPrincipal{
			// inject-into targets the master service — must fail.
			{Name: "ci", Verbs: clawapi.AllReadVerbs, Scope: "pod", InjectInto: "octopus"},
		},
		ClawAPI: &pod.ClawAPIConfig{
			Addr:               ":8080",
			PrincipalsHostPath: filepath.Join(runtimeDir, "claw-api", "principals.json"),
		},
	}

	_, err := prepareClawAPIRuntime(runtimeDir, p, map[string]*driver.ResolvedClaw{
		"octopus": {Count: 1},
	})
	if err == nil {
		t.Fatal("expected error for inject-into targeting master service")
	}
	if !strings.Contains(err.Error(), "reserved") {
		t.Fatalf("expected 'reserved' in error, got: %v", err)
	}
}

func TestPrepareClawAPIRuntimeRejectsInjectIntoSelfPrincipalService(t *testing.T) {
	runtimeDir := t.TempDir()
	p := &pod.Pod{
		Name:   "ops",
		Master: "octopus",
		Services: map[string]*pod.Service{
			"octopus":  {Environment: map[string]string{}, Claw: &pod.ClawBlock{}},
			"reporter": {Environment: map[string]string{}, Claw: &pod.ClawBlock{ClawAPIMode: "self"}},
		},
		Principals: []pod.PodPrincipal{
			// inject-into targets a claw-api: self service — must fail.
			{Name: "external", Verbs: clawapi.AllReadVerbs, Scope: "pod", InjectInto: "reporter"},
		},
		ClawAPI: &pod.ClawAPIConfig{
			Addr:               ":8080",
			PrincipalsHostPath: filepath.Join(runtimeDir, "claw-api", "principals.json"),
		},
	}

	_, err := prepareClawAPIRuntime(runtimeDir, p, map[string]*driver.ResolvedClaw{
		"octopus":  {Count: 1},
		"reporter": {Count: 1},
	})
	if err == nil {
		t.Fatal("expected error for inject-into targeting claw-api: self service")
	}
	if !strings.Contains(err.Error(), "reserved") {
		t.Fatalf("expected 'reserved' in error, got: %v", err)
	}
}

func TestValidateClawAPIDeclarationsAllowsNoMasterNoDeclarations(t *testing.T) {
	p := &pod.Pod{
		Services: map[string]*pod.Service{
			"analyst": {Claw: &pod.ClawBlock{}},
		},
	}
	if err := validateClawAPIDeclarations(p); err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
}

func TestValidateClawAPIDeclarationsRejectsPrincipalsWithoutMaster(t *testing.T) {
	p := &pod.Pod{
		Principals: []pod.PodPrincipal{
			{Name: "ci", Verbs: []string{clawapi.VerbFleetStatus}, Scope: "pod"},
		},
		Services: map[string]*pod.Service{},
	}
	err := validateClawAPIDeclarations(p)
	if err == nil {
		t.Fatal("expected error for principals without master")
	}
	if !strings.Contains(err.Error(), "x-claw.master") {
		t.Fatalf("expected x-claw.master in error, got: %v", err)
	}
}

func TestValidateClawAPIDeclarationsRejectsClawAPISelfWithoutMaster(t *testing.T) {
	p := &pod.Pod{
		Services: map[string]*pod.Service{
			"reporter": {Claw: &pod.ClawBlock{ClawAPIMode: "self"}},
		},
	}
	err := validateClawAPIDeclarations(p)
	if err == nil {
		t.Fatal("expected error for claw-api: self without master")
	}
	if !strings.Contains(err.Error(), "x-claw.master") {
		t.Fatalf("expected x-claw.master in error, got: %v", err)
	}
}

func TestValidateClawAPIDeclarationsAllowsDeclarationsWithMaster(t *testing.T) {
	p := &pod.Pod{
		Master: "octopus",
		Principals: []pod.PodPrincipal{
			{Name: "ci", Verbs: []string{clawapi.VerbFleetStatus}, Scope: "pod"},
		},
		Services: map[string]*pod.Service{
			"octopus":  {Claw: &pod.ClawBlock{}},
			"reporter": {Claw: &pod.ClawBlock{ClawAPIMode: "self"}},
		},
	}
	if err := validateClawAPIDeclarations(p); err != nil {
		t.Fatalf("expected no error with master set, got: %v", err)
	}
}

func TestValidateManagedCapabilityDeclarationsAllowsCllamaManagedService(t *testing.T) {
	const yaml = `
x-claw:
  pod: capabilities-pod

services:
  analyst:
    image: analyst:latest
    x-claw:
      agent: ./AGENTS.md
      cllama: passthrough
      tools:
        - trading-api
      memory:
        service: team-memory
`

	p, err := pod.Parse(strings.NewReader(yaml))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	err = validateManagedCapabilityDeclarations(p, map[string]*driver.ResolvedClaw{
		"analyst": {Cllama: []string{"passthrough"}},
	})
	if err != nil {
		t.Fatalf("expected cllama-managed service to pass validation, got: %v", err)
	}
}

func TestValidateManagedCapabilityDeclarationsRejectsNonCllamaServiceLevelDeclarations(t *testing.T) {
	const yaml = `
x-claw:
  pod: capabilities-pod

services:
  analyst:
    image: analyst:latest
    x-claw:
      agent: ./AGENTS.md
      tools:
        - trading-api
      memory:
        service: team-memory
`

	p, err := pod.Parse(strings.NewReader(yaml))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	err = validateManagedCapabilityDeclarations(p, map[string]*driver.ResolvedClaw{
		"analyst": {},
	})
	if err == nil {
		t.Fatal("expected non-cllama capability declaration error")
	}
	for _, want := range []string{"service \"analyst\"", "x-claw.tools", "x-claw.memory", "x-claw.cllama"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("expected %q in error, got: %v", want, err)
		}
	}
}

func TestValidateManagedCapabilityDeclarationsRejectsInheritedDefaultsWithoutCllama(t *testing.T) {
	const yaml = `
x-claw:
  pod: defaults-pod
  tools-defaults:
    - service: trading-api
  memory-defaults:
    service: team-memory

services:
  worker:
    image: worker:latest
    x-claw:
      agent: ./AGENTS.md
`

	p, err := pod.Parse(strings.NewReader(yaml))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	err = validateManagedCapabilityDeclarations(p, map[string]*driver.ResolvedClaw{
		"worker": {},
	})
	if err == nil {
		t.Fatal("expected inherited non-cllama capability declaration error")
	}
	for _, want := range []string{"service \"worker\"", "x-claw.tools", "x-claw.memory"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("expected %q in error, got: %v", want, err)
		}
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

func TestBuildPlannedServiceImagesBuildOnlyClawfile(t *testing.T) {
	tmpDir := t.TempDir()
	clawfilePath := filepath.Join(tmpDir, "agents", "shared", "OpenClawfile")
	if err := os.MkdirAll(filepath.Dir(clawfilePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(clawfilePath, []byte("FROM alpine\nCLAW_TYPE openclaw\nAGENT AGENTS.md\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	svc := &pod.Service{
		Compose: map[string]interface{}{
			"build": map[string]interface{}{
				"context":    ".",
				"dockerfile": filepath.ToSlash(filepath.Join("agents", "shared", "OpenClawfile")),
			},
		},
	}
	prevExists := imageExistsLocally
	prevGenerate := generateClawDockerfile
	prevBuildGenerated := buildGeneratedImage
	prevDockerBuild := dockerBuildTaggedImage
	defer func() {
		imageExistsLocally = prevExists
		generateClawDockerfile = prevGenerate
		buildGeneratedImage = prevBuildGenerated
		dockerBuildTaggedImage = prevDockerBuild
	}()

	imageExistsLocally = func(string) bool { return false }
	generatedPath := filepath.Join(tmpDir, "Dockerfile.generated")
	generateClawDockerfile = func(path string) (string, error) {
		if path != clawfilePath {
			t.Fatalf("expected Clawfile path %q, got %q", clawfilePath, path)
		}
		return generatedPath, nil
	}
	var builtTag string
	var builtContext string
	buildGeneratedImage = func(path, tag, contextDir string) error {
		if path != generatedPath {
			t.Fatalf("expected generated path %q, got %q", generatedPath, path)
		}
		builtTag = tag
		builtContext = contextDir
		return nil
	}
	dockerBuildTaggedImage = func(string, string, string, map[string]buildArgValue, string) error {
		t.Fatal("unexpected plain docker build for Clawfile build")
		return nil
	}

	plans, err := planPodServiceImages(&pod.Pod{
		Name: "Research Pod",
		Services: map[string]*pod.Service{
			"bot": svc,
		},
	})
	if err != nil {
		t.Fatalf("planPodServiceImages: %v", err)
	}
	if len(plans) != 1 {
		t.Fatalf("expected one planned image, got %d", len(plans))
	}
	imageRef := plans[0].ImageRef
	if err := buildPlannedServiceImages("claw-pod.yml", tmpDir, plans, false); err != nil {
		t.Fatalf("buildPlannedServiceImages: %v", err)
	}
	if imageRef != "claw-local/research-pod-bot:latest" {
		t.Fatalf("unexpected generated image ref: %q", imageRef)
	}
	if svc.Image != imageRef {
		t.Fatalf("expected service image to be set to %q, got %q", imageRef, svc.Image)
	}
	if got := svc.Compose["image"]; got != imageRef {
		t.Fatalf("expected compose image to be set to %q, got %v", imageRef, got)
	}
	if builtTag != imageRef {
		t.Fatalf("expected built tag %q, got %q", imageRef, builtTag)
	}
	if builtContext != tmpDir {
		t.Fatalf("expected build context %q, got %q", tmpDir, builtContext)
	}
}

func TestBuildPlannedServiceImagesRebuildsClawfileBuildWhenTagExistsLocally(t *testing.T) {
	tmpDir := t.TempDir()
	clawfilePath := filepath.Join(tmpDir, "agents", "shared", "OpenClawfile")
	if err := os.MkdirAll(filepath.Dir(clawfilePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(clawfilePath, []byte("FROM alpine\nCLAW_TYPE openclaw\nAGENT AGENTS.md\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	svc := &pod.Service{
		Image: "ghcr.io/example/bot:latest",
		Compose: map[string]interface{}{
			"build": map[string]interface{}{
				"context":    ".",
				"dockerfile": filepath.ToSlash(filepath.Join("agents", "shared", "OpenClawfile")),
			},
		},
	}
	prevExists := imageExistsLocally
	prevGenerate := generateClawDockerfile
	prevBuildGenerated := buildGeneratedImage
	prevDockerBuild := dockerBuildTaggedImage
	defer func() {
		imageExistsLocally = prevExists
		generateClawDockerfile = prevGenerate
		buildGeneratedImage = prevBuildGenerated
		dockerBuildTaggedImage = prevDockerBuild
	}()

	imageExistsLocally = func(image string) bool { return image == svc.Image }
	generatedPath := filepath.Join(tmpDir, "Dockerfile.generated")
	generateClawDockerfile = func(path string) (string, error) {
		if path != clawfilePath {
			t.Fatalf("expected Clawfile path %q, got %q", clawfilePath, path)
		}
		return generatedPath, nil
	}
	var built bool
	buildGeneratedImage = func(path, tag, contextDir string) error {
		built = true
		if path != generatedPath {
			t.Fatalf("expected generated path %q, got %q", generatedPath, path)
		}
		if tag != svc.Image {
			t.Fatalf("expected built tag %q, got %q", svc.Image, tag)
		}
		if contextDir != tmpDir {
			t.Fatalf("expected build context %q, got %q", tmpDir, contextDir)
		}
		return nil
	}
	dockerBuildTaggedImage = func(string, string, string, map[string]buildArgValue, string) error {
		t.Fatal("unexpected plain docker build for Clawfile build")
		return nil
	}

	plans, err := planPodServiceImages(&pod.Pod{
		Name: "Research Pod",
		Services: map[string]*pod.Service{
			"bot": svc,
		},
	})
	if err != nil {
		t.Fatalf("planPodServiceImages: %v", err)
	}
	if err := buildPlannedServiceImages("claw-pod.yml", tmpDir, plans, false); err != nil {
		t.Fatalf("buildPlannedServiceImages: %v", err)
	}
	imageRef := plans[0].ImageRef
	if imageRef != svc.Image {
		t.Fatalf("expected image ref %q, got %q", svc.Image, imageRef)
	}
	if !built {
		t.Fatal("expected managed image build to run even when the tagged image already exists locally")
	}
}

func TestBuildPlannedServiceImagesBuildsPlainDockerfile(t *testing.T) {
	tmpDir := t.TempDir()
	dockerfilePath := filepath.Join(tmpDir, "Dockerfile")
	if err := os.WriteFile(dockerfilePath, []byte("FROM alpine\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	svc := &pod.Service{
		Image: "ghcr.io/example/bot:latest",
		Compose: map[string]interface{}{
			"build": map[string]interface{}{
				"context":    ".",
				"dockerfile": "Dockerfile",
				"target":     "runner",
				"args": map[string]interface{}{
					"FOO": "bar",
				},
			},
		},
	}
	prevExists := imageExistsLocally
	prevGenerate := generateClawDockerfile
	prevBuildGenerated := buildGeneratedImage
	prevDockerBuild := dockerBuildTaggedImage
	defer func() {
		imageExistsLocally = prevExists
		generateClawDockerfile = prevGenerate
		buildGeneratedImage = prevBuildGenerated
		dockerBuildTaggedImage = prevDockerBuild
	}()

	imageExistsLocally = func(string) bool { return false }
	generateClawDockerfile = func(string) (string, error) {
		t.Fatal("unexpected Clawfile generation for plain Dockerfile build")
		return "", nil
	}
	buildGeneratedImage = func(string, string, string) error {
		t.Fatal("unexpected generated-image build for plain Dockerfile build")
		return nil
	}

	var gotImageRef, gotDockerfile, gotContext, gotTarget string
	var gotArgs map[string]buildArgValue
	dockerBuildTaggedImage = func(imageRef, dockerfile, contextDir string, args map[string]buildArgValue, target string) error {
		gotImageRef = imageRef
		gotDockerfile = dockerfile
		gotContext = contextDir
		gotArgs = args
		gotTarget = target
		return nil
	}

	plans, err := planPodServiceImages(&pod.Pod{
		Name: "test-pod",
		Services: map[string]*pod.Service{
			"bot": svc,
		},
	})
	if err != nil {
		t.Fatalf("planPodServiceImages: %v", err)
	}
	if err := buildPlannedServiceImages("claw-pod.yml", tmpDir, plans, false); err != nil {
		t.Fatalf("buildPlannedServiceImages: %v", err)
	}
	imageRef := plans[0].ImageRef
	if imageRef != "ghcr.io/example/bot:latest" {
		t.Fatalf("expected image ref to remain unchanged, got %q", imageRef)
	}
	if gotImageRef != imageRef {
		t.Fatalf("expected docker build image ref %q, got %q", imageRef, gotImageRef)
	}
	if gotDockerfile != dockerfilePath {
		t.Fatalf("expected dockerfile %q, got %q", dockerfilePath, gotDockerfile)
	}
	if gotContext != tmpDir {
		t.Fatalf("expected build context %q, got %q", tmpDir, gotContext)
	}
	if gotTarget != "runner" {
		t.Fatalf("expected target runner, got %q", gotTarget)
	}
	if gotArgs["FOO"] != (buildArgValue{Value: "bar"}) {
		t.Fatalf("expected build args to be passed through, got %v", gotArgs)
	}
}

func TestWarnRunnerBaseDriftPrintsHint(t *testing.T) {
	prevExists := imageExistsLocally
	prevInspect := inspectClawImage
	prevResolve := resolveLocalRunnerProvenance
	defer func() {
		imageExistsLocally = prevExists
		inspectClawImage = prevInspect
		resolveLocalRunnerProvenance = prevResolve
	}()

	imageExistsLocally = func(string) bool { return true }
	inspectClawImage = func(string) (*inspect.ClawInfo, error) {
		return &inspect.ClawInfo{
			RunnerDriver: "openclaw",
			RunnerBuilt:  "openclaw:built-20260415-oldoldoldold",
			RunnerImage:  "sha256:oldoldoldold1234",
		}, nil
	}
	resolveLocalRunnerProvenance = func(string, driver.RunnerBaseProvider) (*clawfile.RunnerProvenance, error) {
		return &clawfile.RunnerProvenance{
			BuiltRef: "openclaw:built-20260415-newnewnewnew",
			ImageID:  "sha256:newnewnewnew5678",
		}, nil
	}

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	prevStdout := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = prevStdout }()

	warnRunnerBaseDrift("examples/quickstart/claw-pod.yml", []plannedServiceImage{
		{
			ServiceName: "analyst",
			ImageRef:    "claw-local/test-analyst:latest",
			BuildConfig: &serviceBuildConfig{Context: "."},
		},
	})

	if err := w.Close(); err != nil {
		t.Fatalf("close write pipe: %v", err)
	}
	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read warning output: %v", err)
	}
	out := string(data)
	if !strings.Contains(out, "analyst: built against openclaw:built-20260415-oldoldoldold") {
		t.Fatalf("expected drift warning, got %q", out)
	}
	if !strings.Contains(out, "current local alias is openclaw:built-20260415-newnewnewnew") {
		t.Fatalf("expected current local alias in warning, got %q", out)
	}
	if !strings.Contains(out, "consider running: claw build -f examples/quickstart/claw-pod.yml") {
		t.Fatalf("expected claw build hint, got %q", out)
	}
}

func TestParseBuildArgsPreservesPassthroughSemantics(t *testing.T) {
	args, err := parseBuildArgs([]interface{}{"FROM_SHELL", "EXPLICIT_EMPTY=", "FOO=bar"})
	if err != nil {
		t.Fatalf("parseBuildArgs(list): %v", err)
	}
	if got := args["FROM_SHELL"]; got != (buildArgValue{Passthrough: true}) {
		t.Fatalf("expected FROM_SHELL passthrough, got %#v", got)
	}
	if got := args["EXPLICIT_EMPTY"]; got != (buildArgValue{Value: ""}) {
		t.Fatalf("expected EXPLICIT_EMPTY explicit empty, got %#v", got)
	}
	if got := args["FOO"]; got != (buildArgValue{Value: "bar"}) {
		t.Fatalf("expected FOO=bar, got %#v", got)
	}

	args, err = parseBuildArgs(map[string]interface{}{
		"FROM_MAP": nil,
		"EMPTY":    "",
	})
	if err != nil {
		t.Fatalf("parseBuildArgs(map): %v", err)
	}
	if got := args["FROM_MAP"]; got != (buildArgValue{Passthrough: true}) {
		t.Fatalf("expected FROM_MAP passthrough, got %#v", got)
	}
	if got := args["EMPTY"]; got != (buildArgValue{Value: ""}) {
		t.Fatalf("expected EMPTY explicit empty, got %#v", got)
	}
}

func TestDockerBuildTaggedImageDefaultOmitsEqualsForPassthroughArgs(t *testing.T) {
	dir := t.TempDir()
	binDir := filepath.Join(dir, "bin")
	argsFile := filepath.Join(dir, "docker-args.txt")
	dockerPath := filepath.Join(binDir, "docker")

	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dockerPath, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$DOCKER_ARGS_FILE\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	t.Setenv("DOCKER_ARGS_FILE", argsFile)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	err := dockerBuildTaggedImageDefault(
		"example:latest",
		"/tmp/Dockerfile",
		"/tmp/context",
		map[string]buildArgValue{
			"BAR": {Value: ""},
			"FOO": {Passthrough: true},
		},
		"",
	)
	if err != nil {
		t.Fatalf("dockerBuildTaggedImageDefault: %v", err)
	}

	data, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("read fake docker args: %v", err)
	}
	got := strings.Split(strings.TrimSpace(string(data)), "\n")
	want := []string{
		"build",
		"-t", "example:latest",
		"-f", "/tmp/Dockerfile",
		"--build-arg", "BAR=",
		"--build-arg", "FOO",
		"/tmp/context",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("unexpected docker args: got %v want %v", got, want)
	}
}

func TestResolveManagedServiceImageRequiresImageOrBuild(t *testing.T) {
	svc := &pod.Service{}
	p := &pod.Pod{Name: "test-pod"}

	prevExists := imageExistsLocally
	defer func() { imageExistsLocally = prevExists }()
	imageExistsLocally = func(string) bool { return false }

	_, err := resolveManagedServiceImage(t.TempDir(), p, "bot", svc)
	if err == nil {
		t.Fatal("expected missing image/build error")
	}
	if !strings.Contains(err.Error(), "require image: or build:") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestEnsureImagePullsBeforeTryingLocalBuild(t *testing.T) {
	prevExists := imageExistsLocally
	prevFindRepoRoot := findClawdapusRepoRoot
	prevRunInfra := runInfraDockerCommand
	defer func() {
		imageExistsLocally = prevExists
		findClawdapusRepoRoot = prevFindRepoRoot
		runInfraDockerCommand = prevRunInfra
	}()

	imageExistsLocally = func(string) bool { return false }
	findClawdapusRepoRoot = func() (string, bool) {
		t.Fatal("unexpected repo-root lookup after successful pull")
		return "", false
	}

	var calls [][]string
	runInfraDockerCommand = func(args ...string) error {
		calls = append(calls, append([]string(nil), args...))
		return nil
	}

	if err := ensureImage("ghcr.io/mostlydev/cllama:latest", "cllama", "cllama/Dockerfile", "cllama"); err != nil {
		t.Fatalf("ensureImage: %v", err)
	}

	want := [][]string{{"pull", "ghcr.io/mostlydev/cllama:latest"}}
	if !slices.EqualFunc(calls, want, func(a, b []string) bool { return slices.Equal(a, b) }) {
		t.Fatalf("unexpected docker calls: got %v want %v", calls, want)
	}
}

func TestEnsureImageFallsBackToLocalBuildAfterPullFailure(t *testing.T) {
	prevExists := imageExistsLocally
	prevFindRepoRoot := findClawdapusRepoRoot
	prevRunInfra := runInfraDockerCommand
	defer func() {
		imageExistsLocally = prevExists
		findClawdapusRepoRoot = prevFindRepoRoot
		runInfraDockerCommand = prevRunInfra
	}()

	repoRoot := t.TempDir()
	dockerfilePath := filepath.Join(repoRoot, "cllama", "Dockerfile")
	if err := os.MkdirAll(filepath.Dir(dockerfilePath), 0o755); err != nil {
		t.Fatalf("mkdir dockerfile dir: %v", err)
	}
	if err := os.WriteFile(dockerfilePath, []byte("FROM alpine\n"), 0o644); err != nil {
		t.Fatalf("write dockerfile: %v", err)
	}

	imageExistsLocally = func(string) bool { return false }
	findClawdapusRepoRoot = func() (string, bool) { return repoRoot, true }

	var calls [][]string
	runInfraDockerCommand = func(args ...string) error {
		calls = append(calls, append([]string(nil), args...))
		if len(args) > 0 && args[0] == "pull" {
			return fmt.Errorf("pull failed")
		}
		return nil
	}

	if err := ensureImage("ghcr.io/mostlydev/cllama:latest", "cllama", "cllama/Dockerfile", "cllama"); err != nil {
		t.Fatalf("ensureImage: %v", err)
	}

	want := [][]string{
		{"pull", "ghcr.io/mostlydev/cllama:latest"},
		{"build", "-t", "ghcr.io/mostlydev/cllama:latest", "-f", dockerfilePath, filepath.Join(repoRoot, "cllama")},
	}
	if !slices.EqualFunc(calls, want, func(a, b []string) bool { return slices.Equal(a, b) }) {
		t.Fatalf("unexpected docker calls: got %v want %v", calls, want)
	}
}

func TestEnsureImageFallsBackToRemoteBuildWithoutRepoRoot(t *testing.T) {
	prevExists := imageExistsLocally
	prevFindRepoRoot := findClawdapusRepoRoot
	prevRunInfra := runInfraDockerCommand
	defer func() {
		imageExistsLocally = prevExists
		findClawdapusRepoRoot = prevFindRepoRoot
		runInfraDockerCommand = prevRunInfra
	}()

	imageExistsLocally = func(string) bool { return false }
	findClawdapusRepoRoot = func() (string, bool) { return "", false }

	var calls [][]string
	runInfraDockerCommand = func(args ...string) error {
		calls = append(calls, append([]string(nil), args...))
		if len(args) > 0 && args[0] == "pull" {
			return fmt.Errorf("pull failed")
		}
		return nil
	}

	if err := ensureImage("ghcr.io/mostlydev/clawdash:latest", "clawdash", "dockerfiles/clawdash/Dockerfile", "."); err != nil {
		t.Fatalf("ensureImage: %v", err)
	}

	want := [][]string{
		{"pull", "ghcr.io/mostlydev/clawdash:latest"},
		{"build", "-t", "ghcr.io/mostlydev/clawdash:latest", "https://github.com/mostlydev/clawdapus.git#master:."},
	}
	if !slices.EqualFunc(calls, want, func(a, b []string) bool { return slices.Equal(a, b) }) {
		t.Fatalf("unexpected docker calls: got %v want %v", calls, want)
	}
}

func TestResolveServiceSurfaceSkillsLeavesUndescribedServicesInlineOnly(t *testing.T) {
	prevExists := imageExistsLocally
	prevInspect := inspectClawImage
	prevLoadDescriptor := loadDescriptorFromImage
	defer func() {
		imageExistsLocally = prevExists
		inspectClawImage = prevInspect
		loadDescriptorFromImage = prevLoadDescriptor
	}()

	imageExistsLocally = func(string) bool { return true }
	inspectClawImage = func(string) (*inspect.ClawInfo, error) {
		return &inspect.ClawInfo{}, nil
	}
	loadDescriptorFromImage = func(string, string) (*describe.ServiceDescriptor, error) {
		return nil, os.ErrNotExist
	}

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

	p := &pod.Pod{
		Services: map[string]*pod.Service{
			"api-server": {Image: "example/api"},
			"db":         {Image: "example/db"},
		},
	}

	updatedSurfaces, skills, err := resolveServiceSurfaceSkills(t.TempDir(), tmpDir, p, surfaces, map[string]string{}, map[string]*inspect.ClawInfo{}, map[string]*describe.ServiceDescriptor{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(skills) != 0 {
		t.Fatalf("expected no generated fallback skills, got %d", len(skills))
	}
	if updatedSurfaces[0].SkillName != "" {
		t.Fatalf("expected api-server to rely on inline metadata only, got %q", updatedSurfaces[0].SkillName)
	}
	if updatedSurfaces[1].SkillName != "" {
		t.Fatalf("expected db to rely on inline metadata only, got %q", updatedSurfaces[1].SkillName)
	}
	if updatedSurfaces[0].ServiceInfo != nil || updatedSurfaces[1].ServiceInfo != nil {
		t.Fatalf("expected no service metadata without descriptors, got %+v %+v", updatedSurfaces[0].ServiceInfo, updatedSurfaces[1].ServiceInfo)
	}
}

func TestResolveServiceSurfaceSkillsPrefersTargetEmit(t *testing.T) {
	prevExists := imageExistsLocally
	prevInspect := inspectClawImage
	prevLoadDescriptor := loadDescriptorFromImage
	prevExtract := extractServiceSkillFromImage
	defer func() {
		imageExistsLocally = prevExists
		inspectClawImage = prevInspect
		loadDescriptorFromImage = prevLoadDescriptor
		extractServiceSkillFromImage = prevExtract
	}()

	imageExistsLocally = func(string) bool { return true }
	inspectClawImage = func(imageRef string) (*inspect.ClawInfo, error) {
		return &inspect.ClawInfo{SkillEmit: "/app/skills/trade.md"}, nil
	}
	loadDescriptorFromImage = func(string, string) (*describe.ServiceDescriptor, error) {
		return nil, os.ErrNotExist
	}
	extractServiceSkillFromImage = func(imageRef string, skillEmitPath string) ([]byte, error) {
		if imageRef != "example/trading-api:latest" {
			t.Fatalf("unexpected image ref: %q", imageRef)
		}
		if skillEmitPath != "/app/skills/trade.md" {
			t.Fatalf("unexpected emit path: %q", skillEmitPath)
		}
		return []byte("# trade\n"), nil
	}

	runtimeDir := t.TempDir()
	surfaces := []driver.ResolvedSurface{{Scheme: "service", Target: "trading-api", Ports: []string{"4000"}}}
	p := &pod.Pod{
		Services: map[string]*pod.Service{
			"trading-api": {Image: "example/trading-api:latest"},
		},
	}

	updatedSurfaces, skills, err := resolveServiceSurfaceSkills(t.TempDir(), runtimeDir, p, surfaces, map[string]string{}, map[string]*inspect.ClawInfo{}, map[string]*describe.ServiceDescriptor{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(skills) != 1 {
		t.Fatalf("expected one resolved skill, got %d", len(skills))
	}
	if updatedSurfaces[0].SkillName != "trade.md" {
		t.Fatalf("expected emitted service skill name, got %q", updatedSurfaces[0].SkillName)
	}
	if skills[0].Name != "trade.md" {
		t.Fatalf("expected extracted emitted skill to be mounted as trade.md, got %q", skills[0].Name)
	}
	data, err := os.ReadFile(skills[0].HostPath)
	if err != nil {
		t.Fatalf("read emitted skill: %v", err)
	}
	if string(data) != "# trade\n" {
		t.Fatalf("unexpected emitted skill content: %q", data)
	}
}

func TestResolveServiceSurfaceSkillsUsesDescriptorMetadataAndManual(t *testing.T) {
	prevExists := imageExistsLocally
	prevInspect := inspectClawImage
	prevLoadDescriptor := loadDescriptorFromImage
	prevExtract := extractServiceSkillFromImage
	defer func() {
		imageExistsLocally = prevExists
		inspectClawImage = prevInspect
		loadDescriptorFromImage = prevLoadDescriptor
		extractServiceSkillFromImage = prevExtract
	}()

	imageExistsLocally = func(string) bool { return true }
	inspectClawImage = func(imageRef string) (*inspect.ClawInfo, error) {
		return &inspect.ClawInfo{DescribePath: "/app/.claw-describe.json"}, nil
	}
	loadDescriptorFromImage = func(imageRef, descriptorPath string) (*describe.ServiceDescriptor, error) {
		if imageRef != "example/trading-api:latest" {
			t.Fatalf("unexpected image ref: %q", imageRef)
		}
		if descriptorPath != "/app/.claw-describe.json" {
			t.Fatalf("unexpected descriptor path: %q", descriptorPath)
		}
		return &describe.ServiceDescriptor{
			Version:     1,
			Description: "Trading API",
			Skill:       "/app/skills/trading-api.md",
			Auth:        &describe.AuthDescriptor{Type: "bearer", Env: "TRADING_API_TOKEN"},
			Endpoints: []describe.EndpointDescriptor{
				{Method: "GET", Path: "/positions", Description: "Open positions"},
			},
		}, nil
	}
	extractServiceSkillFromImage = func(imageRef, skillPath string) ([]byte, error) {
		if skillPath != "/app/skills/trading-api.md" {
			t.Fatalf("unexpected skill path: %q", skillPath)
		}
		return []byte("# manual\n"), nil
	}

	runtimeDir := t.TempDir()
	surfaces := []driver.ResolvedSurface{{Scheme: "service", Target: "trading-api", Ports: []string{"4000"}}}
	p := &pod.Pod{
		Services: map[string]*pod.Service{
			"trading-api": {Image: "example/trading-api:latest"},
		},
	}

	updatedSurfaces, skills, err := resolveServiceSurfaceSkills(t.TempDir(), runtimeDir, p, surfaces, map[string]string{}, map[string]*inspect.ClawInfo{}, map[string]*describe.ServiceDescriptor{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(skills) != 1 {
		t.Fatalf("expected one descriptor-backed skill, got %d", len(skills))
	}
	if updatedSurfaces[0].SkillName != "trading-api.md" {
		t.Fatalf("expected descriptor skill name, got %q", updatedSurfaces[0].SkillName)
	}
	if updatedSurfaces[0].ServiceInfo == nil || updatedSurfaces[0].ServiceInfo.Description != "Trading API" {
		t.Fatalf("expected descriptor metadata on surface, got %+v", updatedSurfaces[0].ServiceInfo)
	}
	if updatedSurfaces[0].ServiceInfo.AuthEnv != "TRADING_API_TOKEN" {
		t.Fatalf("expected descriptor auth env, got %+v", updatedSurfaces[0].ServiceInfo)
	}
}

func TestCollectServiceDescriptorsUsesPerServiceDockerfileLabelsForPlainBuildServices(t *testing.T) {
	podDir := t.TempDir()
	serviceDir := filepath.Join(podDir, "services", "trading-api")
	if err := os.MkdirAll(serviceDir, 0o755); err != nil {
		t.Fatalf("mkdir service dir: %v", err)
	}

	if err := os.WriteFile(filepath.Join(serviceDir, "Dockerfile"), []byte(`
FROM ruby:3.3
LABEL claw.describe=/trading-api.claw-describe.json
`), 0o644); err != nil {
		t.Fatalf("write Dockerfile: %v", err)
	}
	if err := os.WriteFile(filepath.Join(serviceDir, "Dockerfile.sidekiq"), []byte(`
FROM ruby:3.3
LABEL maintainer=ops@example.com
`), 0o644); err != nil {
		t.Fatalf("write Dockerfile.sidekiq: %v", err)
	}
	if err := os.WriteFile(filepath.Join(serviceDir, "trading-api.claw-describe.json"), []byte(`{
  "version": 1,
  "feeds": [
    {
      "name": "market-context",
      "path": "/api/v1/market_context/{claw_id}",
      "ttl": 180
    }
  ]
}`), 0o644); err != nil {
		t.Fatalf("write descriptor: %v", err)
	}

	p := &pod.Pod{
		Services: map[string]*pod.Service{
			"trading-api": {
				Compose: map[string]interface{}{
					"build": map[string]interface{}{
						"context":    "./services/trading-api",
						"dockerfile": "Dockerfile",
					},
				},
			},
			"sidekiq": {
				Compose: map[string]interface{}{
					"build": map[string]interface{}{
						"context":    "./services/trading-api",
						"dockerfile": "Dockerfile.sidekiq",
					},
				},
			},
		},
	}

	descriptors := map[string]*describe.ServiceDescriptor{}
	if err := collectServiceDescriptors(podDir, p, map[string]string{}, map[string]*inspect.ClawInfo{}, descriptors); err != nil {
		t.Fatalf("collect descriptors: %v", err)
	}
	if descriptors["trading-api"] == nil {
		t.Fatal("expected trading-api descriptor from Dockerfile label")
	}
	if descriptors["sidekiq"] != nil {
		t.Fatalf("expected sidekiq to remain undescribed, got %+v", descriptors["sidekiq"])
	}

	registry, err := describe.BuildFeedRegistry(descriptors)
	if err != nil {
		t.Fatalf("build feed registry: %v", err)
	}
	spec, ok := registry["market-context"]
	if !ok {
		t.Fatal("expected market-context feed in registry")
	}
	if spec.Source != "trading-api" {
		t.Fatalf("expected trading-api to own market-context, got %q", spec.Source)
	}
}

func TestResolveServiceMetadataUsesDefaultImageDescriptorPathWhenLabelMissing(t *testing.T) {
	prevExists := imageExistsLocally
	prevInspect := inspectClawImage
	prevLoadDescriptor := loadDescriptorFromImage
	defer func() {
		imageExistsLocally = prevExists
		inspectClawImage = prevInspect
		loadDescriptorFromImage = prevLoadDescriptor
	}()

	imageExistsLocally = func(string) bool { return true }
	inspectClawImage = func(imageRef string) (*inspect.ClawInfo, error) {
		return &inspect.ClawInfo{}, nil
	}
	loadDescriptorFromImage = func(imageRef, descriptorPath string) (*describe.ServiceDescriptor, error) {
		if imageRef != "example/reference-memory:latest" {
			t.Fatalf("unexpected image ref: %q", imageRef)
		}
		if descriptorPath != "/.claw-describe.json" {
			t.Fatalf("expected default image descriptor path, got %q", descriptorPath)
		}
		return &describe.ServiceDescriptor{
			Version:     2,
			Description: "Reference memory",
			Memory:      &describe.MemoryDescriptor{Retain: &describe.MemoryEndpoint{Path: "/retain"}},
		}, nil
	}

	p := &pod.Pod{
		Services: map[string]*pod.Service{
			"mem-svc": {Image: "example/reference-memory:latest"},
		},
	}

	_, _, descriptor, err := resolveServiceMetadata(t.TempDir(), p, "mem-svc", p.Services["mem-svc"], map[string]string{}, map[string]*inspect.ClawInfo{}, map[string]*describe.ServiceDescriptor{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if descriptor == nil || descriptor.Memory == nil || descriptor.Memory.Retain == nil {
		t.Fatalf("expected default image descriptor to load memory capability, got %+v", descriptor)
	}
}

func TestResolveServiceMetadataIgnoresMissingImplicitImageDescriptor(t *testing.T) {
	prevExists := imageExistsLocally
	prevInspect := inspectClawImage
	prevLoadDescriptor := loadDescriptorFromImage
	defer func() {
		imageExistsLocally = prevExists
		inspectClawImage = prevInspect
		loadDescriptorFromImage = prevLoadDescriptor
	}()

	imageExistsLocally = func(string) bool { return true }
	inspectClawImage = func(imageRef string) (*inspect.ClawInfo, error) {
		return &inspect.ClawInfo{}, nil
	}
	loadDescriptorFromImage = func(string, string) (*describe.ServiceDescriptor, error) {
		return nil, os.ErrNotExist
	}

	p := &pod.Pod{
		Services: map[string]*pod.Service{
			"sidecar": {Image: "example/sidecar:latest"},
		},
	}

	_, _, descriptor, err := resolveServiceMetadata(t.TempDir(), p, "sidecar", p.Services["sidecar"], map[string]string{}, map[string]*inspect.ClawInfo{}, map[string]*describe.ServiceDescriptor{})
	if err != nil {
		t.Fatalf("expected missing implicit image descriptor to be ignored, got %v", err)
	}
	if descriptor != nil {
		t.Fatalf("expected nil descriptor when implicit image descriptor is absent, got %+v", descriptor)
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
		"XAI_API_KEY":       "sk-xai",
		"ANTHROPIC_API_KEY": "sk-ant",
		"GEMINI_API_KEY":    "sk-gemini",
		"GOOGLE_API_KEY":    "sk-google",
		"DISCORD_BOT_TOKEN": "keep",
		"LOG_LEVEL":         "info",
	}
	stripLLMKeys(env)
	if _, ok := env["OPENAI_API_KEY"]; ok {
		t.Error("should strip OPENAI_API_KEY")
	}
	if _, ok := env["XAI_API_KEY"]; ok {
		t.Error("should strip XAI_API_KEY")
	}
	if _, ok := env["ANTHROPIC_API_KEY"]; ok {
		t.Error("should strip ANTHROPIC_API_KEY")
	}
	if _, ok := env["GEMINI_API_KEY"]; ok {
		t.Error("should strip GEMINI_API_KEY")
	}
	if _, ok := env["GOOGLE_API_KEY"]; ok {
		t.Error("should strip GOOGLE_API_KEY")
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
		{"OPENAI_API_KEY_1", true},
		{"OPENAI_API_KEY_2", true},
		{"XAI_API_KEY", true},
		{"XAI_API_KEY_1", true},
		{"ANTHROPIC_API_KEY", true},
		{"ANTHROPIC_API_KEY_1", true},
		{"OPENROUTER_API_KEY", true},
		{"OPENROUTER_API_KEY_1", true},
		{"GEMINI_API_KEY", true},
		{"GEMINI_API_KEY_1", true},
		{"GOOGLE_API_KEY", true},
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

// -- mergeProviderSeeds -------------------------------------------------------

func TestMergeProviderSeedsWritesV2File(t *testing.T) {
	dir := t.TempDir()
	p := &pod.Pod{
		Services: map[string]*pod.Service{
			"analyst": {
				Claw: &pod.ClawBlock{
					CllamaEnv: map[string]string{
						"OPENAI_API_KEY": "sk-primary",
					},
				},
			},
		},
	}
	if err := mergeProviderSeeds(dir, p); err != nil {
		t.Fatalf("mergeProviderSeeds: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "providers.json"))
	if err != nil {
		t.Fatalf("read providers.json: %v", err)
	}

	var probe struct {
		Version   int `json:"version"`
		Providers map[string]struct {
			Keys []struct {
				ID     string `json:"id"`
				Secret string `json:"secret"`
				State  string `json:"state"`
				Source string `json:"source"`
			} `json:"keys"`
		} `json:"providers"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		t.Fatalf("parse output: %v", err)
	}
	if probe.Version != 2 {
		t.Errorf("expected version=2, got %d", probe.Version)
	}
	op, ok := probe.Providers["openai"]
	if !ok {
		t.Fatal("openai missing from output")
	}
	if len(op.Keys) != 1 {
		t.Fatalf("expected 1 openai key, got %d", len(op.Keys))
	}
	k := op.Keys[0]
	if k.ID != "seed:OPENAI_API_KEY" {
		t.Errorf("key ID = %q, want seed:OPENAI_API_KEY", k.ID)
	}
	if k.Secret != "sk-primary" {
		t.Errorf("key secret = %q, want sk-primary", k.Secret)
	}
	if k.State != "ready" {
		t.Errorf("key state = %q, want ready", k.State)
	}
}

func TestMergeProviderSeedsWritesXAIProvider(t *testing.T) {
	dir := t.TempDir()
	p := &pod.Pod{
		Services: map[string]*pod.Service{
			"trader": {
				Claw: &pod.ClawBlock{
					CllamaEnv: map[string]string{
						"XAI_API_KEY": "xai-primary",
					},
				},
			},
		},
	}
	if err := mergeProviderSeeds(dir, p); err != nil {
		t.Fatalf("mergeProviderSeeds: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "providers.json"))
	if err != nil {
		t.Fatalf("read providers.json: %v", err)
	}

	var probe struct {
		Providers map[string]struct {
			BaseURL string `json:"base_url"`
			Keys    []struct {
				ID     string `json:"id"`
				Secret string `json:"secret"`
			} `json:"keys"`
		} `json:"providers"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		t.Fatalf("parse providers.json: %v", err)
	}

	xai, ok := probe.Providers["xai"]
	if !ok {
		t.Fatal("xai missing from output")
	}
	if xai.BaseURL != "https://api.x.ai/v1" {
		t.Fatalf("expected xai base URL, got %q", xai.BaseURL)
	}
	if len(xai.Keys) != 1 {
		t.Fatalf("expected 1 xai key, got %d", len(xai.Keys))
	}
	if xai.Keys[0].ID != "seed:XAI_API_KEY" {
		t.Fatalf("expected xai key id seed:XAI_API_KEY, got %q", xai.Keys[0].ID)
	}
	if xai.Keys[0].Secret != "xai-primary" {
		t.Fatalf("expected xai secret preserved, got %q", xai.Keys[0].Secret)
	}
}

func TestMergeProviderSeedsWritesGoogleProvider(t *testing.T) {
	dir := t.TempDir()
	p := &pod.Pod{
		Services: map[string]*pod.Service{
			"analyst": {
				Claw: &pod.ClawBlock{
					CllamaEnv: map[string]string{
						"GEMINI_API_KEY":  "gemini-primary",
						"GOOGLE_API_KEY":  "google-alias",
						"GOOGLE_BASE_URL": "https://proxy.example.test/google",
					},
				},
			},
		},
	}
	if err := mergeProviderSeeds(dir, p); err != nil {
		t.Fatalf("mergeProviderSeeds: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "providers.json"))
	if err != nil {
		t.Fatalf("read providers.json: %v", err)
	}

	var probe struct {
		Providers map[string]struct {
			BaseURL     string `json:"base_url"`
			ActiveKeyID string `json:"active_key_id"`
			Keys        []struct {
				ID     string `json:"id"`
				Secret string `json:"secret"`
			} `json:"keys"`
		} `json:"providers"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		t.Fatalf("parse providers.json: %v", err)
	}

	google, ok := probe.Providers["google"]
	if !ok {
		t.Fatal("google missing from output")
	}
	if google.BaseURL != "https://proxy.example.test/google" {
		t.Fatalf("expected google base URL override, got %q", google.BaseURL)
	}
	if google.ActiveKeyID != "seed:GEMINI_API_KEY" {
		t.Fatalf("expected google active key to prefer GEMINI_API_KEY, got %q", google.ActiveKeyID)
	}
	if len(google.Keys) != 2 {
		t.Fatalf("expected 2 google keys, got %d", len(google.Keys))
	}
	if google.Keys[0].ID != "seed:GEMINI_API_KEY" || google.Keys[0].Secret != "gemini-primary" {
		t.Fatalf("unexpected primary google key: %+v", google.Keys[0])
	}
	if google.Keys[1].ID != "seed:GOOGLE_API_KEY" || google.Keys[1].Secret != "google-alias" {
		t.Fatalf("unexpected alias google key: %+v", google.Keys[1])
	}
}

func TestMergeProviderSeedsUsesGoogleAliasWhenGeminiMissing(t *testing.T) {
	dir := t.TempDir()
	p := &pod.Pod{
		Services: map[string]*pod.Service{
			"analyst": {
				Claw: &pod.ClawBlock{
					CllamaEnv: map[string]string{
						"GOOGLE_API_KEY": "google-alias",
					},
				},
			},
		},
	}
	if err := mergeProviderSeeds(dir, p); err != nil {
		t.Fatalf("mergeProviderSeeds: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "providers.json"))
	if err != nil {
		t.Fatalf("read providers.json: %v", err)
	}

	var probe struct {
		Providers map[string]struct {
			BaseURL     string `json:"base_url"`
			ActiveKeyID string `json:"active_key_id"`
			Keys        []struct {
				ID     string `json:"id"`
				Secret string `json:"secret"`
			} `json:"keys"`
		} `json:"providers"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		t.Fatalf("parse providers.json: %v", err)
	}

	google, ok := probe.Providers["google"]
	if !ok {
		t.Fatal("google missing from output")
	}
	if google.BaseURL != "https://generativelanguage.googleapis.com/v1beta/openai" {
		t.Fatalf("expected default google base URL, got %q", google.BaseURL)
	}
	if google.ActiveKeyID != "seed:GOOGLE_API_KEY" {
		t.Fatalf("expected GOOGLE_API_KEY alias to become active key, got %q", google.ActiveKeyID)
	}
	if len(google.Keys) != 1 || google.Keys[0].ID != "seed:GOOGLE_API_KEY" || google.Keys[0].Secret != "google-alias" {
		t.Fatalf("unexpected google alias seed output: %+v", google.Keys)
	}
}

func TestMergeProviderSeedsPreservesExistingRuntimeKeys(t *testing.T) {
	dir := t.TempDir()

	// Seed an existing v2 file with a runtime key.
	existing := `{
		"version": 2,
		"providers": {
			"openai": {
				"base_url": "https://api.openai.com/v1",
				"auth": "bearer",
				"api_format": "openai",
				"active_key_id": "seed:OPENAI_API_KEY",
				"keys": [
					{"id": "seed:OPENAI_API_KEY", "label": "primary", "secret": "sk-primary",
					 "source": "seed", "state": "ready", "cooldown_until": "",
					 "last_error_code": 0, "last_error_reason": "", "last_error_at": "", "added_at": "2026-03-23T00:00:00Z"},
					{"id": "runtime:extra", "label": "extra", "secret": "sk-runtime",
					 "source": "runtime", "state": "ready", "cooldown_until": "",
					 "last_error_code": 0, "last_error_reason": "", "last_error_at": "", "added_at": "2026-03-23T00:00:00Z"}
				]
			}
		}
	}`
	if err := os.WriteFile(filepath.Join(dir, "providers.json"), []byte(existing), 0o600); err != nil {
		t.Fatal(err)
	}

	p := &pod.Pod{
		Services: map[string]*pod.Service{
			"analyst": {
				Claw: &pod.ClawBlock{
					CllamaEnv: map[string]string{
						"OPENAI_API_KEY": "sk-primary",
					},
				},
			},
		},
	}
	if err := mergeProviderSeeds(dir, p); err != nil {
		t.Fatalf("mergeProviderSeeds: %v", err)
	}

	data, _ := os.ReadFile(filepath.Join(dir, "providers.json"))
	var probe struct {
		Providers map[string]struct {
			Keys []struct {
				ID     string `json:"id"`
				Source string `json:"source"`
			} `json:"keys"`
		} `json:"providers"`
	}
	_ = json.Unmarshal(data, &probe)
	var foundRuntime bool
	for _, k := range probe.Providers["openai"].Keys {
		if k.ID == "runtime:extra" && k.Source == "runtime" {
			foundRuntime = true
		}
	}
	if !foundRuntime {
		t.Error("runtime key was dropped by mergeProviderSeeds")
	}
}

func TestMergeProviderSeedsResetsStateWhenSecretChanges(t *testing.T) {
	dir := t.TempDir()

	// Start with a dead key.
	existing := `{
		"version": 2,
		"providers": {
			"openai": {
				"base_url": "https://api.openai.com/v1",
				"auth": "bearer",
				"api_format": "openai",
				"active_key_id": "seed:OPENAI_API_KEY",
				"keys": [
					{"id": "seed:OPENAI_API_KEY", "label": "primary", "secret": "sk-old",
					 "source": "seed", "state": "dead", "cooldown_until": "",
					 "last_error_code": 401, "last_error_reason": "http_401", "last_error_at": "2026-03-23T00:00:00Z",
					 "added_at": "2026-03-23T00:00:00Z"}
				]
			}
		}
	}`
	if err := os.WriteFile(filepath.Join(dir, "providers.json"), []byte(existing), 0o600); err != nil {
		t.Fatal(err)
	}

	p := &pod.Pod{
		Services: map[string]*pod.Service{
			"analyst": {
				Claw: &pod.ClawBlock{
					CllamaEnv: map[string]string{
						"OPENAI_API_KEY": "sk-new",
					},
				},
			},
		},
	}
	if err := mergeProviderSeeds(dir, p); err != nil {
		t.Fatalf("mergeProviderSeeds: %v", err)
	}

	data, _ := os.ReadFile(filepath.Join(dir, "providers.json"))
	var probe struct {
		Providers map[string]struct {
			Keys []struct {
				Secret string `json:"secret"`
				State  string `json:"state"`
			} `json:"keys"`
		} `json:"providers"`
	}
	_ = json.Unmarshal(data, &probe)
	keys := probe.Providers["openai"].Keys
	if len(keys) == 0 {
		t.Fatal("no keys after merge")
	}
	if keys[0].Secret != "sk-new" {
		t.Errorf("expected secret=sk-new, got %q", keys[0].Secret)
	}
	if keys[0].State != "ready" {
		t.Errorf("expected state=ready after secret change, got %q", keys[0].State)
	}
}

func TestMergeProviderSeedsPreservesStateWhenSecretUnchanged(t *testing.T) {
	dir := t.TempDir()

	// Start with a cooldown key with same secret.
	existing := `{
		"version": 2,
		"providers": {
			"openai": {
				"base_url": "https://api.openai.com/v1",
				"auth": "bearer",
				"api_format": "openai",
				"active_key_id": "seed:OPENAI_API_KEY",
				"keys": [
					{"id": "seed:OPENAI_API_KEY", "label": "primary", "secret": "sk-same",
					 "source": "seed", "state": "cooldown", "cooldown_until": "2026-12-31T23:59:59Z",
					 "last_error_code": 429, "last_error_reason": "rate_limit", "last_error_at": "2026-03-23T00:00:00Z",
					 "added_at": "2026-03-23T00:00:00Z"}
				]
			}
		}
	}`
	if err := os.WriteFile(filepath.Join(dir, "providers.json"), []byte(existing), 0o600); err != nil {
		t.Fatal(err)
	}

	p := &pod.Pod{
		Services: map[string]*pod.Service{
			"analyst": {
				Claw: &pod.ClawBlock{
					CllamaEnv: map[string]string{
						"OPENAI_API_KEY": "sk-same",
					},
				},
			},
		},
	}
	if err := mergeProviderSeeds(dir, p); err != nil {
		t.Fatalf("mergeProviderSeeds: %v", err)
	}

	data, _ := os.ReadFile(filepath.Join(dir, "providers.json"))
	var probe struct {
		Providers map[string]struct {
			Keys []struct {
				State string `json:"state"`
			} `json:"keys"`
		} `json:"providers"`
	}
	_ = json.Unmarshal(data, &probe)
	keys := probe.Providers["openai"].Keys
	if len(keys) == 0 {
		t.Fatal("no keys after merge")
	}
	if keys[0].State != "cooldown" {
		t.Errorf("expected state=cooldown preserved, got %q", keys[0].State)
	}
}

// -- loadOrGenerateUIToken ----------------------------------------------------

func TestLoadOrGenerateUITokenCreatesNewToken(t *testing.T) {
	dir := t.TempDir()
	token, err := loadOrGenerateUIToken(dir)
	if err != nil {
		t.Fatalf("loadOrGenerateUIToken: %v", err)
	}
	if len(token) == 0 {
		t.Error("expected non-empty token")
	}
	// Token should be persisted.
	data, err := os.ReadFile(filepath.Join(dir, "ui-token"))
	if err != nil {
		t.Fatalf("ui-token file not created: %v", err)
	}
	if strings.TrimSpace(string(data)) != token {
		t.Errorf("persisted token %q != returned token %q", strings.TrimSpace(string(data)), token)
	}
}

func TestLoadOrGenerateUITokenRoundTrip(t *testing.T) {
	dir := t.TempDir()
	first, err := loadOrGenerateUIToken(dir)
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	second, err := loadOrGenerateUIToken(dir)
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if first != second {
		t.Errorf("token changed between calls: %q != %q", first, second)
	}
}

func TestBuildFeedManifestSubstitutesClawID(t *testing.T) {
	p := &pod.Pod{
		Name: "test-pod",
		Services: map[string]*pod.Service{
			"trading-api": {
				Expose: []string{"4000"},
			},
			"weston": {
				Claw: &pod.ClawBlock{
					Feeds: []pod.FeedEntry{
						{Name: "market-context", Source: "trading-api", Path: "/api/v1/market_context/{claw_id}", TTL: 180},
					},
				},
			},
			"logan": {
				Claw: &pod.ClawBlock{
					Feeds: []pod.FeedEntry{
						{Name: "{claw_id}-context", Source: "trading-api", Path: "/api/v1/market_context/{claw_id}", TTL: 180},
					},
				},
			},
		},
	}

	westonFeeds, err := buildFeedManifestEntries(p, nil, nil, "weston", "weston", nil)
	if err != nil {
		t.Fatalf("weston feeds: %v", err)
	}
	if len(westonFeeds) != 1 {
		t.Fatalf("expected 1 feed, got %d", len(westonFeeds))
	}
	if westonFeeds[0].Path != "/api/v1/market_context/weston" {
		t.Fatalf("expected weston path substitution, got %q", westonFeeds[0].Path)
	}
	if westonFeeds[0].URL != "http://trading-api:4000/api/v1/market_context/weston" {
		t.Fatalf("expected weston URL substitution, got %q", westonFeeds[0].URL)
	}

	loganFeeds, err := buildFeedManifestEntries(p, nil, nil, "logan", "logan", nil)
	if err != nil {
		t.Fatalf("logan feeds: %v", err)
	}
	if loganFeeds[0].Name != "logan-context" {
		t.Fatalf("expected logan name substitution, got %q", loganFeeds[0].Name)
	}
	if loganFeeds[0].Path != "/api/v1/market_context/logan" {
		t.Fatalf("expected logan path substitution, got %q", loganFeeds[0].Path)
	}
}

func TestBuildFeedManifestUsesOrdinalClawID(t *testing.T) {
	p := &pod.Pod{
		Name: "test-pod",
		Services: map[string]*pod.Service{
			"claw-wall": {
				Image:  resolveConversationWallImageRef(),
				Expose: []string{conversationWallInternalPort},
			},
			"trader": {
				Claw: &pod.ClawBlock{
					Feeds: []pod.FeedEntry{
						{
							Name:   conversationWallFeedName,
							Source: conversationWallServiceName,
							Path:   "/channel-context?consumer={claw_id}&channels=chan-a,chan-b&limit=20",
							TTL:    conversationWallFeedTTL,
						},
					},
				},
			},
		},
	}

	feeds, err := buildFeedManifestEntries(p, nil, nil, "trader", "trader-1", nil)
	if err != nil {
		t.Fatalf("buildFeedManifestEntries: %v", err)
	}
	if len(feeds) != 1 {
		t.Fatalf("expected one feed, got %d", len(feeds))
	}
	if feeds[0].Path != "/channel-context?consumer=trader-1&channels=chan-a,chan-b&limit=20" {
		t.Fatalf("expected ordinal claw_id substitution, got %q", feeds[0].Path)
	}
	if feeds[0].URL != "http://claw-wall:8080/channel-context?consumer=trader-1&channels=chan-a,chan-b&limit=20" {
		t.Fatalf("expected ordinal wall URL, got %q", feeds[0].URL)
	}
}

func TestBuildFeedManifestProjectsBearerAuthFromServiceEnv(t *testing.T) {
	p := &pod.Pod{
		Services: map[string]*pod.Service{
			"weston": {
				Environment: map[string]string{
					"TRADING_API_TOKEN": "${TRADING_API_TOKEN}",
				},
				Claw: &pod.ClawBlock{
					Feeds: []pod.FeedEntry{
						{Name: "market-context", Source: "trading-api", Path: "/api/v1/market_context/{claw_id}", TTL: 180},
					},
				},
			},
			"trading-api": {
				Expose: []string{"4000"},
			},
		},
	}

	feeds, err := buildFeedManifestEntries(
		p,
		map[string]*describe.ServiceDescriptor{
			"trading-api": {
				Version: 1,
				Auth: &describe.AuthDescriptor{
					Type: "bearer",
					Env:  "TRADING_API_TOKEN",
				},
			},
		},
		map[string]string{
			"TRADING_API_TOKEN": "real-token",
		},
		"weston",
		"weston",
		nil,
	)
	if err != nil {
		t.Fatalf("buildFeedManifestEntries: %v", err)
	}
	if len(feeds) != 1 {
		t.Fatalf("expected one feed, got %d", len(feeds))
	}
	if feeds[0].Auth != "real-token" {
		t.Fatalf("expected projected bearer auth, got %q", feeds[0].Auth)
	}
}

func TestResolveToolSubscriptionsSupportsAllAndSubset(t *testing.T) {
	p := &pod.Pod{
		Services: map[string]*pod.Service{
			"analyst": {
				Claw: &pod.ClawBlock{
					Tools: []pod.ToolPolicyEntry{
						{Service: "trading-api", Allow: []string{"all"}},
						{Service: "analytics", Allow: []string{"get_summary"}},
					},
				},
			},
		},
	}

	registry := describe.ToolRegistry{
		"trading-api": {
			{Name: "get_market_context", Service: "trading-api"},
			{Name: "get_positions", Service: "trading-api"},
		},
		"analytics": {
			{Name: "get_summary", Service: "analytics"},
			{Name: "get_report", Service: "analytics"},
		},
	}

	resolved, err := resolveToolSubscriptions(p, registry)
	if err != nil {
		t.Fatalf("resolveToolSubscriptions: %v", err)
	}
	got := resolved["analyst"]
	if len(got) != 3 {
		t.Fatalf("expected 3 resolved tools, got %+v", got)
	}
	if got[0].Service != "trading-api" || got[1].Service != "trading-api" || got[2].Name != "get_summary" {
		t.Fatalf("unexpected resolved tool order: %+v", got)
	}
}

func TestResolveToolSubscriptionsRejectsUnknownTool(t *testing.T) {
	p := &pod.Pod{
		Services: map[string]*pod.Service{
			"analyst": {
				Claw: &pod.ClawBlock{
					Tools: []pod.ToolPolicyEntry{
						{Service: "trading-api", Allow: []string{"missing_tool"}},
					},
				},
			},
		},
	}

	registry := describe.ToolRegistry{
		"trading-api": {
			{Name: "get_market_context", Service: "trading-api"},
		},
	}

	if _, err := resolveToolSubscriptions(p, registry); err == nil {
		t.Fatal("expected unknown tool error")
	}
}

func TestResolveMemorySubscriptionsRejectsMissingCapability(t *testing.T) {
	p := &pod.Pod{
		Services: map[string]*pod.Service{
			"analyst": {
				Claw: &pod.ClawBlock{
					Memory: &pod.MemoryEntry{Service: "team-memory", TimeoutMS: 300},
				},
			},
		},
	}

	if _, err := resolveMemorySubscriptions(p, map[string]*describe.ServiceDescriptor{
		"team-memory": {Version: 2},
	}); err == nil {
		t.Fatal("expected missing memory capability error")
	}
}

func TestBuildToolManifestEntriesNamescopesAndProjectsAuth(t *testing.T) {
	p := &pod.Pod{
		Services: map[string]*pod.Service{
			"analyst": {
				Environment: map[string]string{
					"TRADING_API_TOKEN": "${TRADING_API_TOKEN}",
				},
				Claw: &pod.ClawBlock{},
			},
			"trading-api": {
				Expose: []string{"4000"},
			},
		},
	}

	tools, err := buildToolManifestEntries(
		p,
		map[string]*describe.ServiceDescriptor{
			"trading-api": {
				Version: 2,
				Auth: &describe.AuthDescriptor{
					Type: "bearer",
					Env:  "TRADING_API_TOKEN",
				},
			},
		},
		map[string]string{
			"TRADING_API_TOKEN": "real-token",
		},
		"analyst",
		[]describe.ToolSpec{{
			Name:        "propose_trade",
			Service:     "trading-api",
			Description: "Submit trade proposal",
			InputSchema: map[string]interface{}{"type": "object"},
			HTTP: &describe.ToolHTTP{
				Method:  "POST",
				Path:    "/api/v1/trades",
				Body:    "json",
				BodyKey: "trade",
			},
		}},
		nil,
	)
	if err != nil {
		t.Fatalf("buildToolManifestEntries: %v", err)
	}
	if len(tools) != 1 {
		t.Fatalf("expected 1 tool entry, got %+v", tools)
	}
	if tools[0].Name != "trading-api.propose_trade" {
		t.Fatalf("expected namespaced tool name, got %+v", tools[0])
	}
	if tools[0].Execution.BaseURL != "http://trading-api:4000" {
		t.Fatalf("unexpected tool base URL: %+v", tools[0].Execution)
	}
	if tools[0].Execution.Auth == nil || tools[0].Execution.Auth.Token != "real-token" {
		t.Fatalf("expected projected auth token, got %+v", tools[0].Execution.Auth)
	}
	if tools[0].Execution.BodyKey != "trade" {
		t.Fatalf("expected propagated body key, got %+v", tools[0].Execution)
	}
}

func TestBuildToolManifestEntriesProjectsMCPTools(t *testing.T) {
	p := &pod.Pod{
		Services: map[string]*pod.Service{
			"analyst": {
				Environment: map[string]string{
					"PERPLEXITY_MCP_TOKEN": "${PERPLEXITY_MCP_TOKEN}",
				},
				Claw: &pod.ClawBlock{},
			},
			"perplexity-mcp": {
				Expose: []string{"8080"},
			},
		},
	}

	tools, err := buildToolManifestEntries(
		p,
		map[string]*describe.ServiceDescriptor{
			"perplexity-mcp": {
				Version: 2,
				MCP:     &describe.MCPDescriptor{Transport: "streamable_http", Path: "/mcp"},
				Auth: &describe.AuthDescriptor{
					Type: "bearer",
					Env:  "PERPLEXITY_MCP_TOKEN",
				},
			},
		},
		map[string]string{
			"PERPLEXITY_MCP_TOKEN": "mcp-token",
		},
		"analyst",
		[]describe.ToolSpec{{
			Name:        "search",
			Service:     "perplexity-mcp",
			Description: "Search the web",
			InputSchema: map[string]interface{}{"type": "object"},
			MCP:         &describe.MCPDescriptor{Transport: "streamable_http", Path: "/mcp"},
		}},
		nil,
	)
	if err != nil {
		t.Fatalf("buildToolManifestEntries: %v", err)
	}
	if len(tools) != 1 {
		t.Fatalf("expected 1 tool entry, got %+v", tools)
	}
	if tools[0].Name != "perplexity-mcp.search" {
		t.Fatalf("expected namespaced tool name, got %+v", tools[0])
	}
	execution := tools[0].Execution
	if execution.Transport != "mcp" || execution.Service != "perplexity-mcp" || execution.BaseURL != "http://perplexity-mcp:8080" {
		t.Fatalf("unexpected MCP execution identity: %+v", execution)
	}
	if execution.Path != "/mcp" || execution.ToolName != "search" || execution.Method != "" {
		t.Fatalf("unexpected MCP execution shape: %+v", execution)
	}
	if execution.Auth == nil || execution.Auth.Token != "mcp-token" {
		t.Fatalf("expected projected MCP auth token, got %+v", execution.Auth)
	}
}

func TestBuildMemoryManifestEntryUsesProjectedServiceAuth(t *testing.T) {
	p := &pod.Pod{
		Services: map[string]*pod.Service{
			"analyst": {
				Claw: &pod.ClawBlock{},
			},
			"team-memory": {
				Expose: []string{"8081"},
			},
		},
	}

	entry, err := buildMemoryManifestEntry(
		p,
		map[string]*describe.ServiceDescriptor{
			"team-memory": {
				Version: 2,
				Memory: &describe.MemoryDescriptor{
					Recall: &describe.MemoryEndpoint{Path: "/recall"},
					Retain: &describe.MemoryEndpoint{Path: "/retain"},
				},
			},
		},
		nil,
		"analyst",
		&resolvedMemorySubscription{
			Service: "team-memory",
			Config:  &pod.MemoryEntry{Service: "team-memory", TimeoutMS: 450},
		},
		[]cllama.ServiceAuthEntry{{
			Service:  "team-memory",
			AuthType: "bearer",
			Token:    "memory-token",
		}},
	)
	if err != nil {
		t.Fatalf("buildMemoryManifestEntry: %v", err)
	}
	if entry == nil || entry.Service != "team-memory" {
		t.Fatalf("unexpected memory entry: %+v", entry)
	}
	if entry.BaseURL != "http://team-memory:8081" {
		t.Fatalf("unexpected memory base URL: %+v", entry)
	}
	if entry.Recall == nil || entry.Recall.TimeoutMS != 450 {
		t.Fatalf("unexpected recall config: %+v", entry)
	}
	if entry.Auth == nil || entry.Auth.Token != "memory-token" {
		t.Fatalf("expected projected memory auth, got %+v", entry.Auth)
	}
}

func TestAttachCapabilityProvidersToInternalNetworkAddsProviderServices(t *testing.T) {
	p := &pod.Pod{
		Services: map[string]*pod.Service{
			"analyst": {
				Claw: &pod.ClawBlock{
					Feeds: []pod.FeedEntry{{Name: "market", Source: "market-feed", Path: "/feed", TTL: 30}},
				},
			},
			"market-feed": {Compose: map[string]interface{}{}},
			"tool-api":    {Compose: map[string]interface{}{}},
			"team-memory": {Compose: map[string]interface{}{}},
		},
	}

	err := attachCapabilityProvidersToInternalNetwork(
		p,
		map[string][]describe.ToolSpec{
			"analyst": {{Name: "get_market", Service: "tool-api"}},
		},
		map[string]*resolvedMemorySubscription{
			"analyst": {Service: "team-memory", Config: &pod.MemoryEntry{Service: "team-memory", TimeoutMS: 300}},
		},
	)
	if err != nil {
		t.Fatalf("attachCapabilityProvidersToInternalNetwork: %v", err)
	}

	for _, name := range []string{"market-feed", "tool-api", "team-memory"} {
		networks, ok := p.Services[name].Compose["networks"].([]string)
		if !ok || len(networks) != 1 || networks[0] != clawInternalNetworkName {
			t.Fatalf("expected %s on %s, got %#v", name, clawInternalNetworkName, p.Services[name].Compose["networks"])
		}
	}
}

func TestPrepareHistoryReplayRuntimeProjectsReplayAuthAndEnv(t *testing.T) {
	p := &pod.Pod{
		Services: map[string]*pod.Service{
			"analyst": {
				Claw: &pod.ClawBlock{
					Memory: &pod.MemoryEntry{Service: "team-memory", TimeoutMS: 300},
				},
			},
			"researcher": {
				Claw: &pod.ClawBlock{
					Count:  2,
					Memory: &pod.MemoryEntry{Service: "team-memory", TimeoutMS: 300},
				},
			},
			"team-memory": {
				Environment: map[string]string{},
				Compose:     map[string]interface{}{},
			},
		},
	}

	auth, err := prepareHistoryReplayRuntime(
		p,
		map[string]*driver.ResolvedClaw{
			"analyst":    {Count: 1},
			"researcher": {Count: 2},
		},
		map[string]*resolvedMemorySubscription{
			"analyst":    {Service: "team-memory", Config: &pod.MemoryEntry{Service: "team-memory", TimeoutMS: 300}},
			"researcher": {Service: "team-memory", Config: &pod.MemoryEntry{Service: "team-memory", TimeoutMS: 300}},
		},
	)
	if err != nil {
		t.Fatalf("prepareHistoryReplayRuntime: %v", err)
	}

	env := p.Services["team-memory"].Environment
	if env["CLAW_HISTORY_URL"] != historyReplayBaseURL {
		t.Fatalf("unexpected CLAW_HISTORY_URL: %q", env["CLAW_HISTORY_URL"])
	}
	if env["CLAW_HISTORY_TOKEN"] == "" {
		t.Fatal("expected CLAW_HISTORY_TOKEN to be injected")
	}
	if env["CLAW_HISTORY_AGENT_IDS"] != "analyst,researcher-0,researcher-1" {
		t.Fatalf("unexpected CLAW_HISTORY_AGENT_IDS: %q", env["CLAW_HISTORY_AGENT_IDS"])
	}

	networks, ok := p.Services["team-memory"].Compose["networks"].([]string)
	if !ok || len(networks) != 1 || networks[0] != clawInternalNetworkName {
		t.Fatalf("expected team-memory on %s, got %#v", clawInternalNetworkName, p.Services["team-memory"].Compose["networks"])
	}

	for _, agentID := range []string{"analyst", "researcher-0", "researcher-1"} {
		entry, ok := auth[agentID]
		if !ok {
			t.Fatalf("expected replay auth for %s", agentID)
		}
		if entry.Service != historyReplayAuthService || entry.AuthType != "bearer" || entry.Principal != "team-memory" {
			t.Fatalf("unexpected auth entry for %s: %+v", agentID, entry)
		}
		if entry.Token != env["CLAW_HISTORY_TOKEN"] {
			t.Fatalf("expected %s token to match projected env token", agentID)
		}
	}
}

func TestBuildServiceSurfaceInfoOmitsEndpointsWhenToolsDeclared(t *testing.T) {
	info := buildServiceSurfaceInfo(&describe.ServiceDescriptor{
		Version:     2,
		Description: "Trading API",
		Tools: []describe.ToolDescriptor{{
			Name:        "get_market_context",
			Description: "Retrieve market context",
			InputSchema: map[string]interface{}{"type": "object"},
		}},
		Endpoints: []describe.EndpointDescriptor{{
			Method: "GET",
			Path:   "/api/v1/market_context",
		}},
	})
	if info == nil {
		t.Fatal("expected surface info")
	}
	if len(info.Endpoints) != 0 {
		t.Fatalf("expected endpoints to be suppressed when tools are declared, got %+v", info.Endpoints)
	}
}

func testConversationWallService(token string, channelIDs ...string) *pod.Service {
	environment := make(map[string]string)
	if token != "" {
		environment["DISCORD_BOT_TOKEN"] = token
	}

	channels := make([]driver.ChannelInfo, 0, len(channelIDs))
	for _, channelID := range channelIDs {
		channels = append(channels, driver.ChannelInfo{ID: channelID})
	}

	return &pod.Service{
		Environment: environment,
		Claw: &pod.ClawBlock{
			Handles: map[string]*driver.HandleInfo{
				"discord": {
					Guilds: []driver.GuildInfo{{
						ID:       "guild-1",
						Channels: channels,
					}},
				},
			},
		},
	}
}

func TestInjectConversationWallAddsServiceAndFeed(t *testing.T) {
	t.Setenv("CLAW_WALL_POLL_INTERVAL", "")

	p := &pod.Pod{
		Name: "desk",
		Services: map[string]*pod.Service{
			"observer": testConversationWallService("${OBSERVER_DISCORD_BOT_TOKEN}", "chan-2", "chan-9"),
			"trader":   testConversationWallService("${TRADER_DISCORD_BOT_TOKEN}", "chan-2", "chan-1"),
		},
	}

	resolvedClaws := map[string]*driver.ResolvedClaw{
		"observer": {ServiceName: "observer"},
		"trader":   {ServiceName: "trader", Cllama: []string{"passthrough"}},
	}

	if err := injectConversationWall(p, resolvedClaws); err != nil {
		t.Fatalf("injectConversationWall: %v", err)
	}

	wall := p.Services[conversationWallServiceName]
	if wall == nil {
		t.Fatal("expected claw-wall service to be injected")
	}
	if wall.Image != resolveConversationWallImageRef() {
		t.Fatalf("expected claw-wall image %q, got %q", resolveConversationWallImageRef(), wall.Image)
	}
	if !slices.Equal(wall.Expose, []string{conversationWallInternalPort}) {
		t.Fatalf("expected claw-wall expose %v, got %v", []string{conversationWallInternalPort}, wall.Expose)
	}
	if wall.Environment["CLAW_WALL_TOKENS"] != "chan-1:${TRADER_DISCORD_BOT_TOKEN},chan-2:${OBSERVER_DISCORD_BOT_TOKEN}" {
		t.Fatalf("unexpected CLAW_WALL_TOKENS: %q", wall.Environment["CLAW_WALL_TOKENS"])
	}
	if strings.Contains(wall.Environment["CLAW_WALL_TOKENS"], "chan-9") {
		t.Fatalf("expected unconsumed channel to be excluded, got %q", wall.Environment["CLAW_WALL_TOKENS"])
	}
	if wall.Environment["CLAW_WALL_POLL_INTERVAL"] != conversationWallPollInterval {
		t.Fatalf("unexpected CLAW_WALL_POLL_INTERVAL: %q", wall.Environment["CLAW_WALL_POLL_INTERVAL"])
	}

	traderFeeds := p.Services["trader"].Claw.Feeds
	if len(traderFeeds) != 1 {
		t.Fatalf("expected one injected wall feed, got %+v", traderFeeds)
	}
	if traderFeeds[0].Source != conversationWallServiceName {
		t.Fatalf("expected claw-wall feed source, got %+v", traderFeeds[0])
	}
	if traderFeeds[0].Path != "/channel-context?consumer={claw_id}&channels=chan-1,chan-2&limit=20" {
		t.Fatalf("unexpected wall feed path: %q", traderFeeds[0].Path)
	}
	if len(p.Services["observer"].Claw.Feeds) != 0 {
		t.Fatalf("expected no wall feed for non-cllama service, got %+v", p.Services["observer"].Claw.Feeds)
	}
}

func TestInjectConversationWallPrefersMasterTokenWhenMasterDeclaresChannel(t *testing.T) {
	p := &pod.Pod{
		Name:   "desk",
		Master: "zmaster",
		Services: map[string]*pod.Service{
			"observer": testConversationWallService("${OBSERVER_DISCORD_BOT_TOKEN}", "chan-2"),
			"trader":   testConversationWallService("${TRADER_DISCORD_BOT_TOKEN}", "chan-2"),
			"zmaster":  testConversationWallService("${MASTER_DISCORD_BOT_TOKEN}", "chan-2"),
		},
	}

	resolvedClaws := map[string]*driver.ResolvedClaw{
		"observer": {ServiceName: "observer"},
		"trader":   {ServiceName: "trader", Cllama: []string{"passthrough"}},
		"zmaster":  {ServiceName: "zmaster"},
	}

	if err := injectConversationWall(p, resolvedClaws); err != nil {
		t.Fatalf("injectConversationWall: %v", err)
	}

	wall := p.Services[conversationWallServiceName]
	if wall == nil {
		t.Fatal("expected claw-wall service to be injected")
	}
	if wall.Environment["CLAW_WALL_TOKENS"] != "chan-2:${MASTER_DISCORD_BOT_TOKEN}" {
		t.Fatalf("unexpected CLAW_WALL_TOKENS: %q", wall.Environment["CLAW_WALL_TOKENS"])
	}
}

func TestInjectConversationWallFallsBackWhenMasterDoesNotDeclareChannel(t *testing.T) {
	p := &pod.Pod{
		Name:   "desk",
		Master: "zmaster",
		Services: map[string]*pod.Service{
			"observer": testConversationWallService("${OBSERVER_DISCORD_BOT_TOKEN}", "chan-2"),
			"trader":   testConversationWallService("", "chan-2"),
			"zmaster":  testConversationWallService("${MASTER_DISCORD_BOT_TOKEN}", "chan-9"),
		},
	}

	resolvedClaws := map[string]*driver.ResolvedClaw{
		"observer": {ServiceName: "observer"},
		"trader":   {ServiceName: "trader", Cllama: []string{"passthrough"}},
		"zmaster":  {ServiceName: "zmaster"},
	}

	if err := injectConversationWall(p, resolvedClaws); err != nil {
		t.Fatalf("injectConversationWall: %v", err)
	}

	wall := p.Services[conversationWallServiceName]
	if wall == nil {
		t.Fatal("expected claw-wall service to be injected")
	}
	if wall.Environment["CLAW_WALL_TOKENS"] != "chan-2:${OBSERVER_DISCORD_BOT_TOKEN}" {
		t.Fatalf("unexpected CLAW_WALL_TOKENS: %q", wall.Environment["CLAW_WALL_TOKENS"])
	}
}

func TestInjectConversationWallRejectsConsumedChannelWithoutEligibleReader(t *testing.T) {
	p := &pod.Pod{
		Name: "desk",
		Services: map[string]*pod.Service{
			"observer": testConversationWallService("${OBSERVER_DISCORD_BOT_TOKEN}", "chan-9"),
			"trader":   testConversationWallService("", "chan-2"),
		},
	}

	err := injectConversationWall(p, map[string]*driver.ResolvedClaw{
		"observer": {ServiceName: "observer"},
		"trader":   {ServiceName: "trader", Cllama: []string{"passthrough"}},
	})
	if err == nil {
		t.Fatal("expected missing-reader error")
	}
	if !strings.Contains(err.Error(), "chan-2") {
		t.Fatalf("expected channel ID in error, got %v", err)
	}
}

func TestInjectConversationWallRejectsReservedServiceName(t *testing.T) {
	p := &pod.Pod{
		Services: map[string]*pod.Service{
			conversationWallServiceName: {Image: "busybox"},
			"trader":                    testConversationWallService("token", "chan-1"),
		},
	}

	err := injectConversationWall(p, map[string]*driver.ResolvedClaw{
		"trader": {ServiceName: "trader", Cllama: []string{"passthrough"}},
	})
	if err == nil {
		t.Fatal("expected reserved-name error")
	}
	if !strings.Contains(err.Error(), conversationWallServiceName) {
		t.Fatalf("expected reserved service name in error, got %v", err)
	}
}

// -- mergeProviderSeeds source-field tests ------------------------------------

func TestMergeProviderSeedsPreservesRuntimeProviderSource(t *testing.T) {
	dir := t.TempDir()

	// providers.json has a runtime-added provider that is NOT in any cllama-env.
	existing := `{
		"version": 2,
		"providers": {
			"mistral": {
				"base_url": "https://api.mistral.ai/v1",
				"auth": "bearer",
				"api_format": "openai",
				"source": "runtime",
				"active_key_id": "runtime:mistral:abc",
				"keys": [
					{"id": "runtime:mistral:abc", "label": "primary", "secret": "sk-m",
					 "source": "runtime", "state": "ready", "cooldown_until": "",
					 "last_error_code": 0, "last_error_reason": "", "last_error_at": "",
					 "added_at": "2026-03-24T00:00:00Z"}
				]
			}
		}
	}`
	if err := os.WriteFile(filepath.Join(dir, "providers.json"), []byte(existing), 0o600); err != nil {
		t.Fatal(err)
	}

	// Pod with no cllama-env — mistral will not be seeded.
	p := &pod.Pod{
		Services: map[string]*pod.Service{
			"analyst": {
				Claw: &pod.ClawBlock{
					CllamaEnv: map[string]string{},
				},
			},
		},
	}
	if err := mergeProviderSeeds(dir, p); err != nil {
		t.Fatalf("mergeProviderSeeds: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "providers.json"))
	if err != nil {
		t.Fatalf("read providers.json: %v", err)
	}

	var out struct {
		Providers map[string]struct {
			Source string `json:"source"`
		} `json:"providers"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("parse output: %v", err)
	}

	m, ok := out.Providers["mistral"]
	if !ok {
		t.Fatal("mistral provider missing from output after claw up")
	}
	if m.Source != "runtime" {
		t.Errorf("mistral source = %q, want \"runtime\"", m.Source)
	}
}

func TestMergeProviderSeedsWarnOnSeedOverwritesRuntime(t *testing.T) {
	dir := t.TempDir()

	// providers.json has a runtime-source provider named "openai".
	existing := `{
		"version": 2,
		"providers": {
			"openai": {
				"base_url": "https://api.openai.com/v1",
				"auth": "bearer",
				"api_format": "openai",
				"source": "runtime",
				"active_key_id": "runtime:openai:xyz",
				"keys": [
					{"id": "runtime:openai:xyz", "label": "primary", "secret": "sk-runtime",
					 "source": "runtime", "state": "ready", "cooldown_until": "",
					 "last_error_code": 0, "last_error_reason": "", "last_error_at": "",
					 "added_at": "2026-03-24T00:00:00Z"}
				]
			}
		}
	}`
	if err := os.WriteFile(filepath.Join(dir, "providers.json"), []byte(existing), 0o600); err != nil {
		t.Fatal(err)
	}

	// Pod seeds "openai" via cllama-env — this should trigger the warning and win.
	p := &pod.Pod{
		Services: map[string]*pod.Service{
			"analyst": {
				Claw: &pod.ClawBlock{
					CllamaEnv: map[string]string{
						"OPENAI_API_KEY": "sk-seed",
					},
				},
			},
		},
	}
	// mergeProviderSeeds writes the warning to os.Stderr; we just verify it
	// completes without error and that the seed key wins.
	if err := mergeProviderSeeds(dir, p); err != nil {
		t.Fatalf("mergeProviderSeeds: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "providers.json"))
	if err != nil {
		t.Fatalf("read providers.json: %v", err)
	}

	var out struct {
		Providers map[string]struct {
			Keys []struct {
				ID     string `json:"id"`
				Secret string `json:"secret"`
				Source string `json:"source"`
			} `json:"keys"`
		} `json:"providers"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("parse output: %v", err)
	}

	op, ok := out.Providers["openai"]
	if !ok {
		t.Fatal("openai provider missing from output")
	}

	// Seed key must be present.
	var seedKey *struct {
		ID     string `json:"id"`
		Secret string `json:"secret"`
		Source string `json:"source"`
	}
	for i := range op.Keys {
		if op.Keys[i].Source == "seed" {
			seedKey = &op.Keys[i]
			break
		}
	}
	if seedKey == nil {
		t.Fatal("expected a seed key in openai provider after merge, found none")
	}
	if seedKey.Secret != "sk-seed" {
		t.Errorf("seed key secret = %q, want \"sk-seed\"", seedKey.Secret)
	}
}

func TestEnsurePersistentCllamaDir(t *testing.T) {
	dir := t.TempDir()
	got, err := ensurePersistentCllamaDir(dir, ".claw-auth")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := filepath.Join(dir, ".claw-auth")
	if got != want {
		t.Errorf("got %q; want %q", got, want)
	}
	fi, err := os.Stat(got)
	if err != nil {
		t.Fatalf("dir not created: %v", err)
	}
	if !fi.IsDir() {
		t.Error("expected directory")
	}
	// Check permissions (mask against 0o777 to ignore umask/OS bits)
	if fi.Mode().Perm()&0o777 == 0 {
		t.Error("expected writable permissions")
	}
}

func TestEnsurePersistentCllamaDirIsOutsideRuntimeDir(t *testing.T) {
	podDir := t.TempDir()
	runtimeDir := filepath.Join(podDir, ".claw-runtime")

	authDir, err := ensurePersistentCllamaDir(podDir, ".claw-auth")
	if err != nil {
		t.Fatal(err)
	}
	sessionDir, err := ensurePersistentCllamaDir(podDir, ".claw-session-history")
	if err != nil {
		t.Fatal(err)
	}
	memoryDir, err := ensurePersistentCllamaDir(podDir, ".claw-memory")
	if err != nil {
		t.Fatal(err)
	}

	// Both dirs must be siblings of runtimeDir, not under it
	if strings.HasPrefix(authDir, runtimeDir) {
		t.Errorf("authDir %q is under runtimeDir %q", authDir, runtimeDir)
	}
	if strings.HasPrefix(sessionDir, runtimeDir) {
		t.Errorf("sessionDir %q is under runtimeDir %q", sessionDir, runtimeDir)
	}
	if strings.HasPrefix(memoryDir, runtimeDir) {
		t.Errorf("memoryDir %q is under runtimeDir %q", memoryDir, runtimeDir)
	}
	// Both must be direct children of podDir
	if filepath.Dir(authDir) != podDir {
		t.Errorf("authDir parent = %q; want %q", filepath.Dir(authDir), podDir)
	}
	if filepath.Dir(sessionDir) != podDir {
		t.Errorf("sessionDir parent = %q; want %q", filepath.Dir(sessionDir), podDir)
	}
	if filepath.Dir(memoryDir) != podDir {
		t.Errorf("memoryDir parent = %q; want %q", filepath.Dir(memoryDir), podDir)
	}
}

func TestPreMigratePortableMemoryCopiesServiceRuntimeState(t *testing.T) {
	podDir := t.TempDir()
	runtimeDir := filepath.Join(podDir, ".claw-runtime")
	memoryRoot := filepath.Join(podDir, ".claw-memory")
	legacyDir := filepath.Join(runtimeDir, "tiverton", "hermes-home", "memories")
	if err := os.MkdirAll(legacyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(memoryRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacyDir, "MEMORY.md"), []byte("legacy desk note"), 0o644); err != nil {
		t.Fatal(err)
	}

	p := &pod.Pod{
		Services: map[string]*pod.Service{
			"tiverton": {},
		},
	}
	if err := preMigratePortableMemory(runtimeDir, memoryRoot, p); err != nil {
		t.Fatalf("preMigratePortableMemory returned error: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(memoryRoot, "tiverton", "memory", "MEMORY.md"))
	if err != nil {
		t.Fatalf("read migrated memory: %v", err)
	}
	if string(data) != "legacy desk note" {
		t.Fatalf("unexpected migrated memory: %q", string(data))
	}
}
