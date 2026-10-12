#!/usr/bin/env python3
"""Validate supervisor examples without installing or starting any service."""
from pathlib import Path
import plistlib
import shutil
import subprocess
import tempfile

root=Path(__file__).resolve().parents[2]
examples=root/'examples/operations'
with (examples/'io.github.worxbend.twi-record.plist').open('rb') as file:
    agent=plistlib.load(file)
assert agent['ProgramArguments'][1]=='record'
assert agent['Umask']==0o077
assert agent['ExitTimeOut']>=15
assert '--account' in agent['ProgramArguments']
assert not any('TOKEN' in key for key in agent['EnvironmentVariables'])
with tempfile.TemporaryDirectory(prefix='twi-supervisor-') as fixture:
    unit=Path(fixture)/'twi-record.service'
    unit.write_text((examples/'twi-record.service').read_text().replace('%h/.local/bin/twi','/usr/bin/true'))
    if shutil.which('systemd-analyze'):
        subprocess.run(['systemd-analyze','--user','verify',str(unit)],check=True)
    else:
        print('systemd-analyze absent: skipped actual unit parser')
    compose=Path(fixture)/'compose.yaml'
    shutil.copy2(examples/'compose.record.yaml',compose)
    subprocess.run(['docker','compose','--project-directory',fixture,'--env-file','/dev/null','config','--quiet'],check=True,env=__import__('os').environ|{'TWI_RECORD_CHANNEL':'fixture'})
print('Launchd plist, systemd unit and sanitized Compose examples validated; no services installed')
