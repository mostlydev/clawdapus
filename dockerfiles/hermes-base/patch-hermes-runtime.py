#!/usr/bin/env python3
"""Apply small compatibility fixes to the pinned Hermes checkout.

Hermes v0.22+ is installed editable (upstream refuses wheel builds outside its
Nix derivation), so the patch root is the source tree, not site-packages:

    python3 patch-hermes-runtime.py /opt/hermes-agent

Each patch goes through ``replace_once`` so the docker build fails loud when
upstream drift moves a target string; there is no tolerant/skip mode. When a
patch becomes obsolete because upstream merged the equivalent fix or grew a
native knob, delete the patch instead of reshaping it to match.

Dropped at the v0.22.2 refresh because upstream is now native:
  - Discord members intent: conditional upstream (_needs_server_members_intent).
  - Reply-mention auto-ping: DISCORD_ALLOW_MENTION_REPLIED_USER=false.
  - Blocking slash-command sync: DISCORD_COMMAND_SYNC_POLICY=off.
  - Memory fuzzy match / close-match hints / replace guidance: native in
    tools/memory_tool_store.py (_normalize_for_match, _no_match_error).
  - Memory char limits: config.yaml memory.memory_char_limit /
    memory.user_char_limit (the old HERMES_MEMORY_INDEX_MAX_CHARS /
    HERMES_USER_MEMORY_MAX_CHARS env override and 12000/6000 defaults are gone).
  - skill_manage EBUSY retry: utils.atomic_replace handles EXDEV/EBUSY.
  - base.py tool-only fallback comment (comment only, no behavior).
  - minisweagent_path shim: nothing imports it any more.
"""

from __future__ import annotations

import pathlib
import sys


if len(sys.argv) != 2:
    raise SystemExit("usage: patch-hermes-runtime.py <hermes-source-root>")
root = pathlib.Path(sys.argv[1])
if not (root / "hermes_cli").is_dir():
    raise SystemExit(f"{root} is not a Hermes source tree")


def replace_once(text: str, old: str, new: str, label: str) -> str:
    count = text.count(old)
    if count != 1:
        raise SystemExit(f"expected exactly one anchor for {label}, found {count}")
    return text.replace(old, new, 1)


class Source:
    def __init__(self, relative_path: str):
        self.path = root / relative_path
        if not self.path.is_file():
            raise SystemExit(f"expected Hermes file to exist: {relative_path}")
        self.text = self.path.read_text()

    def replace(self, old: str, new: str, label: str) -> None:
        self.text = replace_once(self.text, old, new, label)

    def save(self) -> None:
        self.path.write_text(self.text)


# ---------------------------------------------------------------------------
# Identity: HERMES_DEFAULT_AGENT_IDENTITY replaces the upstream default identity
# (used when no SOUL.md is present), and suppresses the "You run on Hermes
# Agent (by Nous Research)..." help block that upstream injects right after
# SOUL.md so the contracted identity is not diluted.
# ---------------------------------------------------------------------------
src = Source("agent/prompt_builder.py")
src.replace(
    '''DEFAULT_AGENT_IDENTITY = (
    # A behavior spec''',
    '''_UPSTREAM_DEFAULT_AGENT_IDENTITY = (
    # A behavior spec''',
    "prompt_builder default identity rename",
)
src.replace(
    '''    "it. Depth is earned — give it when the user asks for detail, teaches, or the stakes demand it, not by default."
)
''',
    '''    "it. Depth is earned — give it when the user asks for detail, teaches, or the stakes demand it, not by default."
)

# Clawdapus: a managed identity replaces the upstream default identity layer.
DEFAULT_AGENT_IDENTITY = (
    os.getenv("HERMES_DEFAULT_AGENT_IDENTITY", "").strip() or _UPSTREAM_DEFAULT_AGENT_IDENTITY
)
''',
    "prompt_builder default identity env override",
)
src.save()

src = Source("agent/system_prompt.py")
src.replace(
    '''    if "skill_view" in (agent.valid_tool_names or set()) and "- hermes-agent:" in skills_prompt:
        stable_parts[_help_guidance_slot] = HERMES_AGENT_HELP_GUIDANCE
''',
    '''    if "skill_view" in (agent.valid_tool_names or set()) and "- hermes-agent:" in skills_prompt:
        stable_parts[_help_guidance_slot] = HERMES_AGENT_HELP_GUIDANCE
    # Clawdapus: a managed identity owns self-description; drop the runner help block.
    if os.getenv("HERMES_DEFAULT_AGENT_IDENTITY", "").strip():
        del stable_parts[_help_guidance_slot]
''',
    "system_prompt suppress Hermes help guidance under managed identity",
)
src.save()

