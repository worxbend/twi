#!/usr/bin/env bash
# Assemble the twi microsite into _site/ for GitHub Pages.
#
# The site lives in site/ but its images come from docs/assets/, which is also
# used by the README. Copying rather than duplicating keeps one source of truth
# for the banner, logo, and generated screenshots.
#
# Regenerate the screenshots themselves with:
#   TWI_WRITE_SCREENSHOTS=1 go test ./internal/app -run TestWriteDocsScreenshots
#
# Usage: scripts/build-site.sh [output-dir]   (default: _site)
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
out_dir="${1:-$repo_root/_site}"

if [[ ! -d "$repo_root/site" ]]; then
	echo "error: $repo_root/site not found" >&2
	exit 1
fi

rm -rf "$out_dir"
mkdir -p "$out_dir/assets"

cp -R "$repo_root/site/." "$out_dir/"
cp -R "$repo_root/docs/assets/." "$out_dir/assets/"

# Pages serves this tree directly; .nojekyll stops GitHub from running Jekyll
# over it, which would otherwise ignore any path beginning with an underscore.
touch "$out_dir/.nojekyll"

echo "built site -> $out_dir"
find "$out_dir" -type f | sed "s|$out_dir/|  |" | sort
