#!/usr/bin/env sh
set -eu

RELEASE_URL="https://github.com/r13v/llmgate/releases/download/main"
PACKAGE_PREFIX="llmgate-main"
CHANNEL="main"

TMP_DIR=""
LOCK_HELD=0
lock_dir=""
UPDATE_ERROR="unknown update error"

status() {
	printf '%s\n' "$*" >&2
}

die() {
	status "llmgate run failed: $*"
	exit 1
}

cleanup() {
	if [ -n "$TMP_DIR" ]; then
		rm -rf "$TMP_DIR"
	fi
	if [ "$LOCK_HELD" -eq 1 ] && [ -n "$lock_dir" ]; then
		rm -rf "$lock_dir"
	fi
}
trap cleanup EXIT HUP INT TERM

resolve_os() {
	uname_s="$(uname -s 2>/dev/null || true)"
	case "$uname_s" in
		Linux) printf '%s\n' "linux" ;;
		Darwin) printf '%s\n' "darwin" ;;
		*) die "unsupported OS: ${uname_s:-unknown}" ;;
	esac
}

resolve_arch() {
	uname_m="$(uname -m 2>/dev/null || true)"
	case "$uname_m" in
		x86_64|amd64) printf '%s\n' "amd64" ;;
		arm64|aarch64) printf '%s\n' "arm64" ;;
		*) die "unsupported architecture: ${uname_m:-unknown}" ;;
	esac
}

download_file() {
	download_url="$1"
	download_output="$2"

	if command -v curl >/dev/null 2>&1; then
		curl -fsSL "$download_url" -o "$download_output"
	elif command -v wget >/dev/null 2>&1; then
		wget -q "$download_url" -O "$download_output"
	else
		return 127
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

is_sha256_hex() {
	case "$1" in
		""|*[!0123456789abcdefABCDEF]*) return 1 ;;
	esac
	[ "${#1}" -eq 64 ]
}

read_first_word() {
	read_path="$1"
	[ -f "$read_path" ] || return 1
	awk 'NR == 1 {print $1; exit}' "$read_path"
}

valid_cache_entry() {
	entry_sha="$1"
	is_sha256_hex "$entry_sha" || return 1

	entry_dir="$cache_dir/$entry_sha"
	entry_binary="$entry_dir/llmgate"
	entry_archive_sha_path="$entry_dir/archive.sha256"
	entry_binary_sha_path="$entry_dir/binary.sha256"

	[ -f "$entry_binary" ] || return 1
	[ -f "$entry_archive_sha_path" ] || return 1
	[ -f "$entry_binary_sha_path" ] || return 1

	stored_archive_sha="$(read_first_word "$entry_archive_sha_path" || true)"
	[ "$stored_archive_sha" = "$entry_sha" ] || return 1

	expected_binary_sha="$(read_first_word "$entry_binary_sha_path" || true)"
	is_sha256_hex "$expected_binary_sha" || return 1

	actual_binary_sha="$(sha256_file "$entry_binary")"
	[ "$actual_binary_sha" = "$expected_binary_sha" ]
}

read_current_sha() {
	read_first_word "$current_path"
}

current_cache_is_valid() {
	current_sha="$(read_current_sha || true)"
	valid_cache_entry "$current_sha"
}

run_cached_entry() {
	run_sha="$1"
	shift
	run_binary="$cache_dir/$run_sha/llmgate"
	if [ -n "$TMP_DIR" ]; then
		rm -rf "$TMP_DIR"
		TMP_DIR=""
	fi
	exec "$run_binary" "$@"
}

run_current_cache_with_status() {
	message="$1"
	shift
	current_sha="$(read_current_sha || true)"
	if valid_cache_entry "$current_sha"; then
		if [ -n "$message" ]; then
			status "$message"
		fi
		run_cached_entry "$current_sha" "$@"
	fi
	return 1
}

acquire_update_lock() {
	attempts=0
	while ! mkdir "$lock_dir" 2>/dev/null; do
		attempts=$((attempts + 1))
		if [ "$attempts" -ge 30 ]; then
			return 1
		fi
		sleep 1
	done
	LOCK_HELD=1
	return 0
}

release_update_lock() {
	if [ "$LOCK_HELD" -eq 1 ]; then
		rm -rf "$lock_dir"
		LOCK_HELD=0
	fi
}