# Continuing sessions persist a system_prompt snapshot. When a managed identity
# is set, a stored prompt that still carries the upstream Hermes identity or
# help block is stale: rebuild it instead of reusing the Hermes self-image.
src = Source("agent/conversation_loop.py")
src.replace(
    "import logging\nimport re\n",
    "import logging\nimport os\nimport re\n",
    "conversation_loop os import",
)
src.replace(
    '''def _stored_prompt_matches_runtime(agent, prompt: str) -> bool:
    """Return False when the persisted runtime-identity lines are stale."""
''',
    '''def _stored_prompt_matches_runtime(agent, prompt: str) -> bool:
    """Return False when the persisted runtime-identity lines are stale."""
    _claw_identity = os.getenv("HERMES_DEFAULT_AGENT_IDENTITY", "").strip()
    if _claw_identity and prompt and _claw_identity not in prompt and (
        "You are Hermes Agent" in prompt or "You run on Hermes Agent (by Nous Research)" in prompt
    ):
        logger.info("Refreshing stored system prompt because HERMES_DEFAULT_AGENT_IDENTITY changed")
        return False
''',
    "conversation_loop stored prompt identity invalidation",
)
src.save()

# ---------------------------------------------------------------------------
# Discord intents: voice_states stays True upstream; we never use it and it
# requires elevated bot privileges.
# ---------------------------------------------------------------------------
src = Source("plugins/platforms/discord/adapter.py")
src.replace(
    "            intents.voice_states = True\n",
    "            intents.voice_states = False\n",
    "discord voice intent",
)
src.save()

# ---------------------------------------------------------------------------
# Core tool suppression: CLAWDAPUS_DISABLED_TOOLS strips named tools (e.g.
# text_to_speech) from the core list and every bundle's explicit tools[].
# ---------------------------------------------------------------------------
src = Source("toolsets.py")
src.replace(
    "_HERMES_CORE_TOOLS = [",
    """import os as _claw_os

_CLAW_DISABLED_TOOLS = {
    t.strip()
    for t in _claw_os.getenv("CLAWDAPUS_DISABLED_TOOLS", "").split(",")
    if t.strip()
}


def _claw_filter_tools(tools):
    if not _CLAW_DISABLED_TOOLS:
        return tools
    return [t for t in tools if t not in _CLAW_DISABLED_TOOLS]


_HERMES_CORE_TOOLS = [""",
    "_HERMES_CORE_TOOLS env-driven disable filter prelude",
)
src.replace(
    '    "manage_connections",\n]\n',
    '    "manage_connections",\n]\n_HERMES_CORE_TOOLS = _claw_filter_tools(_HERMES_CORE_TOOLS)\n',
    "_HERMES_CORE_TOOLS env-driven disable filter application",
)
src.replace(
    "TOOLSETS = {",
    "TOOLSETS = {  # filtered post-build via _claw_filter_tools below",
    "TOOLSETS comment marker",
)
src.text += (
    "\n# Clawdapus: apply env-driven tool filter to every named bundle's explicit tools[]\n"
    "# (does not touch includes; nested toolset resolution still works).\n"
    "for _ts_name, _ts_def in list(TOOLSETS.items()):\n"
    "    if isinstance(_ts_def, dict) and isinstance(_ts_def.get(\"tools\"), list):\n"
    "        _ts_def[\"tools\"] = _claw_filter_tools(_ts_def[\"tools\"])\n"
)
src.save()

