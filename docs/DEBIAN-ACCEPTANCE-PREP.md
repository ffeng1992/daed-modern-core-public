# Isolated runtime acceptance entry (not yet executed)

Product source: `e7347b2becc4b4a82adda0b23ce3c9d0621cd5ea`. The tooling branch is separate. The build job compiles only; its image ID proves that the local Docker daemon built an image, not that it ran or passed traffic.

## Fixed installer and preflight

Use the official Debian 13.7.0 amd64 netinst ISO named in `acceptance/debian-13.7-amd64.json`. The SHA-256 in that manifest was read from Debian's versioned `SHA256SUMS`; verify the signed checksum file with a trusted Debian CD signing key and verify the downloaded ISO before a VM run. The ISO has not been downloaded or booted in this tooling round. Run `python3 scripts/preflight-debian-acceptance.py --iso /path/to/debian-13.7.0-amd64-netinst.iso --provenance /path/to/provenance.json` only when a future isolated VM run is authorized. The preflight reads files and does not boot or deploy.

At runtime record `uname -r`, `docker version`, `docker compose version`, the ISO SHA-256, the actual runtime image ID, source SHA, Compose-rendered configuration, and the guest's network interfaces. Docker/Compose versions and Debian kernel are not assumed from the installer version.

## Network boundaries

Use three isolated paths: host-only management into the Debian guest; a test LAN where the client has only the guest as gateway and DNS; and a separate fixture WAN with synthetic DNS and TCP/UDP endpoints. The client must have no interface or route to the fixture WAN or management path. After package/image build, runtime traffic tests should have no production or public network route. Record routes and packet/endpoint counters. As a negative control, block forwarding through the guest and show the same client queries fail; then restore forwarding and show the fixture receives them. A successful client connection without this control is insufficient.

## A/B/C/D execution contract

All stages must run against the **formal runtime image from the public Dockerfile and `docker-compose.yml`**, with its ID matched to build provenance. `daed-isolated-test` is only an isolated daemon artifact for separate internal tests and cannot substitute for this image.

* A: Fresh Debian installation from the fixed ISO; clean public source checkout; published `deploy.sh` flow; formal image and Compose startup; HTTP management health.
* B: First administrator account initialization, real management write, configuration persistence across container restart. Use synthetic nodes and no private subscriptions.
* C: Fixture DNS UDP/TCP; proxied TCP/UDP; direct routing; endpoint counters and bypass negative control. A web health response does not satisfy this stage.
* D: Stop/restart/failure recovery; configuration and network state before/after comparison. Do not assume data deletion on failure.

Write a result JSON with `product_source_sha`, `runtime_image_origin`, `runtime_image_id`, `guest_kernel`, `docker_version`, `compose_version`, and `stages` A/B/C/D. Each stage has `status` and `checks` keyed by the required names in `scripts/check-acceptance-results.py`; each check records `status: PASS` and an evidence path. The separate `official_daed_comparison` has `status: PASS`, `FAIL`, or `NOT_RUN` with a reason when not run. `python3 scripts/check-acceptance-results.py full-report RESULT.json` rejects incomplete, skipped, failed or unevidenced candidate stages. It does not generate runtime evidence.

The official daed comparison needs a trustworthy, hashed official binary and a separate run; if unavailable, report `NOT_RUN`. It does not turn a candidate PASS into FAIL or a candidate FAIL into PASS.

The existing `scripts/test-target-kernel.sh` unpacks a Debian kernel package into the **outer runner root**. It must not be run on a normal host or invoked by this preparation workflow. Its guest exercises selected internal tests on one kernel, not a clean Debian installation or the A/B/C/D contract. GCP remains only a possible future venue; no cloud resources or self-hosted runner are created here.
