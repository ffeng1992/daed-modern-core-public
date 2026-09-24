# Fresh Debian deployment

This path supports a fresh Debian x86_64 host with Docker Engine and the Docker Compose v2 plugin already installed. It builds the pinned daed v1.27.0 + DAE v2.1.1 bridge locally, then starts it as a privileged container using the host network and host PID namespace. The proxy needs these permissions for its Linux networking datapath.

Do not use this installer on a host that already runs daed, DAE, or another traffic interception service. It deliberately stops if it finds a daed system service, a container with the project name, a TCP listener on port 2023, or existing files under `/etc/daed`. It does not migrate an existing database or configuration.

After installing Docker Engine and Compose v2, clone the source and run the installer:

```sh
git clone --recurse-submodules https://github.com/ffeng1992/daed-modern-core.git
cd daed-modern-core
sudo ./deploy.sh
```

The installer checks the pinned DAE source and reviewed patch hashes before building. It asks before starting the service. Pass `--yes` only when you intend to skip that final prompt.

Open `http://<Debian-host-address>:2023` from a trusted management network and complete the daed first-run setup. Restrict access to port 2023 with the host firewall; do not expose the management page directly to the public internet.

The persistent application data is in `/etc/daed`. The installer never deletes it. The container has `restart: unless-stopped`; normal host restart brings it back after Docker starts.

## Stop and remove

From the cloned project directory:

```sh
sudo docker compose down
```

This stops and removes the container but leaves `/etc/daed` intact. Keep a separate backup before any manual data migration or removal.

## Scope

This is a fresh-install path, not an in-place updater or an official daed package. Existing installations require a separately reviewed backup, migration, and rollback procedure. The project does not claim compatibility with every Debian kernel, network topology, provider, or load profile. Review [the compatibility manifest](../compatibility/versions.json) and source licenses before use.
