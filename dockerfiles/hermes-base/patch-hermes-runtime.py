#!/usr/bin/env python3
"""Apply small compatibility fixes to the pinned Hermes install."""

from __future__ import annotations

import pathlib
import shutil
import sysconfig


purelib = pathlib.Path(sysconfig.get_paths()["purelib"])


def replace_once(text: str, old: str, new: str, label: str) -> str:
    if old not in text:
        raise SystemExit(f"expected to patch {label}")
    return text.replace(old, new, 1)

shutil.copy("/tmp/minisweagent_path.py", purelib / "minisweagent_path.py")

# Replace only the first identity layer. Hermes memory/session/skill guidance
# remains upstream and is still injected when the matching tools are present.
prompt_builder = purelib / "agent" / "prompt_builder.py"
text = prompt_builder.read_text()
text = replace_once(
    text,
    '''DEFAULT_AGENT_IDENTITY = (
    "You are Hermes Agent, an intelligent AI assistant created by Nous Research. "
    "You are helpful, knowledgeable, and direct. You assist users with a wide "
    "range of tasks including answering questions, writing and editing code, "
    "analyzing information, creative work, and executing actions via your tools. "
    "You communicate clearly, admit uncertainty when appropriate, and prioritize "
    "being genuinely useful over being verbose unless otherwise directed below. "
    "Be targeted and efficient in your exploration and investigations."
)''',
    '''DEFAULT_AGENT_IDENTITY = (
    os.getenv("HERMES_DEFAULT_AGENT_IDENTITY", "").strip()
    or (
        "You are Hermes Agent, an intelligent AI assistant created by Nous Research. "
        "You are helpful, knowledgeable, and direct. You assist users with a wide "
        "range of tasks including answering questions, writing and editing code, "
        "analyzing information, creative work, and executing actions via your tools. "
        "You communicate clearly, admit uncertainty when appropriate, and prioritize "
        "being genuinely useful over being verbose unless otherwise directed below. "
        "Be targeted and efficient in your exploration and investigations."
    )
)''',
    "prompt_builder default identity env override",
)
prompt_builder.write_text(text)

discord_adapter = purelib / "gateway" / "platforms" / "discord.py"
text = discord_adapter.read_text()
text = replace_once(
    text,
    "            intents.members = True\n",
    "            intents.members = False\n",
    "discord members intent",
)
text = replace_once(
    text,
    "            intents.voice_states = True\n",
    "            intents.voice_states = False\n",
    "discord voice intent",
)
text = replace_once(
    text,
    # The blank line between _resolve_allowed_usernames and # Sync slash commands
    # has 16 spaces of trailing whitespace in the upstream source.
    "                # Resolve any usernames in the allowed list to numeric IDs\n"
    "                await adapter_self._resolve_allowed_usernames()\n"
    "                \n"
    "                # Sync slash commands with Discord\n"
    "                try:\n"
    "                    synced = await adapter_self._client.tree.sync()\n"
    "                    logger.info(\"[%s] Synced %d slash command(s)\", adapter_self.name, len(synced))\n"
    "                except Exception as e:  # pragma: no cover - defensive logging\n"
    "                    logger.warning(\"[%s] Slash command sync failed: %s\", adapter_self.name, e, exc_info=True)\n"
    "                adapter_self._ready_event.set()\n",
    """                # Mark the gateway ready before best-effort post-connect work.
                adapter_self._ready_event.set()

                async def finalize_startup():
                    client = adapter_self._client
                    if client is None:
                        return

                    # Resolve any usernames in the allowed list to numeric IDs.
                    await adapter_self._resolve_allowed_usernames()

                    # Slash-command sync can be slow on larger guilds; keep it best-effort.
                    try:
                        synced = await asyncio.wait_for(client.tree.sync(), timeout=20)
                        logger.info("[%s] Synced %d slash command(s)", adapter_self.name, len(synced))
                    except Exception as e:  # pragma: no cover - defensive logging
                        logger.warning("[%s] Slash command sync failed: %s", adapter_self.name, e, exc_info=True)

                asyncio.create_task(finalize_startup())
""",
    "discord ready handler",
)
# ── Disable reply-mentions so agent replies do not ping the original author ──
# Discord's default is replied_user=True, which triggers mention loops in
# multi-agent pods. We inject allowed_mentions on every channel.send that
# carries a reference.
text = replace_once(
    text,
    """                    msg = await channel.send(
                        content=chunk,
                        reference=chunk_reference,
                    )""",
    """                    msg = await channel.send(
                        content=chunk,
                        reference=chunk_reference,
                        allowed_mentions=discord.AllowedMentions(replied_user=False),
                    )""",
    "discord reply mention (primary send)",
)
text = replace_once(
    text,
    """                        msg = await channel.send(
                            content=chunk,
                            reference=None,
                        )""",
    """                        msg = await channel.send(
                            content=chunk,
                            reference=None,
                            allowed_mentions=discord.AllowedMentions(replied_user=False),
                        )""",
    "discord reply mention (fallback send)",
)
discord_adapter.write_text(text)

