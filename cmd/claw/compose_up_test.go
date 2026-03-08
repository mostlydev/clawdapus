package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

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
		&pod.ClawdashConfig{},
	)

	want := []string{"assistant", "clawdash", "cllama", "worker-0", "worker-1"}
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
	clawfilePath := filepath.Join(tmpDir, "Clawfile")
	if err := os.WriteFile(clawfilePath, []byte("FROM alpine\nCLAW_TYPE openclaw\nAGENT AGENTS.md\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	svc := &pod.Service{
		Compose: map[string]interface{}{
			"build": map[string]interface{}{
				"context": ".",
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
	buildGeneratedImage = func(path, tag string) error {
		if path != generatedPath {
			t.Fatalf("expected generated path %q, got %q", generatedPath, path)
		}
		builtTag = tag
		return nil
	}
	dockerBuildTaggedImage = func(string, string, string, map[string]string, string) error {
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
	buildGeneratedImage = func(string, string) error {
		t.Fatal("unexpected generated-image build for plain Dockerfile build")
		return nil
	}

	var gotImageRef, gotDockerfile, gotContext, gotTarget string
	var gotArgs map[string]string
	dockerBuildTaggedImage = func(imageRef, dockerfile, contextDir string, args map[string]string, target string) error {
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
	if gotArgs["FOO"] != "bar" {
		t.Fatalf("expected build args to be passed through, got %v", gotArgs)
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

func TestResolveServiceSurfaceSkillsFallsBackWhenNoEmitExists(t *testing.T) {
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

	updatedSurfaces, skills, err := resolveServiceSurfaceSkills(t.TempDir(), tmpDir, p, surfaces, map[string]string{}, map[string]*inspect.ClawInfo{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(skills) != 2 {
		t.Fatalf("expected 2 generated skills, got %d", len(skills))
	}
	if updatedSurfaces[0].SkillName != "surface-api-server.md" {
		t.Fatalf("expected api-server fallback skill name, got %q", updatedSurfaces[0].SkillName)
	}
	if updatedSurfaces[1].SkillName != "surface-db.md" {
		t.Fatalf("expected db fallback skill name, got %q", updatedSurfaces[1].SkillName)
	}

	if skills[0].Name != "surface-api-server.md" && skills[1].Name != "surface-api-server.md" {
		t.Fatalf("expected generated skill for api-server, got %v", []string{skills[0].Name, skills[1].Name})
	}
	if skills[0].Name != "surface-db.md" && skills[1].Name != "surface-db.md" {
		t.Fatalf("expected generated skill for db, got %v", []string{skills[0].Name, skills[1].Name})
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

	updatedSurfaces, skills, err := resolveServiceSurfaceSkills(t.TempDir(), runtimeDir, p, surfaces, map[string]string{}, map[string]*inspect.ClawInfo{})
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
