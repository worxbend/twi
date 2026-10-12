#!/usr/bin/env bash
set -euo pipefail
root="$(cd "$(dirname "$0")/.." && pwd)"
out="${TWI_SNAP_OUT:-$root/dist/snap}"
mode=lxd
image="${TWI_SNAPCRAFT_IMAGE:-ghcr.io/canonical/snapcraft@sha256:0443273552768a3230c2ede3aa47e567da0242bfbb0a7bb1283093208c404a0c}"
while [[ $# -gt 0 ]]; do
  case "$1" in
    --docker) mode=docker; shift ;;
    --out) [[ $# -ge 2 ]] || { echo "--out requires DIR" >&2; exit 2; }; out="$2"; shift 2 ;;
    --image) [[ $# -ge 2 ]] || { echo "--image requires NAME" >&2; exit 2; }; image="$2"; shift 2 ;;
    --help) echo "Usage: scripts/snap-build-smoke.sh [--docker] [--out DIR] [--image SNAPCRAFT_IMAGE]"; exit 0 ;;
    *) echo "unknown option: $1" >&2; exit 2 ;;
  esac
done
fixture="$(mktemp -d)"
trap 'rm -rf "$fixture"' EXIT
version="${TWI_BUILD_VERSION:-$(git -C "$root" describe --tags --always --dirty)}"
mkdir -p "$out"
out="$(cd "$out" && pwd)"
# Copy only package build inputs. In particular, never copy .env, credentials,
# recorded chat, host config, or the repository's local run reports.
python3 - "$root" "$fixture" "$mode" <<'PY'
from pathlib import Path
import shutil,sys
root,fixture=map(Path,sys.argv[1:3])
for name in ('cmd','internal','snap'):shutil.copytree(root/name,fixture/name)
for name in ('go.mod','go.sum','LICENSE'):shutil.copy2(root/name,fixture/name)
if sys.argv[3]=='docker':
    import yaml
    recipe=fixture/'snap/snapcraft.yaml'
    project=yaml.safe_load(recipe.read_text())
    # Snapd is absent inside this container. The exact project toolchain is
    # mounted read-only instead of installing the same Go version as a snap.
    project['parts']['twi'].pop('build-snaps',None)
    project['parts']['twi']['build-environment']=[{'CGO_ENABLED':'0'},{'GOTOOLCHAIN':'local'},{'GOROOT':'/opt/go'},{'GOMODCACHE':'/gomod'},{'GOCACHE':'/tmp/go-cache'}]
    recipe.write_text(yaml.safe_dump(project,sort_keys=False))
PY
if [[ "$mode" == docker ]]; then
  "$root/scripts/check-toolchain.sh"
  docker run --rm --workdir /source --entrypoint /bin/sh \
    --mount "type=bind,src=$fixture,dst=/source" \
    --mount "type=bind,src=$(go env GOROOT),dst=/opt/go,readonly" \
    --mount "type=bind,src=$(go env GOMODCACHE)/cache,dst=/host-cache,readonly" \
    --tmpfs /gomod:rw,size=2g \
    --env PATH=/opt/go/bin:/usr/libexec/snapcraft:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin \
    --env GOROOT=/opt/go --env GOTOOLCHAIN=local --env "TWI_BUILD_VERSION=$version" \
    --env "TWI_FIXTURE_UID=$(id -u)" --env "TWI_FIXTURE_GID=$(id -g)" \
    "$image" -c 'mkdir /gomod/cache; cp -a /host-cache/. /gomod/cache/; chmod -R u+w /gomod/cache; snapcraft pack --destructive-mode --platform amd64 --output /source/twi.snap; status=$?; chown -R "$TWI_FIXTURE_UID:$TWI_FIXTURE_GID" /source; exit "$status"'
else
  (cd "$fixture" && TWI_BUILD_VERSION="$version" snapcraft pack --use-lxd --platform amd64 --output "$fixture/twi.snap")
fi
cp "$fixture/twi.snap" "$out/twi_amd64.snap"
(cd "$out" && sha256sum twi_amd64.snap > twi_amd64.snap.sha256 && sha256sum -c twi_amd64.snap.sha256)
"$root/scripts/snap-smoke.sh" "$out/twi_amd64.snap"
echo "Snap artifact: $out/twi_amd64.snap"
