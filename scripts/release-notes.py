#!/usr/bin/env python3
"""Extract a tagged release's exact changelog section without shell evaluation."""
import argparse
from pathlib import Path
import re


def release_notes(changelog, tag):
    if not re.fullmatch(r'v\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?', tag):
        raise ValueError('release tag must be a semantic version prefixed with v')
    version = re.escape(tag[1:])
    match = re.search(r'^## \[' + version + r'\][^\n]*\n(?P<body>.*?)(?=^## \[|\Z)', changelog, re.MULTILINE | re.DOTALL)
    if not match or not match['body'].strip():
        raise ValueError('tagged release has no changelog notes')
    return match['body'].strip() + '\n'


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--tag', required=True)
    parser.add_argument('--changelog', type=Path, default=Path('CHANGELOG.md'))
    parser.add_argument('--output', type=Path, required=True)
    args = parser.parse_args()
    try:
        notes = release_notes(args.changelog.read_text(), args.tag)
    except (OSError, ValueError) as error:
        parser.error(str(error))
    args.output.write_text(notes)


if __name__ == '__main__':
    main()
