#!/usr/bin/env bash
set -euo pipefail
[[ $(uname -r) == '6.12.90+deb13.1-amd64' ]]
uname -r
export GITHUB_ACTIONS=true DAED_ISOLATED_ACCEPTANCE=1 DAED_ACCEPTANCE_CYCLES=5 DAED_ACCEPTANCE_SOAK=0
unshare --mount --net sh -ec '
 mount --make-rprivate /
 mount -t bpf bpf /sys/fs/bpf
 ip link set lo up
 exec .artifacts/dae-preparation.test -test.v -test.run "^TestIsolated(RuntimeCoordinator|LANForwarding|GraphQLRuntime)$" -test.timeout 15m
'
if [ -f .artifacts/original-daed ]; then
 unshare --mount --net sh -ec '
  mount --make-rprivate /
  mount -t bpf bpf /sys/fs/bpf
  ip link set lo up
  timeout 5m python3 scripts/test-process-persistence.py .artifacts/daed-isolated-test --datapath --original .artifacts/original-daed
 '
fi
echo TARGET_KERNEL_ACCEPTANCE_PASS
