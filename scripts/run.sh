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

# ShellCheck may not model trap-dispatched functions as reachable.
# shellcheck disable=SC2317,SC2329
cleanup() {
	if [ -n "$TMP_DIR" ]; then
		rm -rf "$TMP_DIR"
	fi
	if [ "$LOCK_HELD" -eq 1 ] && [ -n "$lock_dir" ]; then
		rm -rf "$lock_dir"
	fi
}
trap 'cleanup' EXIT HUP INT TERM

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

json_escape() {
	printf '%s' "$1" | sed 's/\\/\\\\/g; s/"/\\"/g'
}

metadata_value() {
	metadata_key="$1"
	[ -f "$metadata_path" ] || return 1
	awk -v key="\"$metadata_key\"" '
		index($0, key) {
			sub(/^[^:]*:[[:space:]]*"/, "")
			sub(/",[[:space:]]*$/, "")
			sub(/"[[:space:]]*$/, "")
			print
			exit
		}
	' "$metadata_path"
}

valid_installed_command() {
	[ ! -L "$install_path" ] || return 1
	[ -f "$install_path" ] || return 1
	[ -f "$metadata_path" ] || return 1

	metadata_product="$(metadata_value "product" || true)"
	[ "$metadata_product" = "llmgate" ] || return 1

	metadata_channel="$(metadata_value "channel" || true)"
	[ "$metadata_channel" = "$CHANNEL" ] || return 1

	metadata_install_path="$(metadata_value "install_path" || true)"
	[ "$metadata_install_path" = "$install_path" ] || return 1

	expected_binary_sha="$(metadata_value "binary_sha256" || true)"
	is_sha256_hex "$expected_binary_sha" || return 1

	actual_binary_sha="$(sha256_file "$install_path")"
	[ "$actual_binary_sha" = "$expected_binary_sha" ]
}

install_path_is_replaceable() {
	[ ! -L "$install_path" ] || return 1
	if [ ! -e "$install_path" ]; then
		return 0
	fi
	valid_installed_command
}

installed_archive_sha() {
	metadata_value "archive_sha256"
}

can_reopen_tty_for_wizard() {
	[ "$#" -eq 0 ] || return 1
	[ ! -t 0 ] || return 1
	[ -c /dev/tty ] || return 1
	( : </dev/tty ) 2>/dev/null
}

print_path_hint() {
	case ":${PATH:-}:" in
		*":$install_dir:"*) return 0 ;;
	esac
	status "llmgate installed at $install_path"
	status "Add $install_dir to PATH to run llmgate directly."
}

run_installed_command() {
	if [ -n "$TMP_DIR" ]; then
		rm -rf "$TMP_DIR"
		TMP_DIR=""
	fi
	print_path_hint
	if can_reopen_tty_for_wizard "$@"; then
		exec "$install_path" "$@" </dev/tty
	fi
	exec "$install_path" "$@"
}

run_installed_with_status() {
	message="$1"
	shift
	if valid_installed_command; then
		if [ -n "$message" ]; then
			status "$message"
		fi
		run_installed_command "$@"
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

write_metadata() {
	write_archive_sha="$1"
	write_binary_sha="$2"
	metadata_tmp="$state_dir/install.json.$$"
	installed_at="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
	escaped_install_path="$(json_escape "$install_path")"
	escaped_archive_name="$(json_escape "$archive_name")"
	{
		printf '{\n'
		printf '  "schema_version": 1,\n'
		printf '  "product": "llmgate",\n'
		printf '  "channel": "%s",\n' "$CHANNEL"
		printf '  "install_path": "%s",\n' "$escaped_install_path"
		printf '  "archive_name": "%s",\n' "$escaped_archive_name"
		printf '  "archive_sha256": "%s",\n' "$write_archive_sha"
		printf '  "binary_sha256": "%s",\n' "$write_binary_sha"
		printf '  "installed_at": "%s"\n' "$installed_at"
		printf '}\n'
	} >"$metadata_tmp" || {
		rm -f "$metadata_tmp"
		return 1
	}
	mv "$metadata_tmp" "$metadata_path"
}

install_or_update() {
	update_archive_sha="$1"
	UPDATE_ERROR="unknown update error"

	if ! command -v tar >/dev/null 2>&1; then
		UPDATE_ERROR="tar is required"
		return 1
	fi

	if ! install_path_is_replaceable; then
		UPDATE_ERROR="canonical install path is not owned by llmgate: $install_path"
		return 1
	fi

	archive_path="$TMP_DIR/$archive_name"
	extract_dir="$TMP_DIR/extract"
	stage_binary="$install_dir/.llmgate.$$"

	rm -rf "$extract_dir" "$stage_binary"
	mkdir -p "$extract_dir" "$install_dir" "$state_dir" || {
		UPDATE_ERROR="could not create install directories"
		return 1
	}

	if ! download_file "$RELEASE_URL/$archive_name" "$archive_path"; then
		UPDATE_ERROR="could not download $archive_name"
		rm -f "$stage_binary"
		return 1
	fi

	actual_archive_sha="$(sha256_file "$archive_path")"
	if [ "$actual_archive_sha" != "$update_archive_sha" ]; then
		UPDATE_ERROR="checksum mismatch for $archive_name"
		rm -f "$stage_binary"
		return 1
	fi

	if ! tar -xzf "$archive_path" -C "$extract_dir"; then
		UPDATE_ERROR="could not unpack $archive_name"
		rm -f "$stage_binary"
		return 1
	fi

	extracted_binary="$extract_dir/llmgate"
	if [ ! -f "$extracted_binary" ]; then
		UPDATE_ERROR="archive did not contain llmgate"
		rm -f "$stage_binary"
		return 1
	fi

	cp "$extracted_binary" "$stage_binary" || {
		UPDATE_ERROR="could not stage llmgate"
		rm -f "$stage_binary"
		return 1
	}
	chmod 0755 "$stage_binary" || {
		UPDATE_ERROR="could not mark staged llmgate executable"
		rm -f "$stage_binary"
		return 1
	}
	binary_sha="$(sha256_file "$stage_binary")"

	if ! install_path_is_replaceable; then
		UPDATE_ERROR="canonical install path changed before replacement: $install_path"
		rm -f "$stage_binary"
		return 1
	fi

	mv "$stage_binary" "$install_path" || {
		UPDATE_ERROR="could not replace installed llmgate"
		rm -f "$stage_binary"
		return 1
	}

	if ! write_metadata "$update_archive_sha" "$binary_sha"; then
		UPDATE_ERROR="could not write install metadata"
		return 1
	fi

	return 0
}

os_name="$(resolve_os)"
arch_name="$(resolve_arch)"
archive_name="$PACKAGE_PREFIX-$os_name-$arch_name.tar.gz"

if [ -z "${HOME:-}" ]; then
	die "HOME is required"
fi

install_dir="$HOME/.local/bin"
install_path="$install_dir/llmgate"
state_base="${XDG_STATE_HOME:-$HOME/.local/state}"
state_dir="$state_base/llmgate"
metadata_path="$state_dir/install.json"
lock_dir="$state_dir/.lock"

mkdir -p "$state_dir" || die "could not create state directory: $state_dir"

TMP_DIR="$(mktemp -d 2>/dev/null || mktemp -d -t llmgate)"
checksums_path="$TMP_DIR/checksums.txt"

if ! download_file "$RELEASE_URL/checksums.txt" "$checksums_path"; then
	run_installed_with_status "Could not check for updates; running installed llmgate." "$@" || true
	die "could not check for updates and no valid installed llmgate is available"
fi

expected_archive_sha="$(awk -v name="$archive_name" '$2 == name {print $1; exit}' "$checksums_path")"
if ! is_sha256_hex "$expected_archive_sha"; then
	run_installed_with_status "Could not verify latest release; running installed llmgate." "$@" || true
	die "checksum entry not found for $archive_name"
fi

current_archive_sha="$(installed_archive_sha || true)"
if [ "$current_archive_sha" = "$expected_archive_sha" ] && valid_installed_command; then
	run_installed_command "$@"
fi

if ! acquire_update_lock; then
	run_installed_with_status "Could not acquire update lock; running installed llmgate." "$@" || true
	die "could not acquire update lock and no valid installed llmgate is available"
fi

current_archive_sha="$(installed_archive_sha || true)"
if [ "$current_archive_sha" = "$expected_archive_sha" ] && valid_installed_command; then
	release_update_lock
	run_installed_command "$@"
fi

if valid_installed_command; then
	status "Updating llmgate..."
else
	status "Downloading llmgate..."
fi

if ! install_or_update "$expected_archive_sha"; then
	release_update_lock
	run_installed_with_status "Could not update llmgate; running installed llmgate." "$@" || true
	die "could not update llmgate: $UPDATE_ERROR"
fi

release_update_lock

if valid_installed_command; then
	run_installed_command "$@"
fi

die "installed llmgate could not be verified"
