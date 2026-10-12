#!/usr/bin/env python3
"""Explicit controlled-channel Twitch read/send/history validation.

Credentials are read only from a private environment file. Reports contain
status, identities, counts, and message IDs, never tokens or captured chat text.
Sending requires --send and a named channel; deletion additionally requires the
moderator:manage:chat_messages grant. No third-party message service is used.
"""
import argparse
import datetime
import importlib.util
import json
import os
import pathlib
import signal
import sqlite3
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid

sys.dont_write_bytecode = True
spec = importlib.util.spec_from_file_location('private_env', pathlib.Path(__file__).with_name('run-with-env.py'))
private_env = importlib.util.module_from_spec(spec)
spec.loader.exec_module(private_env)


def api(values, path, parameters=None, method='GET'):
    base = 'https://api.twitch.tv/helix/'
    if path == 'validate':
        url = 'https://id.twitch.tv/oauth2/validate'
        headers = {'Authorization': 'OAuth ' + values['TWITCH_ACCESS_TOKEN'].removeprefix('oauth:')}
    else:
        url = base + path
        if parameters:
            url += '?' + urllib.parse.urlencode(parameters)
        headers = {'Authorization': 'Bearer ' + values['TWITCH_ACCESS_TOKEN'].removeprefix('oauth:'), 'Client-Id': values['TWITCH_CLIENT_ID']}
    request = urllib.request.Request(url, headers=headers, method=method)
    try:
        with urllib.request.urlopen(request, timeout=15) as response:
            raw = response.read(1048577)
            if len(raw) > 1048576:
                raise RuntimeError('bounded response limit exceeded')
            return response.status, json.loads(raw) if raw else {}
    except urllib.error.HTTPError as error:
        return error.code, {}


