#!/usr/bin/env python3
"""Apply small compatibility fixes to the pinned Hermes install.

Each patch goes through ``replace_once`` so the docker build fails loud when
upstream drift moves a target string. When a patch becomes obsolete because
upstream merged the equivalent fix, delete the patch instead of reshaping it
to match — every line we don't carry is technical debt we don't pay back.
"""

from __future__ import annotations

import pathlib
import shutil
import sysconfig


purelib = pathlib.Path(sysconfig.get_paths()["purelib"])


def replace_once(text: str, old: str, new: str, label: str) -> str:
    if old not in text:
        raise SystemExit(f"expected to patch {label}")
    return text.replace(old, new, 1)


def replace_once_any(text: str, options: list[tuple[str, str]], label: str) -> str:
    for old, new in options:
        if old in text:
            return text.replace(old, new, 1)
    raise SystemExit(f"expected to patch {label}")


def first_existing(*relative_paths: str) -> pathlib.Path:
    for relative_path in relative_paths:
        candidate = purelib / relative_path
        if candidate.exists():
            return candidate
    joined = ", ".join(relative_paths)
    raise SystemExit(f"expected one installed Hermes file to exist: {joined}")

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

# Discord intents: voice_states stays True upstream; we never use it and it
# requires elevated bot privileges. members is conditional upstream now and
# resolves to False for our pods (numeric DISCORD_ALLOWED_USERS, no roles),
# so the previous unconditional False patch is obsolete and dropped.
discord_adapter = first_existing(
    "plugins/platforms/discord/adapter.py",
    "gateway/platforms/discord.py",
)
text = discord_adapter.read_text()
text = replace_once(
    text,
    "            intents.voice_states = True\n",
    "            intents.voice_states = False\n",
    "discord voice intent",
)
discord_adapter.write_text(text)

# Reply-mention auto-pings (replied_user=True default in `_build_allowed_mentions`)
# are now controlled by the env var DISCORD_ALLOW_MENTION_REPLIED_USER. The
# Hermes driver sets it to "false" by default for any Discord-enabled service,
# so the old per-channel.send patches are obsolete and dropped.

# Tool-only mode: prefer send_message without dropping final text.
# HERMES_TOOL_ONLY_MODE makes send_message the preferred visible-delivery path.
# The gateway runner suppresses duplicate final text when send_message already
# succeeded in the current turn; otherwise base delivery remains a fallback so
# plain final answers are not silently lost.
base_adapter = purelib / "gateway" / "platforms" / "base.py"
text = base_adapter.read_text()
text = replace_once_any(
    text,
    [
        (
            "                # Send the text portion\n                if text_content:\n",
            "                # Send the text portion. In HERMES_TOOL_ONLY_MODE, run.py\n"
            "                # clears this text only when the current turn already sent\n"
            "                # a visible message via send_message; otherwise this is the\n"
            "                # fallback that prevents final answers from disappearing.\n"
            "                if text_content:\n",
        ),
        (
            "                # Send the text portion\n                if text_content and not _tts_caption_delivered:\n",
            "                # Send the text portion. In HERMES_TOOL_ONLY_MODE, run.py\n"
            "                # clears this text only when the current turn already sent\n"
            "                # a visible message via send_message; otherwise this is the\n"
            "                # fallback that prevents final answers from disappearing.\n"
            "                if text_content and not _tts_caption_delivered:\n",
        ),
    ],
    "base platform tool-only mode fallback delivery",
)
base_adapter.write_text(text)

