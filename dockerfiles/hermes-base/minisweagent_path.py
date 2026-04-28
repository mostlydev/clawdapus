"""Compatibility shim for Hermes installs without the mini-swe-agent checkout."""

from __future__ import annotations

import importlib.util
import os
import sys
from pathlib import Path


def get_repo_root() -> str:
    return os.getcwd()


def discover_minisweagent_src(repo_root: Path | None = None) -> Path | None:
    root = (repo_root or Path.cwd()).resolve()
    candidates = [
        root / "mini-swe-agent" / "src",
        Path("/opt/hermes-agent") / "mini-swe-agent" / "src",
    ]
    for candidate in candidates:
        if candidate.is_dir():
            return candidate
    return None


def ensure_minisweagent_on_path(repo_root: Path | None = None) -> Path | None:
    if importlib.util.find_spec("minisweagent") is not None:
        return None
    src = discover_minisweagent_src(repo_root)
    if src is None:
        return None
    src_str = str(src)
    if src_str not in sys.path:
        sys.path.insert(0, src_str)
    return src
