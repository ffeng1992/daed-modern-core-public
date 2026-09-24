#!/usr/bin/env bash
set -Eeuo pipefail

ROOT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
ASSUME_YES=0
if [[ "${1:-}" == "--yes" ]]; then
  ASSUME_YES=1
elif [[ $# -ne 0 ]]; then
  echo "Usage: sudo ./deploy.sh [--yes]" >&2
  exit 2
fi

fail() { echo "Deploy stopped: $*" >&2; exit 1; }

[[ "${EUID}" -eq 0 ]] || fail "Run with sudo so Docker and /etc/daed are managed as root."
[[ "$(uname -s)" == "Linux" ]] || fail "This installer supports Linux hosts only."
[[ -r /etc/os-release ]] || fail "Cannot identify the Linux distribution."
# shellcheck disable=SC1091
source /etc/os-release
[[ "${ID:-}" == "debian" ]] || fail "Supported host: Debian. Other distributions are not yet verified."
[[ "$(uname -m)" == "x86_64" ]] || fail "Only x86_64 has been verified for this deployment path."

command -v docker >/dev/null || fail "Install Docker Engine and the Compose plugin first; see docs/DEPLOYMENT.md."
docker compose version >/dev/null 2>&1 || fail "Docker Compose v2 plugin is required."
docker info >/dev/null 2>&1 || fail "Docker Engine is not running or the current account cannot access it."
command -v ss >/dev/null || fail "Install iproute2 so the installer can check port conflicts."
command -v python3 >/dev/null || fail "Install Python 3 so the pinned source can be verified."

cd "${ROOT_DIR}"
[[ -z "$(git status --porcelain --untracked-files=normal)" ]] || fail "The source checkout is modified. Use a clean clone so the pinned build can be verified."
python3 scripts/verify-core-source.py >/dev/null || fail "Pinned DAE source or reviewed patch checks failed."

if command -v systemctl >/dev/null; then
  for service in daed.service dae.service; do
    systemctl is-active --quiet "${service}" && fail "${service} is active. This installer only supports a fresh host."
  done
fi
for service in daed dae; do
  for unit in "/etc/systemd/system/${service}.service" "/lib/systemd/system/${service}.service" "/usr/lib/systemd/system/${service}.service"; do
    [[ ! -e "${unit}" ]] || fail "Existing ${service} system service found; this installer is for fresh installs only."
  done
done

if docker container inspect daed-modern-core >/dev/null 2>&1; then
  fail "A daed-modern-core container already exists. Use the documented update procedure instead."
fi
listeners="$(ss -H -ltn 'sport = :2023')"
if [[ -n "${listeners}" ]]; then
  fail "TCP port 2023 is already in use. No service was changed."
fi

if [[ -d /etc/daed ]] && find /etc/daed -mindepth 1 -maxdepth 1 -print -quit | grep -q .; then
  fail "/etc/daed already contains data. This installer will not replace or migrate it."
fi

echo "This will build the bridge image and start a privileged, host-networked container."
echo "It only supports a fresh Debian x86_64 installation and will preserve /etc/daed."
if [[ "${ASSUME_YES}" -ne 1 ]]; then
  read -r -p "Continue? Type yes: " answer
  [[ "${answer}" == "yes" ]] || fail "Cancelled; no service was started."
fi

install -d -o root -g root -m 0750 /etc/daed
docker compose up -d --build

for _ in $(seq 1 90); do
  state="$(docker inspect --format '{{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}}' daed-modern-core 2>/dev/null || true)"
  [[ "${state}" == "healthy" ]] && { echo "daed Modern Core Bridge is healthy on TCP port 2023."; exit 0; }
  [[ "${state}" == "unhealthy" || "${state}" == "exited" ]] && break
  sleep 2
done

docker compose ps >&2 || true
fail "The container did not become healthy. It has not been removed so its state can be inspected; /etc/daed was not erased."
