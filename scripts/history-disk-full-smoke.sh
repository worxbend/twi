#!/usr/bin/env bash
set -euo pipefail
# This test fills only a fresh container-owned tmpfs. The host filesystem is never
# filled. Supply an existing local image containing a Linux runtime if preferred.
image="${TWI_HISTORY_SMOKE_IMAGE:-twi:automation-validation}"
workspace="$(cd "$(dirname "$0")/.." && pwd)"
fixture="$(mktemp -d)"
trap 'rm -rf "$fixture"' EXIT
cd "$workspace"
CGO_ENABLED=0 go test -c -o "$fixture/history.test" ./internal/history
chmod 755 "$fixture" "$fixture/history.test"
docker run --rm --network none --read-only --user 1000:1000 \
  --tmpfs /state:rw,size=8m,mode=1777 \
  --mount "type=bind,src=$fixture/history.test,dst=/history.test,readonly" \
  --env TWI_ISOLATED_FULL_FILESYSTEM=/state \
  --entrypoint /history.test "$image" \
  -test.v -test.run '^TestIsolatedFilesystemFull$'
