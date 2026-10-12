#!/usr/bin/env bash
set -euo pipefail
snap="$(realpath "${1:?Usage: scripts/snap-smoke.sh FILE.snap}")"
fixture="$(mktemp -d)"
trap 'rm -rf "$fixture"' EXIT
unsquashfs -quiet -d "$fixture/unpacked" "$snap"
export XDG_CONFIG_HOME="$fixture/config" XDG_CACHE_HOME="$fixture/cache" XDG_STATE_HOME="$fixture/state" TERM=xterm-256color
export TWI_TWITCH_USERNAME= TWI_TWITCH_OAUTH_TOKEN= TWI_TWITCH_REFRESH_TOKEN= TWI_TWITCH_CLIENT_ID= TWI_TWITCH_CLIENT_SECRET=
export TWITCH_USERNAME= TWITCH_ACCESS_TOKEN= TWITCH_REFRESH_TOKEN= TWITCH_CLIENT_ID= TWITCH_CLIENT_SECRET=
mkdir -p "$XDG_CONFIG_HOME" "$XDG_CACHE_HOME" "$XDG_STATE_HOME"
"$fixture/unpacked/bin/twi" --help >/dev/null
"$fixture/unpacked/bin/twi" version
"$fixture/unpacked/bin/twi" doctor >/dev/null
"$fixture/unpacked/bin/twi" produce -t alpha -m fixture --dry-run >/dev/null
"$fixture/unpacked/bin/twi" chat --mock --channel alpha >/dev/null
python3 - "$fixture/unpacked/meta/snap.yaml" "$fixture/unpacked/bin/twi" <<'PY'
import json,re,subprocess,sys
metadata=open(sys.argv[1]).read()
assert re.search(r'^confinement: classic$',metadata,re.M)
assert re.search(r'^  twi:\n(?:    .*\n)*?    command: bin/twi$',metadata,re.M)
snap_version=re.search(r'^version: (.+)$',metadata,re.M).group(1).strip("\"'")
version=subprocess.check_output([sys.argv[2],'version'],text=True)
assert snap_version in version,(snap_version,version)
info=json.loads(subprocess.check_output(['go','version','-m','-json',sys.argv[2]],text=True))
settings={s['Key']:s['Value'] for s in info['Settings']}
assert settings['CGO_ENABLED']=='0'
assert settings['GOOS']=='linux'
print('Snap metadata, CGO0 build information and packaged binary smokes passed')
PY