# ---------------------------------------------------------------------------
# Memory: oldest-entry eviction instead of refusing an add at the cap, and
# host-readable (0o666) memory files after every atomic rewrite.
# ---------------------------------------------------------------------------
src = Source("tools/memory_tool_store.py")
src.replace(
    '''        def _add(entries, limit):
            if content in entries:
                return self._success_response(target, "Entry already exists (no duplicate added).")
            if len(ENTRY_DELIMITER.join(entries + [content])) > limit:
''',
    '''        def _add(entries, limit):
            if content in entries:
                return self._success_response(target, "Entry already exists (no duplicate added).")
            # Clawdapus: evict oldest entries (first in file) to fit a new entry that is
            # itself within the limit, instead of freezing memory writes at the cap.
            if len(content) <= limit and len(ENTRY_DELIMITER.join(entries + [content])) > limit:
                kept = list(entries)
                evicted = []
                while kept and len(ENTRY_DELIMITER.join(kept + [content])) > limit:
                    evicted.append(kept.pop(0))
                noun = "entry" if len(evicted) == 1 else "entries"
                return kept + [content], (
                    f"Entry added. Evicted {len(evicted)} oldest {noun} to stay within the memory limit."
                ), {"evicted_count": len(evicted), "evicted_entries": evicted}
            if len(ENTRY_DELIMITER.join(entries + [content])) > limit:
''',
    "memory add oldest-entry eviction",
)
src.replace(
    '            atomic_write_text(path, ENTRY_DELIMITER.join(entries), tmp_prefix=".mem_")\n',
    '            atomic_write_text(path, ENTRY_DELIMITER.join(entries), tmp_prefix=".mem_", mode=0o666)\n',
    "memory file post-rewrite mode",
)
src.save()

# ---------------------------------------------------------------------------
# skill_manage: accept absolute file_path values that point inside the skill's
# own directory by relativizing them before validation. Paths outside the skill
# directory stay absolute and are rejected by the upstream validator.
# ---------------------------------------------------------------------------
src = Source("tools/skill_manager_tool.py")
src.replace(
    "\ndef skill_manage(\n",
    '''
def _claw_relativize_skill_path(name, file_path):
    if not file_path or not name or not Path(file_path).is_absolute():
        return file_path
    try:
        existing = _find_skill(name)
        if not existing:
            return file_path
        return str(Path(file_path).resolve().relative_to(Path(existing["path"]).resolve()))
    except (ValueError, OSError):
        return file_path


def skill_manage(
''',
    "skill_manage absolute path helper",
)
src.replace(
    "    args = dict(content=content, category=category, file_path=file_path, file_content=file_content,\n",
    "    file_path = _claw_relativize_skill_path(name, file_path)\n"
    "    args = dict(content=content, category=category, file_path=file_path, file_content=file_content,\n",
    "skill_manage absolute file_path normalization",
)
src.save()

# ---------------------------------------------------------------------------
# Cron transient-failure delivery: provider/cllama outages are logged and
# recorded on the job run (and in the incident ledger) but are not posted into
# user channels. HERMES_CRON_DELIVER_TRANSIENT_FAILURES=1 restores delivery.
# ---------------------------------------------------------------------------
src = Source("cron/scheduler.py")
src.replace(
    "\ndef _compose_run_delivery(\n",
    '''
def _claw_should_deliver_cron_failure(error) -> bool:
    if os.getenv("HERMES_CRON_DELIVER_TRANSIENT_FAILURES") == "1":
        return True
    text = str(error or "").lower()
    if not text:
        return True
    transient_markers = (
        "upstream request failed", "internal server error", "bad gateway",
        "service unavailable", "gateway timeout", "temporarily unavailable",
        "rate limit", "rate_limit", "timeout", "timed out", "connection reset",
        "connection aborted", "connection refused", "econnreset",
        "429", "502", "503", "504",
    )
    return not any(marker in text for marker in transient_markers)


def _compose_run_delivery(
''',
    "cron scheduler transient failure delivery classifier",
)
src.replace(
    '''        if incident_acked:
            deliver_content = ""
        elif agent_declared:
''',
    '''        if incident_acked:
            deliver_content = ""
        elif not agent_declared and not _claw_should_deliver_cron_failure(error):
            logger.warning("Job '%s': suppressing transient cron failure delivery: %s", job["id"], error)
            deliver_content = ""
        elif agent_declared:
''',
    "cron scheduler suppress transient failure delivery",
)
src.save()

