# Three-layer architecture correction (design candidate)

Status: **design only**. The current Dockerfile, Compose file, and deployment script still produce a modified, single daed binary. They are legacy integration paths and **must not be used to claim or deploy the three-layer architecture**. This branch does not yet contain a deployable bridge or a passing runtime acceptance.

## Required process boundary

1. An unmodified official daed release runs with `--api-only`, binds its management API to loopback, and owns only its database and management UI. Its native run/reload status must not be presented as proof that the separate DAE is active.
2. An unmodified official DAE release is the sole owner of transparent forwarding, eBPF, routing, and its DNS listener. Its configuration lives outside daed's directory.
3. A separate bridge process reads authorized management data through official daed GraphQL or a read-only snapshot, converts it into a DAE configuration, validates it with the official `dae validate` command, atomically publishes it, and controls the official DAE service through a narrowly scoped helper. It reports actual DAE state and never modifies official binaries or sources.

The official daed service declares `Conflicts=dae.service`. Coexistence therefore requires a separate API-only unit rather than enabling both upstream default units. The bridge must not intercept or rewrite official daed behavior. Official API-only mode can acknowledge an internal reload without launching a DAE datapath, so its native page is limited to configuration editing through a restricted management connection; operators must not use its native run/reload controls as DAE controls. A separate bridge page on a separate port owns actual DAE on/off/reload and displays actual DAE health. Isolation testing must prove that daed configuration edits do not accidentally activate its embedded datapath or create a false running indication.

## Current incompatibilities to remove

- `Dockerfile` applies `experiments/ifindex-shutdown-join.patch` and `experiments/sniffer-lifetime.patch` to `wing/dae-core` and builds `daed` with the patched DAE linked in.
- `wing/go.mod` replaces DAE with the local `dae-core` gitlink; `wing/dae/run.go` and `wing/dae/runtime_coordinator.go` construct and operate the core inside daed.
- The legacy container and unit run that integrated binary as the sole runtime.

Both patches fix internal DAE lifecycle behavior; an external process cannot reproduce their in-process synchronization. A separate official DAE must pass its own unpatched lifecycle and traffic tests, or the migration remains blocked pending an official upstream fix.

## External dependency pins for evaluation

| Dependency | Official source | Official x86_64 archive SHA-256 | Extracted ELF SHA-256 |
| --- | --- | --- | --- |
| daed v2.1.1 | `daeuniverse/daed` tag `b3043aa7ce07c774c65e546112aa2c7a1c12edb5` | `aa36b9df5558d47ec0d488f98b92b934c49c08b7f87575061b76498383b3bff1` | `49e3d2e55b16f32751252d2d6b4973c2f6c8de14d37229654e5f46a153d42a0a` |
| DAE v2.1.1 | `daeuniverse/dae` tag `dbae2e82d3ed5324e1648548720f8bbc8cde3882` | `c550af580dc97cc90986260da78c4bf72917f3213c2b5d8b79c45199d9c3dc90` | `a217bf5edf5a5cac371e086af952b5c676e6ecaf8d7b07ba491916b656b66122` |

The official daed tag points to `dae-wing` `dc503088945812c11235b35362d2bfa1a4c3bdf0`, whose `dae-core` gitlink is `85a1fc3c06e3765d143c868ba97ecd0be2aab4ea`. This is distinct from the standalone DAE v2.1.1 source. Archive and ELF hashes were checked locally against official Release digests; executable build information and runtime behavior still require isolated Linux validation.

## Acceptance before any deployment

Build only the separate bridge; never output a reconstructed daed/DAE binary. In a disposable Debian amd64 environment, run both official ELFs and the bridge as separate processes. Test authenticated configuration read/save, conversion and `dae validate`, truthful on/off/reload semantics, DNS UDP/TCP, direct/proxy/block TCP and UDP with independent client/target namespaces, component failures, restart persistence, and file hashes before/after. Do not use real subscriptions or production credentials. Keep the existing integration history intact; replace the legacy build/deploy path only in a reviewed correction branch and never force-push a default branch.