def save_report(path, result):
    if not path:
        return
    destination = pathlib.Path(path)
    descriptor, temporary = tempfile.mkstemp(prefix=destination.name + '.', dir=destination.parent)
    try:
        with os.fdopen(descriptor, 'w') as stream:
            json.dump(result, stream, indent=2)
            stream.write('\n')
            stream.flush()
            os.fsync(stream.fileno())
        os.replace(temporary, destination)
    finally:
        if os.path.exists(temporary):
            os.unlink(temporary)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--binary', required=True)
    parser.add_argument('--env-file', default='.env')
    parser.add_argument('--account-file', help='private named-account credential JSON obtained by twi login')
    parser.add_argument('--transport', choices=('eventsub', 'irc'), default='eventsub')
    parser.add_argument('--channel', required=True)
    parser.add_argument('--send', action='store_true', help='send three uniquely marked validation messages')
    parser.add_argument('--delete', action='store_true', help='delete only messages sent by this validation run')
    parser.add_argument('--report', help='redacted machine-readable validation evidence')
    args = parser.parse_args()
    values = private_env.private_environment(args.env_file)
    if args.account_file:
        values.update(private_env.private_account_environment(args.account_file))
    result = {'schema_version': 1, 'channel': args.channel, 'transport': args.transport, 'checks': {}}
    status, identity = api(values, 'validate')
    if status != 200 or identity.get('login') != args.channel or identity.get('client_id') != values.get('TWITCH_CLIENT_ID'):
        raise RuntimeError('controlled identity validation failed')
    scopes = identity.get('scopes', [])
    result['checks']['identity'] = 'passed'
    result['scopes'] = scopes
    user_id = identity['user_id']
    if ('user:read:chat' if args.transport == 'eventsub' else 'chat:read') not in scopes:
        raise RuntimeError('selected transport read scope missing')
    if args.send and 'user:write:chat' not in scopes:
        raise RuntimeError('send scope missing')
    if args.delete and 'moderator:manage:chat_messages' not in scopes:
        raise RuntimeError('message deletion scope missing; complete validation login first')
    status, shared = api(values, 'shared_chat/session', {'broadcaster_id': user_id})
    if args.send and status != 200:
        raise RuntimeError('cannot verify controlled shared-chat visibility before sending')
    if status == 200 and shared.get('data'):
        if len(shared['data'][0].get('participants', [])) > 1:
            raise RuntimeError('controlled channel is in shared chat; use a standalone validation session')
    marker = 'twi-validation-' + uuid.uuid4().hex[:12]
    result['marker'] = marker
    receipts = []
    with tempfile.TemporaryDirectory(prefix='twi-live-private-') as root:
        environment = os.environ.copy()
        environment.update(values)
        environment.update({'XDG_CONFIG_HOME': root + '/config', 'XDG_CACHE_HOME': root + '/cache', 'XDG_STATE_HOME': root + '/state'})
        store = root + '/history/history.sqlite3'
        stderr = tempfile.TemporaryFile(mode='w+b')
        record = subprocess.Popen([args.binary, 'record', '-t', args.channel, '--transport', args.transport, '--history', store, '--connect-timeout', '45s'], env=environment, stdout=subprocess.PIPE, stderr=stderr)
        def command(*arguments):
            completed = subprocess.run([args.binary, *arguments], env=environment, capture_output=True, timeout=60)
            rows = [json.loads(line) for line in completed.stdout.splitlines()]
            return completed.returncode, rows
        try:
            deadline = time.monotonic() + 50
            healthy = False
            while time.monotonic() < deadline:
                if record.poll() is not None:
                    raise RuntimeError('recorder stopped before readiness')
                if pathlib.Path(store).exists():
                    code, rows = command('history', 'status', '--history', store)
                    if code == 0 and rows and any(c.get('healthy') for c in rows[0].get('captures', []) or []):
                        healthy = True
                        break
                time.sleep(0.1)
            if not healthy:
                raise RuntimeError('recorder readiness deadline expired')
            result['checks'][args.transport + '_readiness'] = 'passed'
            if args.send:
                texts = [marker + ' read/send integration', marker + ' reply integration']
                prefix = marker + ' Unicode boundary 🌍 '
                texts.append(prefix + 'x' * (500 - len(prefix)))
                for index, text in enumerate(texts):
                    arguments = ['produce', '-t', args.channel, '-m', text, '--min-interval', '1s']
                    if index == 1:
                        arguments.extend(['--reply-to', receipts[0]['message_id']])
                    code, rows = command(*arguments)
                    if code != 0 or len(rows) != 1 or rows[0].get('status') != 'sent':
                        result['checks']['unicode_500' if index == 2 else 'send'] = {'exit_code': code, 'status': rows[0].get('status') if rows else 'missing'}
                        raise RuntimeError('validation send was not acknowledged')
                    receipts.append(rows[0])
                    # Retain only acknowledged IDs even if a later check fails,
                    # so controlled messages can still be cleaned up precisely.
                    result['message_ids'] = [r['message_id'] for r in receipts]
                    save_report(args.report, result)
                result['checks']['send_reply_unicode_500'] = 'passed'
                deadline = time.monotonic() + 10
                observed = []
                while time.monotonic() < deadline:
                    code, rows = command('consume', '-t', args.channel, '--history', store, '--offset', 'beginning', '--events', 'all')
                    if code == 0:
                        observed = [r for r in rows if r.get('id') in {receipt['message_id'] for receipt in receipts} and r.get('type') == 'message']
                        if len(observed) == len(receipts):
                            break
                    time.sleep(0.1)
                if len(observed) != len(receipts):
                    raise RuntimeError('same-account messages were not observed by recorder')
                reply = next(r for r in observed if r['id'] == receipts[1]['message_id'])
                if not reply.get('reply') or reply['reply'].get('parent_id') != receipts[0]['message_id']:
                    raise RuntimeError('received reply identity mismatch')
                result['checks']['same_account_recording_and_reply'] = 'passed'
                code, rows = command('history', 'search', '--history', store, '-t', args.channel, '--query', marker)
                if code != 0 or len(rows) != len(receipts):
                    raise RuntimeError('history search mismatch')
                result['checks']['history_search'] = 'passed'
                if args.delete:
                    for receipt in receipts:
                        status, _ = api(values, 'moderation/chat', {'broadcaster_id': user_id, 'moderator_id': user_id, 'message_id': receipt['message_id']}, 'DELETE')
                        if status != 204:
                            raise RuntimeError('controlled message deletion failed')
                    deadline = time.monotonic() + 10
                    while time.monotonic() < deadline:
                        code, rows = command('history', 'search', '--history', store, '-t', args.channel, '--query', marker)
                        if code == 0 and not rows:
                            break
                        time.sleep(0.1)
                    if rows:
                        raise RuntimeError('deleted message body remains searchable')
                    wanted = {receipt['message_id'] for receipt in receipts}
                    deadline = time.monotonic() + 10
                    controls = []
                    while time.monotonic() < deadline:
                        code, rows = command('consume', '-t', args.channel, '--history', store, '--offset', 'beginning', '--events', 'all')
                        controls = [row for row in rows if row.get('target_message_id') in wanted and row.get('type') in ('delete', 'message_deleted')]
                        if code == 0 and {row['target_message_id'] for row in controls} == wanted:
                            break
                        time.sleep(0.1)
                    if {row.get('target_message_id') for row in controls} != wanted:
                        raise RuntimeError('controlled deletions were not observed by recorder')
                    result['checks']['moderation_control_receipt'] = 'passed'
                    if args.transport == 'eventsub':
                        actor_ids = {row.get('target_message_id') for row in controls if (row.get('moderator') or {}).get('id') == user_id and row.get('action') == 'delete'}
                        if actor_ids != wanted:
                            raise RuntimeError('enhanced moderation actor metadata was not observed')
                        result['checks']['moderation_actor_identity'] = 'passed'
                    with sqlite3.connect('file:' + urllib.parse.quote(store) + '?mode=ro', uri=True) as database:
                        placeholders = ','.join('?' for _ in wanted)
                        persisted = database.execute('SELECT payload FROM events WHERE kind=\'message\' AND message_id IN (' + placeholders + ')', tuple(wanted)).fetchall()
                    if len(persisted) != len(wanted):
                        raise RuntimeError('controlled message rows missing after deletion')
                    for (payload,) in persisted:
                        row = json.loads(payload)
                        if not row.get('deleted') or row.get('text') or row.get('system_text') or row.get('fragments'):
                            raise RuntimeError('deleted message body remains in persisted history')
                    result['checks']['persisted_body_redaction'] = 'passed'
                    result['checks']['deletion_and_history_redaction'] = 'passed'
                else:
                    result['checks']['deletion_and_history_redaction'] = 'not requested or scope unavailable'
                result['message_ids'] = [r['message_id'] for r in receipts]
            record.send_signal(signal.SIGTERM)
            record.wait(timeout=5)
            if record.returncode != 143:
                raise RuntimeError('recorder SIGTERM exit mismatch')
            result['checks']['recorder_sigterm'] = 'passed'
            code, rows = command('history', 'status', '--history', store)
            if code != 0 or any(not c.get('clean') for c in rows[0].get('captures', []) or []):
                raise RuntimeError('recorder did not close capture cleanly')
            result['checks']['clean_capture_boundary'] = 'passed'
        finally:
            if record.poll() is None:
                record.terminate()
                try:
                    record.wait(timeout=5)
                except subprocess.TimeoutExpired:
                    record.kill()
                    record.wait()
            stderr.close()
    encoded = json.dumps(result, indent=2) + '\n'
    save_report(args.report, result)
    print(encoded, end='')
    return 0


if __name__ == '__main__':
    try:
        sys.exit(main())
    except (OSError, ValueError, RuntimeError, KeyError, subprocess.TimeoutExpired) as error:
        # Only curated locally generated failures are printed. External responses
        # and exception URLs/headers can contain credentials and stay private.
        print('live validation failed: ' + (str(error) if isinstance(error, RuntimeError) else type(error).__name__), file=sys.stderr)
        sys.exit(1)
