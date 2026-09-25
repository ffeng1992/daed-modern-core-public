# Fresh Debian deployment

This repository provides a **fresh-install path** for Linux hosts identified as Debian on `x86_64`. The installer does not impose or verify a minimum Debian release, kernel version, or complete kernel/BPF feature set. The compatibility manifest describes kernel compatibility as limited; check the actual host and datapath requirements before proceeding. No claim of real-host installation or proxy-traffic acceptance is made by the build checks.

## Host requirements

The host needs:

- Debian (`ID=debian`) on Linux and `x86_64`; run the installer as root, normally with `sudo`.
- Git, for the recursive source checkout and the installer's clean-checkout/source verification.
- Python 3, used by the pinned-source verifier.
- `iproute2` (the `ss` command), used to check whether TCP port 2023 is already listening.
- Docker Engine, a working Docker daemon accessible to root, and the Docker Compose v2 plugin.
- Outbound network access for the recursive Git clone and image build. The build fetches base images, package/module dependencies, and GeoIP/geosite data.

The Docker build installs its own build-stage tools and runtimes (Node.js 22, pnpm 10.24.0, Go 1.26.5, Git, make, LLVM/Clang 15, and certificates). These image-internal tools do not replace the host requirements above. The final image also installs certificates and downloads GeoIP/geosite data.

No minimum Debian or kernel version is declared. The container is privileged, uses host networking and the host PID namespace, and mounts host `/sys` and `/sys/fs/bpf`; kernel, BPF, firewall, and topology suitability must be assessed for the target host. The isolated target-kernel test is not part of the normal CI workflow and does not establish a supported kernel range.

## Checks and administrator review

Before asking for confirmation, the installer checks the current OS and architecture, required commands, Docker Compose v2 and daemon availability, a clean checkout, and the pinned DAE source/patches. When `systemctl` is available, it checks whether `daed.service`/`dae.service` is active; it also checks for daed/dae unit files in the listed systemd unit directories, a container named `daed-modern-core`, a TCP listener on port 2023, and entries directly under `/etc/daed`.

Those checks are deliberately specific, not a general host audit. They do not find arbitrary traffic-interception services, differently named containers, all processes or ports, UDP conflicts, firewall/routing changes, or kernel feature gaps. The administrator must check for those and ensure this host is suitable. Do not use this fresh-install path on a host with another traffic-interception setup or existing daed configuration.

## Install

Clone the repository and all pinned submodules, then run the installer:

```sh
git clone --recurse-submodules https://github.com/ffeng1992/daed-modern-core-public.git
cd daed-modern-core-public
sudo ./deploy.sh
```

The source verifier checks the pinned DAE submodule revision and reviewed patch hashes. The script displays a confirmation prompt before creating `/etc/daed` or invoking Compose. Entering anything other than `yes`, or ending input, exits before those actions. `--yes` skips only this confirmation; it does not skip preflight checks.

After confirmation, the script creates `/etc/daed` if needed and runs `docker compose up -d --build`. A build or startup error exits without automatic cleanup or rollback. Build cache or partially created container state may remain. If the container reports `unhealthy`/`exited`, or does not become healthy within about three minutes, the script prints Compose status and exits; it leaves the container and `/etc/daed` in place for inspection. It does not delete application data. The Compose restart policy is `unless-stopped`.

Inspect the service from the cloned project directory:

```sh
sudo docker compose ps
sudo docker compose logs --tail=200
```

To stop it while retaining the container and data, run `sudo docker compose stop`. To stop and remove the container while retaining `/etc/daed`, run `sudo docker compose down`. Neither command deletes `/etc/daed`; back it up before any separately planned manual data operation. Do not remove existing files as a way to bypass the installer's fresh-install checks.

## First-run setup and acceptance

The installer operator must open `http://<Debian-host-address>:2023` from a trusted management network and complete the page's first-run flow. The administrator creates the initial account (or signs in if an account already exists), then configures/imports proxy nodes or a subscription, DNS, groups, and routing in the management page. The application may create default records, but that does not configure the administrator's real provider or prove the desired traffic split. Restrict access to TCP port 2023 with the host firewall; do not expose the management page directly to the public internet.

The Compose health check requests the local management page over HTTP. A passing check means that this HTTP endpoint responded; it does not prove GraphQL operations, DAE datapath readiness, DNS behavior, node availability, routing, or real proxy/direct traffic. The administrator must separately test DNS and representative proxy/direct destinations and confirm the intended routing from client devices.

The GitHub CI builds the web UI and container image, runs source/configuration checks and backend Go tests (with the `dae_stub_ebpf` test tag), checks deployment-document command consistency, and checks shell syntax. These cloud-runner results show that those build/test steps passed for that commit. They do not install the bridge on a Debian host, validate its kernel/network environment or real eBPF behavior, complete first-run configuration, or test real proxy traffic. Only separate host and traffic acceptance can establish those results.

## Scope

This is a fresh-install path, not an in-place updater or an official daed package. Existing installations require a separately reviewed backup, migration, and rollback procedure. The project does not claim compatibility with every Debian kernel, network topology, provider, or load profile. Review [the compatibility manifest](../compatibility/versions.json) and source licenses before use.
