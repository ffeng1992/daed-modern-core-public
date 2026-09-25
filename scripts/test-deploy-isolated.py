#!/usr/bin/env python3
"""Exercise deploy.sh in a temporary filesystem with command shims.

Only absolute host paths in a temporary copy are relocated under a private
test root. The deployment control flow remains the checked-in deploy.sh. No
real Docker, systemd, host /etc, VM, or network device is accessed.
"""

from __future__ import annotations

import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys
import tempfile

ROOT = Path(__file__).resolve().parents[1]
SHIM_SOURCE = r'''#!/usr/bin/env python3
import json, os, pathlib, sys
name = pathlib.Path(sys.argv[0]).name
args = sys.argv[1:]
scenario = os.environ["SCENARIO"]
log = pathlib.Path(os.environ["ACTION_LOG"])
def record(action):
    with log.open("a") as stream:
        stream.write(json.dumps([name, action, *args]) + "\n")
record("call")
if name == "uname":
    if args == ["-s"]: print("Darwin" if scenario == "wrong_os" else "Linux")
    elif args == ["-m"]: print("aarch64" if scenario == "wrong_arch" else "x86_64")
    sys.exit(0)
if name == "docker":
    if args[:2] == ["compose", "version"]: sys.exit(1 if scenario == "compose_missing" else 0)
    if args == ["info"]: sys.exit(1 if scenario == "docker_down" else 0)
    if args[:2] == ["container", "inspect"]: sys.exit(0 if scenario == "existing_container" else 1)
    if args[:2] == ["compose", "up"]:
        record("compose_up")
        sys.exit(1 if scenario == "build_failure" else 0)
    if args[:2] == ["inspect", "--format"]:
        record("health_inspect")
        if scenario == "healthy": print("healthy")
        elif scenario == "unhealthy": print("unhealthy")
        elif scenario == "start_failure": print("exited")
        else: print("starting")
        sys.exit(0)
    if args[:2] == ["compose", "ps"]: record("compose_ps")
    if "down" in args or "rm" in args: record("delete_action")
    sys.exit(0)
if name == "ss":
    if scenario == "port_busy": print("LISTEN 0 128 *:2023 *:*")
    sys.exit(0)
if name == "git":
    if scenario == "dirty_source": print(" M deploy.sh")
    sys.exit(0)
if name == "python3":
    if any(arg.endswith("verify-core-source.py") for arg in args) and scenario == "source_failure": sys.exit(1)
    sys.exit(0)
if name == "systemctl":
    active = scenario == "active_daed" and args[-1:] == ["daed.service"]
    active |= scenario == "active_dae" and args[-1:] == ["dae.service"]
    sys.exit(0 if active and args[:1] == ["is-active"] else 1)
if name == "install":
    record("install_action")
    target = pathlib.Path(args[-1])
    target.mkdir(parents=True, exist_ok=True)
    sys.exit(0)
if name == "sleep": sys.exit(0)
sys.exit(127)
'''


def read_actions(log: Path) -> list[list[str]]:
    if not log.exists():
        return []
    return [json.loads(line) for line in log.read_text().splitlines()]