# ---------------------------------------------------------------------------
# cllama consumer session epoch: when the main client targets the in-pod cllama
# host, attach X-Claw-Consumer-Session-Epoch from CLLAMA_CONSUMER_SESSION_EPOCH.
# ---------------------------------------------------------------------------
src = Source("agent/agent_runtime_helpers.py")
src.replace(
    '    _validate_proxy_env_urls()\n    _validate_base_url(client_kwargs.get("base_url"))\n',
    '    import os as _claw_os\n'
    '    _claw_epoch = _claw_os.getenv("CLLAMA_CONSUMER_SESSION_EPOCH", "").strip()\n'
    '    _claw_base_host = base_url_hostname(str(client_kwargs.get("base_url", "") or ""))\n'
    '    if _claw_epoch and (_claw_base_host == "cllama" or _claw_base_host.startswith("cllama-")):\n'
    '        _claw_headers = dict(client_kwargs.get("default_headers") or {})\n'
    '        _claw_headers["X-Claw-Consumer-Session-Epoch"] = _claw_epoch\n'
    '        client_kwargs["default_headers"] = _claw_headers\n'
    '    _validate_proxy_env_urls()\n    _validate_base_url(client_kwargs.get("base_url"))\n',
    "agent runtime cllama consumer session epoch header",
)
src.save()

# ---------------------------------------------------------------------------
# Tool-only mode: force tool_choice=required on the first chat-completions call
# of each user turn so the model must call a tool (preferably send_message).
# ---------------------------------------------------------------------------
src = Source("agent/chat_completion_helpers.py")
src.replace(
    '''    kwargs = _build_api_kwargs_for_mode(agent, api_messages, tools_for_api)
    return merge_session_affinity_headers(
''',
    '''    kwargs = _build_api_kwargs_for_mode(agent, api_messages, tools_for_api)
    kwargs = _claw_apply_tool_only_choice(agent, kwargs, api_messages)
    return merge_session_affinity_headers(
''',
    "build_api_kwargs tool-only mode hook",
)
src.replace(
    "\ndef _build_api_kwargs_for_mode(",
    '''
def _claw_apply_tool_only_choice(agent, kwargs: dict, api_messages: list) -> dict:
    # Clawdapus tool-only mode. Inspect only messages after the latest user
    # message so prior turns' tool_calls do not satisfy this turn. OpenAI-wire
    # chat completions only: other transports spell tool_choice differently.
    if not os.getenv("HERMES_TOOL_ONLY_MODE") or not isinstance(kwargs, dict):
        return kwargs
    if getattr(agent, "api_mode", "") in ("anthropic_messages", "bedrock_converse", "codex_responses"):
        return kwargs
    if not kwargs.get("tools"):
        return kwargs
    last_user = max(
        (i for i, m in enumerate(api_messages or []) if isinstance(m, dict) and m.get("role") == "user"),
        default=-1,
    )
    used_tools = any(
        isinstance(m, dict) and m.get("role") == "assistant" and m.get("tool_calls")
        for m in (api_messages or [])[last_user + 1:]
    )
    if not used_tools:
        kwargs["tool_choice"] = "required"
    return kwargs


def _build_api_kwargs_for_mode(''',
    "tool-only mode tool_choice helper",
)
src.save()

# ---------------------------------------------------------------------------
# Silent final: when HERMES_ALLOW_SILENT_FINAL=1, an empty visible final
# response ends the turn as a completed no-op instead of retrying/nudging.
# Managed messaging agents often reply via send_message and then have nothing
# else to say.
# ---------------------------------------------------------------------------
src = Source("agent/turn_final_response.py")
src.replace(
    "import logging\n",
    "import logging\nimport os\n",
    "turn_final_response os import",
)
src.replace(
    '''    if not agent._has_content_after_think_block(final_response):
        _ev = recover_empty_response(
''',
    '''    if not agent._has_content_after_think_block(final_response) and os.getenv("HERMES_ALLOW_SILENT_FINAL") == "1":
        logger.debug("Silent final enabled; treating empty visible response as completed no-op")
        agent._empty_content_retries = 0
        agent._thinking_prefill_retries = 0
        final_response = ""
        _turn_exit_reason = "silent_final"
        return _verdict("break")

    if not agent._has_content_after_think_block(final_response):
        _ev = recover_empty_response(
''',
    "turn final response silent final opt-in",
)
src.save()

