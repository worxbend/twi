#!/usr/bin/env python3
"""Create an isolated task snapshot for hosted validation; leave source checkout untouched.

Default prepares a clone and commit only. --push is an explicit operator action.
No tags, pull requests, releases, comments or installed services are created.
"""
import argparse
import os
import runpy
import datetime
from pathlib import Path
import re
import shutil
import subprocess
import tempfile

ROOT=Path(__file__).resolve().parents[2]
ROOTS={'.github','cmd','internal','docs','examples','scripts','snap','site'}
TOP={'.gitignore','.dockerignore','README.md','CHANGELOG.md','LICENSE','PLAN.md','Makefile','Dockerfile','compose.yaml','go.mod','go.sum'}


def git(*args,cwd=ROOT):
    return subprocess.check_output(['git',*args],cwd=cwd,text=True).strip()


def approved(path):
    p=Path(path)
    if p.parts[0] not in ROOTS and path not in TOP:return False
    if any(part in {'.git','.local','__pycache__','screenshots','recordings'} for part in p.parts):return False
    if p.name.startswith('.env'):return False
    if p.suffix in {'.log','.snap','.sqlite3','.db','.pyc','.test','.out','.prof'}:return False
    if p.name.endswith(('.sqlite3-wal','.sqlite3-shm','.sqlite3.lock','.db-wal','.db-shm','.db.lock')):return False
    # Credential fixture filenames are allowed only under tracked testdata.
    if re.search(r'credentials.*\.json$',p.name,re.I) and 'testdata' not in p.parts:return False
    return True


def known_secret_values(extra_accounts):
    loader=runpy.run_path(str(ROOT/'scripts/run-with-env.py'))
    values=[]
    environment=ROOT/'.env'
    if environment.exists():values.append(loader['private_environment'](environment))
    config=Path(os.environ.get('XDG_CONFIG_HOME',str(Path.home()/'.config')))/'twi'
    candidates=[config/'accounts/validation.json',config/'credentials.json',*extra_accounts]
    for account in candidates:
        if account.exists():values.append(loader['private_account_environment'](account))
    secrets=set()
    for mapping in values:
        for key,value in mapping.items():
            if value and ('TOKEN' in key or 'SECRET' in key):
                secrets.add(value.encode())
                if value.startswith('oauth:'):secrets.add(value[6:].encode())
    return secrets


def reject_known_secrets(path,values):
    content=path.read_bytes()
    if any(secret in content for secret in values):
        raise SystemExit('known secret detected in approved snapshot input: '+str(path.relative_to(ROOT)))


def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--directory',type=Path)
    parser.add_argument('--branch')
    parser.add_argument('--push',action='store_true')
    parser.add_argument('--secret-account-file',type=Path,action='append',default=[],help='optional private account JSON for known-secret preflight only')
    args=parser.parse_args()
    branch=args.branch or 'codex/plan-validation-'+datetime.datetime.now(datetime.timezone.utc).strftime('%Y%m%d-%H%M%S')
    if not re.fullmatch(r'codex/plan-validation-[a-zA-Z0-9-]+',branch):raise SystemExit('branch must use dedicated codex/plan-validation- prefix')
    destination=args.directory or Path(tempfile.mkdtemp(prefix='twi-hosted-snapshot-'))/'repo'
    if destination.resolve().is_relative_to(ROOT):raise SystemExit('snapshot destination must be outside the source checkout')
    if destination.exists():raise SystemExit('snapshot destination must not exist')
    destination.parent.mkdir(parents=True,exist_ok=True)
    try:secrets=known_secret_values(args.secret_account_file)
    except (OSError,ValueError):raise SystemExit('could not validate private known-secret inputs; refusing snapshot')
    before_head=git('rev-parse','HEAD')
    before_status=git('status','--porcelain=v1','-z')
    origin=git('remote','get-url','origin')
    if origin not in {'git@github.com:worxbend/twi.git','https://github.com/worxbend/twi.git'}:raise SystemExit('unexpected origin; refuse validation push')
    subprocess.run(['git','clone','--no-hardlinks',str(ROOT),str(destination)],check=True)
    git('remote','set-url','origin',origin,cwd=destination)
    git('switch','-c',branch,cwd=destination)
    tracked=git('ls-files','-z').split('\0')
    untracked=git('ls-files','--others','--exclude-standard','-z').split('\0')
    for path in sorted(set(tracked+untracked)):
        if not path or not approved(path):continue
        source=ROOT/path;target=destination/path
        if not source.exists():
            if target.exists():target.unlink()
            continue
        if source.is_symlink():raise SystemExit('task snapshot refuses symlink input: '+path)
        if not source.is_file():continue
        target.parent.mkdir(parents=True,exist_ok=True)
        reject_known_secrets(source,secrets)
        shutil.copy2(source,target)
    git('add','--all',cwd=destination)
    changed=git('diff','--cached','--name-only','-z',cwd=destination).split('\0')
    if any(path and not approved(path) for path in changed):raise SystemExit('snapshot staged a file outside the task allowlist')
    git('diff','--cached','--check',cwd=destination)
    git('commit','-m','Implement planned chat automation, history, lifecycle and release hardening',cwd=destination)
    sha=git('rev-parse','HEAD',cwd=destination)
    assert git('rev-parse','HEAD')==before_head
    assert git('status','--porcelain=v1','-z')==before_status,'source checkout changed while preparing snapshot'
    print('Snapshot directory:',destination)
    print('Snapshot branch:',branch)
    print('Snapshot commit:',sha)
    print('Task files staged:',sum(bool(path) for path in changed))
    print('Known-secret preflight passed; private values were neither printed nor copied')
    if args.push:
        subprocess.run(['git','push','origin','HEAD:refs/heads/'+branch],cwd=destination,check=True)
        print('Pushed validation branch only; no tag/release/PR/comment created')


if __name__=='__main__':main()