update_cache() {
	update_archive_sha="$1"
	UPDATE_ERROR="unknown update error"

	if ! command -v tar >/dev/null 2>&1; then
		UPDATE_ERROR="tar is required"
		return 1
	fi

	archive_path="$TMP_DIR/$archive_name"
	extract_dir="$TMP_DIR/extract"
	stage_dir="$cache_dir/.stage-$update_archive_sha-$$"
	entry_dir="$cache_dir/$update_archive_sha"

	rm -rf "$extract_dir" "$stage_dir"
	mkdir -p "$extract_dir" "$stage_dir" || {
		UPDATE_ERROR="could not create temporary directories"
		return 1
	}

	if ! download_file "$RELEASE_URL/$archive_name" "$archive_path"; then
		UPDATE_ERROR="could not download $archive_name"
		rm -rf "$stage_dir"
		return 1
	fi

	actual_archive_sha="$(sha256_file "$archive_path")"
	if [ "$actual_archive_sha" != "$update_archive_sha" ]; then
		UPDATE_ERROR="checksum mismatch for $archive_name"
		rm -rf "$stage_dir"
		return 1
	fi

	if ! tar -xzf "$archive_path" -C "$extract_dir"; then
		UPDATE_ERROR="could not unpack $archive_name"
		rm -rf "$stage_dir"
		return 1
	fi

	extracted_binary="$extract_dir/llmgate"
	if [ ! -f "$extracted_binary" ]; then
		UPDATE_ERROR="archive did not contain llmgate"
		rm -rf "$stage_dir"
		return 1
	fi

	chmod 0755 "$extracted_binary" || {
		UPDATE_ERROR="could not mark llmgate executable"
		rm -rf "$stage_dir"
		return 1
	}

	binary_sha="$(sha256_file "$extracted_binary")"
	cp "$extracted_binary" "$stage_dir/llmgate" || {
		UPDATE_ERROR="could not stage llmgate"
		rm -rf "$stage_dir"
		return 1
	}
	chmod 0755 "$stage_dir/llmgate" || {
		UPDATE_ERROR="could not mark staged llmgate executable"
		rm -rf "$stage_dir"
		return 1
	}
	printf '%s  %s\n' "$update_archive_sha" "$archive_name" >"$stage_dir/archive.sha256" || {
		UPDATE_ERROR="could not write archive metadata"
		rm -rf "$stage_dir"
		return 1
	}
	printf '%s  %s\n' "$binary_sha" "llmgate" >"$stage_dir/binary.sha256" || {
		UPDATE_ERROR="could not write binary metadata"
		rm -rf "$stage_dir"
		return 1
	}

	rm -rf "$entry_dir"
	mv "$stage_dir" "$entry_dir" || {
		UPDATE_ERROR="could not replace cache entry"
		rm -rf "$stage_dir"
		return 1
	}

	current_tmp="$cache_dir/.current.$$"
	printf '%s\n' "$update_archive_sha" >"$current_tmp" || {
		UPDATE_ERROR="could not write current cache pointer"
		return 1
	}
	mv "$current_tmp" "$current_path" || {
		UPDATE_ERROR="could not replace current cache pointer"
		rm -f "$current_tmp"
		return 1
	}

	return 0
}

os_name="$(resolve_os)"
arch_name="$(resolve_arch)"
archive_name="$PACKAGE_PREFIX-$os_name-$arch_name.tar.gz"

cache_base="${XDG_CACHE_HOME:-}"
if [ -z "$cache_base" ]; then
	if [ -z "${HOME:-}" ]; then
		die "HOME is required when XDG_CACHE_HOME is not set"
	fi
	cache_base="$HOME/.cache"
fi

cache_dir="$cache_base/llmgate/$CHANNEL/$os_name-$arch_name"
current_path="$cache_dir/current"
lock_dir="$cache_dir/.lock"

mkdir -p "$cache_dir" || die "could not create cache directory: $cache_dir"

TMP_DIR="$(mktemp -d 2>/dev/null || mktemp -d -t llmgate)"
checksums_path="$TMP_DIR/checksums.txt"

if ! download_file "$RELEASE_URL/checksums.txt" "$checksums_path"; then
	run_current_cache_with_status "Could not check for updates; running cached llmgate." "$@" || true
	die "could not check for updates and no valid cached llmgate is available"
fi

expected_archive_sha="$(awk -v name="$archive_name" '$2 == name {print $1; exit}' "$checksums_path")"
if ! is_sha256_hex "$expected_archive_sha"; then
	run_current_cache_with_status "Could not verify latest release; running cached llmgate." "$@" || true
	die "checksum entry not found for $archive_name"
fi

current_sha="$(read_current_sha || true)"
if [ "$current_sha" = "$expected_archive_sha" ] && valid_cache_entry "$current_sha"; then
	run_cached_entry "$current_sha" "$@"
fi

if ! acquire_update_lock; then
	run_current_cache_with_status "Could not acquire update lock; running cached llmgate." "$@" || true
	die "could not acquire update lock and no valid cached llmgate is available"
fi

current_sha="$(read_current_sha || true)"
if [ "$current_sha" = "$expected_archive_sha" ] && valid_cache_entry "$current_sha"; then
	release_update_lock
	run_cached_entry "$current_sha" "$@"
fi

if current_cache_is_valid; then
	status "Updating llmgate..."
else
	status "Downloading llmgate..."
fi

if ! update_cache "$expected_archive_sha"; then
	release_update_lock
	run_current_cache_with_status "Could not update llmgate; running cached llmgate." "$@" || true
	die "could not update llmgate: $UPDATE_ERROR"
fi

release_update_lock

if valid_cache_entry "$expected_archive_sha"; then
	run_cached_entry "$expected_archive_sha" "$@"
fi

die "updated cache entry could not be verified"
