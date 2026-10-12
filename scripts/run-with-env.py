#!/usr/bin/env python3
"""Run a command with a private environment file without evaluating shell code."""
import argparse
import json
import os
import pathlib
import re
import stat
import sys


def private_environment(path):
    path = pathlib.Path(path)
    info = path.lstat()
    if not stat.S_ISREG(info.st_mode) or info.st_mode & 0o077:
        raise ValueError('environment file must be a private regular file (0600)')
    if hasattr(os, 'geteuid') and info.st_uid != os.geteuid():
        raise ValueError('environment file must belong to the current user')
    if info.st_size > 65536:
        raise ValueError('environment file exceeds 64 KiB')
    values = {}
    for line in path.read_text(encoding='utf-8').splitlines():
        line = line.strip()
        if not line or line.startswith('#'):
            continue
        key, separator, value = line.partition('=')
        if not separator or not re.fullmatch(r'[A-Za-z_][A-Za-z0-9_]*', key):
            raise ValueError('invalid environment assignment')
        if len(value) >= 2 and value[0] == value[-1] and value[0] in "\"'":
            value = value[1:-1]
        if '\x00' in value:
            raise ValueError('environment value contains NUL')
        values[key] = value
    return values


def private_account_environment(path):
    path = pathlib.Path(path)
    info = path.lstat()
    if not stat.S_ISREG(info.st_mode) or info.st_mode & 0o077 or info.st_uid != os.geteuid() or info.st_size > 65536:
        raise ValueError('account file must be a private owned regular file below 64 KiB')
    record = json.loads(path.read_text(encoding='utf-8'))
    if record.get('version') != 1 or not isinstance(record.get('twitch'), dict):
        raise ValueError('invalid account record')
    account = record['twitch']
    values = {}
    for field, keys in (
        ('client_id', ('TWITCH_CLIENT_ID', 'TWI_TWITCH_CLIENT_ID')),
        ('access_token', ('TWITCH_ACCESS_TOKEN', 'TWI_TWITCH_OAUTH_TOKEN')),
        ('refresh_token', ('TWITCH_REFRESH_TOKEN', 'TWI_TWITCH_REFRESH_TOKEN')),
        ('login', ('TWITCH_USERNAME', 'TWI_TWITCH_USERNAME')),
    ):
        value = account.get(field, '')
        if not isinstance(value, str) or '\x00' in value or (field != 'refresh_token' and not value):
            raise ValueError('invalid account credential field')
        for key in keys:
            values[key] = value
    return values


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--env-file', default='.env')
    parser.add_argument('--account-file', help='private named-account JSON; overrides user credential environment aliases')
    parser.add_argument('command', nargs=argparse.REMAINDER)
    args = parser.parse_args()
    if not args.command:
        parser.error('a command is required')
    try:
        environment = os.environ.copy()
        environment.update(private_environment(args.env_file))
        if args.account_file:
            environment.update(private_account_environment(args.account_file))
        os.execvpe(args.command[0], args.command, environment)
    except (OSError, ValueError):
        print('could not load private environment or start command', file=sys.stderr)
        return 7


if __name__ == '__main__':
    sys.exit(main())