def run_case(label: str, scenario: str, *, stdin: str | None = None,
             missing: str | None = None, nonroot: bool = False) -> tuple[int, str, Path, list[list[str]]]:
    temp = Path(tempfile.mkdtemp(prefix="deploy-test-"))
    temp.chmod(0o777)
    root = temp / "host"
    root.mkdir(mode=0o755)
    for path in (root / "etc/systemd/system", root / "lib/systemd/system", root / "usr/lib/systemd/system"):
        path.mkdir(parents=True, exist_ok=True)
    if scenario != "missing_os_release":
        (root / "etc/os-release").write_text("ID=ubuntu\n" if scenario == "wrong_distro" else "ID=debian\n")
    work = temp / "checkout"
    work.mkdir()
    script = (ROOT / "deploy.sh").read_text()
    replacements = {
        "/etc/os-release": '"$TEST_ROOT/etc/os-release"',
        "/etc/daed": "$TEST_ROOT/etc/daed",
        "/etc/systemd": "$TEST_ROOT/etc/systemd",
        "/usr/lib/systemd": "@@TEST_USRLIB_SYSTEMD@@",
        "/lib/systemd": "$TEST_ROOT/lib/systemd",
    }
    for old, new in replacements.items():
        if old not in script:
            raise AssertionError(f"deploy.sh path relocation target missing: {old}")
        script = script.replace(old, new)
    script = script.replace("@@TEST_USRLIB_SYSTEMD@@", "$TEST_ROOT/usr/lib/systemd")
    if re.search(r"(^|[\s\"'=])/(?:etc/os-release|etc/daed(?:/|\b)|etc/systemd/|lib/systemd/|usr/lib/systemd/)", script, re.M):
        raise AssertionError("refusing to run test copy with an absolute host system path still present")
    (work / "deploy.sh").write_text(script)
    (work / "deploy.sh").chmod(0o755)
    (work / "docker-compose.yml").write_text("services: {}\n")
    (work / "scripts").mkdir()
    (work / "scripts/verify-core-source.py").write_text("# isolated fixture\n")

    shim_dir = temp / "bin"
    shim_dir.mkdir()
    shim_file = shim_dir / "shim"
    shim_file.write_text(SHIM_SOURCE.replace("#!/usr/bin/env python3", f"#!{sys.executable}"))
    shim_file.chmod(0o755)
    names = ["uname", "docker", "ss", "git", "python3", "systemctl", "install", "sleep"]
    for name in names:
        if name != missing:
            (shim_dir / name).symlink_to(shim_file)
    for name in ("find", "grep", "seq", "dirname"):
        (shim_dir / name).symlink_to(shutil.which(name))
    if scenario == "unit_file":
        (root / "etc/systemd/system/dae.service").write_text("[Unit]\n")
    if scenario == "existing_config":
        cfg = root / "etc/daed"
        cfg.mkdir(parents=True)
        (cfg / "keep.db").write_text("keep\n")

    log = temp / "actions.jsonl"
    env = os.environ.copy()
    env.update({
        "PATH": str(shim_dir),
        "TEST_ROOT": str(root),
        "SCENARIO": scenario,
        "ACTION_LOG": str(log),
    })
    kwargs = {}
    if nonroot:
        if os.geteuid() != 0:
            raise RuntimeError("nonroot case requires the test harness to run as root")
        kwargs["preexec_fn"] = lambda: (os.setgid(65534), os.setuid(65534))
    completed = subprocess.run(
        ["/bin/bash", str(work / "deploy.sh")], cwd=work, env=env,
        input=stdin, text=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT,
        timeout=20, **kwargs,
    )
    actions = read_actions(log)
    return completed.returncode, completed.stdout, root, actions


def check(label: str, scenario: str, *, expected: int = 1, stdin: str | None = None,
          missing: str | None = None, nonroot: bool = False,
          forbidden: tuple[str, ...] = ("install_action", "compose_up", "delete_action"),
          required_text: str | None = None) -> None:
    code, output, hostroot, actions = run_case(label, scenario, stdin=stdin, missing=missing, nonroot=nonroot)
    if code != expected:
        raise AssertionError(f"{label}: expected exit {expected}, got {code}; output:\n{output}")
    action_names = [row[1] for row in actions]
    for action in forbidden:
        if action in action_names:
            raise AssertionError(f"{label}: forbidden action {action} occurred: {actions}")
    if required_text and required_text not in output:
        raise AssertionError(f"{label}: expected text missing: {required_text!r}; output:\n{output}")
    if "install_action" not in action_names and (hostroot / "etc/daed").exists() and scenario != "existing_config":
        raise AssertionError(f"{label}: config directory was created before confirmation/preflight: {actions}")
    if "install_action" in action_names and not (hostroot / "etc/daed").is_dir():
        raise AssertionError(f"{label}: expected post-confirmation config directory is missing: {actions}")
    if scenario == "existing_config" and not (hostroot / "etc/daed/keep.db").is_file():
        raise AssertionError("existing configuration was removed")
    print(f"PASS {label}")
    shutil.rmtree(hostroot.parent)


