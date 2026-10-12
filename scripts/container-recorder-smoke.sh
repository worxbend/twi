#!/usr/bin/env bash
set -euo pipefail
image="${1:-twi:local}"
root="$(cd "$(dirname "$0")/.." && pwd)"
fixture="$(mktemp -d)"
container=""
cleanup() { if [[ -n "$container" ]]; then docker rm -f "$container" >/dev/null 2>&1 || true; fi; rm -rf "$fixture"; }
trap cleanup EXIT
cd "$root"
CGO_ENABLED=0 go test -c -o "$fixture/cli.test" ./internal/cli
chmod 755 "$fixture" "$fixture/cli.test"
uid="$(docker run --rm --network none --entrypoint id "$image" -u)"
if [[ "$uid" == 0 ]]; then echo "container runtime must be nonroot" >&2; exit 1; fi
for signal in TERM INT; do
  container="$(docker run -d --read-only --network none \
    --tmpfs "/state:rw,size=16m,mode=0700,uid=$uid,gid=$uid" \
    --tmpfs "/tmp:rw,size=16m,mode=1777" \
    --mount "type=bind,src=$fixture/cli.test,dst=/cli.test,readonly" \
    --env TWI_CONTAINER_RECORDER_MODE=signal \
    --entrypoint /cli.test "$image" -test.v -test.run '^TestContainerRecorderHelper$')"
  ready=0
  for attempt in {1..200}; do
    logs="$(docker logs "$container" 2>&1)"
    if [[ "$logs" == *"recorder ready"* ]]; then ready=1; break; fi
    if [[ "$(docker inspect --format '{{.State.Running}}' "$container")" == false ]]; then break; fi
    sleep 0.05
  done
  if [[ "$ready" != 1 ]]; then docker logs "$container"; echo "recorder fixture readiness timeout" >&2; exit 1; fi
  docker kill --signal "$signal" "$container" >/dev/null
  status="$(docker wait "$container")"
  docker logs "$container"
  [[ "$status" == 0 ]] || { echo "recorder $signal failed: exit $status" >&2; exit 1; }
  docker rm "$container" >/dev/null; container=""
done
# An unwritable state directory must fail without falling back to another path.
docker run --rm --read-only --network none \
  --tmpfs /readonly:rw,size=1m,mode=0555 \
  --tmpfs /tmp:rw,size=16m,mode=1777 \
  --mount "type=bind,src=$fixture/cli.test,dst=/cli.test,readonly" \
  --env TWI_CONTAINER_RECORDER_MODE=permissions \
  --entrypoint /cli.test "$image" -test.v -test.run '^TestContainerRecorderHelper$'
echo "nonroot recorder TERM/INT and permission smokes passed"
