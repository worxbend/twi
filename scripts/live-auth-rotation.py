#!/usr/bin/env python3
"""Opt-in controlled-account refresh through two real recorder processes.

Replaces only the selected account's access token with an invalid test value,
then requires both readers to recover using coordinated OAuth refresh. The
rotated token pair remains saved privately. No chat messages are sent.
"""
import argparse
import ctypes
import importlib.util
import json
import os
import pathlib
import select
import signal
import struct
import subprocess
import sys
import tempfile
import time

sys.dont_write_bytecode = True
spec = importlib.util.spec_from_file_location('live', pathlib.Path(__file__).with_name('live-validation.py'))
live = importlib.util.module_from_spec(spec)
spec.loader.exec_module(live)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--binary', required=True)
    parser.add_argument('--env-file', default='.env')
    parser.add_argument('--account-file', required=True)
    parser.add_argument('--account', default='validation')
    parser.add_argument('--channel', required=True)
    parser.add_argument('--report', required=True)
    args = parser.parse_args()
    if args.channel != 'worxbend' or args.account != 'validation':
        raise RuntimeError('rotation suite restricted to the authorized validation account')
    account_path = pathlib.Path(args.account_file).resolve(strict=True)
    standard_path = pathlib.Path(os.environ.get('XDG_CONFIG_HOME', str(pathlib.Path.home() / '.config'))) / 'twi/accounts/validation.json'
    if account_path != standard_path.resolve(strict=True):
        raise RuntimeError('account path must match the CLI selected account')
    values = live.private_env.private_environment(args.env_file)
    values.update(live.private_env.private_account_environment(args.account_file))
    status, identity = live.api(values, 'validate')
    if status != 200 or identity.get('login') != args.channel or identity.get('client_id') != values['TWITCH_CLIENT_ID']:
        raise RuntimeError('controlled identity validation failed')
    original = json.loads(account_path.read_text())
    invalid = json.loads(json.dumps(original))
    invalid['twitch']['access_token'] = 'twi-intentionally-invalid-live-validation'
    live.save_report(account_path, invalid)
    watcher = -1
    if sys.platform.startswith('linux'):
        library = ctypes.CDLL(None, use_errno=True)
        watcher = library.inotify_init1(os.O_NONBLOCK | os.O_CLOEXEC)
        if watcher < 0 or library.inotify_add_watch(watcher, os.fsencode(account_path.parent), 0x00000080) < 0:
            if watcher >= 0:
                os.close(watcher)
            watcher = -1
    children = []
    errors = []
    report = {'schema_version': 1, 'channel': args.channel, 'checks': {}}
    try:
        with tempfile.TemporaryDirectory(prefix='twi-auth-rotation-private-') as root:
            environment = os.environ.copy()
            environment.update(values)
            environment['XDG_STATE_HOME'] = root + '/state'
            environment['XDG_CACHE_HOME'] = root + '/cache'
            stores = [root + '/one.sqlite3', root + '/two.sqlite3']
            for store in stores:
                stderr = tempfile.TemporaryFile()
                errors.append(stderr)
                children.append(subprocess.Popen([args.binary, 'record', '--account', args.account, '--transport', 'eventsub', '--channel', args.channel, '--history', store, '--connect-timeout', '45s'], env=environment, stdout=subprocess.DEVNULL, stderr=stderr))
            deadline = time.monotonic() + 55
            healthy = set()
            while time.monotonic() < deadline and len(healthy) < 2:
                for index, store in enumerate(stores):
                    if children[index].poll() is not None:
                        raise RuntimeError('recorder did not recover credentials')
                    if not pathlib.Path(store).exists():
                        continue
                    check = subprocess.run([args.binary, 'history', 'status', '--history', store], env=environment, capture_output=True, timeout=5)
                    if check.returncode == 0:
                        rows = [json.loads(line) for line in check.stdout.splitlines()]
                        if rows and any(capture.get('healthy') for capture in rows[0].get('captures', []) or []):
                            healthy.add(index)
                time.sleep(0.1)
            if len(healthy) != 2:
                raise RuntimeError('concurrent reader readiness deadline expired')
            current_values = dict(values)
            current_values.update(live.private_env.private_account_environment(account_path))
            status, current_identity = live.api(current_values, 'validate')
            if status != 200 or current_identity.get('login') != args.channel or current_identity.get('client_id') != values['TWITCH_CLIENT_ID']:
                raise RuntimeError('rotated identity validation failed')
            if current_values['TWITCH_ACCESS_TOKEN'] == invalid['twitch']['access_token']:
                raise RuntimeError('rotated credentials were not persisted')
            report['checks']['two_processes_recovered_readiness'] = 'passed'
            report['checks']['rotated_identity_and_persistence'] = 'passed'
            if watcher >= 0:
                persisted = 0
                while select.select([watcher], [], [], 0)[0]:
                    data = os.read(watcher, 65536)
                    offset = 0
                    while offset < len(data):
                        _, mask, _, length = struct.unpack_from('iIII', data, offset)
                        name = data[offset + 16:offset + 16 + length].split(b'\0', 1)[0]
                        if mask & 0x80 and name == os.fsencode(account_path.name):
                            persisted += 1
                        offset += 16 + length
                if persisted != 1:
                    raise RuntimeError('expected exactly one persisted coordinated rotation')
                report['checks']['single_persisted_rotation'] = 'passed'
            for child in children:
                child.send_signal(signal.SIGTERM)
            for child in children:
                child.wait(timeout=5)
                if child.returncode != 143:
                    raise RuntimeError('rotating recorder shutdown exit mismatch')
            report['checks']['both_recorders_sigterm'] = 'passed'
            for store in stores:
                check = subprocess.run([args.binary, 'history', 'status', '--history', store], env=environment, capture_output=True, timeout=5)
                rows = [json.loads(line) for line in check.stdout.splitlines()]
                if check.returncode or not rows or any(not capture.get('clean') for capture in rows[0].get('captures', []) or []):
                    raise RuntimeError('rotating reader capture boundary was not clean')
            report['checks']['both_clean_capture_boundaries'] = 'passed'
    finally:
        for child in children:
            if child.poll() is None:
                child.terminate()
                try:
                    child.wait(timeout=5)
                except subprocess.TimeoutExpired:
                    child.kill()
                    child.wait()
        for stderr in errors:
            stderr.close()
        if watcher >= 0:
            os.close(watcher)
        # Never restore a superseded refresh token after a successful rotation.
        current = json.loads(account_path.read_text())
        if current['twitch']['access_token'] == invalid['twitch']['access_token']:
            live.save_report(account_path, original)
    live.save_report(args.report, report)
    print(json.dumps(report, indent=2))


if __name__ == '__main__':
    try:
        main()
    except (OSError, ValueError, RuntimeError, KeyError, subprocess.TimeoutExpired) as error:
        print('live auth rotation failed: ' + (str(error) if isinstance(error, RuntimeError) else type(error).__name__), file=sys.stderr)
        sys.exit(1)
