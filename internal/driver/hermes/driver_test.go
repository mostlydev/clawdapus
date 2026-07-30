package hermes

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mostlydev/clawdapus/internal/driver"
	"github.com/mostlydev/clawdapus/internal/driver/shared"
)

func TestDriverRegistered(t *testing.T) {
	d, err := driver.Lookup("hermes")
	if err != nil {
		t.Fatalf("hermes driver not registered: %v", err)
	}
	if d == nil {
		t.Fatal("hermes driver is nil")
	}
}

func TestValidateRequiresAgentPath(t *testing.T) {
	d := &Driver{}
	rc := &driver.ResolvedClaw{
		ServiceName: "hermes",
		Models:      map[string]string{"primary": "openrouter/anthropic/claude-sonnet-4"},
		Handles:     map[string]*driver.HandleInfo{"discord": {}},
		Environment: map[string]string{
			"DISCORD_BOT_TOKEN":  "discord-token",
			"OPENROUTER_API_KEY": "or-key",
		},
	}

	err := d.Validate(rc)
	if err == nil {
		t.Fatal("expected error for missing agent host path")
	}
	if !strings.Contains(err.Error(), "no agent host path") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateRejectsUnsupportedHandle(t *testing.T) {
	rc, _ := newTestRC(t)
	rc.Handles = map[string]*driver.HandleInfo{
		"discord": {},
		"signal":  {},
	}

	err := (&Driver{}).Validate(rc)
	if err == nil {
		t.Fatal("expected unsupported HANDLE validation error")
	}
	if !strings.Contains(err.Error(), "not supported") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateRequiresSupportedHandle(t *testing.T) {
	rc, _ := newTestRC(t)
	rc.Handles = map[string]*driver.HandleInfo{}

	err := (&Driver{}).Validate(rc)
	if err == nil {
		t.Fatal("expected supported HANDLE validation error")
	}
	if !strings.Contains(err.Error(), "no supported HANDLE platforms") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateAcceptsComposeEnvTokenReference(t *testing.T) {
	t.Setenv("ALLEN_BOT_TOKEN", "")

	rc, _ := newTestRC(t)
	rc.Environment["DISCORD_BOT_TOKEN"] = "${ALLEN_BOT_TOKEN}"

	if err := (&Driver{}).Validate(rc); err != nil {
		t.Fatalf("Validate should accept Compose env token references: %v", err)
	}
}

func TestValidateRejectsBlankHandleToken(t *testing.T) {
	rc, _ := newTestRC(t)
	rc.Environment["DISCORD_BOT_TOKEN"] = "  "

	err := (&Driver{}).Validate(rc)
	if err == nil {
		t.Fatal("expected blank DISCORD_BOT_TOKEN validation error")
	}
	if !strings.Contains(err.Error(), "DISCORD_BOT_TOKEN") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateSlackRequiresAppToken(t *testing.T) {
	rc, _ := newTestRC(t)
	rc.Handles = map[string]*driver.HandleInfo{"slack": {}}
	delete(rc.Environment, "SLACK_APP_TOKEN")

	err := (&Driver{}).Validate(rc)
	if err == nil {
		t.Fatal("expected missing SLACK_APP_TOKEN validation error")
	}
	if !strings.Contains(err.Error(), "SLACK_BOT_TOKEN and SLACK_APP_TOKEN") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateRejectsInvalidCron(t *testing.T) {
	rc, _ := newTestRC(t)
	rc.Invocations = []driver.Invocation{{Schedule: "@hourly", Message: "Ping"}}

	err := (&Driver{}).Validate(rc)
	if err == nil {
		t.Fatal("expected invalid cron validation error")
	}
	if !strings.Contains(err.Error(), "invalid cron expression") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestMaterializeRejectsEmptyCllamaToken(t *testing.T) {
	rc, tmp := newTestRC(t)
	rc.Cllama = []string{"passthrough"}
	rc.CllamaToken = ""

	// Validate should pass — cllama token is not yet available at validation time
	// (the two-pass compose-up generates it between Validate and Materialize).
	if err := (&Driver{}).Validate(rc); err != nil {
		t.Fatalf("Validate should pass with empty cllama token: %v", err)
	}

	runtimeDir := filepath.Join(tmp, "runtime")
	if err := os.MkdirAll(runtimeDir, 0o700); err != nil {
		t.Fatal(err)
	}
	_, err := (&Driver{}).Materialize(rc, driver.MaterializeOpts{RuntimeDir: runtimeDir, PodName: "test"})
	if err == nil {
		t.Fatal("expected Materialize to reject empty CLLAMA token")
	}
	if !strings.Contains(err.Error(), "CLLAMA is enabled but token is empty") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestMaterializeWritesRuntimeLayout(t *testing.T) {
	rc, tmp := newTestRC(t)
	rc.PersonaHostPath = ""
	rc.Invocations = []driver.Invocation{
		{Schedule: "*/5 * * * *", Message: "Ping status", Name: "status"},
	}

	runtimeDir := filepath.Join(tmp, "runtime")
	if err := os.MkdirAll(runtimeDir, 0o700); err != nil {
		t.Fatal(err)
	}

	result, err := (&Driver{}).Materialize(rc, driver.MaterializeOpts{RuntimeDir: runtimeDir, PodName: "fleet-echo"})
	if err != nil {
		t.Fatalf("Materialize returned error: %v", err)
	}

	if _, err := os.Stat(filepath.Join(runtimeDir, "hermes-home", "config.yaml")); err != nil {
		t.Fatalf("expected config.yaml: %v", err)
	}
	if _, err := os.Stat(filepath.Join(runtimeDir, "hermes-home", ".env")); err != nil {
		t.Fatalf("expected .env: %v", err)
	}
	if _, err := os.Stat(filepath.Join(runtimeDir, "hermes-home", "cron", "jobs.json")); err != nil {
		t.Fatalf("expected jobs.json: %v", err)
	}
	agentsData, err := os.ReadFile(filepath.Join(runtimeDir, "workspace", "AGENTS.md"))
	if err != nil {
		t.Fatalf("read AGENTS.md: %v", err)
	}
	if !strings.Contains(string(agentsData), "You are Hermes.") {
		t.Fatalf("expected original contract content in AGENTS.md")
	}
	if !strings.Contains(string(agentsData), "fleet-echo") {
		t.Fatalf("expected pod context in AGENTS.md")
	}

	if result.ReadOnly != true {
		t.Fatal("expected Hermes to run with read-only rootfs")
	}
	if result.SkillDir != hermesHomeDir+"/skills" {
		t.Fatalf("unexpected SkillDir: %q", result.SkillDir)
	}
	if result.SkillLayout != "directory" {
		t.Fatalf("unexpected SkillLayout: %q", result.SkillLayout)
	}
	if result.User != "0:0" {
		t.Fatalf("unexpected User: %q", result.User)
	}
	if result.Environment["HERMES_HOME"] != hermesHomeDir {
		t.Fatalf("unexpected HERMES_HOME: %q", result.Environment["HERMES_HOME"])
	}
	if got := result.Environment[hermesGatewayLockDirEnv]; got != hermesDefaultGatewayLockDir {
		t.Fatalf("expected %s=%s, got %q", hermesGatewayLockDirEnv, hermesDefaultGatewayLockDir, got)
	}
	if got := result.Environment["XDG_STATE_HOME"]; got != hermesDefaultXDGStateHome {
		t.Fatalf("expected XDG_STATE_HOME=%s, got %q", hermesDefaultXDGStateHome, got)
	}
	if got := result.Environment["NO_PROXY"]; got != hermesDefaultNoProxy {
		t.Fatalf("expected NO_PROXY=%s, got %q", hermesDefaultNoProxy, got)
	}
	if got := result.Environment["no_proxy"]; got != hermesDefaultNoProxy {
		t.Fatalf("expected no_proxy=%s, got %q", hermesDefaultNoProxy, got)
	}
	if got := result.Environment[hermesDefaultAgentIdentityEnv]; got != managedDefaultAgentIdentity {
		t.Fatalf("unexpected %s: %q", hermesDefaultAgentIdentityEnv, got)
	}
	if got := result.Environment["HERMES_TOOL_ONLY_MODE"]; got != "1" {
		t.Fatalf("expected HERMES_TOOL_ONLY_MODE=1, got %q", got)
	}
	if got := result.Environment[hermesAllowSilentFinalEnv]; got != "1" {
		t.Fatalf("expected %s=1, got %q", hermesAllowSilentFinalEnv, got)
	}
	if got := result.Environment[hermesToolProgressModeEnv]; got != "off" {
		t.Fatalf("expected %s=off, got %q", hermesToolProgressModeEnv, got)
	}
	if got := result.Environment[hermesChatStatusDeliveryEnv]; got != "off" {
		t.Fatalf("expected %s=off, got %q", hermesChatStatusDeliveryEnv, got)
	}
	if got := result.Environment[clawdapusDisabledToolsEnv]; got != hermesTextToSpeechTool {
		t.Fatalf("expected %s=%s, got %q", clawdapusDisabledToolsEnv, hermesTextToSpeechTool, got)
	}
	if result.Environment["MESSAGING_CWD"] != hermesWorkspaceDir {
		t.Fatalf("unexpected MESSAGING_CWD: %q", result.Environment["MESSAGING_CWD"])
	}
	if result.Environment["TERMINAL_CWD"] != hermesWorkspaceDir {
		t.Fatalf("unexpected TERMINAL_CWD: %q", result.Environment["TERMINAL_CWD"])
	}
	if result.Environment[shared.PortableMemoryEnv] != shared.PortableMemoryDir {
		t.Fatalf("unexpected %s: %q", shared.PortableMemoryEnv, result.Environment[shared.PortableMemoryEnv])
	}

	hasPortableMemoryMount := false
	hasHermesMemoryMount := false
	for _, mount := range result.Mounts {
		switch mount.ContainerPath {
		case shared.PortableMemoryDir:
			hasPortableMemoryMount = true
			if mount.ReadOnly {
				t.Fatal("portable memory mount should be writable")
			}
		case hermesHomeDir + "/memories":
			hasHermesMemoryMount = true
			if mount.ReadOnly {
				t.Fatal("Hermes memories mount should be writable")
			}
		}
	}
	if !hasPortableMemoryMount {
		t.Fatal("expected portable memory mount")
	}
	if !hasHermesMemoryMount {
		t.Fatal("expected Hermes memories mount")
	}

	// Default SOUL.md should be written when no persona is configured
	soulData, err := os.ReadFile(filepath.Join(runtimeDir, "hermes-home", "SOUL.md"))
	if err != nil {
		t.Fatalf("expected default SOUL.md: %v", err)
	}
	soulStr := string(soulData)
	if !strings.Contains(soulStr, "# Hermes") {
		t.Fatalf("expected titlecase agent name in SOUL.md header, got: %s", soulStr[:100])
	}
	if !strings.Contains(soulStr, "fleet-echo") {
		t.Fatalf("expected pod name in SOUL.md")
	}
	if strings.Contains(soulStr, "Nous Research") {
		t.Fatal("default SOUL.md should not contain Hermes runner identity")
	}
}

func TestMaterializeGatewayLocksStayWritableWithReadOnlyRootfs(t *testing.T) {
	rc, tmp := newTestRC(t)
	runtimeDir := filepath.Join(tmp, "runtime")
	if err := os.MkdirAll(runtimeDir, 0o700); err != nil {
		t.Fatal(err)
	}

	result, err := (&Driver{}).Materialize(rc, driver.MaterializeOpts{RuntimeDir: runtimeDir, PodName: "test"})
	if err != nil {
		t.Fatalf("Materialize returned error: %v", err)
	}

	if result.ReadOnly != true {
		t.Fatal("test requires Hermes to run with read-only rootfs")
	}
	for key, want := range map[string]string{
		hermesGatewayLockDirEnv: hermesDefaultGatewayLockDir,
		"NO_PROXY":              hermesDefaultNoProxy,
		"XDG_STATE_HOME":        hermesDefaultXDGStateHome,
		"no_proxy":              hermesDefaultNoProxy,
	} {
		if got := result.Environment[key]; got != want {
			t.Fatalf("expected container env %s=%s, got %q", key, want, got)
		}
	}

	envData, err := os.ReadFile(filepath.Join(runtimeDir, "hermes-home", ".env"))
	if err != nil {
		t.Fatalf("read .env: %v", err)
	}
	env := string(envData)
	for _, expected := range []string{
		hermesGatewayLockDirEnv + "=" + hermesDefaultGatewayLockDir + "\n",
		"NO_PROXY=" + hermesDefaultNoProxy + "\n",
		"XDG_STATE_HOME=" + hermesDefaultXDGStateHome + "\n",
		"no_proxy=" + hermesDefaultNoProxy + "\n",
	} {
		if !strings.Contains(env, expected) {
			t.Fatalf("expected %q in .env, got:\n%s", expected, env)
		}
	}
	if strings.Contains(env, "/root/.local") {
		t.Fatalf("Hermes gateway state must not default under read-only /root/.local, got:\n%s", env)
	}
}

func TestMaterializeAllowsGatewayStateOverrides(t *testing.T) {
	rc, tmp := newTestRC(t)
	rc.Environment[hermesGatewayLockDirEnv] = "/custom/locks"
	rc.Environment["XDG_STATE_HOME"] = "/custom/state"
	rc.Environment["NO_PROXY"] = "localhost,cllama,internal"
	rc.Environment["no_proxy"] = "localhost,cllama,internal"
	runtimeDir := filepath.Join(tmp, "runtime")
	if err := os.MkdirAll(runtimeDir, 0o700); err != nil {
		t.Fatal(err)
	}

	result, err := (&Driver{}).Materialize(rc, driver.MaterializeOpts{RuntimeDir: runtimeDir, PodName: "test"})
	if err != nil {
		t.Fatalf("Materialize returned error: %v", err)
	}

	if got := result.Environment[hermesGatewayLockDirEnv]; got != "/custom/locks" {
		t.Fatalf("expected %s override, got %q", hermesGatewayLockDirEnv, got)
	}
	if got := result.Environment["XDG_STATE_HOME"]; got != "/custom/state" {
		t.Fatalf("expected XDG_STATE_HOME override, got %q", got)
	}
	if got := result.Environment["NO_PROXY"]; got != "localhost,cllama,internal" {
		t.Fatalf("expected NO_PROXY override, got %q", got)
	}
	if got := result.Environment["no_proxy"]; got != "localhost,cllama,internal" {
		t.Fatalf("expected no_proxy override, got %q", got)
	}

	envData, err := os.ReadFile(filepath.Join(runtimeDir, "hermes-home", ".env"))
	if err != nil {
		t.Fatalf("read .env: %v", err)
	}
	env := string(envData)
	for _, expected := range []string{
		hermesGatewayLockDirEnv + "=/custom/locks\n",
		"NO_PROXY=localhost,cllama,internal\n",
		"XDG_STATE_HOME=/custom/state\n",
		"no_proxy=localhost,cllama,internal\n",
	} {
		if !strings.Contains(env, expected) {
			t.Fatalf("expected %q in .env, got:\n%s", expected, env)
		}
	}
}

func TestMaterializePersonaSoulOverridesDefault(t *testing.T) {
	rc, tmp := newTestRC(t)
	personaDir := filepath.Join(tmp, "persona")
	if err := os.MkdirAll(personaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(personaDir, "SOUL.md"), []byte("Custom persona soul"), 0o644); err != nil {
		t.Fatal(err)
	}
	rc.PersonaHostPath = personaDir

	runtimeDir := filepath.Join(tmp, "runtime")
	if err := os.MkdirAll(runtimeDir, 0o700); err != nil {
		t.Fatal(err)
	}

	_, err := (&Driver{}).Materialize(rc, driver.MaterializeOpts{RuntimeDir: runtimeDir, PodName: "test"})
	if err != nil {
		t.Fatalf("Materialize returned error: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(runtimeDir, "hermes-home", "SOUL.md"))
	if err != nil {
		t.Fatalf("expected SOUL.md: %v", err)
	}
	if string(data) != "Custom persona soul" {
		t.Fatalf("expected persona SOUL.md to override default, got: %q", string(data))
	}
}

func TestMaterializeCopiesPersonaSoul(t *testing.T) {
	rc, tmp := newTestRC(t)
	personaDir := filepath.Join(tmp, "persona")
	if err := os.MkdirAll(personaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(personaDir, "SOUL.md"), []byte("Persona soul"), 0o644); err != nil {
		t.Fatal(err)
	}
	rc.PersonaHostPath = personaDir

	runtimeDir := filepath.Join(tmp, "runtime")
	if err := os.MkdirAll(runtimeDir, 0o700); err != nil {
		t.Fatal(err)
	}

	result, err := (&Driver{}).Materialize(rc, driver.MaterializeOpts{RuntimeDir: runtimeDir})
	if err != nil {
		t.Fatalf("Materialize returned error: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(runtimeDir, "hermes-home", "SOUL.md"))
	if err != nil {
		t.Fatalf("expected copied SOUL.md: %v", err)
	}
	if string(data) != "Persona soul" {
		t.Fatalf("unexpected SOUL.md content: %q", string(data))
	}
	if result.Environment["CLAW_PERSONA_DIR"] != hermesPersonaDir {
		t.Fatalf("unexpected CLAW_PERSONA_DIR: %q", result.Environment["CLAW_PERSONA_DIR"])
	}
}

func TestCopyPersonaSoulNoopWhenMissing(t *testing.T) {
	personaDir := t.TempDir() // no SOUL.md inside
	homeDir := t.TempDir()

	if err := CopyPersonaSoul(personaDir, homeDir); err != nil {
		t.Fatalf("CopyPersonaSoul should succeed when SOUL.md is absent: %v", err)
	}
	if _, err := os.Stat(filepath.Join(homeDir, "SOUL.md")); !os.IsNotExist(err) {
		t.Fatal("expected no SOUL.md in homeDir when persona has none")
	}
}

func TestMaterializeCllamaSetsContainerEnv(t *testing.T) {
	rc, tmp := newTestRC(t)
	rc.Cllama = []string{"passthrough"}
	rc.CllamaToken = "weston:abc123"

	runtimeDir := filepath.Join(tmp, "runtime")
	if err := os.MkdirAll(runtimeDir, 0o700); err != nil {
		t.Fatal(err)
	}

	result, err := (&Driver{}).Materialize(rc, driver.MaterializeOpts{RuntimeDir: runtimeDir, PodName: "test"})
	if err != nil {
		t.Fatalf("Materialize returned error: %v", err)
	}

	if got := result.Environment["OPENAI_BASE_URL"]; got != "http://cllama:8080/v1" {
		t.Fatalf("expected OPENAI_BASE_URL=http://cllama:8080/v1, got %q", got)
	}
	if got := result.Environment["OPENAI_API_KEY"]; got != "weston:abc123" {
		t.Fatalf("expected OPENAI_API_KEY=weston:abc123, got %q", got)
	}

	// Verify config.yaml also has the routing
	configData, err := os.ReadFile(filepath.Join(runtimeDir, "hermes-home", "config.yaml"))
	if err != nil {
		t.Fatalf("read config.yaml: %v", err)
	}
	cfg := string(configData)
	if !strings.Contains(cfg, "base_url: http://cllama:8080/v1") {
		t.Fatalf("expected base_url in config.yaml, got:\n%s", cfg)
	}
	if !strings.Contains(cfg, "api_key: weston:abc123") {
		t.Fatalf("expected api_key in config.yaml, got:\n%s", cfg)
	}
	if !strings.Contains(cfg, "provider: custom") {
		t.Fatalf("expected custom provider in config.yaml, got:\n%s", cfg)
	}
}

func TestMaterializeDefaultsAutoThreadOff(t *testing.T) {
	rc, tmp := newTestRC(t)
	runtimeDir := filepath.Join(tmp, "runtime")
	if err := os.MkdirAll(runtimeDir, 0o700); err != nil {
		t.Fatal(err)
	}

	result, err := (&Driver{}).Materialize(rc, driver.MaterializeOpts{RuntimeDir: runtimeDir, PodName: "test"})
	if err != nil {
		t.Fatalf("Materialize returned error: %v", err)
	}

	if got := result.Environment["DISCORD_AUTO_THREAD"]; got != "false" {
		t.Fatalf("expected DISCORD_AUTO_THREAD=false, got %q", got)
	}
}

func TestMaterializeSlackAppliesTextChannelDefaults(t *testing.T) {
	rc, tmp := newTestRC(t)
	rc.Handles = map[string]*driver.HandleInfo{"slack": {}}
	runtimeDir := filepath.Join(tmp, "runtime")
	if err := os.MkdirAll(runtimeDir, 0o700); err != nil {
		t.Fatal(err)
	}

	result, err := (&Driver{}).Materialize(rc, driver.MaterializeOpts{RuntimeDir: runtimeDir, PodName: "test"})
	if err != nil {
		t.Fatalf("Materialize returned error: %v", err)
	}

	if got := result.Environment["HERMES_TOOL_ONLY_MODE"]; got != "1" {
		t.Fatalf("expected HERMES_TOOL_ONLY_MODE=1, got %q", got)
	}
	if got := result.Environment[hermesToolProgressModeEnv]; got != "off" {
		t.Fatalf("expected %s=off, got %q", hermesToolProgressModeEnv, got)
	}
	if got := result.Environment[hermesAllowSilentFinalEnv]; got != "1" {
		t.Fatalf("expected %s=1, got %q", hermesAllowSilentFinalEnv, got)
	}
	if got := result.Environment[clawdapusDisabledToolsEnv]; got != hermesTextToSpeechTool {
		t.Fatalf("expected %s=%s, got %q", clawdapusDisabledToolsEnv, hermesTextToSpeechTool, got)
	}
	if got := result.Environment["SLACK_REQUIRE_MENTION"]; got != "true" {
		t.Fatalf("expected SLACK_REQUIRE_MENTION=true, got %q", got)
	}
}

func TestMaterializeSlackAllowsMentionDefaultOverride(t *testing.T) {
	rc, tmp := newTestRC(t)
	rc.Handles = map[string]*driver.HandleInfo{"slack": {}}
	rc.Environment["SLACK_REQUIRE_MENTION"] = "false"
	rc.Environment["SLACK_ALLOW_BOTS"] = "mentions"
	runtimeDir := filepath.Join(tmp, "runtime")
	if err := os.MkdirAll(runtimeDir, 0o700); err != nil {
		t.Fatal(err)
	}

	result, err := (&Driver{}).Materialize(rc, driver.MaterializeOpts{RuntimeDir: runtimeDir, PodName: "test"})
	if err != nil {
		t.Fatalf("Materialize returned error: %v", err)
	}

	if got := result.Environment["SLACK_REQUIRE_MENTION"]; got != "false" {
		t.Fatalf("expected SLACK_REQUIRE_MENTION override, got %q", got)
	}
	if got := result.Environment["SLACK_ALLOW_BOTS"]; got != "mentions" {
		t.Fatalf("expected SLACK_ALLOW_BOTS override, got %q", got)
	}

	envData, err := os.ReadFile(filepath.Join(runtimeDir, "hermes-home", ".env"))
	if err != nil {
		t.Fatalf("read .env: %v", err)
	}
	if !strings.Contains(string(envData), "SLACK_REQUIRE_MENTION=false\n") {
		t.Fatalf("expected SLACK_REQUIRE_MENTION override in .env, got:\n%s", envData)
	}
	if !strings.Contains(string(envData), "SLACK_ALLOW_BOTS=mentions\n") {
		t.Fatalf("expected SLACK_ALLOW_BOTS override in .env, got:\n%s", envData)
	}
}

func TestMaterializeAllowsToolProgressOverride(t *testing.T) {
	rc, tmp := newTestRC(t)
	rc.Environment[hermesToolProgressModeEnv] = "verbose"
	runtimeDir := filepath.Join(tmp, "runtime")
	if err := os.MkdirAll(runtimeDir, 0o700); err != nil {
		t.Fatal(err)
	}

	result, err := (&Driver{}).Materialize(rc, driver.MaterializeOpts{RuntimeDir: runtimeDir, PodName: "test"})
	if err != nil {
		t.Fatalf("Materialize returned error: %v", err)
	}

	if got := result.Environment[hermesToolProgressModeEnv]; got != "verbose" {
		t.Fatalf("expected %s override, got %q", hermesToolProgressModeEnv, got)
	}

	envData, err := os.ReadFile(filepath.Join(runtimeDir, "hermes-home", ".env"))
	if err != nil {
		t.Fatalf("read .env: %v", err)
	}
	if !strings.Contains(string(envData), hermesToolProgressModeEnv+"=verbose\n") {
		t.Fatalf("expected %s override in .env, got:\n%s", hermesToolProgressModeEnv, envData)
	}
}

func TestMaterializeAllowsSilentFinalDefaultOverride(t *testing.T) {
	rc, tmp := newTestRC(t)
	rc.Environment[hermesAllowSilentFinalEnv] = "0"
	runtimeDir := filepath.Join(tmp, "runtime")
	if err := os.MkdirAll(runtimeDir, 0o700); err != nil {
		t.Fatal(err)
	}

	result, err := (&Driver{}).Materialize(rc, driver.MaterializeOpts{RuntimeDir: runtimeDir, PodName: "test"})
	if err != nil {
		t.Fatalf("Materialize returned error: %v", err)
	}

	if got := result.Environment[hermesAllowSilentFinalEnv]; got != "0" {
		t.Fatalf("expected %s override, got %q", hermesAllowSilentFinalEnv, got)
	}

	envData, err := os.ReadFile(filepath.Join(runtimeDir, "hermes-home", ".env"))
	if err != nil {
		t.Fatalf("read .env: %v", err)
	}
	env := string(envData)
	if !strings.Contains(env, hermesAllowSilentFinalEnv+"=0\n") {
		t.Fatalf("expected %s override in .env, got:\n%s", hermesAllowSilentFinalEnv, env)
	}
	if strings.Contains(env, hermesAllowSilentFinalEnv+"=1\n") {
		t.Fatalf("expected no default %s=1 when override is set, got:\n%s", hermesAllowSilentFinalEnv, env)
	}
}

func TestMaterializeAllowsChatStatusDeliveryOverride(t *testing.T) {
	rc, tmp := newTestRC(t)
	rc.Environment[hermesChatStatusDeliveryEnv] = "on"
	runtimeDir := filepath.Join(tmp, "runtime")
	if err := os.MkdirAll(runtimeDir, 0o700); err != nil {
		t.Fatal(err)
	}

	result, err := (&Driver{}).Materialize(rc, driver.MaterializeOpts{RuntimeDir: runtimeDir, PodName: "test"})
	if err != nil {
		t.Fatalf("Materialize returned error: %v", err)
	}

	if got := result.Environment[hermesChatStatusDeliveryEnv]; got != "on" {
		t.Fatalf("expected %s override, got %q", hermesChatStatusDeliveryEnv, got)
	}

	envData, err := os.ReadFile(filepath.Join(runtimeDir, "hermes-home", ".env"))
	if err != nil {
		t.Fatalf("read .env: %v", err)
	}
	env := string(envData)
	if !strings.Contains(env, hermesChatStatusDeliveryEnv+"=on\n") {
		t.Fatalf("expected %s override in .env, got:\n%s", hermesChatStatusDeliveryEnv, env)
	}
	if strings.Contains(env, hermesChatStatusDeliveryEnv+"=off\n") {
		t.Fatalf("expected no default %s=off when override is set, got:\n%s", hermesChatStatusDeliveryEnv, env)
	}
}

func TestMaterializeHonorsHermesAllowToolsOptIn(t *testing.T) {
	rc, tmp := newTestRC(t)
	rc.Hermes = &driver.HermesConfig{AllowTools: []string{hermesTextToSpeechTool}}
	runtimeDir := filepath.Join(tmp, "runtime")
	if err := os.MkdirAll(runtimeDir, 0o700); err != nil {
		t.Fatal(err)
	}

	result, err := (&Driver{}).Materialize(rc, driver.MaterializeOpts{RuntimeDir: runtimeDir, PodName: "test"})
	if err != nil {
		t.Fatalf("Materialize returned error: %v", err)
	}

	if _, ok := result.Environment[clawdapusDisabledToolsEnv]; ok {
		t.Fatalf("expected no %s in container env, got %q", clawdapusDisabledToolsEnv, result.Environment[clawdapusDisabledToolsEnv])
	}

	envData, err := os.ReadFile(filepath.Join(runtimeDir, "hermes-home", ".env"))
	if err != nil {
		t.Fatalf("read .env: %v", err)
	}
	if strings.Contains(string(envData), clawdapusDisabledToolsEnv+"=") {
		t.Fatalf("expected no %s in .env, got:\n%s", clawdapusDisabledToolsEnv, envData)
	}
}

func TestMaterializeWritesExplicitDisabledToolsWithoutChatHandle(t *testing.T) {
	rc, tmp := newTestRC(t)
	rc.Handles = nil
	rc.Hermes = &driver.HermesConfig{DisableTools: []string{"skill_manage"}}
	runtimeDir := filepath.Join(tmp, "runtime")
	if err := os.MkdirAll(runtimeDir, 0o700); err != nil {
		t.Fatal(err)
	}

	result, err := (&Driver{}).Materialize(rc, driver.MaterializeOpts{RuntimeDir: runtimeDir, PodName: "test"})
	if err != nil {
		t.Fatalf("Materialize returned error: %v", err)
	}

	if got := result.Environment[clawdapusDisabledToolsEnv]; got != "skill_manage" {
		t.Fatalf("expected %s=skill_manage in container env, got %q", clawdapusDisabledToolsEnv, got)
	}

	envData, err := os.ReadFile(filepath.Join(runtimeDir, "hermes-home", ".env"))
	if err != nil {
		t.Fatalf("read .env: %v", err)
	}
	if !strings.Contains(string(envData), clawdapusDisabledToolsEnv+"=skill_manage\n") {
		t.Fatalf("expected explicit disabled tool in .env, got:\n%s", envData)
	}
}

func TestMaterializeWritesAllowSilentEnv(t *testing.T) {
	rc, tmp := newTestRC(t)
	rc.Hermes = &driver.HermesConfig{AllowSilent: true}
	runtimeDir := filepath.Join(tmp, "runtime")
	if err := os.MkdirAll(runtimeDir, 0o700); err != nil {
		t.Fatal(err)
	}

	result, err := (&Driver{}).Materialize(rc, driver.MaterializeOpts{RuntimeDir: runtimeDir, PodName: "test"})
	if err != nil {
		t.Fatalf("Materialize returned error: %v", err)
	}

	if got := result.Environment[hermesAllowSilentFinalEnv]; got != "1" {
		t.Fatalf("expected %s=1 in container env, got %q", hermesAllowSilentFinalEnv, got)
	}

	envData, err := os.ReadFile(filepath.Join(runtimeDir, "hermes-home", ".env"))
	if err != nil {
		t.Fatalf("read .env: %v", err)
	}
	if !strings.Contains(string(envData), hermesAllowSilentFinalEnv+"=1\n") {
		t.Fatalf("expected %s=1 in .env, got:\n%s", hermesAllowSilentFinalEnv, envData)
	}
}

func newTestRC(t *testing.T) (*driver.ResolvedClaw, string) {
	t.Helper()
	tmp := t.TempDir()
	agentPath := filepath.Join(tmp, "AGENTS.md")
	if err := os.WriteFile(agentPath, []byte("# Agent\n\nYou are Hermes."), 0o644); err != nil {
		t.Fatal(err)
	}

	return &driver.ResolvedClaw{
		ServiceName:   "hermes",
		ClawType:      "hermes",
		AgentHostPath: agentPath,
		Models:        map[string]string{"primary": "openrouter/anthropic/claude-sonnet-4"},
		Handles: map[string]*driver.HandleInfo{
			"discord": {},
		},
		Environment: map[string]string{
			"DISCORD_BOT_TOKEN":  "discord-token",
			"OPENROUTER_API_KEY": "or-key",
			"SLACK_BOT_TOKEN":    "slack-bot",
			"SLACK_APP_TOKEN":    "slack-app",
		},
	}, tmp
}
