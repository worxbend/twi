#!/usr/bin/env sh
set -eu

usage() {
	cat <<'USAGE'
Usage: scripts/release-dry-run.sh [--out DIR] [--image NAME] [--targets LIST] [--skip-docker]

Build trimmed twi binaries for the supported release targets, emit SHA-256
checksums beside each binary, smoke the native binary, build the Docker image,
and smoke the container. LIST is a space-separated GOOS/GOARCH list.

Environment overrides:
  TWI_RELEASE_DIR       Output directory, default dist/release
  TWI_RELEASE_IMAGE     Docker image tag, default twi:local
  TWI_RELEASE_TARGETS   Target list, default release matrix
  TWI_RELEASE_GO_VERSION Go version passed to Docker, default go.mod toolchain
USAGE
}

script_dir=$(CDPATH= cd "$(dirname "$0")" && pwd)
repo_root=$(CDPATH= cd "$script_dir/.." && pwd)

out_dir=${TWI_RELEASE_DIR:-"$repo_root/dist/release"}
image=${TWI_RELEASE_IMAGE:-"twi:local"}
targets=${TWI_RELEASE_TARGETS:-"linux/amd64 linux/arm64 darwin/amd64 darwin/arm64"}
skip_docker=0

while [ "$#" -gt 0 ]; do
	case "$1" in
		--help|-h)
			usage
			exit 0
			;;
		--out)
			if [ "$#" -lt 2 ]; then
				echo "missing value for --out" >&2
				exit 2
			fi
			out_dir=$2
			shift 2
			;;
		--image)
			if [ "$#" -lt 2 ]; then
				echo "missing value for --image" >&2
				exit 2
			fi
			image=$2
			shift 2
			;;
		--targets)
			if [ "$#" -lt 2 ]; then
				echo "missing value for --targets" >&2
				exit 2
			fi
			targets=$2
			shift 2
			;;
		--skip-docker)
			skip_docker=1
			shift
			;;
		*)
			echo "unknown option: $1" >&2
			usage >&2
			exit 2
			;;
	esac
done

cd "$repo_root"

runtime_dir=$(mktemp -d "${TMPDIR:-/tmp}/twi-release-runtime.XXXXXX")
cleanup() {
	rm -rf "$runtime_dir"
}
trap cleanup EXIT HUP INT TERM

toolchain=$(awk '$1 == "toolchain" { print $2; exit }' go.mod)
if [ -z "$toolchain" ]; then
	echo "go.mod must declare a toolchain for release builds" >&2
	exit 1
fi
go_version=${TWI_RELEASE_GO_VERSION:-"${toolchain#go}"}

export GOTOOLCHAIN=${GOTOOLCHAIN:-auto}
export TERM=${TERM:-xterm-256color}
export XDG_CONFIG_HOME="$runtime_dir/config"
export XDG_CACHE_HOME="$runtime_dir/cache"
export TWI_TWITCH_USERNAME=
export TWI_TWITCH_OAUTH_TOKEN=
export TWI_TWITCH_REFRESH_TOKEN=
export TWI_TWITCH_CLIENT_ID=
export TWI_TWITCH_CLIENT_SECRET=
export TWITCH_USERNAME=
export TWITCH_ACCESS_TOKEN=
export TWITCH_REFRESH_TOKEN=
export TWITCH_CLIENT_ID=
export TWITCH_CLIENT_SECRET=

mkdir -p "$out_dir" "$XDG_CONFIG_HOME" "$XDG_CACHE_HOME"

checksum_file() {
	file=$1
	dir=$(dirname "$file")
	base=$(basename "$file")
	if command -v sha256sum >/dev/null 2>&1; then
		(cd "$dir" && sha256sum "$base" > "$base.sha256")
	elif command -v shasum >/dev/null 2>&1; then
		(cd "$dir" && shasum -a 256 "$base" > "$base.sha256")
	else
		echo "sha256sum or shasum is required to write checksums" >&2
		exit 1
	fi
}