# The gateway's empty-response normalizer would otherwise rewrite a completed
# silent no-op into a visible "no response was generated" warning.
src = Source("gateway/run.py")
src.replace(
    '    if agent_result.get("failed"):\n',
    '    if (\n'
    '        os.getenv("HERMES_ALLOW_SILENT_FINAL") == "1"\n'
    '        and agent_result.get("completed")\n'
    '        and not agent_result.get("failed")\n'
    '        and not agent_result.get("partial")\n'
    '        and not agent_result.get("interrupted")\n'
    '    ):\n'
    '        logger.debug("Silent final enabled; suppressing empty-response warning")\n'
    '        return response\n'
    '\n'
    '    if agent_result.get("failed"):\n',
    "gateway silent final empty-response normalization",
)

# Clawdapus-managed chat surfaces treat status_callback output as runtime
# telemetry. Keep upstream behavior when unset, but suppress chat delivery when
# HERMES_CHAT_STATUS_DELIVERY is explicitly false/off/0.
src.replace(
    '''    text = str(message or "").strip()
    if not text:
        return None
    if _gateway_surface_passes_raw_text(platform):
''',
    '''    text = str(message or "").strip()
    if not text:
        return None
    _claw_status_delivery = os.getenv("HERMES_CHAT_STATUS_DELIVERY", "").strip().lower()
    if _claw_status_delivery and _claw_status_delivery not in {"1", "true", "yes", "on", "chat", "visible", "all"}:
        return None
    if _gateway_surface_passes_raw_text(platform):
''',
    "gateway managed chat status delivery gate",
)
src.save()

# Tool-only mode: suppress duplicate final text when the turn already delivered
# a visible message via send_message. Otherwise base delivery stays the
# fallback so plain final answers are not silently lost.
src = Source("gateway/run_turn.py")
src.replace(
    "\nclass GatewayTurnMixin:",
    '''
def _claw_tool_call_name(tool_call) -> str:
    if not isinstance(tool_call, dict):
        return ""
    function = tool_call.get("function") or {}
    if isinstance(function, dict) and function.get("name"):
        return str(function.get("name"))
    return str(tool_call.get("name") or "")


def _claw_tool_call_arguments(tool_call) -> dict:
    if not isinstance(tool_call, dict):
        return {}
    function = tool_call.get("function") or {}
    raw_args = function.get("arguments") if isinstance(function, dict) else None
    if isinstance(raw_args, dict):
        return raw_args
    if isinstance(raw_args, str) and raw_args.strip():
        try:
            parsed = json.loads(raw_args)
            return parsed if isinstance(parsed, dict) else {}
        except Exception:
            return {}
    return {}


def _claw_turn_sent_message(messages) -> bool:
    send_call_ids = set()
    saw_send_without_id = False
    for msg in messages or []:
        if not isinstance(msg, dict) or msg.get("role") != "assistant":
            continue
        for tool_call in msg.get("tool_calls") or []:
            if _claw_tool_call_name(tool_call) != "send_message":
                continue
            args = _claw_tool_call_arguments(tool_call)
            action = str(args.get("action") or "send").strip().lower()
            if action != "send" or not str(args.get("message") or "").strip():
                continue
            call_id = tool_call.get("id")
            if call_id:
                send_call_ids.add(call_id)
            else:
                saw_send_without_id = True
    if not send_call_ids:
        return saw_send_without_id
    saw_result = False
    for msg in messages or []:
        if not isinstance(msg, dict) or msg.get("role") != "tool":
            continue
        if msg.get("tool_call_id") not in send_call_ids:
            continue
        saw_result = True
        content = msg.get("content")
        if isinstance(content, str):
            try:
                content = json.loads(content)
            except Exception:
                content = {}
        if isinstance(content, dict) and (content.get("success") or content.get("skipped")):
            return True
    return saw_send_without_id or not saw_result


class GatewayTurnMixin:''',
    "gateway run_turn send_message detection helpers",
)
src.replace(
    '        agent_messages = agent_result.get("messages", [])\n',
    '        agent_messages = agent_result.get("messages", [])\n'
    '        if os.getenv("HERMES_TOOL_ONLY_MODE") and response and not _intentional_silence:\n'
    '            _claw_offset = agent_result.get("history_offset", len(history))\n'
    '            _claw_turn = agent_messages[_claw_offset:] if len(agent_messages) > _claw_offset else agent_messages\n'
    '            if _claw_turn_sent_message(_claw_turn):\n'
    '                logger.info("Suppressing duplicate final text after send_message in tool-only mode")\n'
    '                response = ""\n',
    "gateway run_turn suppress duplicate final text after send_message",
)
src.save()

print("hermes runtime patches applied")