# Core tool suppression: hide disabled runtime tools from model manifests.
# Hermes exposes text_to_speech in its Discord core toolset when edge_tts is
# importable. Clawdapus keeps the tool registered but strips it from platform
# toolsets when CLAWDAPUS_DISABLED_TOOLS names it.
toolsets_py = purelib / "toolsets.py"
text = toolsets_py.read_text()
text = replace_once(
    text,
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
text = replace_once(
    text,
    "    \"computer_use\",\n]\n",
    "    \"computer_use\",\n]\n_HERMES_CORE_TOOLS = _claw_filter_tools(_HERMES_CORE_TOOLS)\n",
    "_HERMES_CORE_TOOLS env-driven disable filter application",
)
text = replace_once(
    text,
    "TOOLSETS = {",
    "TOOLSETS = {  # filtered post-build via _claw_filter_tools below",
    "TOOLSETS comment marker",
)
text += (
    "\n# Apply env-driven tool filter to every named bundle's explicit tools[]\n"
    "# (does not touch includes; nested toolset resolution still works).\n"
    "for _ts_name, _ts_def in list(TOOLSETS.items()):\n"
    "    if isinstance(_ts_def, dict) and \"tools\" in _ts_def and isinstance(_ts_def[\"tools\"], list):\n"
    "        _ts_def[\"tools\"] = _claw_filter_tools(_ts_def[\"tools\"])\n"
)
toolsets_py.write_text(text)

config_py = purelib / "hermes_cli" / "config.py"
text = config_py.read_text()
text = replace_once(
    text,
    '        "memory_char_limit": 2200,   # ~800 tokens at 2.75 chars/token\n'
    '        "user_char_limit": 1375,     # ~500 tokens at 2.75 chars/token\n',
    '        "memory_char_limit": 12000,  # Clawdapus default; override with HERMES_MEMORY_INDEX_MAX_CHARS\n'
    '        "user_char_limit": 6000,     # Clawdapus default; override with HERMES_USER_MEMORY_MAX_CHARS\n',
    "Hermes memory default limits",
)
config_py.write_text(text)

memory_tool = purelib / "tools" / "memory_tool.py"
text = memory_tool.read_text()
text = replace_once_any(
    text,
    [
        (
            "import json\nimport logging\nimport os\nimport re\nimport tempfile\n",
            "import difflib\nimport json\nimport logging\nimport os\nimport re\nimport tempfile\n",
        ),
        (
            "import json\nimport logging\nimport os\nimport tempfile\n",
            "import difflib\nimport json\nimport logging\nimport os\nimport re\nimport tempfile\n",
        ),
    ],
    "Hermes memory fuzzy-match import",
)
text = replace_once(
    text,
    'ENTRY_DELIMITER = "\\n§\\n"\n',
    '''ENTRY_DELIMITER = "\\n§\\n"


def _normalize_match_text(value: str) -> str:
    """Normalize memory match text for tolerant substring lookup."""
    return " ".join(re.sub(r"\\W+", " ", str(value or "").casefold()).split())


def _entry_matches(entry: str, old_text: str) -> bool:
    """Return true when old_text identifies entry exactly or after light normalization."""
    if old_text in entry:
        return True
    needle = _normalize_match_text(old_text)
    if not needle:
        return False
    return needle in _normalize_match_text(entry)


def _memory_match_previews(entries: List[str], old_text: str, limit: int = 3) -> List[str]:
    """Return likely entries for a missed locator so the next call can recover."""
    needle = _normalize_match_text(old_text)
    if not needle:
        return []
    scored = []
    for entry in entries:
        normalized_entry = _normalize_match_text(entry)
        if not normalized_entry:
            continue
        score = difflib.SequenceMatcher(None, needle, normalized_entry).ratio()
        if score >= 0.28:
            scored.append((score, entry))
    scored.sort(key=lambda item: item[0], reverse=True)
    return [
        entry[:160] + ("..." if len(entry) > 160 else "")
        for _, entry in scored[:limit]
    ]


def _memory_match_hint(entries: List[str], old_text: str) -> str:
    close_matches = _memory_match_previews(entries, old_text)
    if not close_matches:
        return ""
    return " Close matches: " + " | ".join(close_matches)
''',
    "Hermes memory tolerant match helpers",
)
memory_add_eviction = '''            # Calculate what the new total would be. If the new entry can
            # fit by evicting older entries, evict oldest-first instead of
            # freezing memory writes at the cap.
            new_entries = entries + [content]
            new_total = len(ENTRY_DELIMITER.join(new_entries))
            evicted_entries = []

            if new_total > limit and len(content) > limit:
                current = self._char_count(target)
                return {
                    "success": False,
                    "error": (
                        f"Memory at {current:,}/{limit:,} chars. "
                        f"This entry is {len(content):,} chars and cannot fit within the limit. "
                        f"Shorten the entry before saving it."
                    ),
                    "current_entries": entries,
                    "usage": f"{current:,}/{limit:,}",
                }

            while new_total > limit and entries:
                evicted_entries.append(entries.pop(0))
                new_entries = entries + [content]
                new_total = len(ENTRY_DELIMITER.join(new_entries))

            if new_total > limit:
                current = self._char_count(target)
                return {
                    "success": False,
                    "error": (
                        f"Memory at {current:,}/{limit:,} chars. "
                        f"Adding this entry ({len(content)} chars) would exceed the limit. "
                        f"Shorten the entry before saving it."
                    ),
                    "current_entries": entries,
                    "usage": f"{current:,}/{limit:,}",
                }

            entries.append(content)
            self._set_entries(target, entries)
            self.save_to_disk(target)

        response = self._success_response(target, "Entry added.")
        if evicted_entries:
            response["evicted_count"] = len(evicted_entries)
            response["evicted_entries"] = evicted_entries
            response["message"] = (
                f"Entry added. Evicted {len(evicted_entries)} oldest "
                f"{'entry' if len(evicted_entries) == 1 else 'entries'} to stay within the memory limit."
            )
        return response'''
text = replace_once_any(
    text,
    [
        ('''            # Calculate what the new total would be
            new_entries = entries + [content]
            new_total = len(ENTRY_DELIMITER.join(new_entries))

            if new_total > limit:
                current = self._char_count(target)
                return {
                    "success": False,
                    "error": (
                        f"Memory at {current:,}/{limit:,} chars. "
                        f"Adding this entry ({len(content)} chars) would exceed the limit. "
                        f"Replace or remove existing entries first."
                    ),
                    "current_entries": entries,
                    "usage": f"{current:,}/{limit:,}",
                }

            entries.append(content)
            self._set_entries(target, entries)
            self.save_to_disk(target)

        return self._success_response(target, "Entry added.")''', memory_add_eviction),
        ('''            # Calculate what the new total would be
            new_entries = entries + [content]
            new_total = len(ENTRY_DELIMITER.join(new_entries))

            if new_total > limit:
                current = self._char_count(target)
                return {
                    "success": False,
                    "error": (
                        f"Memory at {current:,}/{limit:,} chars. "
                        f"Adding this entry ({len(content)} chars) would exceed the limit. "
                        f"Consolidate now: use 'replace' to merge overlapping entries into "
                        f"shorter ones or 'remove' stale or less important entries (see "
                        f"current_entries below), then retry this add — all in this turn."
                    ),
                    "current_entries": entries,
                    "usage": f"{current:,}/{limit:,}",
                }

            entries.append(content)
            self._set_entries(target, entries)
            self.save_to_disk(target)

        return self._success_response(target, "Entry added.")''', memory_add_eviction),
    ],
    "Hermes memory add oldest-entry eviction",
)
text = replace_once(
    text,
    '''            matches = [(i, e) for i, e in enumerate(entries) if old_text in e]

            if not matches:
                return {"success": False, "error": f"No entry matched '{old_text}'."}
''',
    '''            matches = [(i, e) for i, e in enumerate(entries) if _entry_matches(e, old_text)]

            if not matches:
                close_matches = _memory_match_previews(entries, old_text)
                response = {
                    "success": False,
                    "error": (
                        f"No entry matched '{old_text}'. "
                        "Use an exact substring from current_entries or one of close_matches."
                    ),
                    "current_entries": entries,
                }
                if close_matches:
                    response["close_matches"] = close_matches
                return response
''',
    "Hermes memory replace tolerant miss response",
)
text = replace_once(
    text,
    '''            matches = [(i, e) for i, e in enumerate(entries) if old_text in e]

            if not matches:
                return {"success": False, "error": f"No entry matched '{old_text}'."}
''',
    '''            matches = [(i, e) for i, e in enumerate(entries) if _entry_matches(e, old_text)]

            if not matches:
                close_matches = _memory_match_previews(entries, old_text)
                response = {
                    "success": False,
                    "error": (
                        f"No entry matched '{old_text}'. "
                        "Use an exact substring from current_entries or one of close_matches."
                    ),
                    "current_entries": entries,
                }
                if close_matches:
                    response["close_matches"] = close_matches
                return response
''',
    "Hermes memory remove tolerant miss response",
)
text = replace_once(
    text,
    '''                    matches = [j for j, e in enumerate(working) if old_text in e]
                    if not matches:
                        return self._batch_error(target, f"{pos}: no entry matched '{old_text}'.")
''',
    '''                    matches = [j for j, e in enumerate(working) if _entry_matches(e, old_text)]
                    if not matches:
                        return self._batch_error(
                            target,
                            f"{pos}: no entry matched '{old_text}'." + _memory_match_hint(working, old_text),
                        )
''',
    "Hermes memory batch replace tolerant miss response",
)
text = replace_once(
    text,
    '''                    matches = [j for j, e in enumerate(working) if old_text in e]
                    if not matches:
                        return self._batch_error(target, f"{pos}: no entry matched '{old_text}'.")
''',
    '''                    matches = [j for j, e in enumerate(working) if _entry_matches(e, old_text)]
                    if not matches:
                        return self._batch_error(
                            target,
                            f"{pos}: no entry matched '{old_text}'." + _memory_match_hint(working, old_text),
                        )
''',
    "Hermes memory batch remove tolerant miss response",
)
text = replace_once_any(
    text,
    [
        ('''        if not content:
            return tool_error("content is required for 'replace' action.", success=False)
''', '''        if not content:
            return tool_error(
                "content is required for 'replace' action. Include full replacement content "
                "and old_text in the same call; use 'remove' to delete an entry.",
                success=False,
            )
'''),
        ('''    if action == "replace" and (not old_text or not content):
        missing = "old_text" if not old_text else "content"
        return tool_error(f"{missing} is required for 'replace' action.", success=False)
''', '''    if action == "replace" and (not old_text or not content):
        missing = "old_text" if not old_text else "content"
        if missing == "content":
            return tool_error(
                "content is required for 'replace' action. Include full replacement content "
                "and old_text in the same call; use 'remove' to delete an entry.",
                success=False,
            )
        return tool_error(f"{missing} is required for 'replace' action.", success=False)
'''),
    ],
    "Hermes memory replace missing-content guidance",
)
text = replace_once_any(
    text,
    [
        ('''            "content": {
                "type": "string",
                "description": "The entry content. Required for 'add' and 'replace'."
            },
            "old_text": {
                "type": "string",
                "description": "Short unique substring identifying the entry to replace or remove."
            },
''', '''            "content": {
                "type": "string",
                "description": "The entry content. Required for 'add' and 'replace'; for replace, send the full replacement entry in the same call."
            },
            "old_text": {
                "type": "string",
                "description": "Short unique substring identifying the entry to replace or remove. Matching tolerates case, whitespace, and punctuation."
            },
'''),
        ('''            "content": {
                "type": "string",
                "description": "The entry content. Required for 'add' and 'replace' (single-op shape)."
            },
            "old_text": {
                "type": "string",
                "description": "Short unique substring identifying the entry to replace or remove (single-op shape)."
            },
''', '''            "content": {
                "type": "string",
                "description": "The entry content. Required for 'add' and 'replace'; for replace, send the full replacement entry in the same call."
            },
            "old_text": {
                "type": "string",
                "description": "Short unique substring identifying the entry to replace or remove. Matching tolerates case, whitespace, and punctuation."
            },
'''),
    ],
    "Hermes memory replace schema guidance",
)
text = replace_once(
    text,
    '''                atomic_replace(tmp_path, path)
''',
    '''                real_path = atomic_replace(tmp_path, path)
                os.chmod(real_path, 0o666)
''',
    "Hermes memory file post-rewrite mode",
)
memory_tool.write_text(text)

skill_manager_tool = purelib / "tools" / "skill_manager_tool.py"
text = skill_manager_tool.read_text()
# Keep patch literals free of trailing whitespace while still matching Hermes
# source that carries indented blank lines inside docstrings.
text = text.replace("    \n", "\n")
text = replace_once(
    text,
    "import json\nimport logging\nimport os\nimport re\nimport shutil\nimport tempfile\n",
    "import errno\nimport json\nimport logging\nimport os\nimport re\nimport shutil\nimport tempfile\nimport time\n",
    "Hermes skill_manage retry imports",
)
text = replace_once(
    text,
    '''def _validate_file_path(file_path: str) -> Optional[str]:
    """
    Validate a file path for write_file/remove_file.
    Must be under an allowed subdirectory and not escape the skill dir.
    """
    from tools.path_security import has_traversal_component

    if not file_path:
        return "file_path is required."

    normalized = Path(file_path)

    # Prevent path traversal (checked before any allow-listing so the SKILL.md
    # exception below can never be reached by a traversal-laden path).
    if has_traversal_component(file_path):
        return "Path traversal ('..') is not allowed."

    # SKILL.md is the canonical skill file and lives at the skill root, not
    # under an allowed subdirectory. Accept its two natural spellings —
    # 'SKILL.md' and '<skill-name>/SKILL.md' — so callers can target the main
    # file. The traversal guard above still applies, so this can't escape.
    if normalized.parts and normalized.name == "SKILL.md":
        if len(normalized.parts) == 1 or len(normalized.parts) == 2:
            return None

    # Must be under an allowed subdirectory
    if not normalized.parts or normalized.parts[0] not in ALLOWED_SUBDIRS:
        allowed = ", ".join(sorted(ALLOWED_SUBDIRS))
        return f"File must be under one of: {allowed}. Got: '{file_path}'"

    # Must have a filename (not just a directory)
    if len(normalized.parts) < 2:
        return f"Provide a file path, not just a directory. Example: '{normalized.parts[0]}/myfile.md'"

    return None
''',
    '''def _validate_file_path(file_path: str, skill_dir: Path | None = None) -> Optional[str]:
    """
    Validate a file path for write_file/remove_file.
    Must be under an allowed subdirectory and not escape the skill dir.
    """
    from tools.path_security import has_traversal_component

    if not file_path:
        return "file_path is required."

    normalized = Path(file_path)

    if normalized.is_absolute():
        if skill_dir is None:
            return "Absolute paths require a known skill directory."
        try:
            normalized = normalized.resolve().relative_to(skill_dir.resolve())
        except ValueError:
            return f"Absolute path must stay within skill directory '{skill_dir}'. Got: '{file_path}'"

    # Prevent path traversal (checked before any allow-listing so the SKILL.md
    # exception below can never be reached by a traversal-laden path).
    if has_traversal_component(file_path):
        return "Path traversal ('..') is not allowed."

    # SKILL.md is the canonical skill file and lives at the skill root, not
    # under an allowed subdirectory. Accept its two natural spellings —
    # 'SKILL.md' and '<skill-name>/SKILL.md' — so callers can target the main
    # file. The traversal guard above still applies, so this can't escape.
    if normalized.parts and normalized.name == "SKILL.md":
        if len(normalized.parts) == 1 or len(normalized.parts) == 2:
            return None

    # Must be under an allowed subdirectory
    if not normalized.parts or normalized.parts[0] not in ALLOWED_SUBDIRS:
        allowed = ", ".join(sorted(ALLOWED_SUBDIRS))
        return f"File must be under one of: {allowed}. Got: '{file_path}'"

    # Must have a filename (not just a directory)
    if len(normalized.parts) < 2:
        return f"Provide a file path, not just a directory. Example: '{normalized.parts[0]}/myfile.md'"

    return None
''',
    "Hermes skill_manage absolute supporting-file validation",
)
text = replace_once(
    text,
    '''def _atomic_write_text(file_path: Path, content: str, encoding: str = "utf-8") -> None:
    """
    Atomically write text content to a file.

    Uses a temporary file in the same directory and os.replace() to ensure
    the target file is never left in a partially-written state if the process
    crashes or is interrupted.

    Args:
        file_path: Target file path
        content: Content to write
        encoding: Text encoding (default: utf-8)
    """
    file_path.parent.mkdir(parents=True, exist_ok=True)
    fd, temp_path = tempfile.mkstemp(
        dir=str(file_path.parent),
        prefix=f".{file_path.name}.tmp.",
        suffix="",
    )
    try:
        with os.fdopen(fd, "w", encoding=encoding) as f:
            f.write(content)
        atomic_replace(temp_path, file_path)
    except Exception:
        # Clean up temp file on error
        try:
            os.unlink(temp_path)
        except OSError:
            logger.error("Failed to remove temporary file %s during atomic write", temp_path, exc_info=True)
        raise
''',
    '''def _claw_path_is_mountpoint(path: Path) -> bool:
    """Return True when /proc/self/mountinfo identifies path as a mount target."""
    try:
        target = str(path.resolve())
        with open("/proc/self/mountinfo", encoding="utf-8") as f:
            for line in f:
                fields = line.split()
                if len(fields) > 4 and fields[4].replace("\\\\040", " ") == target:
                    return True
    except Exception:
        return False
    return False


def _atomic_write_text(file_path: Path, content: str, encoding: str = "utf-8") -> None:
    """
    Atomically write text content to a file.

    Uses a temporary file in the same directory and os.replace() to ensure
    the target file is never left in a partially-written state if the process
    crashes or is interrupted. Overlayfs can transiently return EBUSY while
    readers hold the target open, so retry before falling back to in-place
    replacement for ordinary files. File-level mount targets stay protected:
    those are managed outside the agent's writable skill workspace.

    Args:
        file_path: Target file path
        content: Content to write
        encoding: Text encoding (default: utf-8)
    """
    file_path.parent.mkdir(parents=True, exist_ok=True)
    fd, temp_path = tempfile.mkstemp(
        dir=str(file_path.parent),
        prefix=f".{file_path.name}.tmp.",
        suffix="",
    )
    try:
        with os.fdopen(fd, "w", encoding=encoding) as f:
            f.write(content)

        last_busy = None
        for attempt in range(5):
            try:
                atomic_replace(temp_path, file_path)
                return
            except OSError as exc:
                if exc.errno != errno.EBUSY:
                    raise
                last_busy = exc
                if _claw_path_is_mountpoint(file_path):
                    raise OSError(
                        errno.EBUSY,
                        (
                            f"Cannot replace mounted skill file '{file_path}'. "
                            "This skill is managed outside the agent workspace; "
                            "report the desired edit instead."
                        ),
                    ) from exc
                time.sleep(0.05 * (attempt + 1))

        logger.warning(
            "skill_manage: atomic_replace stayed busy for %s; falling back to in-place write",
            file_path,
        )
        with open(file_path, "w", encoding=encoding) as f:
            f.write(content)
        os.unlink(temp_path)
        if last_busy is not None:
            logger.debug("skill_manage: recovered from EBUSY while writing %s", file_path)
    except Exception:
        # Clean up temp file on error
        try:
            os.unlink(temp_path)
        except OSError:
            logger.error("Failed to remove temporary file %s during atomic write", temp_path, exc_info=True)
        raise
''',
    "Hermes skill_manage EBUSY retry and fallback write",
)
text = replace_once(
    text,
    '''        err = _validate_file_path(file_path)
        if err:
            return {"success": False, "error": err}
        target, err = _resolve_skill_target(skill_dir, file_path)
        if err:
            return {"success": False, "error": err}
''',
    '''        err = _validate_file_path(file_path, skill_dir)
        if err:
            return {"success": False, "error": err}
        target, err = _resolve_skill_target(skill_dir, file_path)
        if err:
            return {"success": False, "error": err}
''',
    "Hermes skill_manage patch absolute file_path validation",
)
text = replace_once(
    text,
    '''def _write_file(name: str, file_path: str, file_content: str) -> Dict[str, Any]:
    """Add or overwrite a supporting file within any skill directory."""
    err = _validate_file_path(file_path)
    if err:
        return {"success": False, "error": err}

    if not file_content and file_content != "":
        return {"success": False, "error": "file_content is required."}

    # Check size limits
    content_bytes = len(file_content.encode("utf-8"))
    if content_bytes > MAX_SKILL_FILE_BYTES:
        return {
            "success": False,
            "error": (
                f"File content is {content_bytes:,} bytes "
                f"(limit: {MAX_SKILL_FILE_BYTES:,} bytes / 1 MiB). "
                f"Consider splitting into smaller files."
            ),
        }
    err = _validate_content_size(file_content, label=file_path)
    if err:
        return {"success": False, "error": err}

    existing = _find_skill(name)
    if not existing:
        return {"success": False, "error": _skill_not_found_error(name, " Create it first with action='create'.")}

    target, err = _resolve_skill_target(existing["path"], file_path)
    if err:
        return {"success": False, "error": err}
''',
    '''def _write_file(name: str, file_path: str, file_content: str) -> Dict[str, Any]:
    """Add or overwrite a supporting file within any skill directory."""
    if not file_content and file_content != "":
        return {"success": False, "error": "file_content is required."}

    # Check size limits
    content_bytes = len(file_content.encode("utf-8"))
    if content_bytes > MAX_SKILL_FILE_BYTES:
        return {
            "success": False,
            "error": (
                f"File content is {content_bytes:,} bytes "
                f"(limit: {MAX_SKILL_FILE_BYTES:,} bytes / 1 MiB). "
                f"Consider splitting into smaller files."
            ),
        }
    err = _validate_content_size(file_content, label=file_path)
    if err:
        return {"success": False, "error": err}

    existing = _find_skill(name)
    if not existing:
        return {"success": False, "error": _skill_not_found_error(name, " Create it first with action='create'.")}

    err = _validate_file_path(file_path, existing["path"])
    if err:
        return {"success": False, "error": err}

    target, err = _resolve_skill_target(existing["path"], file_path)
    if err:
        return {"success": False, "error": err}
''',
    "Hermes skill_manage write_file absolute path validation",
)
text = replace_once(
    text,
    '''def _remove_file(name: str, file_path: str) -> Dict[str, Any]:
    """Remove a supporting file from any skill directory."""
    err = _validate_file_path(file_path)
    if err:
        return {"success": False, "error": err}

    existing = _find_skill(name)
    if not existing:
        return {"success": False, "error": _skill_not_found_error(name)}

    skill_dir = existing["path"]

    target, err = _resolve_skill_target(skill_dir, file_path)
    if err:
        return {"success": False, "error": err}
''',
    '''def _remove_file(name: str, file_path: str) -> Dict[str, Any]:
    """Remove a supporting file from any skill directory."""
    existing = _find_skill(name)
    if not existing:
        return {"success": False, "error": _skill_not_found_error(name)}
    skill_dir = existing["path"]

    err = _validate_file_path(file_path, skill_dir)
    if err:
        return {"success": False, "error": err}

    target, err = _resolve_skill_target(skill_dir, file_path)
    if err:
        return {"success": False, "error": err}
''',
    "Hermes skill_manage remove_file absolute path validation",
)
skill_manager_tool.write_text(text)

cron_scheduler = purelib / "cron" / "scheduler.py"
text = cron_scheduler.read_text()

# Cron jobs initialize Hermes with skip_memory=True so their system prompts do
# not mutate user representations. Keep the advertised tools consistent with
# that runtime capability while leaving native memory available to interactive
# agents.
text = replace_once(
    text,
    "    Three protected toolsets are always disabled in cron context:\n"
    "      - ``cronjob`` — would let a cron-spawned agent schedule more cron jobs\n"
    "      - ``messaging`` — interactive, needs a live gateway session\n"
    "      - ``clarify`` — interactive, blocks waiting for user input\n",
    "    Four protected toolsets are always disabled in cron context:\n"
    "      - ``cronjob`` — would let a cron-spawned agent schedule more cron jobs\n"
    "      - ``messaging`` — interactive, needs a live gateway session\n"
    "      - ``clarify`` — interactive, blocks waiting for user input\n"
    "      - ``memory`` — unavailable because cron agents use ``skip_memory=True``\n",
    "cron scheduler protected toolsets documentation",
)
text = replace_once(
    text,
    '    disabled = ["cronjob", "messaging", "clarify"]\n',
    '    disabled = ["cronjob", "messaging", "clarify", "memory"]\n',
    "cron scheduler memory tool suppression",
)

# Cron transient-failure delivery: provider/cllama outages are already logged
# and recorded on the job run. They should not be posted into user channels as
# actionable cron responses, while real operator-actionable cron failures still
# surface normally.
text = replace_once(
    text,
    "def _deliver_result(job: dict, content: str, adapters=None, loop=None) -> Optional[str]:\n",
    "def _claw_should_deliver_cron_failure(error: str | None) -> bool:\n"
    "    if os.getenv(\"HERMES_CRON_DELIVER_TRANSIENT_FAILURES\") == \"1\":\n"
    "        return True\n"
    "    text = str(error or \"\").lower()\n"
    "    if not text:\n"
    "        return True\n"
    "    transient_markers = (\n"
    "        \"upstream request failed\",\n"
    "        \"internal server error\",\n"
    "        \"bad gateway\",\n"
    "        \"service unavailable\",\n"
    "        \"gateway timeout\",\n"
    "        \"temporarily unavailable\",\n"
    "        \"rate limit\",\n"
    "        \"rate_limit\",\n"
    "        \"timeout\",\n"
    "        \"timed out\",\n"
    "        \"connection reset\",\n"
    "        \"connection aborted\",\n"
    "        \"econnreset\",\n"
    "        \"429\",\n"
    "        \"502\",\n"
    "        \"503\",\n"
    "        \"504\",\n"
    "    )\n"
    "    return not any(marker in text for marker in transient_markers)\n"
    "\n"
    "\n"
    "def _deliver_result(job: dict, content: str, adapters=None, loop=None) -> Optional[str]:\n",
    "cron scheduler transient failure delivery classifier",
)
cron_suppress_old = (
    "                # Deliver the final response to the origin/target chat.\n"
    "                # If the agent responded with [SILENT], skip delivery (but\n"
    "                # output is already saved above).  Failed jobs deliver unless\n"
    "                # the failure is a transient provider/cllama outage; those are\n"
    "                # recorded in job state/logs without becoming channel noise.\n"
    "                if success:\n"
    "                    deliver_content = final_response\n"
    "                elif _claw_should_deliver_cron_failure(error):\n"
    "                    deliver_content = f\"⚠️ Cron job '{job.get('name', job['id'])}' failed:\\n{error}\"\n"
    "                else:\n"
    "                    logger.warning(\"Job '%s': suppressing transient cron failure delivery: %s\", job[\"id\"], error)\n"
    "                    deliver_content = None\n"
    "                should_deliver = bool(deliver_content)\n"
)
cron_suppress_current = (
    "        # Deliver the final response to the origin/target chat.\n"
    "        # If the agent responded with [SILENT], skip delivery (but\n"
    "        # output is already saved above).  Failed jobs deliver unless\n"
    "        # the failure is a transient provider/cllama outage; those are\n"
    "        # recorded in job state/logs without becoming channel noise.\n"
    "        if success:\n"
    "            deliver_content = final_response\n"
    "        elif _claw_should_deliver_cron_failure(error):\n"
    "            deliver_content = _summarize_cron_failure_for_delivery(job, error)\n"
    "        else:\n"
    "            logger.warning(\"Job '%s': suppressing transient cron failure delivery: %s\", job[\"id\"], error)\n"
    "            deliver_content = \"\"\n"
)
text = replace_once_any(
    text,
    [
        (
            "                # Deliver the final response to the origin/target chat.\n"
            "                # If the agent responded with [SILENT], skip delivery (but\n"
            "                # output is already saved above).  Failed jobs always deliver.\n"
            "                deliver_content = final_response if success else f\"⚠️ Cron job '{job.get('name', job['id'])}' failed:\\n{error}\"\n"
            "                should_deliver = bool(deliver_content)\n",
            cron_suppress_old,
        ),
        (
            "        # Deliver the final response to the origin/target chat.\n"
            "        # If the agent responded with [SILENT], skip delivery (but\n"
            "        # output is already saved above).  Failed jobs always deliver.\n"
            "        deliver_content = final_response if success else _summarize_cron_failure_for_delivery(job, error)\n",
            cron_suppress_current,
        ),
    ],
    "cron scheduler suppress transient failure delivery",
)
cron_scheduler.write_text(text)

# cllama consumer session epoch: when Hermes is routed through the in-pod
# cllama OpenAI-compatible base URL, attach the process-stable restart epoch
# generated by entrypoint.sh. Keep this at the Hermes client factory layer so
# provider-specific transport kwargs keep working unchanged.
runtime_helpers = first_existing("agent/agent_runtime_helpers.py", "run_agent.py")
text = runtime_helpers.read_text()
if "import os\n" not in text:
    text = replace_once(
        text,
        "import logging\n",
        "import logging\nimport os\n",
        "run_agent runtime helpers os import",
    )
text = replace_once_any(
    text,
    [
        (
            "        client_kwargs = dict(client_kwargs)\n"
            "        _validate_proxy_env_urls()\n"
            "        _validate_base_url(client_kwargs.get(\"base_url\"))\n",
            "        client_kwargs = dict(client_kwargs)\n"
            "        _claw_epoch = os.getenv(\"CLLAMA_CONSUMER_SESSION_EPOCH\", \"\").strip()\n"
            "        _claw_base_host = base_url_hostname(str(client_kwargs.get(\"base_url\", \"\") or \"\"))\n"
            "        if _claw_epoch and (_claw_base_host == \"cllama\" or _claw_base_host.startswith(\"cllama-\")):\n"
            "            _claw_headers = dict(client_kwargs.get(\"default_headers\") or {})\n"
            "            _claw_headers[\"X-Claw-Consumer-Session-Epoch\"] = _claw_epoch\n"
            "            client_kwargs[\"default_headers\"] = _claw_headers\n"
            "        _validate_proxy_env_urls()\n"
            "        _validate_base_url(client_kwargs.get(\"base_url\"))\n",
        ),
        (
            "    client_kwargs = dict(client_kwargs)\n"
            "    _validate_proxy_env_urls()\n"
            "    _validate_base_url(client_kwargs.get(\"base_url\"))\n",
            "    client_kwargs = dict(client_kwargs)\n"
            "    _claw_epoch = os.getenv(\"CLLAMA_CONSUMER_SESSION_EPOCH\", \"\").strip()\n"
            "    _claw_base_host = base_url_hostname(str(client_kwargs.get(\"base_url\", \"\") or \"\"))\n"
            "    if _claw_epoch and (_claw_base_host == \"cllama\" or _claw_base_host.startswith(\"cllama-\")):\n"
            "        _claw_headers = dict(client_kwargs.get(\"default_headers\") or {})\n"
            "        _claw_headers[\"X-Claw-Consumer-Session-Epoch\"] = _claw_epoch\n"
            "        client_kwargs[\"default_headers\"] = _claw_headers\n"
            "    _validate_proxy_env_urls()\n"
            "    _validate_base_url(client_kwargs.get(\"base_url\"))\n",
        ),
    ],
    "run_agent cllama consumer session epoch header",
)
runtime_helpers.write_text(text)

agent_init = first_existing("agent/agent_init.py", "run_agent.py")
text = agent_init.read_text()
memory_limit_patch_self = (
    "                    def _claw_memory_limit(env_name: str, configured, fallback: int) -> int:\n"
    "                        raw = os.getenv(env_name, \"\").strip()\n"
    "                        value = raw if raw else configured\n"
    "                        try:\n"
    "                            parsed = int(value)\n"
    "                        except (TypeError, ValueError):\n"
    "                            parsed = fallback\n"
    "                        return parsed if parsed > 0 else fallback\n"
    "\n"
    "                    self._memory_store = MemoryStore(\n"
    "                        memory_char_limit=_claw_memory_limit(\n"
    "                            \"HERMES_MEMORY_INDEX_MAX_CHARS\",\n"
    "                            mem_config.get(\"memory_char_limit\", 12000),\n"
    "                            12000,\n"
    "                        ),\n"
    "                        user_char_limit=_claw_memory_limit(\n"
    "                            \"HERMES_USER_MEMORY_MAX_CHARS\",\n"
    "                            mem_config.get(\"user_char_limit\", 6000),\n"
    "                            6000,\n"
    "                        ),\n"
    "                    )\n"
)
memory_limit_patch_agent = (
    "                def _claw_memory_limit(env_name: str, configured, fallback: int) -> int:\n"
    "                    raw = os.getenv(env_name, \"\").strip()\n"
    "                    value = raw if raw else configured\n"
    "                    try:\n"
    "                        parsed = int(value)\n"
    "                    except (TypeError, ValueError):\n"
    "                        parsed = fallback\n"
    "                    return parsed if parsed > 0 else fallback\n"
    "\n"
    "                agent._memory_store = MemoryStore(\n"
    "                    memory_char_limit=_claw_memory_limit(\n"
    "                        \"HERMES_MEMORY_INDEX_MAX_CHARS\",\n"
    "                        mem_config.get(\"memory_char_limit\", 12000),\n"
    "                        12000,\n"
    "                    ),\n"
    "                    user_char_limit=_claw_memory_limit(\n"
    "                        \"HERMES_USER_MEMORY_MAX_CHARS\",\n"
    "                        mem_config.get(\"user_char_limit\", 6000),\n"
    "                        6000,\n"
    "                    ),\n"
    "                )\n"
)
text = replace_once_any(
    text,
    [
        (
            "                    self._memory_store = MemoryStore(\n"
            "                        memory_char_limit=mem_config.get(\"memory_char_limit\", 2200),\n"
            "                        user_char_limit=mem_config.get(\"user_char_limit\", 1375),\n"
            "                    )\n",
            memory_limit_patch_self,
        ),
        (
            "                agent._memory_store = MemoryStore(\n"
            "                    memory_char_limit=mem_config.get(\"memory_char_limit\", 2200),\n"
            "                    user_char_limit=mem_config.get(\"user_char_limit\", 1375),\n"
            "                )\n",
            memory_limit_patch_agent,
        ),
    ],
    "run_agent Hermes memory env-configurable limits",
)
agent_init.write_text(text)

# Continuing gateway sessions persist a system_prompt snapshot. If the managed
# identity changes, rebuild the prompt instead of reusing a stale Hermes identity.
conversation_loop = first_existing("agent/conversation_loop.py", "run_agent.py")
text = conversation_loop.read_text()
text = replace_once_any(
    text,
    [
        (
            '                        stored_prompt = session_row.get("system_prompt") or None\n',
            '                        stored_prompt = session_row.get("system_prompt") or None\n'
            '                        default_identity = os.getenv("HERMES_DEFAULT_AGENT_IDENTITY", "").strip()\n'
            '                        if stored_prompt and default_identity and not stored_prompt.startswith(default_identity):\n'
            '                            logger.info("Refreshing stored system prompt because HERMES_DEFAULT_AGENT_IDENTITY changed")\n'
            '                            stored_prompt = None\n',
        ),
        (
            '                    stored_prompt = raw_prompt\n'
            '                    stored_state = "present"\n',
            '                    stored_prompt = raw_prompt\n'
            '                    default_identity = os.getenv("HERMES_DEFAULT_AGENT_IDENTITY", "").strip()\n'
            '                    if stored_prompt and default_identity and not stored_prompt.startswith(default_identity):\n'
            '                        logger.info("Refreshing stored system prompt because HERMES_DEFAULT_AGENT_IDENTITY changed")\n'
            '                        stored_prompt = None\n'
            '                        stored_state = "stale_identity"\n'
            '                    else:\n'
            '                        stored_state = "present"\n',
        ),
    ],
    "run_agent stored prompt identity invalidation",
)
conversation_loop.write_text(text)

# Tool-only mode: force tool_choice=required per user turn.
# Upstream now has a provider-profile path and a legacy fallback inside
# `build_api_kwargs`. Capture the chat-completions kwargs in both paths and
# inject `tool_choice="required"` at the start of each user turn so the model
# must call a tool (preferably send_message) before falling back to plain text.
chat_completion_helpers = first_existing("agent/chat_completion_helpers.py", "run_agent.py")
text = chat_completion_helpers.read_text()
text = replace_once(
    text,
    "        return _ct.build_kwargs(\n"
    "            model=agent.model,\n"
    "            messages=api_messages,\n"
    "            tools=tools_for_api,\n"
    "            base_url=agent.base_url,\n",
    "        _claw_kwargs = _ct.build_kwargs(\n"
    "            model=agent.model,\n"
    "            messages=api_messages,\n"
    "            tools=tools_for_api,\n"
    "            base_url=agent.base_url,\n",
    "run_agent provider-profile _build_api_kwargs capture for tool-only mode",
)
text = replace_once(
    text,
    "            qwen_session_metadata=_qwen_meta,\n"
    "        )\n"
    "\n"
    "    # ── Legacy flag path",
    "            qwen_session_metadata=_qwen_meta,\n"
    "        )\n"
    "        return _claw_apply_tool_only_choice(agent, _claw_kwargs, api_messages)\n"
    "\n"
    "    # ── Legacy flag path",
    "run_agent provider-profile tool-only mode return",
)
text = replace_once(
    text,
    "    return _ct.build_kwargs(\n"
    "        model=agent.model,\n"
    "        messages=_msgs_for_chat,\n"
    "        tools=tools_for_api,\n"
    "        base_url=agent.base_url,\n",
    "    _claw_kwargs = _ct.build_kwargs(\n"
    "        model=agent.model,\n"
    "        messages=_msgs_for_chat,\n"
    "        tools=tools_for_api,\n"
    "        base_url=agent.base_url,\n",
    "run_agent legacy _build_api_kwargs capture for tool-only mode",
)
text = replace_once(
    text,
    "        provider_name=agent.provider,\n"
    "    )\n"
    "\n"
    "\n"
    "\n"
    "def build_assistant_message(agent, assistant_message, finish_reason: str) -> dict:\n",
    "        provider_name=agent.provider,\n"
    "    )\n"
    "    return _claw_apply_tool_only_choice(agent, _claw_kwargs, _msgs_for_chat)\n"
    "\n"
    "\n"
    "def _claw_apply_tool_only_choice(agent, _claw_kwargs: dict, api_messages: list) -> dict:\n"
    "        # In tool-only mode, force tool_choice=required on the first LLM\n"
    "        # call of each user turn. Inspect only messages after the latest\n"
    "        # user message so prior turns' tool_calls do not satisfy this turn.\n"
    "        if os.getenv(\"HERMES_TOOL_ONLY_MODE\") and _claw_kwargs.get(\"tools\"):\n"
    "            _last_user_index = max(\n"
    "                (\n"
    "                    _idx\n"
    "                    for _idx, _m in enumerate(api_messages)\n"
    "                    if isinstance(_m, dict) and _m.get(\"role\") == \"user\"\n"
    "                ),\n"
    "                default=-1,\n"
    "            )\n"
    "            _already_used_tools_this_turn = any(\n"
    "                isinstance(_m, dict)\n"
    "                and _m.get(\"role\") == \"assistant\"\n"
    "                and _m.get(\"tool_calls\")\n"
    "                for _m in api_messages[_last_user_index + 1 :]\n"
    "            )\n"
    "            if not _already_used_tools_this_turn:\n"
    "                _claw_kwargs[\"tool_choice\"] = \"required\"\n"
    "        return _claw_kwargs\n"
    "\n"
    "\n"
    "\n"
    "def build_assistant_message(agent, assistant_message, finish_reason: str) -> dict:\n",
    "run_agent tool_choice=required per turn in tool-only mode",
)
chat_completion_helpers.write_text(text)

# Silent-final opt-in: when HERMES_ALLOW_SILENT_FINAL=1, treat empty visible
# final responses as a successful no-op turn instead of retrying/nudging.
# Managed messaging agents often deliver the visible reply through send_message
# and then intentionally have nothing else to say.
text = conversation_loop.read_text()
text = replace_once(
    text,
    '                if not agent._has_content_after_think_block(final_response):\n',
    '                if not agent._has_content_after_think_block(final_response):\n'
    '                    if os.getenv("HERMES_ALLOW_SILENT_FINAL") == "1":\n'
    '                        logger.debug("Silent final enabled; treating empty visible response as completed no-op")\n'
    '                        agent._empty_content_retries = 0\n'
    '                        agent._cleanup_task_resources(effective_task_id)\n'
    '                        agent._persist_session(messages, conversation_history)\n'
    '                        return {\n'
    '                            "final_response": None,\n'
    '                            "messages": messages,\n'
    '                            "api_calls": api_call_count,\n'
    '                            "completed": True,\n'
    '                            "partial": False,\n'
    '                        }\n',
    "run_agent silent final opt-in",
)
conversation_loop.write_text(text)

gateway_run = purelib / "gateway" / "run.py"
text = gateway_run.read_text()

# Clawdapus-managed chat surfaces treat status_callback output as runtime
# telemetry. Keep upstream behavior when unset, but suppress chat delivery when
# HERMES_CHAT_STATUS_DELIVERY is explicitly false/off/0.
text = replace_once(
    text,
    "    if not text:\n"
    "        return None\n"
    "    if _gateway_platform_value(platform) != \"telegram\":\n",
    "    if not text:\n"
    "        return None\n"
    "    _claw_status_delivery = os.getenv(\"HERMES_CHAT_STATUS_DELIVERY\", \"\").strip().lower()\n"
    "    if _claw_status_delivery and _claw_status_delivery not in {\"1\", \"true\", \"yes\", \"on\", \"chat\", \"visible\", \"all\"}:\n"
    "        return None\n"
    "    if _gateway_platform_value(platform) != \"telegram\":\n",
    "gateway run managed chat status delivery gate",
)

# The run_agent.py patch above marks empty visible final responses as
# successful completed no-op turns when HERMES_ALLOW_SILENT_FINAL=1.  The
# gateway has a second empty-response normalizer that otherwise rewrites that
# completed no-op into a visible "no response was generated" warning.
text = replace_once(
    text,
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
    "gateway run silent final empty-response normalization",
)

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

# Suppress duplicate final text after send_message in tool-only mode.
# Anchor on the agent_messages assignment which is now a few lines below the
# `response = ...` line (upstream inserted the "(empty)" sentinel handler in
# between).
text = replace_once(
    text,
    '            agent_messages = agent_result.get("messages", [])\n',
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
