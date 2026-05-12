#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DIST_DIR="${DIST_DIR:-$ROOT_DIR/dist}"
GO_BIN="${GO:-go}"
VERSION="${VERSION:-main}"
COMMIT="${COMMIT:-${GITHUB_SHA:-}}"
DATE="${DATE:-}"
PACKAGE_PREFIX="${PACKAGE_PREFIX:-llmgate-main}"
DRY_RUN=0

if [ -z "$COMMIT" ]; then
	COMMIT="$(git -C "$ROOT_DIR" rev-parse --short=12 HEAD 2>/dev/null || echo unknown)"
fi

if [ -z "$DATE" ]; then
	DATE="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
fi

usage() {
	cat <<EOF
Usage: scripts/package.sh [--dry-run] [--dist DIR]

Build release archives for the rolling main prerelease.

Environment:
  GO          Go command to use (default: go)
  VERSION     Build version (default: main)
  COMMIT      Build commit SHA (default: GITHUB_SHA or git HEAD)
  DATE        Build date (default: current UTC time)
  DIST_DIR    Output directory (default: ./dist)
EOF
}

while [ "$#" -gt 0 ]; do
	case "$1" in
		--dry-run)
			DRY_RUN=1
			shift
			;;
		--dist)
			if [ "$#" -lt 2 ]; then
				echo "missing value for --dist" >&2
				exit 2
			fi
			DIST_DIR="$2"
			shift 2
			;;
		--help)
			usage
			exit 0
			;;
		*)
			echo "unknown argument: $1" >&2
			usage >&2
			exit 2
			;;
	esac
done

TARGETS=(
	"linux/amd64/tar.gz"
	"linux/arm64/tar.gz"
	"darwin/amd64/tar.gz"
	"darwin/arm64/tar.gz"
	"windows/amd64/zip"
	"windows/arm64/zip"
)

required_file() {
	local path="$1"
	if [ ! -f "$path" ]; then
		echo "required file is missing: $path" >&2
		exit 1
	fi
}

required_file "$ROOT_DIR/README.md"
required_file "$ROOT_DIR/LICENSE"

if [ "$DRY_RUN" -eq 1 ]; then
	echo "version=$VERSION"
	echo "commit=$COMMIT"
	echo "date=$DATE"
	for target in "${TARGETS[@]}"; do
		IFS=/ read -r goos goarch format <<<"$target"
		case "$format" in
			tar.gz) extension="tar.gz" ;;
			zip) extension="zip" ;;
			*)
				echo "unsupported archive format: $format" >&2
				exit 1
				;;
		esac
		echo "$goos-$goarch -> $DIST_DIR/$PACKAGE_PREFIX-$goos-$goarch.$extension"
	done
	echo "checksums -> $DIST_DIR/checksums.txt"
	exit 0
fi

if ! command -v "$GO_BIN" >/dev/null 2>&1; then
	echo "go command not found: $GO_BIN" >&2
	exit 1
fi

if ! command -v tar >/dev/null 2>&1; then
	echo "tar is required for Unix release archives" >&2
	exit 1
fi

if ! command -v zip >/dev/null 2>&1; then
	echo "zip is required for Windows release archives" >&2
	exit 1
fi

if [ "$DIST_DIR" = "/" ]; then
	echo "refusing to use / as DIST_DIR" >&2
	exit 1
fi

rm -rf "$DIST_DIR"
mkdir -p "$DIST_DIR"

WORK_DIR="$(mktemp -d)"
cleanup() {
	rm -rf "$WORK_DIR"
}
trap cleanup EXIT

LDFLAGS="-X github.com/r13v/llmgate/internal/version.version=$VERSION -X github.com/r13v/llmgate/internal/version.commit=$COMMIT -X github.com/r13v/llmgate/internal/version.date=$DATE"

for target in "${TARGETS[@]}"; do
	IFS=/ read -r goos goarch format <<<"$target"
	stage="$WORK_DIR/$goos-$goarch"
	mkdir -p "$stage"

	binary="llmgate"
	if [ "$goos" = "windows" ]; then
		binary="llmgate.exe"
	fi

	echo "building $goos/$goarch"
	GOOS="$goos" GOARCH="$goarch" CGO_ENABLED=0 "$GO_BIN" build -trimpath -ldflags "$LDFLAGS" -o "$stage/$binary" ./cmd/llmgate
	chmod 755 "$stage/$binary"
	cp "$ROOT_DIR/README.md" "$stage/README.md"
	cp "$ROOT_DIR/LICENSE" "$stage/LICENSE"

	case "$format" in
		tar.gz)
			archive="$DIST_DIR/$PACKAGE_PREFIX-$goos-$goarch.tar.gz"
			COPYFILE_DISABLE=1 tar -czf "$archive" -C "$stage" "$binary" README.md LICENSE
			;;
		zip)
			archive="$DIST_DIR/$PACKAGE_PREFIX-$goos-$goarch.zip"
			(
				cd "$stage"
				COPYFILE_DISABLE=1 zip -X -q "$archive" "$binary" README.md LICENSE
			)
			;;
		*)
			echo "unsupported archive format: $format" >&2
			exit 1
			;;
	esac
done

checksum_file() {
	local file="$1"
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$file" | awk '{print $1}'
	elif command -v shasum >/dev/null 2>&1; then
		shasum -a 256 "$file" | awk '{print $1}'
	elif command -v openssl >/dev/null 2>&1; then
		openssl dgst -sha256 -r "$file" | awk '{print $1}'
	else
		echo "no SHA-256 checksum tool found" >&2
		exit 1
	fi
}

checksums="$DIST_DIR/checksums.txt"
: >"$checksums"
for archive in "$DIST_DIR"/"$PACKAGE_PREFIX"-*.tar.gz "$DIST_DIR"/"$PACKAGE_PREFIX"-*.zip; do
	[ -e "$archive" ] || continue
	printf "%s  %s\n" "$(checksum_file "$archive")" "$(basename "$archive")" >>"$checksums"
done

echo "wrote release artifacts to $DIST_DIR"