verify_checksum() {
	file=$1
	dir=$(dirname "$file")
	base=$(basename "$file")
	if command -v sha256sum >/dev/null 2>&1; then
		(cd "$dir" && sha256sum -c "$base.sha256")
	else
		(cd "$dir" && shasum -a 256 -c "$base.sha256")
	fi
}

native_goos=$(go env GOOS)
native_goarch=$(go env GOARCH)
native_bin=

for target in $targets; do
	goos=${target%/*}
	goarch=${target#*/}
	if [ "$goos" = "$target" ] || [ -z "$goos" ] || [ -z "$goarch" ]; then
		echo "invalid target $target; expected GOOS/GOARCH" >&2
		exit 2
	fi

	name="twi_${goos}_${goarch}"
	if [ "$goos" = "windows" ]; then
		echo "windows is not a supported release target" >&2
		exit 2
	fi
	bin="$out_dir/$name"

	echo "building $target -> $bin"
	env CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" \
		go build -trimpath -ldflags="-s -w" -o "$bin" ./cmd/twi
	checksum_file "$bin"
	verify_checksum "$bin"

	if [ "$goos" = "$native_goos" ] && [ "$goarch" = "$native_goarch" ]; then
		native_bin=$bin
	fi
done

if [ -n "$native_bin" ]; then
	echo "smoking native binary $native_bin"
	"$native_bin" --help >/dev/null
	"$native_bin" doctor >/dev/null
	"$native_bin" chat --mock --channel example >/dev/null
else
	echo "native target $native_goos/$native_goarch is not in TWI_RELEASE_TARGETS; skipping native binary smoke" >&2
fi

if [ "$skip_docker" -eq 0 ]; then
	if ! command -v docker >/dev/null 2>&1; then
		echo "docker is required unless --skip-docker is set" >&2
		exit 1
	fi

	echo "building Docker image $image with Go $go_version"
	docker build --build-arg "GO_VERSION=$go_version" -t "$image" "$repo_root"

	echo "smoking Docker image $image"
	docker run --rm \
		-e TWI_TWITCH_USERNAME= \
		-e TWI_TWITCH_OAUTH_TOKEN= \
		-e TWI_TWITCH_REFRESH_TOKEN= \
		-e TWI_TWITCH_CLIENT_ID= \
		-e TWI_TWITCH_CLIENT_SECRET= \
		-e TWITCH_USERNAME= \
		-e TWITCH_ACCESS_TOKEN= \
		-e TWITCH_REFRESH_TOKEN= \
		-e TWITCH_CLIENT_ID= \
		-e TWITCH_CLIENT_SECRET= \
		"$image" --help >/dev/null
	docker run --rm \
		-e TWI_TWITCH_USERNAME= \
		-e TWI_TWITCH_OAUTH_TOKEN= \
		-e TWI_TWITCH_REFRESH_TOKEN= \
		-e TWI_TWITCH_CLIENT_ID= \
		-e TWI_TWITCH_CLIENT_SECRET= \
		-e TWITCH_USERNAME= \
		-e TWITCH_ACCESS_TOKEN= \
		-e TWITCH_REFRESH_TOKEN= \
		-e TWITCH_CLIENT_ID= \
		-e TWITCH_CLIENT_SECRET= \
		"$image" doctor >/dev/null
	docker run --rm \
		-e TWI_TWITCH_USERNAME= \
		-e TWI_TWITCH_OAUTH_TOKEN= \
		-e TWI_TWITCH_REFRESH_TOKEN= \
		-e TWI_TWITCH_CLIENT_ID= \
		-e TWI_TWITCH_CLIENT_SECRET= \
		-e TWITCH_USERNAME= \
		-e TWITCH_ACCESS_TOKEN= \
		-e TWITCH_REFRESH_TOKEN= \
		-e TWITCH_CLIENT_ID= \
		-e TWITCH_CLIENT_SECRET= \
		"$image" chat --mock --channel example >/dev/null
else
	echo "skipping Docker build and smoke checks"
fi

echo "release dry-run artifacts:"
find "$out_dir" -maxdepth 1 -type f -name 'twi_*' -print | sort
