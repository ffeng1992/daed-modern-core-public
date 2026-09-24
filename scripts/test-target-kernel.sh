#!/usr/bin/env bash
# Disposable GitHub runner only. Never install or boot on the production host.
set -euo pipefail
[[ ${GITHUB_ACTIONS:-} == true ]] || { echo 'Disposable CI only'; exit 1; }
kernel_release='6.12.90+deb13.1-amd64'
kernel_file='linux-image-6.12.90+deb13.1-amd64_6.12.90-2_amd64.deb'
sudo apt-get install -y qemu-system-x86 busybox-static udev kmod rsync zstd python3-venv
curl --fail --location --retry 2 "https://security.debian.org/debian-security/pool/updates/main/l/linux-signed-amd64/$kernel_file" -o .artifacts/target-kernel.deb
echo '51f5afc2bd6f8fb9b42d6095537aba968d6567ef807e69c80726247922ee0ea0  .artifacts/target-kernel.deb' | sha256sum -c -
sudo dpkg-deb -x .artifacts/target-kernel.deb /
sudo depmod -a "$kernel_release"
python3 -m venv .artifacts/virtme-env
.artifacts/virtme-env/bin/pip install 'virtme-ng==1.41'
# No guest networking; isolated fixtures create their own interfaces.
# Host userspace snapshot with exact Debian kernel, not a full Debian guest.
sudo env PATH="$PWD/.artifacts/virtme-env/bin:$PATH" timeout 20m .artifacts/virtme-env/bin/vng --run "/boot/vmlinuz-$kernel_release" --force-9p --user root --memory 3G --cpus 2 --exec 'bash scripts/test-target-kernel-guest.sh' | tee .artifacts/target-kernel.log
grep -qx 'TARGET_KERNEL_ACCEPTANCE_PASS' .artifacts/target-kernel.log
