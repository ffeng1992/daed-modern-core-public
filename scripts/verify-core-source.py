#!/usr/bin/env python3
"""Check this integration's pinned core before building; never downloads sources."""
import argparse
import hashlib
import json
from pathlib import Path
import re
import subprocess
import sys


def git(root, *args):
    return subprocess.check_output(["git", "-C", str(root), *args], text=True).strip()


def outbound_replacement(path):
    matches = re.findall(
        r"^replace github\.com/daeuniverse/outbound\s*=>\s*(\S+\s+\S+)\s*$",
        path.read_text(), re.MULTILINE,
    )
    if len(matches) != 1:
        raise ValueError(f"Expected one explicit outbound replacement in {path.name}")
    return " ".join(matches[0].split())


def verify(root, apply_patch=False):
    manifest = json.loads((root / "compatibility/versions.json").read_text())
    if manifest["schema"] != 1:
        raise ValueError("Unsupported compatibility manifest schema")
    core = root / "wing/dae-core"
    expected = manifest["sources"]["dae"]["commit"]
    entry = git(root, "ls-tree", "HEAD", "wing/dae-core").split()
    if entry[:3] != ["160000", "commit", expected]:
        raise ValueError("Core gitlink does not match compatibility manifest")
    if git(core, "rev-parse", "HEAD") != expected:
        raise ValueError("Checked-out core revision does not match compatibility manifest")
    if git(core, "status", "--porcelain", "--untracked-files=normal"):
        raise ValueError("Core checkout must be clean before source verification")
    if outbound_replacement(root / "wing/go.mod") != outbound_replacement(core / "go.mod"):
        raise ValueError("Backend and core outbound replacements differ")
    patches = []
    for key in ("shutdown_patch", "sniffer_patch"):
        patch = manifest[key]
        patch_path = (root / patch["path"]).resolve()
        if not patch_path.is_relative_to(root.resolve()):
            raise ValueError("Patch path leaves the repository")
        if hashlib.sha256(patch_path.read_bytes()).hexdigest() != patch["sha256"]:
            raise ValueError("Patch checksum differs from reviewed manifest")
        changed = [line.split("\t")[-1] for line in git(core, "apply", "--numstat", str(patch_path)).splitlines()]
        if changed != patch["files"]:
            raise ValueError("Patch changes files outside the reviewed scope")
        git(core, "apply", "--check", str(patch_path))
        patches.append((patch, patch_path))
    report = {
        "integration_revision": git(root, "rev-parse", "HEAD"),
        "integration_dirty_before": bool(git(root, "status", "--porcelain")),
        "core_revision": expected,
        "core_variant": "private-lifecycle-fixes" if apply_patch else "unmodified-upstream",
        "applied_patch_sha256": [p[0]["sha256"] for p in patches] if apply_patch else None,
        "production_acceptance": "NOT_EVALUATED_BY_THIS_CHECK",
    }
    if apply_patch:
        for _, patch_path in patches:
            git(core, "apply", str(patch_path))
    return report


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--apply-shutdown-patch", action="store_true")
    args = parser.parse_args()
    try:
        result = verify(Path(__file__).resolve().parents[1], args.apply_shutdown_patch)
    except (ValueError, KeyError, OSError, subprocess.CalledProcessError) as exc:
        sys.exit(f"Core source verification FAILED: {exc}")
    print(json.dumps(result, indent=2))
