package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mostlydev/clawdapus/internal/clawapi"
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

func TestResetRuntimeDirClearsStaleContents(t *testing.T) {
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

	if err := resetRuntimeDir(runtimeDir); err != nil {
		t.Fatalf("reset runtime dir: %v", err)
	}

	info, err := os.Stat(runtimeDir)
	if err != nil {
		t.Fatalf("stat runtime dir: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("expected runtime dir to exist as directory")
	}
	if _, err := os.Stat(staleDir); !os.IsNotExist(err) {
		t.Fatalf("expected stale dir to be removed, got err=%v", err)
	}
	if _, err := os.Stat(staleFile); !os.IsNotExist(err) {
		t.Fatalf("expected stale file to be removed, got err=%v", err)
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

func TestResolveManagedServiceImageBuildOnlyClawfile(t *testing.T) {
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
	p := &pod.Pod{Name: "Research Pod"}

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

	imageRef, err := resolveManagedServiceImage(tmpDir, p, "bot", svc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
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

func TestResolveManagedServiceImageBuildsPlainDockerfile(t *testing.T) {
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
	p := &pod.Pod{Name: "test-pod"}

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

	imageRef, err := resolveManagedServiceImage(tmpDir, p, "bot", svc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
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
	defer func() {
		imageExistsLocally = prevExists
		inspectClawImage = prevInspect
	}()

	imageExistsLocally = func(string) bool { return true }
	inspectClawImage = func(string) (*inspect.ClawInfo, error) {
		return &inspect.ClawInfo{}, nil
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
	prevExtract := extractServiceSkillFromImage
	defer func() {
		imageExistsLocally = prevExists
		inspectClawImage = prevInspect
		extractServiceSkillFromImage = prevExtract
	}()

	imageExistsLocally = func(string) bool { return true }
	inspectClawImage = func(imageRef string) (*inspect.ClawInfo, error) {
		return &inspect.ClawInfo{SkillEmit: "/app/skills/trade.md"}, nil
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
		{"OPENAI_API_KEY_1", true},
		{"OPENAI_API_KEY_2", true},
		{"ANTHROPIC_API_KEY", true},
		{"ANTHROPIC_API_KEY_1", true},
		{"OPENROUTER_API_KEY", true},
		{"OPENROUTER_API_KEY_1", true},
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
				Image:  conversationWallImageRef,
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

func TestInjectConversationWallAddsServiceAndFeed(t *testing.T) {
	p := &pod.Pod{
		Name: "desk",
		Services: map[string]*pod.Service{
			"observer": {
				Environment: map[string]string{
					"DISCORD_BOT_TOKEN": "${OBSERVER_DISCORD_BOT_TOKEN}",
				},
				Claw: &pod.ClawBlock{
					Handles: map[string]*driver.HandleInfo{
						"discord": {
							Guilds: []driver.GuildInfo{{
								ID: "guild-1",
								Channels: []driver.ChannelInfo{
									{ID: "chan-2"},
								},
							}},
						},
					},
				},
			},
			"trader": {
				Environment: map[string]string{
					"DISCORD_BOT_TOKEN": "${TRADER_DISCORD_BOT_TOKEN}",
				},
				Claw: &pod.ClawBlock{
					Handles: map[string]*driver.HandleInfo{
						"discord": {
							Guilds: []driver.GuildInfo{{
								ID: "guild-1",
								Channels: []driver.ChannelInfo{
									{ID: "chan-2"},
									{ID: "chan-1"},
								},
							}},
						},
					},
				},
			},
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
	if wall.Image != conversationWallImageRef {
		t.Fatalf("expected claw-wall image %q, got %q", conversationWallImageRef, wall.Image)
	}
	if !slices.Equal(wall.Expose, []string{conversationWallInternalPort}) {
		t.Fatalf("expected claw-wall expose %v, got %v", []string{conversationWallInternalPort}, wall.Expose)
	}
	if wall.Environment["CLAW_WALL_TOKENS"] != "chan-1:${TRADER_DISCORD_BOT_TOKEN},chan-2:${OBSERVER_DISCORD_BOT_TOKEN},chan-2:${TRADER_DISCORD_BOT_TOKEN}" {
		t.Fatalf("unexpected CLAW_WALL_TOKENS: %q", wall.Environment["CLAW_WALL_TOKENS"])
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

func TestInjectConversationWallRejectsReservedServiceName(t *testing.T) {
	p := &pod.Pod{
		Services: map[string]*pod.Service{
			conversationWallServiceName: {Image: "busybox"},
			"trader": {
				Environment: map[string]string{"DISCORD_BOT_TOKEN": "token"},
				Claw: &pod.ClawBlock{
					Handles: map[string]*driver.HandleInfo{
						"discord": {
							Guilds: []driver.GuildInfo{{Channels: []driver.ChannelInfo{{ID: "chan-1"}}}},
						},
					},
				},
			},
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

	// Both dirs must be siblings of runtimeDir, not under it
	if strings.HasPrefix(authDir, runtimeDir) {
		t.Errorf("authDir %q is under runtimeDir %q", authDir, runtimeDir)
	}
	if strings.HasPrefix(sessionDir, runtimeDir) {
		t.Errorf("sessionDir %q is under runtimeDir %q", sessionDir, runtimeDir)
	}
	// Both must be direct children of podDir
	if filepath.Dir(authDir) != podDir {
		t.Errorf("authDir parent = %q; want %q", filepath.Dir(authDir), podDir)
	}
	if filepath.Dir(sessionDir) != podDir {
		t.Errorf("sessionDir parent = %q; want %q", filepath.Dir(sessionDir), podDir)
	}
}
