#!/bin/sh
set -eu
repo_dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$repo_dir"
expected=$(awk '$1 == "toolchain" {sub(/^go/, "", $2); print $2}' go.mod)
actual=$(awk -F= '/^ARG GO_VERSION=/ {print $2}' Dockerfile)
if [ "$expected" != "$actual" ]; then
  echo "Docker Go $actual differs from go.mod toolchain $expected" >&2
  exit 1
fi
active=$(go version | awk '{sub(/^go/, "", $3); print $3}')
if [ "$active" != "$expected" ]; then
  echo "Active Go $active differs from pinned toolchain $expected" >&2
  exit 1
fi
track=$(sed -n 's/.*go\/\([0-9][0-9]*\.[0-9][0-9]*\)\/.*/\1/p' snap/snapcraft.yaml)
minor=$(printf '%s' "$expected" | cut -d. -f1,2)
if [ "$track" != "$minor" ]; then
  echo "Snap Go track $track differs from pinned toolchain family $minor" >&2
  exit 1
fi
printf 'Go/Docker/Snap toolchain aligned: %s (Snap track %s)\n' "$expected" "$track"
