#!/usr/bin/env bash
set -euo pipefail
snap="$(realpath "${1:?Usage: scripts/operations/snap-launcher-vm-smoke.sh FILE.snap}")"
for tool in qemu-system-x86_64 qemu-img xorriso ssh scp ssh-keygen curl python3; do
  command -v "$tool" >/dev/null || { echo "missing prerequisite: $tool" >&2; exit 1; }
done
[[ -r /dev/kvm && -w /dev/kvm ]] || { echo "current user lacks KVM access" >&2; exit 1; }
fixture="$(mktemp -d)"
chmod 700 "$fixture"
pid=""
cleanup() { if [[ -n "$pid" ]]; then kill "$pid" 2>/dev/null || true; fi; rm -rf "$fixture"; }
trap cleanup EXIT
# Every guest disk, key, seed and output is task-owned. No host sudo, host mount,
# agent forwarding, real credentials, .env, or source checkout enters the guest.
base=https://cloud-images.ubuntu.com/noble/current
curl --proto '=https' --tlsv1.2 -fsSL "$base/SHA256SUMS" -o "$fixture/SHA256SUMS"
curl --proto '=https' --tlsv1.2 -fsSL "$base/noble-server-cloudimg-amd64.img" -o "$fixture/base.img"
python3 - "$fixture" <<'PY'
import hashlib,sys
from pathlib import Path
p=Path(sys.argv[1]);expected=next(line.split()[0] for line in (p/'SHA256SUMS').read_text().splitlines() if line.rstrip().endswith('noble-server-cloudimg-amd64.img'))
assert hashlib.sha256((p/'base.img').read_bytes()).hexdigest()==expected
print('Official Ubuntu24 cloud image SHA256 verified')
PY
qemu-img create -f qcow2 -b "$fixture/base.img" "$fixture/guest.qcow2" 12G >/dev/null
ssh-keygen -t ed25519 -N '' -f "$fixture/key" -C twi-snap-isolated-smoke >/dev/null
port="$(python3 - <<'PY'
import socket
with socket.socket() as s:s.bind(('127.0.0.1',0));print(s.getsockname()[1])
PY
)"
python3 - "$fixture" <<'PY'
from pathlib import Path
import sys
p=Path(sys.argv[1]);key=(p/'key.pub').read_text().strip()
(p/'user-data').write_text('#cloud-config\nusers:\n  - default\n  - name: tester\n    sudo: ALL=(ALL) NOPASSWD:ALL\n    groups: [sudo]\n    shell: /bin/bash\n    ssh_authorized_keys:\n      - '+key+'\nssh_pwauth: false\ndisable_root: true\n')
(p/'meta-data').write_text('instance-id: twi-snap-smoke\nlocal-hostname: twi-snap-smoke\n')
(p/'network-config').write_text('version: 2\nethernets:\n  all:\n    match:\n      name: "e*"\n    dhcp4: true\n')
PY
xorriso -as mkisofs -quiet -output "$fixture/seed.iso" -volid cidata -joliet -rock "$fixture/user-data" "$fixture/meta-data" "$fixture/network-config"
qemu-system-x86_64 -enable-kvm -cpu host -m 2048 -smp 2 -display none -monitor none \
 -drive "file=$fixture/guest.qcow2,if=virtio,format=qcow2" \
 -drive "file=$fixture/seed.iso,format=raw,media=cdrom,readonly=on" \
 -netdev "user,id=net0,hostfwd=tcp:127.0.0.1:$port-:22" -device virtio-net-pci,netdev=net0 \
 -pidfile "$fixture/qemu.pid" -daemonize -serial "file:$fixture/serial.log"
pid="$(cat "$fixture/qemu.pid")"
ssh_options=(-i "$fixture/key" -o IdentitiesOnly=yes -o IdentityAgent=none -o StrictHostKeyChecking=accept-new -o "UserKnownHostsFile=$fixture/known_hosts" -o ConnectTimeout=3)
ready=0
for attempt in {1..60}; do
  if ssh "${ssh_options[@]}" -p "$port" tester@127.0.0.1 true 2>/dev/null; then ready=1; break; fi
  sleep 2
done
[[ "$ready" == 1 ]] || { echo "isolated guest SSH startup timeout" >&2; exit 1; }
ssh "${ssh_options[@]}" -p "$port" tester@127.0.0.1 'cloud-init status --wait; snap version'
scp "${ssh_options[@]}" -P "$port" "$snap" tester@127.0.0.1:/home/tester/twi.snap
ssh "${ssh_options[@]}" -p "$port" tester@127.0.0.1 'set -eu
sudo -n snap install --dangerous --classic /home/tester/twi.snap
umask 077
mkdir -p /tmp/twi-smoke/config /tmp/twi-smoke/cache /tmp/twi-smoke/state
export XDG_CONFIG_HOME=/tmp/twi-smoke/config XDG_CACHE_HOME=/tmp/twi-smoke/cache XDG_STATE_HOME=/tmp/twi-smoke/state TERM=xterm-256color
/snap/bin/twi --help >/dev/null
/snap/bin/twi version
/snap/bin/twi doctor >/dev/null
/snap/bin/twi produce -t example -m fixture --dry-run >/dev/null
/snap/bin/twi record --help >/dev/null 2>&1
/snap/bin/twi chat --mock --channel example >/dev/null
snap list twi
sudo -n snap remove --purge twi
echo "Installed snapd launcher smokes passed in isolated Ubuntu24 KVM guest"'
