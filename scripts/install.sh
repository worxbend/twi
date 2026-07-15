#!/usr/bin/env sh
set -eu

usage() {
	cat <<'USAGE'
Usage: install.sh [--version TAG] [--dir DIR]

Download a twi release binary from GitHub Releases, verify its sha256
checksum, and install it as `twi` in a directory on PATH. If that directory
is not already on PATH, add it to ~/.bashrc and ~/.zshrc.

Options:
  --version TAG   Install a specific release tag instead of the latest
                   (e.g. v0.3.0)
  --dir DIR       Install directory (default: $HOME/.local/bin)
  --help          Show this help text

Environment overrides:
  TWI_INSTALL_VERSION  Same as --version
  TWI_INSTALL_DIR      Same as --dir

Only Linux amd64/arm64 binaries are published; see docs/release.md to build
from source on other platforms.
USAGE
}

repo="worxbend/twi"
version=${TWI_INSTALL_VERSION:-latest}
install_dir=${TWI_INSTALL_DIR:-"$HOME/.local/bin"}

while [ "$#" -gt 0 ]; do
	case "$1" in
	--version)
		if [ "$#" -lt 2 ]; then
			echo "missing value for --version" >&2
			exit 2
		fi
		version=$2
		shift 2
		;;
	--dir)
		if [ "$#" -lt 2 ]; then
			echo "missing value for --dir" >&2
			exit 2
		fi
		install_dir=$2
		shift 2
		;;
	--help | -h)
		usage
		exit 0
		;;
	*)
		echo "unknown option: $1" >&2
		usage >&2
		exit 2
		;;
	esac
done

os=$(uname -s)
case "$os" in
Linux) ;;
*)
	echo "twi's install script only supports Linux (got $os)." >&2
	echo "Build from source or run scripts/release-dry-run.sh; see docs/release.md." >&2
	exit 1
	;;
esac

arch=$(uname -m)
case "$arch" in
x86_64 | amd64) goarch=amd64 ;;
aarch64 | arm64) goarch=arm64 ;;
*)
	echo "unsupported architecture: $arch (twi publishes linux amd64/arm64 only)" >&2
	exit 1
	;;
esac

asset="twi_linux_${goarch}"

if [ "$version" = "latest" ]; then
	base_url="https://github.com/${repo}/releases/latest/download"
else
	base_url="https://github.com/${repo}/releases/download/${version}"
fi

fetch() {
	url=$1
	out=$2
	if command -v curl >/dev/null 2>&1; then
		curl --proto '=https' --tlsv1.2 -fsSL "$url" -o "$out"
	elif command -v wget >/dev/null 2>&1; then
		wget -q "$url" -O "$out"
	else
		echo "curl or wget is required to install twi" >&2
		exit 1
	fi
}

verify_checksum() {
	dir=$1
	file=$2
	(
		cd "$dir"
		if command -v sha256sum >/dev/null 2>&1; then
			sha256sum -c "$file.sha256"
		elif command -v shasum >/dev/null 2>&1; then
			shasum -a 256 -c "$file.sha256"
		else
			echo "sha256sum or shasum is required to verify the download" >&2
			exit 1
		fi
	)
}

tmp_dir=$(mktemp -d "${TMPDIR:-/tmp}/twi-install.XXXXXX")
cleanup() {
	rm -rf "$tmp_dir"
}
trap cleanup EXIT HUP INT TERM

echo "downloading $asset ($version)..." >&2
fetch "$base_url/$asset" "$tmp_dir/$asset"
fetch "$base_url/$asset.sha256" "$tmp_dir/$asset.sha256"

echo "verifying checksum..." >&2
verify_checksum "$tmp_dir" "$asset"

mkdir -p "$install_dir"
install -m 0755 "$tmp_dir/$asset" "$install_dir/twi"
echo "installed twi to $install_dir/twi" >&2

add_path_line() {
	rc_file=$1
	if grep -qF "$install_dir" "$rc_file" 2>/dev/null; then
		return 0
	fi
	{
		echo ""
		echo "# added by twi's install.sh"
		echo "export PATH=\"$install_dir:\$PATH\""
	} >>"$rc_file"
	echo "added $install_dir to PATH in $rc_file" >&2
}

case ":$PATH:" in
*":$install_dir:"*) ;;
*)
	add_path_line "$HOME/.bashrc"
	add_path_line "$HOME/.zshrc"
	echo "PATH updated; restart your shell (or run: export PATH=\"$install_dir:\$PATH\") to use twi" >&2
	;;
esac

"$install_dir/twi" --help >/dev/null
echo "twi installed successfully; run 'twi --help' to get started" >&2
