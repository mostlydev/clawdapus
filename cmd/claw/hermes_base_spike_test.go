//go:build spike

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestSpikeHermesBaseImageContract(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", ".."))
	tag := fmt.Sprintf("clawdapus-hermes-base-contract:%d", os.Getpid())

	spikeBuildImage(t, filepath.Join(repoRoot, "dockerfiles", "hermes-base"), tag, "Dockerfile")
	t.Cleanup(func() {
		_ = exec.Command("docker", "image", "rm", "-f", tag).Run()
	})

	assertDockerInspectJSON(t, tag, "Config.Entrypoint", `["/usr/bin/tini","--"]`)
	assertDockerInspectJSON(t, tag, "Config.Cmd", `["hermes-entrypoint"]`)

	script := `grep -q 'exec hermes gateway run' /usr/local/bin/hermes-entrypoint
grep -q 'CLLAMA_CONSUMER_SESSION_EPOCH' /usr/local/bin/hermes-entrypoint
python - <<'PY'
import importlib.util
import inspect
import os
from pathlib import Path

os.environ["HERMES_DEFAULT_AGENT_IDENTITY"] = "Clawdapus identity probe"
os.environ["CLAWDAPUS_DISABLED_TOOLS"] = "text_to_speech"
os.environ["CLLAMA_CONSUMER_SESSION_EPOCH"] = "epoch-contract"
os.environ["HERMES_GATEWAY_LOCK_DIR"] = "/tmp/hermes-gateway-locks"
os.environ["XDG_STATE_HOME"] = "/tmp/xdg-state"
os.environ["HERMES_ALLOW_SILENT_FINAL"] = "1"
assert importlib.util.find_spec("minisweagent_path") is not None
import tools.terminal_tool

from cron import scheduler as cron_scheduler
assert not cron_scheduler._claw_should_deliver_cron_failure("upstream request failed")
assert not cron_scheduler._claw_should_deliver_cron_failure("Internal Server Error")
assert cron_scheduler._claw_should_deliver_cron_failure("prompt injection scanner blocked the job")
os.environ["HERMES_CRON_DELIVER_TRANSIENT_FAILURES"] = "1"
assert cron_scheduler._claw_should_deliver_cron_failure("upstream request failed")
del os.environ["HERMES_CRON_DELIVER_TRANSIENT_FAILURES"]

from gateway.status import _get_lock_dir
assert str(_get_lock_dir()) == "/tmp/hermes-gateway-locks"

from toolsets import _HERMES_CORE_TOOLS, TOOLSETS
assert "text_to_speech" not in _HERMES_CORE_TOOLS
for _name, _toolset in TOOLSETS.items():
    if isinstance(_toolset, dict) and isinstance(_toolset.get("tools"), list):
        assert "text_to_speech" not in _toolset["tools"], _name

from agent.prompt_builder import DEFAULT_AGENT_IDENTITY, MEMORY_GUIDANCE, SESSION_SEARCH_GUIDANCE, SKILLS_GUIDANCE
assert DEFAULT_AGENT_IDENTITY == "Clawdapus identity probe"
assert "You are Hermes Agent" not in DEFAULT_AGENT_IDENTITY
assert "You have persistent memory across sessions" in MEMORY_GUIDANCE
assert "session_search" in SESSION_SEARCH_GUIDANCE
assert "skill_manage" in SKILLS_GUIDANCE

from run_agent import AIAgent
agent = AIAgent(
    base_url="http://127.0.0.1:9/v1",
    api_key="test",
    model="test/model",
    enabled_toolsets=[],
    disabled_toolsets=["all"],
    quiet_mode=True,
    skip_context_files=True,
    skip_memory=True,
)
prompt = agent._build_system_prompt()
assert prompt.startswith("Clawdapus identity probe"), prompt[:200]
assert not prompt.startswith("You are Hermes Agent"), prompt[:200]

import agent.agent_runtime_helpers as runtime_helpers
import agent.chat_completion_helpers as chat_completion_helpers
import agent.conversation_loop as conversation_loop

tool_source = Path(chat_completion_helpers.__file__).read_text()
assert "_already_used_tools_this_turn" in tool_source
assert "api_messages[_last_user_index + 1 :]" in tool_source

silent_source = Path(conversation_loop.__file__).read_text()
assert "HERMES_ALLOW_SILENT_FINAL" in silent_source
assert "Silent final enabled; treating empty visible response as completed no-op" in silent_source
assert '"completed": True' in silent_source
assert silent_source.index('os.getenv("HERMES_ALLOW_SILENT_FINAL") == "1"') < silent_source.index("Model returned empty after tool calls")

runtime_source = inspect.getsource(runtime_helpers.create_openai_client)
assert "X-Claw-Consumer-Session-Epoch" in runtime_source
assert "CLLAMA_CONSUMER_SESSION_EPOCH" in runtime_source

epoch_agent = AIAgent(
    base_url="http://cllama:8080/v1",
    api_key="test",
    model="test/model",
    enabled_toolsets=[],
    disabled_toolsets=["all"],
    quiet_mode=True,
    skip_context_files=True,
    skip_memory=True,
)
headers = {str(k).lower(): v for k, v in dict(getattr(epoch_agent.client, "default_headers", {}) or {}).items()}
assert headers.get("x-claw-consumer-session-epoch") == "epoch-contract", headers

from gateway.run import GatewayRunner, _normalize_empty_agent_response
from gateway.config import Platform
from gateway.run import _prepare_gateway_status_message
os.environ.pop("HERMES_CHAT_STATUS_DELIVERY", None)
assert _prepare_gateway_status_message(Platform.DISCORD, "lifecycle", "Retrying in 1s") == "Retrying in 1s"
os.environ["HERMES_CHAT_STATUS_DELIVERY"] = "off"
assert _prepare_gateway_status_message(Platform.DISCORD, "lifecycle", "Retrying in 1s") is None
assert _prepare_gateway_status_message(Platform.SLACK, "warn", "Auxiliary title generation failed") is None
os.environ["HERMES_CHAT_STATUS_DELIVERY"] = "on"
assert _prepare_gateway_status_message(Platform.DISCORD, "lifecycle", "Retrying in 1s") == "Retrying in 1s"
del os.environ["HERMES_CHAT_STATUS_DELIVERY"]
assert _normalize_empty_agent_response(
    {"api_calls": 1, "completed": True, "partial": False},
    "",
) == ""
assert "request failed" in _normalize_empty_agent_response(
    {"api_calls": 1, "completed": True, "failed": True, "error": "boom"},
    "",
)
assert "Processing stopped" in _normalize_empty_agent_response(
    {"api_calls": 1, "completed": True, "partial": True, "error": "cut off"},
    "",
)

assert GatewayRunner._claw_turn_sent_message([
    {
        "role": "assistant",
        "tool_calls": [
            {
                "id": "call_send",
                "function": {
                    "name": "send_message",
                    "arguments": '{"target":"discord:123","message":"visible"}',
                },
            }
        ],
    },
    {"role": "tool", "tool_call_id": "call_send", "content": '{"success": true}'},
])
assert not GatewayRunner._claw_turn_sent_message([
    {
        "role": "assistant",
        "tool_calls": [
            {
                "id": "call_list",
                "function": {
                    "name": "send_message",
                    "arguments": '{"action":"list"}',
                },
            }
        ],
    },
    {"role": "tool", "tool_call_id": "call_list", "content": '{"targets": []}'},
])
assert not GatewayRunner._claw_turn_sent_message([
    {
        "role": "assistant",
        "tool_calls": [
            {
                "id": "call_failed",
                "function": {
                    "name": "send_message",
                    "arguments": '{"target":"discord:123","message":"visible"}',
                },
            }
        ],
    },
    {"role": "tool", "tool_call_id": "call_failed", "content": '{"error": "send failed"}'},
])
print("ok")
PY`
	cmd := exec.Command("docker", "run", "--rm", tag, "sh", "-lc", script)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Hermes base image contract failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "ok") {
		t.Fatalf("expected import probe to print ok, got:\n%s", out)
	}
}

func assertDockerInspectJSON(t *testing.T, tag, field, want string) {
	t.Helper()
	cmd := exec.Command("docker", "image", "inspect", "--format", "{{json ."+field+"}}", tag)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("inspect %s: %v\n%s", field, err, out)
	}
	got := strings.TrimSpace(string(out))
	if got != want {
		t.Fatalf("inspect %s = %s, want %s", field, got, want)
	}
}