# ── Tool-only mode: prefer send_message without dropping final text ──────────
# HERMES_TOOL_ONLY_MODE makes send_message the preferred visible-delivery path.
# The gateway runner suppresses duplicate final text when send_message already
# succeeded in the current turn; otherwise base delivery remains a fallback so
# plain final answers are not silently lost.
base_adapter = purelib / "gateway" / "platforms" / "base.py"
text = base_adapter.read_text()
text = replace_once(
    text,
    "                # Send the text portion\n                if text_content:\n",
    "                # Send the text portion. In HERMES_TOOL_ONLY_MODE, run.py\n"
    "                # clears this text only when the current turn already sent\n"
    "                # a visible message via send_message; otherwise this is the\n"
    "                # fallback that prevents final answers from disappearing.\n"
    "                if text_content:\n",
    "base platform tool-only mode fallback delivery",
)
base_adapter.write_text(text)

# ── Tool-only mode: force tool_choice=required per user turn ─────────────────
# In HERMES_TOOL_ONLY_MODE, LLMs should start each user turn by calling a tool,
# preferably send_message for visible communication. Force tool_choice=required
# until the current user turn has used a tool; final text remains a fallback.
run_agent = purelib / "run_agent.py"
text = run_agent.read_text()
# Continuing gateway sessions persist a system_prompt snapshot. If the managed
# identity changes, rebuild the prompt instead of reusing a stale Hermes identity.
text = replace_once(
    text,
    '                        stored_prompt = session_row.get("system_prompt") or None\n',
    '                        stored_prompt = session_row.get("system_prompt") or None\n'
    '                        default_identity = os.getenv("HERMES_DEFAULT_AGENT_IDENTITY", "").strip()\n'
    '                        if stored_prompt and default_identity and not stored_prompt.startswith(default_identity):\n'
    '                            logger.info("Refreshing stored system prompt because HERMES_DEFAULT_AGENT_IDENTITY changed")\n'
    '                            stored_prompt = None\n',
    "run_agent stored prompt identity invalidation",
)
text = replace_once(
    text,
    '        api_kwargs = {\n'
    '            "model": self.model,\n'
    '            "messages": sanitized_messages,\n'
    '            "tools": self.tools if self.tools else None,\n'
    '            "timeout": float(os.getenv("HERMES_API_TIMEOUT", 900.0)),\n'
    '        }\n'
    '\n'
    '        if self.max_tokens is not None:\n'
    '            api_kwargs.update(self._max_tokens_param(self.max_tokens))',
    '        api_kwargs = {\n'
    '            "model": self.model,\n'
    '            "messages": sanitized_messages,\n'
    '            "tools": self.tools if self.tools else None,\n'
    '            "timeout": float(os.getenv("HERMES_API_TIMEOUT", 900.0)),\n'
    '        }\n'
    '\n'
    '        # In tool-only mode, force tool_choice=required on the first LLM call\n'
    '        # of each user turn. Previous turns may contain tool_calls, so only\n'
    '        # inspect messages after the latest user message.\n'
    '        if os.getenv("HERMES_TOOL_ONLY_MODE") and api_kwargs.get("tools"):\n'
    '            _last_user_index = max(\n'
    '                (\n'
    '                    _idx\n'
    '                    for _idx, _m in enumerate(sanitized_messages)\n'
    '                    if isinstance(_m, dict) and _m.get("role") == "user"\n'
    '                ),\n'
    '                default=-1,\n'
    '            )\n'
    '            _already_used_tools_this_turn = any(\n'
    '                isinstance(_m, dict)\n'
    '                and _m.get("role") == "assistant"\n'
    '                and _m.get("tool_calls")\n'
    '                for _m in sanitized_messages[_last_user_index + 1 :]\n'
    '            )\n'
    '            if not _already_used_tools_this_turn:\n'
    '                api_kwargs["tool_choice"] = "required"\n'
    '\n'
    '        if self.max_tokens is not None:\n'
    '            api_kwargs.update(self._max_tokens_param(self.max_tokens))',
    "run_agent tool_choice=required per turn in tool-only mode",
)
run_agent.write_text(text)

