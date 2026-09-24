# Contributing

Changes must preserve the upstream daed and DAE license notices. Keep bridge-specific changes small and identify which pinned upstream interface they adapt.

Before submitting a change, initialize the pinned DAE submodule and run:

```sh
python3 scripts/verify-core-source.py
pnpm install --frozen-lockfile
pnpm build --filter daed
python3 -m unittest discover -s scripts -p 'test_core_source.py' -v
python3 -m unittest discover -s scripts -p 'test_config_contract.py' -v
(cd wing && go test -mod=readonly -race -tags dae_stub_ebpf -count=1 -timeout=10m ./...)
docker build --tag daed-modern-core:test .
```

Do not include production databases, subscription URLs, credentials, traffic captures, private hostnames, or local evidence. Report which kernel and deployment path you tested, and distinguish isolated tests from live-device results.
