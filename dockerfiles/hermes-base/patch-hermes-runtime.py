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

# ── Tool-only mode: suppress auto-routing of bare text responses ─────────────
# When HERMES_TOOL_ONLY_MODE is set, agents must use send_message to post
# explicitly. The gateway session becomes private — no text response is
# auto-delivered to the triggering channel.
base_adapter = purelib / "gateway" / "platforms" / "base.py"
text = base_adapter.read_text()
text = replace_once(
    text,
    "                # Send the text portion\n                if text_content:\n",
    "                # Send the text portion — skipped in tool-only mode where\n"
    "                # agents post via send_message rather than auto-routing.\n"
    "                if text_content and not os.getenv(\"HERMES_TOOL_ONLY_MODE\"):\n",
    "base platform tool-only mode gate",
)
base_adapter.write_text(text)

# ── Tool-only mode: force tool_choice=required on first turn ─────────────────
# In HERMES_TOOL_ONLY_MODE, bare text is suppressed (above patch). But LLMs
# default to generating text rather than calling tools. Force tool_choice=required
# on the first LLM call (before any tool results) so the agent MUST call
# send_message (or another tool) to communicate. After a tool executes, the
# LLM is free to produce a final text response (which base.py will suppress).
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
    '        # In tool-only mode, force tool_choice=required only on the very first\n'
    '        # LLM call (before any tool has been called). Once an assistant message\n'
    '        # with tool_calls appears, the agent is already in tool-use mode —\n'
    '        # revert to auto so it can wrap up with a final text response\n'
    '        # (which base.py will suppress before it reaches the channel).\n'
    '        if os.getenv("HERMES_TOOL_ONLY_MODE") and api_kwargs.get("tools"):\n'
    '            _already_used_tools = any(\n'
    '                isinstance(_m, dict)\n'
    '                and _m.get("role") == "assistant"\n'
    '                and _m.get("tool_calls")\n'
    '                for _m in sanitized_messages\n'
    '            )\n'
    '            if not _already_used_tools:\n'
    '                api_kwargs["tool_choice"] = "required"\n'
    '\n'
    '        if self.max_tokens is not None:\n'
    '            api_kwargs.update(self._max_tokens_param(self.max_tokens))',
    "run_agent tool_choice=required for first turn in tool-only mode",
)
run_agent.write_text(text)
