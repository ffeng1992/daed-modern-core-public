# Isolated experiments — not deployment artifacts

`ifindex-shutdown-join.patch` is an explicitly selected private modification of the pinned DAE core. It waits for the ifindex watcher to return before closing BPF maps or completing a borrowed generation's Close. Cancellation alone does not establish that ordering.

The default source checkout and workflow remain unpatched. Applying this patch means the tested core is **modified**, even though the submodule commit remains pinned to official v2.1.1. Do not describe a patched test binary as official/unmodified. CI prints this distinction and provides no artifact upload or deployment.

Select `experimental_shutdown_join=true` in the private compatibility workflow only for this experiment. The patch checks the exact core commit before application. Run the same preparation cases without the option for the baseline; ten fresh processes/namespaces per case retain the race detector and all DNS assertions. No sleeps, skipped cleanup or suppressed race reports are added.

This experiment does not enable the live integration. Deployment policy and the complete lifecycle/forwarding matrix remain unresolved.