def main() -> int:
    if os.geteuid() != 0:
        print("Run this test as root (for example: sudo python3 scripts/test-deploy-isolated.py).", file=sys.stderr)
        return 2

    check("rejects non-root", "baseline", nonroot=True, required_text="Run with sudo")
    check("rejects non-Linux", "wrong_os", required_text="Linux hosts only")
    check("rejects missing OS release data", "missing_os_release", required_text="Cannot identify the Linux distribution")
    check("rejects non-Debian", "wrong_distro", required_text="Supported host: Debian")
    check("rejects unsupported architecture", "wrong_arch", required_text="Only x86_64")
    check("rejects missing Docker", "baseline", missing="docker", required_text="Install Docker Engine")
    check("rejects missing Git", "baseline", missing="git", required_text="Install Git")
    check("rejects missing iproute2/ss", "baseline", missing="ss", required_text="Install iproute2")
    check("rejects missing Python", "baseline", missing="python3", required_text="Install Python 3")
    check("rejects missing Compose v2", "compose_missing", required_text="Docker Compose v2")
    check("rejects unavailable Docker daemon", "docker_down", required_text="Docker Engine is not running")
    check("rejects dirty source", "dirty_source", required_text="source checkout is modified")
    check("rejects pinned-source verification failure", "source_failure", required_text="source or reviewed patch checks failed")
    check("rejects active daed service", "active_daed", required_text="daed.service is active")
    check("rejects active dae service", "active_dae", required_text="dae.service is active")
    check("rejects existing systemd unit", "unit_file", required_text="Existing dae system service")
    check("rejects same-name container", "existing_container", required_text="fresh installs only")
    check("rejects TCP 2023 listener", "port_busy", required_text="TCP port 2023")
    check("rejects existing config and preserves it", "existing_config", required_text="will not replace or migrate")
    check("cancel leaves no config/build/start/delete", "baseline", stdin="no\n")
    check("EOF leaves no config/build/start/delete", "baseline", stdin="")
    postconfirm = ("delete_action",)
    check("Compose build failure exits nonzero and retains state", "build_failure", stdin="yes\n", forbidden=postconfirm)
    check("container startup failure exits nonzero and retains state", "start_failure", stdin="yes\n", forbidden=postconfirm, required_text="did not become healthy")
    check("unhealthy container is retained for inspection", "unhealthy", stdin="yes\n", forbidden=postconfirm, required_text="did not become healthy")
    check("health timeout exits nonzero and retains container", "timeout", stdin="yes\n", forbidden=postconfirm, required_text="did not become healthy")

    code, output, hostroot, actions = run_case("healthy simulated", "healthy", stdin="yes\n")
    names = [row[1] for row in actions]
    if code != 0 or "Management page HTTP health check passed" not in output:
        raise AssertionError(f"healthy simulation did not pass its intended check:\n{output}")
    if "Complete the first-run setup" not in output or "verify proxy traffic separately" not in output:
        raise AssertionError("success output does not distinguish page health from traffic acceptance")
    if "deployed successfully" in output.lower() or "traffic verified" in output.lower():
        raise AssertionError("simulated success was overstated as real deployment/traffic acceptance")
    if "compose_up" not in names or not (hostroot / "etc/daed").is_dir():
        raise AssertionError("healthy simulation did not traverse the install/start control flow")
    if "delete_action" in names:
        raise AssertionError("deployment control flow unexpectedly deleted state")
    print("PASS simulated healthy path: actual deploy.sh control flow, explicit HTTP-only claim")
    shutil.rmtree(hostroot.parent)
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except (AssertionError, OSError, subprocess.SubprocessError, RuntimeError) as exc:
        print(f"FAIL: {exc}", file=sys.stderr)
        raise SystemExit(1)