gateway_run = purelib / "gateway" / "run.py"
text = gateway_run.read_text()
text = replace_once(
    text,
    '    async def _handle_message(self, event: MessageEvent) -> Optional[str]:\n'
    '        """\n',
    '    @staticmethod\n'
    '    def _claw_tool_call_name(tool_call: dict) -> str:\n'
    '        if not isinstance(tool_call, dict):\n'
    '            return ""\n'
    '        function = tool_call.get("function") or {}\n'
    '        if isinstance(function, dict) and function.get("name"):\n'
    '            return str(function.get("name"))\n'
    '        return str(tool_call.get("name") or "")\n'
    '\n'
    '    @staticmethod\n'
    '    def _claw_tool_call_arguments(tool_call: dict) -> dict:\n'
    '        if not isinstance(tool_call, dict):\n'
    '            return {}\n'
    '        function = tool_call.get("function") or {}\n'
    '        raw_args = function.get("arguments") if isinstance(function, dict) else None\n'
    '        if isinstance(raw_args, dict):\n'
    '            return raw_args\n'
    '        if isinstance(raw_args, str) and raw_args.strip():\n'
    '            try:\n'
    '                parsed = json.loads(raw_args)\n'
    '                return parsed if isinstance(parsed, dict) else {}\n'
    '            except Exception:\n'
    '                return {}\n'
    '        return {}\n'
    '\n'
    '    @classmethod\n'
    '    def _claw_turn_sent_message(cls, messages: list) -> bool:\n'
    '        send_call_ids = set()\n'
    '        saw_send_without_id = False\n'
    '        for msg in messages or []:\n'
    '            if not isinstance(msg, dict) or msg.get("role") != "assistant":\n'
    '                continue\n'
    '            for tool_call in msg.get("tool_calls") or []:\n'
    '                if cls._claw_tool_call_name(tool_call) != "send_message":\n'
    '                    continue\n'
    '                args = cls._claw_tool_call_arguments(tool_call)\n'
    '                action = str(args.get("action") or "send").strip().lower()\n'
    '                if action != "send" or not str(args.get("message") or "").strip():\n'
    '                    continue\n'
    '                call_id = tool_call.get("id")\n'
    '                if call_id:\n'
    '                    send_call_ids.add(call_id)\n'
    '                else:\n'
    '                    saw_send_without_id = True\n'
    '\n'
    '        if not send_call_ids:\n'
    '            return saw_send_without_id\n'
    '\n'
    '        saw_result = False\n'
    '        for msg in messages or []:\n'
    '            if not isinstance(msg, dict) or msg.get("role") != "tool":\n'
    '                continue\n'
    '            if msg.get("tool_call_id") not in send_call_ids:\n'
    '                continue\n'
    '            saw_result = True\n'
    '            content = msg.get("content")\n'
    '            if isinstance(content, str):\n'
    '                try:\n'
    '                    content = json.loads(content)\n'
    '                except Exception:\n'
    '                    content = {}\n'
    '            if isinstance(content, dict) and (content.get("success") or content.get("skipped")):\n'
    '                return True\n'
    '        return saw_send_without_id or not saw_result\n'
    '\n'
    '    async def _handle_message(self, event: MessageEvent) -> Optional[str]:\n'
    '        """\n',
    "gateway run send_message detection helpers",
)
text = replace_once(
    text,
    '            response = agent_result.get("final_response") or ""\n'
    '            agent_messages = agent_result.get("messages", [])\n',
    '            response = agent_result.get("final_response") or ""\n'
    '            agent_messages = agent_result.get("messages", [])\n'
    '\n'
    '            if os.getenv("HERMES_TOOL_ONLY_MODE") and response:\n'
    '                history_len_for_delivery = agent_result.get("history_offset", len(history))\n'
    '                turn_messages_for_delivery = (\n'
    '                    agent_messages[history_len_for_delivery:]\n'
    '                    if len(agent_messages) > history_len_for_delivery\n'
    '                    else agent_messages\n'
    '                )\n'
    '                if self._claw_turn_sent_message(turn_messages_for_delivery):\n'
    '                    logger.info("Suppressing duplicate final text after send_message in tool-only mode")\n'
    '                    response = ""\n',
    "gateway run suppress duplicate final text after send_message",
)
gateway_run.write_text(text)
