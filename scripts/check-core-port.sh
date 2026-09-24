#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
# This builds a non-deployable API compatibility probe, not a proxy release.
mkdir -p .artifacts
(cd wing && go generate ./graphql/service/config/global && go test ./compatconfig)
(cd wing && GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -mod=readonly -tags dae_stub_ebpf -o ../.artifacts/daed-api-compile-check .)
printf '%s\n' 'API compile and configuration checks passed; live/eBPF/UI acceptance is still pending.'
