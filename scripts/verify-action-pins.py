#!/usr/bin/env python3
"""Check immutable workflow pins; --online also verifies upstream release tags."""
import argparse
import json
from pathlib import Path
import re
import subprocess

parser=argparse.ArgumentParser(description=__doc__)
parser.add_argument('--online',action='store_true')
args=parser.parse_args()
root=Path(__file__).resolve().parents[1]
pins=json.loads((root/'.github/action-pins.json').read_text())['actions']
count=0
for workflow in (root/'.github/workflows').glob('*.yml'):
    for action,commit in re.findall(r'uses:\s*([^\s@]+)@([^\s#]+)',workflow.read_text()):
        if not re.fullmatch(r'[a-f0-9]{40}',commit):
            raise SystemExit(f'{workflow.name}: {action} is not pinned to a full commit')
        if action not in pins or pins[action]['commit']!=commit:
            raise SystemExit(f'{workflow.name}: {action} differs from reviewed action pin manifest')
        count+=1
if args.online:
    for action,pin in pins.items():
        tag='refs/tags/'+pin['tag']
        result=subprocess.check_output(['git','ls-remote','https://github.com/'+action+'.git',tag,tag+'^{}'],text=True)
        refs={line.split()[1]:line.split()[0] for line in result.splitlines()}
        actual=refs.get(tag+'^{}',refs.get(tag))
        if actual!=pin['commit']:
            raise SystemExit(f'{action}: upstream {pin["tag"]} no longer resolves to reviewed commit')
print(f'Verified {count} immutable workflow action references'+(' against upstream tags' if args.online else ''))
