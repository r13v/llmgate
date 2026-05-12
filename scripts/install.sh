#!/usr/bin/env sh
set -eu

RELEASE_URL="${LLMGATE_RELEASE_URL:-https://github.com/r13v/llmgate/releases/download/main}"
PACKAGE_PREFIX="${LLMGATE_PACKAGE_PREFIX:-llmgate-main}"
DRY_RUN=0

usage() {
	cat <<EOF
Usage: install.sh [--dry-run]

Install llmgate from the rolling main prerelease.

Environment:
  LLMGATE_INSTALL_DIR  Install directory (default: /usr/local/bin when writable, otherwise \$HOME/.local/bin)
  LLMGATE_OS           Override detected OS: linux or darwin
  LLMGATE_ARCH         Override detected arch: amd64 or arm64
EOF
}

die() {
	echo "llmgate install: $*" >&2
	exit 1
}

while [ "$#" -gt 0 ]; do
	case "$1" in
		--dry-run)
			DRY_RUN=1
			shift
			;;
		--help|-h)
			usage
			exit 0
			;;
		*)
			die "unknown argument: $1"
			;;
	esac
done

resolve_os() {
	if [ -n "${LLMGATE_OS:-}" ]; then
		case "$LLMGATE_OS" in
			linux|darwin) printf '%s\n' "$LLMGATE_OS" ;;
			*) die "unsupported LLMGATE_OS: $LLMGATE_OS" ;;
		esac
		return
	fi

	uname_s="$(uname -s 2>/dev/null || true)"
	case "$uname_s" in
		Linux) printf '%s\n' "linux" ;;
		Darwin) printf '%s\n' "darwin" ;;
		*) die "unsupported OS: ${uname_s:-unknown}" ;;
	esac
}

resolve_arch() {
	if [ -n "${LLMGATE_ARCH:-}" ]; then
		case "$LLMGATE_ARCH" in
			amd64|arm64) printf '%s\n' "$LLMGATE_ARCH" ;;
			*) die "unsupported LLMGATE_ARCH: $LLMGATE_ARCH" ;;
		esac
		return
	fi

	uname_m="$(uname -m 2>/dev/null || true)"
	case "$uname_m" in
		x86_64|amd64) printf '%s\n' "amd64" ;;
		arm64|aarch64) printf '%s\n' "arm64" ;;
		*) die "unsupported architecture: ${uname_m:-unknown}" ;;
	esac
}

resolve_install_dir() {
	if [ -n "${LLMGATE_INSTALL_DIR:-}" ]; then
		printf '%s\n' "$LLMGATE_INSTALL_DIR"
		return
	fi

	if [ -d /usr/local/bin ] && [ -w /usr/local/bin ]; then
		printf '%s\n' "/usr/local/bin"
		return
	fi

	if [ -z "${HOME:-}" ]; then
		die "HOME is required when /usr/local/bin is not writable"
	fi
	printf '%s\n' "$HOME/.local/bin"
}

download() {
	download_url="$1"
	download_output="$2"

	if command -v curl >/dev/null 2>&1; then
		curl -fsSL "$download_url" -o "$download_output"
	elif command -v wget >/dev/null 2>&1; then
		wget -q "$download_url" -O "$download_output"
	else
		die "curl or wget is required"
	fi
}

sha256_file() {
	sha_file="$1"

	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$sha_file" | awk '{print $1}'
	elif command -v shasum >/dev/null 2>&1; then
		shasum -a 256 "$sha_file" | awk '{print $1}'
	elif command -v openssl >/dev/null 2>&1; then
		openssl dgst -sha256 -r "$sha_file" | awk '{print $1}'
	else
		die "no SHA-256 checksum tool found"
	fi
}

os_name="$(resolve_os)"
arch_name="$(resolve_arch)"
install_dir="$(resolve_install_dir)"
archive_name="$PACKAGE_PREFIX-$os_name-$arch_name.tar.gz"

if [ "$DRY_RUN" -eq 1 ]; then
	cat <<EOF
dry_run=1
release_url=$RELEASE_URL
archive=$archive_name
install_dir=$install_dir
EOF
	exit 0
fi

if ! command -v tar >/dev/null 2>&1; then
	die "tar is required"
fi

tmp_dir="$(mktemp -d 2>/dev/null || mktemp -d -t llmgate)"
cleanup() {
	rm -rf "$tmp_dir"
}
trap cleanup EXIT HUP INT TERM

checksums_path="$tmp_dir/checksums.txt"
archive_path="$tmp_dir/$archive_name"

echo "Downloading checksums from $RELEASE_URL/checksums.txt"
download "$RELEASE_URL/checksums.txt" "$checksums_path"

echo "Downloading $archive_name"
download "$RELEASE_URL/$archive_name" "$archive_path"

expected_sha="$(awk -v name="$archive_name" '$2 == name {print $1; exit}' "$checksums_path")"
if [ -z "$expected_sha" ]; then
	die "checksum entry not found for $archive_name"
fi

actual_sha="$(sha256_file "$archive_path")"
if [ "$actual_sha" != "$expected_sha" ]; then
	die "checksum mismatch for $archive_name"
fi

tar -xzf "$archive_path" -C "$tmp_dir"
binary_path="$tmp_dir/llmgate"
if [ ! -f "$binary_path" ]; then
	die "archive did not contain llmgate"
fi

mkdir -p "$install_dir"
target_path="$install_dir/llmgate"
if command -v install >/dev/null 2>&1; then
	install -m 0755 "$binary_path" "$target_path"
else
	cp "$binary_path" "$target_path"
	chmod 0755 "$target_path"
fi

echo "llmgate installed to $target_path"
case ":${PATH:-}:" in
	*":$install_dir:"*) ;;
	*) echo "Add $install_dir to PATH if llmgate is not found by your shell." ;;
esac
