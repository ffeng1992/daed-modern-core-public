#!/usr/bin/env python3
"""Check that documented fresh-install commands clone and enter this repo.

This validates command text and the deploy.sh entry point only; it never runs
the deployment script or changes a host.
"""
from pathlib import Path
import re
import shlex
import sys

ROOT = Path(__file__).resolve().parents[1]
REPO_URL = "https://github.com/ffeng1992/daed-modern-core-public.git"
DOCS = (Path("README.md"), Path("docs/DEPLOYMENT.md"))


def shell_blocks(path):
    content = (ROOT / path).read_text()
    return re.findall(r"```(?:sh|bash)\s*\n(.*?)```", content, re.S)


def command_tokens(line):
    line = line.strip()
    if line.startswith("$ "):
        line = line[2:]
    try:
        return shlex.split(line)
    except ValueError:
        return []


def check_doc(path):
    for block in shell_blocks(path):
        lines = [line.strip() for line in block.splitlines() if line.strip()]
        token_lines = [command_tokens(line) for line in lines]
        clone_index = next(
            (i for i, tokens in enumerate(token_lines)
             if len(tokens) >= 3 and tokens[:2] == ["git", "clone"]
             and REPO_URL in tokens),
            None,
        )
        if clone_index is None or clone_index + 2 >= len(token_lines):
            continue

        clone = token_lines[clone_index]
        url_index = clone.index(REPO_URL)
        clone_dir = clone[url_index + 1] if url_index + 1 < len(clone) else Path(REPO_URL).name.removesuffix(".git")
        expected_dir = Path(REPO_URL).name.removesuffix(".git")
        if clone_dir != expected_dir:
            raise ValueError(f"{path}: clone destination does not match this repository")

        cd = token_lines[clone_index + 1]
        if cd != ["cd", clone_dir]:
            raise ValueError(f"{path}: cd path does not match clone destination")

        deploy = token_lines[clone_index + 2]
        if deploy == ["sudo", "./deploy.sh"]:
            entry = ROOT / "deploy.sh"
        elif deploy == ["./deploy.sh"]:
            entry = ROOT / "deploy.sh"
        else:
            raise ValueError(f"{path}: documented next command must invoke ./deploy.sh")
        if not entry.is_file() or not entry.stat().st_mode & 0o111:
            raise ValueError("deploy.sh is missing or not executable in the repository root")
        return

    raise ValueError(f"{path}: no complete clone, cd, deploy command sequence found")


def main():
    for doc in DOCS:
        check_doc(doc)
        print(f"PASS {doc}: clone directory, cd path, deploy.sh entry")


if __name__ == "__main__":
    try:
        main()
    except (OSError, ValueError) as exc:
        print(f"FAIL: {exc}", file=sys.stderr)
        raise SystemExit(1)
