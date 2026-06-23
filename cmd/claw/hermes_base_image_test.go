package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/mostlydev/clawdapus/internal/driver/hermes"
	"github.com/mostlydev/clawdapus/internal/infraimages"
)

func TestHermesBaseImageSourceContract(t *testing.T) {
	root := hermesBaseRepoRoot(t)

	dockerfile := readHermesBaseFile(t, root, "Dockerfile")
	entrypoint := readHermesBaseFile(t, root, "entrypoint.sh")
	patch := readHermesBaseFile(t, root, "patch-hermes-runtime.py")
	minisweagentShim := readHermesBaseFile(t, root, "minisweagent_path.py")

	if infraimages.DefaultHermesBaseTag != hermes.BaseImageVersion {
		t.Fatalf("DefaultHermesBaseTag = %q, want %q", infraimages.DefaultHermesBaseTag, hermes.BaseImageVersion)
	}
	for _, want := range []string{
		`ARG HERMES_UPSTREAM_TAG=` + hermes.UpstreamTag,
		`COPY minisweagent_path.py /tmp/minisweagent_path.py`,
		`COPY patch-hermes-runtime.py /tmp/patch-hermes-runtime.py`,
		`RUN python3 /tmp/patch-hermes-runtime.py`,
		`ENTRYPOINT ["/usr/bin/tini", "--"]`,
		`CMD ["hermes-entrypoint"]`,
	} {
		if !strings.Contains(dockerfile, want) {
			t.Fatalf("Hermes base Dockerfile missing %q", want)
		}
	}
	if strings.Contains(entrypoint, "gateway start") || !strings.Contains(entrypoint, "exec hermes gateway run") {
		t.Fatalf("Hermes base entrypoint must foreground the gateway with `gateway run`:\n%s", entrypoint)
	}
	for _, want := range []string{
		`CLLAMA_CONSUMER_SESSION_EPOCH`,
		`/proc/sys/kernel/random/uuid`,
	} {
		if !strings.Contains(entrypoint, want) {
			t.Fatalf("Hermes base entrypoint missing %q", want)
		}
	}
	for _, want := range []string{
		`HERMES_DEFAULT_AGENT_IDENTITY`,
		`DEFAULT_AGENT_IDENTITY = (`,
		`not stored_prompt.startswith(default_identity)`,
		`shutil.copy("/tmp/minisweagent_path.py", purelib / "minisweagent_path.py")`,
		`HERMES_TOOL_ONLY_MODE`,
		`HERMES_CHAT_STATUS_DELIVERY`,
		`gateway run managed chat status delivery gate`,
		`_claw_turn_sent_message`,
		`Suppressing duplicate final text after send_message`,
		`_already_used_tools_this_turn`,
		`CLAWDAPUS_DISABLED_TOOLS`,
		`_claw_filter_tools`,
		`_HERMES_CORE_TOOLS = _claw_filter_tools(_HERMES_CORE_TOOLS)`,
		`"memory_char_limit": 12000`,
		`HERMES_MEMORY_INDEX_MAX_CHARS`,
		`HERMES_USER_MEMORY_MAX_CHARS`,
		`Evicted {len(evicted_entries)} oldest`,
		`response["evicted_count"] = len(evicted_entries)`,
		`os.chmod(real_path, 0o666)`,
		`HERMES_CRON_DELIVER_TRANSIENT_FAILURES`,
		`_claw_should_deliver_cron_failure`,
		`upstream request failed`,
		`suppressing transient cron failure delivery`,
		`HERMES_ALLOW_SILENT_FINAL`,
		`Silent final enabled; treating empty visible response as completed no-op`,
		`Silent final enabled; suppressing empty-response warning`,
		`agent_result.get("completed")`,
		`intents.voice_states = False`,
		`CLLAMA_CONSUMER_SESSION_EPOCH`,
		`X-Claw-Consumer-Session-Epoch`,
		`_claw_base_host == \"cllama\"`,
	} {
		if !strings.Contains(patch, want) {
			t.Fatalf("Hermes runtime patch missing %q", want)
		}
	}
	if !strings.Contains(minisweagentShim, "def ensure_minisweagent_on_path") {
		t.Fatal("minisweagent_path.py must provide ensure_minisweagent_on_path for tools.terminal_tool")
	}
}

func hermesBaseRepoRoot(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", ".."))
}

func readHermesBaseFile(t *testing.T, root, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, "dockerfiles", "hermes-base", name))
	if err != nil {
		t.Fatalf("read Hermes base %s: %v", name, err)
	}
	return string(data)
}
