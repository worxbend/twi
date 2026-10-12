#!/usr/bin/env python3
"""Opt-in app-token/source routing and native IRC action delivery validation.

Uses a standalone authorized channel, records through EventSub, deletes only
observed test message IDs, and retains curated evidence without chat bodies.
"""
import argparse
import importlib.util
import json
import os
import pathlib
import signal
import subprocess
import sys
import tempfile
import time
import uuid

sys.dont_write_bytecode = True
spec = importlib.util.spec_from_file_location('live', pathlib.Path(__file__).with_name('live-validation.py'))
live = importlib.util.module_from_spec(spec)
spec.loader.exec_module(live)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--binary', required=True)
    parser.add_argument('--env-file', default='.env')
    parser.add_argument('--account-file', required=True)
    parser.add_argument('--channel', required=True)
    parser.add_argument('--send', action='store_true', required=True)
    parser.add_argument('--report', required=True)
    args = parser.parse_args()
    values = live.private_env.private_environment(args.env_file)
    values.update(live.private_env.private_account_environment(args.account_file))
    status, identity = live.api(values, 'validate')
    if status != 200 or identity.get('login') != args.channel or args.channel != 'worxbend' or identity.get('client_id') != values['TWITCH_CLIENT_ID']:
        raise RuntimeError('controlled identity validation failed')
    if 'moderator:manage:chat_messages' not in identity.get('scopes', []):
        raise RuntimeError('controlled cleanup grant missing')
    user_id = identity['user_id']
    status, shared = live.api(values, 'shared_chat/session', {'broadcaster_id': user_id})
    if status != 200 or shared.get('data'):
        raise RuntimeError('send-mode suite requires verified standalone controlled channel')
    marker = 'twi-modes-' + uuid.uuid4().hex[:12]
    report = {'schema_version': 1, 'channel': args.channel, 'checks': {}, 'marker': marker, 'message_ids': []}
    observed = set()
    cleaned = set()
    with tempfile.TemporaryDirectory(prefix='twi-modes-private-') as root:
        environment = os.environ.copy()
        environment.update(values)
        environment.update(XDG_CONFIG_HOME=root + '/config', XDG_STATE_HOME=root + '/state', XDG_CACHE_HOME=root + '/cache')
        store = root + '/history.sqlite3'
        errors = tempfile.TemporaryFile()
        reader = subprocess.Popen([args.binary, 'record', '--transport', 'eventsub', '-t', args.channel, '--history', store, '--connect-timeout', '45s'], env=environment, stdout=subprocess.DEVNULL, stderr=errors)

        def command(*arguments):
            result = subprocess.run([args.binary, *arguments], env=environment, capture_output=True, timeout=60)
            return result.returncode, [json.loads(line) for line in result.stdout.splitlines()]

        def snapshot():
            code, rows = command('consume', '-t', args.channel, '--history', store, '--offset', 'beginning', '--events', 'all')
            return rows if code == 0 else []

        try:
            deadline = time.monotonic() + 50
            ready = False
            while time.monotonic() < deadline:
                if reader.poll() is not None:
                    raise RuntimeError('send-mode recorder failed before readiness')
                if pathlib.Path(store).exists():
                    code, rows = command('history', 'status', '--history', store)
                    if code == 0 and rows and any(c.get('healthy') for c in rows[0].get('captures', []) or []):
                        ready = True
                        break
                time.sleep(0.1)
            if not ready:
                raise RuntimeError('send-mode recorder readiness deadline expired')
            modes = [('app_source', ['--auth', 'app', '--sender', args.channel, '--shared-chat', 'source']), ('app_all_standalone', ['--auth', 'app', '--sender', args.channel, '--shared-chat', 'all']), ('irc_action', ['--transport', 'irc', '--action'])]
            for name, flags in modes:
                text = marker + ' ' + name
                code, receipts = command('produce', '-t', args.channel, '-m', text, '--min-interval', '1s', *flags)
                if code != 0 or len(receipts) != 1:
                    raise RuntimeError('send-mode command failed: ' + name)
                receipt = receipts[0]
                if name == 'irc_action':
                    if receipt.get('status') != 'unconfirmed' or receipt.get('message_id'):
                        raise RuntimeError('IRC action claimed an acknowledged send')
                elif receipt.get('status') != 'sent' or not receipt.get('message_id'):
                    raise RuntimeError('app send was not acknowledged')
                elif receipt.get('for_source_only') != (name == 'app_source'):
                    raise RuntimeError('app routing receipt did not match selected mode')
                deadline = time.monotonic() + 12
                found = None
                while time.monotonic() < deadline:
                    for row in snapshot():
                        if row.get('type') == 'message' and row.get('text') == text and (row.get('author') or {}).get('id') == user_id:
                            found = row
                            observed.add(row['id'])
                            break
                    if found:
                        break
                    time.sleep(0.1)
                report['message_ids'] = sorted(observed)
                live.save_report(args.report, report)
                if not found:
                    raise RuntimeError('send mode was not observed: ' + name)
                if name != 'irc_action' and found['id'] != receipt['message_id']:
                    raise RuntimeError('app acknowledgement differs from observed identity')
                if name == 'irc_action' and found.get('message_kind') != 'action':
                    raise RuntimeError('native action did not normalize as action')
                report['checks'][name + '_receipt_and_observation'] = 'passed'
            for message_id in observed:
                status, _ = live.api(values, 'moderation/chat', {'broadcaster_id': user_id, 'moderator_id': user_id, 'message_id': message_id}, 'DELETE')
                if status != 204:
                    raise RuntimeError('send-mode cleanup failed')
                cleaned.add(message_id)
            deadline = time.monotonic() + 12
            while time.monotonic() < deadline:
                code, rows = command('history', 'search', '--history', store, '-t', args.channel, '--query', marker)
                if code == 0 and not rows:
                    break
                time.sleep(0.1)
            if code or rows:
                raise RuntimeError('send-mode history retained removed text')
            report['checks']['all_test_messages_deleted_and_redacted'] = 'passed'
            reader.send_signal(signal.SIGTERM)
            reader.wait(timeout=5)
            if reader.returncode != 143:
                raise RuntimeError('send-mode recorder shutdown mismatch')
            code, rows = command('history', 'status', '--history', store)
            if code or not rows or any(not capture.get('clean') for capture in rows[0].get('captures', []) or []):
                raise RuntimeError('send-mode capture boundary not clean')
            report['checks']['clean_recorder_shutdown'] = 'passed'
        finally:
            # Preserve acknowledged IDs and clean observed test messages even
            # when a later assertion fails. Never clear the whole chat room.
            for message_id in observed - cleaned:
                status, _ = live.api(values, 'moderation/chat', {'broadcaster_id': user_id, 'moderator_id': user_id, 'message_id': message_id}, 'DELETE')
                if status == 204:
                    cleaned.add(message_id)
            report['message_ids'] = sorted(observed)
            report['cleaned_message_ids'] = sorted(cleaned)
            live.save_report(args.report, report)
            if reader.poll() is None:
                reader.terminate()
                try:
                    reader.wait(timeout=5)
                except subprocess.TimeoutExpired:
                    reader.kill()
                    reader.wait()
            errors.close()
    print(json.dumps(report, indent=2))


if __name__ == '__main__':
    try:
        main()
    except (OSError, ValueError, RuntimeError, KeyError, subprocess.TimeoutExpired) as error:
        print('live send modes failed: ' + (str(error) if isinstance(error, RuntimeError) else type(error).__name__), file=sys.stderr)
        sys.exit(1)
