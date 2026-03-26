"""Stub module so Hermes can import minisweagent_path without the full SWE-agent install."""
import os

def get_repo_root() -> str:
    return os.getcwd()
