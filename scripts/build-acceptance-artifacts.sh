#!/usr/bin/env bash
# Compilation only. Never runs the daemon, tests, VM, deploy.sh or privileged services.
set -euo pipefail
product_sha=e7347b2becc4b4a82adda0b23ce3c9d0621cd5ea
repo=$(git rev-parse --show-toplevel)
out=${1:?usage: build-acceptance-artifacts.sh OUTPUT_DIRECTORY}
mkdir -p "$out"
out=$(cd "$out" && pwd)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
git clone --quiet --no-hardlinks "$repo" "$tmp/source"
git -C "$tmp/source" checkout --quiet --detach "$product_sha"
git -C "$tmp/source" submodule update --init --recursive
[[ $(git -C "$tmp/source" rev-parse HEAD) == "$product_sha" ]]
git -C "$tmp/source" submodule status --recursive > "$out/submodules.txt"
if grep -Eq '^[-+U]' "$out/submodules.txt"; then
  echo 'Submodule missing or mismatched' >&2; exit 1
fi
python3 "$tmp/source/scripts/verify-core-source.py"
for patch in ifindex-shutdown-join sniffer-lifetime; do
  (cd "$tmp/source" && sha256sum "experiments/$patch.patch")
done > "$out/patches.sha256"
tag="acceptance-${product_sha:0:12}"
docker build --build-arg DAED_VERSION=source-build --target build-bundle \
  -t "$tag:bundle" "$tmp/source"
docker build --build-arg "BUILDER_IMAGE=$tag:bundle" \
  -f "$repo/scripts/acceptance-artifacts.Dockerfile" -t "$tag:artifacts" "$tmp/source"
docker build --build-arg DAED_VERSION=source-build -t "$tag:runtime" "$tmp/source"
container=$(docker create "$tag:artifacts")
trap 'docker rm -f "$container" >/dev/null 2>&1 || true; rm -rf "$tmp"' EXIT
docker cp "$container:/build/dae-preparation.test" "$out/dae-preparation.test"
docker cp "$container:/build/daed-isolated-test" "$out/daed-isolated-test"
docker cp "$container:/build/source/wing/dae-core/control/bpf_bpfeb.o" "$out/bpf_bpfeb.o"
docker rm "$container" >/dev/null
trap 'rm -rf "$tmp"' EXIT
sha256sum "$out/dae-preparation.test" "$out/daed-isolated-test" "$out/bpf_bpfeb.o" > "$out/artifacts.sha256"
docker run --rm --entrypoint sh "$tag:artifacts" -c \
  'go version; clang-15 --version | head -1; llvm-strip-15 --version | head -1; make --version | head -1' \
  > "$out/toolchain.txt"
docker run --rm --entrypoint sh node:22-bookworm-slim -c 'node --version; corepack --version' \
  >> "$out/toolchain.txt"
docker version --format 'Docker server {{.Server.Version}}' >> "$out/toolchain.txt"
git --version >> "$out/toolchain.txt"
python3 --version >> "$out/toolchain.txt" 2>&1
echo 'pnpm 10.24.0 (pinned in Dockerfile)' >> "$out/toolchain.txt"
python3 - "$out" "$product_sha" "$tag" "$repo" "$tmp/source" <<'PY'
import hashlib, json, pathlib, subprocess, sys
out, source_sha, tag, repo, source_path = sys.argv[1:]
out = pathlib.Path(out)
def image_id(kind):
    return subprocess.check_output(['docker','image','inspect','--format','{{.Id}}',f'{tag}:{kind}'],text=True).strip()
def digest(path):
    h=hashlib.sha256()
    with path.open('rb') as f:
        for b in iter(lambda:f.read(1024*1024),b''): h.update(b)
    return h.hexdigest()
files=['dae-preparation.test','daed-isolated-test','bpf_bpfeb.o']
record={
 'product_source_sha':source_sha,
 'tooling_sha':subprocess.check_output(['git','-C',repo,'rev-parse','HEAD'],text=True).strip(),
 'submodules':(out/'submodules.txt').read_text().splitlines(),
 'patches_sha256':(out/'patches.sha256').read_text().splitlines(),
 'toolchain':(out/'toolchain.txt').read_text().splitlines(),
 'build_tag':tag,
 'build_source':{'sha':source_sha,'temporary_path_on_runner':source_path},
 'commands':[
  'git submodule update --init --recursive',
  'python3 scripts/verify-core-source.py',
  f'docker build --build-arg DAED_VERSION=source-build --target build-bundle -t {tag}:bundle {source_path}',
  'Dockerfile build-bundle: validate patch hashes, git apply --check, git apply, make -C wing deps, CGO_ENABLED=0 go build -mod=readonly -trimpath -tags deployment_candidate,embedallowed -ldflags AppName=daed,AppVersion=source-build',
  f'docker build --build-arg BUILDER_IMAGE={tag}:bundle -f {repo}/scripts/acceptance-artifacts.Dockerfile -t {tag}:artifacts {source_path}',
  'CGO_ENABLED=0 go test -c -mod=readonly -trimpath -tags isolated_acceptance -o /build/dae-preparation.test ./dae',
  'CGO_ENABLED=0 go build -mod=readonly -trimpath -tags isolated_acceptance,embedallowed -ldflags AppName=daed,AppVersion=source-build -o /build/daed-isolated-test .',
  f'docker build --build-arg DAED_VERSION=source-build -t {tag}:runtime {source_path}'],
 'artifacts':{name:{'sha256':digest(out/name),'bytes':(out/name).stat().st_size} for name in files},
 'image_ids':{kind:image_id(kind) for kind in ('bundle','artifacts','runtime')},
 'registry_digest':None,
 'execution':{'kernel_tests':'NOT_RUN','full_debian_A_B_C_D':'NOT_RUN','official_comparison':'NOT_RUN'}
}
(out/'provenance.json').write_text(json.dumps(record,indent=2,ensure_ascii=False)+'\n')
PY
echo "Compilation complete; provenance: $out/provenance.json"
