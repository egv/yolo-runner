#!/usr/bin/env bash

set -euo pipefail

ACTION="install"
INPUT_OS=""
INPUT_ARCH=""
RELEASE_BASE_URL="https://github.com/egv/yolo-runner/releases/latest/download"
FORCED_INSTALL_DIR=""
CHECKSUM_ARTIFACT=""
CHECKSUM_MANIFEST=""

usage() {
	cat <<'EOF'
Usage:
  ./install.sh [--plan] [--test-checksum <artifact> <manifest>] [--os <Darwin|Linux|Windows>] [--arch <amd64|arm64>] [--release-base <url>] [--install-dir <path>]

  --plan
      Print resolved install plan without performing network calls.

  --test-checksum <artifact> <manifest>
      Validate an artifact against a checksum manifest.
EOF
	exit 1
}

while [[ $# -gt 0 ]]; do
	case "$1" in
		--plan)
			ACTION="plan"
			shift
			;;
		--test-checksum)
			ACTION="test-checksum"
			shift
			if [[ $# -lt 2 ]]; then
				usage
			fi
			CHECKSUM_ARTIFACT=$1
			CHECKSUM_MANIFEST=$2
			shift 2
			;;
		--os)
			shift
			INPUT_OS=${1:-}
			shift
			;;
		--arch)
			shift
			INPUT_ARCH=${1:-}
			shift
			;;
		--release-base)
			shift
			RELEASE_BASE_URL=${1:-}
			shift
			;;
		--install-dir)
			shift
			FORCED_INSTALL_DIR=${1:-}
			shift
			;;
		*)
			usage
		esac
done

canonicalize_os() {
	local os=${1:-}
	case "$os" in
	Linux|linux)
		echo "linux"
		;;
	Darwin|darwin)
		echo "darwin"
		;;
	Windows|windows|MINGW*|MSYS*|CYGWIN*)
		echo "windows"
		;;
	*)
		echo "unsupported"
		;;
	esac
}

canonicalize_arch() {
	local arch=${1:-}
	case "$arch" in
	ax64|x86_64)
		echo "amd64"
		;;
	aarch64|arm64)
		echo "arm64"
		;;
	amd64)
		echo "amd64"
		;;
	*)
		echo "unsupported"
		;;
	esac
}

resolve_platform() {
	local detected_os="$1"
	local detected_arch="$2"

	local os
	local arch
	os=$(canonicalize_os "$detected_os")
	if [[ "$os" == "unsupported" ]]; then
		echo "unsupported OS: $detected_os" >&2
		exit 1
	fi

	arch=$(canonicalize_arch "$detected_arch")
	if [[ "$arch" == "unsupported" ]]; then
		echo "unsupported architecture: $detected_arch" >&2
		exit 1
	fi

	echo "$os $arch"
}

artifact_name() {
	local os="$1"
	local arch="$2"
	local ext="tar.gz"
	if [[ "$os" == "windows" ]]; then
		ext="zip"
	fi
	echo "yolo-runner_${os}_${arch}.${ext}"
}

binary_name() {
	local os="$1"
	if [[ "$os" == "windows" ]]; then
		echo "yolo-runner.exe"
		return
	fi
	echo "yolo-runner"
}

checksum_path() {
	local artifact_path="$1"
	echo "checksums-${artifact_path}.txt"
}

install_dir_for_os() {
	local os="$1"
	local home="${HOME:-/tmp}"
	if [[ -n "$FORCED_INSTALL_DIR" ]]; then
		echo "$FORCED_INSTALL_DIR"
		return
	fi
	if [[ "$os" == "windows" ]]; then
		if [[ -n "${LOCALAPPDATA:-}" ]]; then
			echo "${LOCALAPPDATA}/yolo-runner/bin"
			return
		fi
		echo "$home/.local/bin"
		return
	fi
	echo "$home/.local/bin"
}

build_urls() {
	local os="$1"
	local arch="$2"
	local artifact
	artifact=$(artifact_name "$os" "$arch")
	echo "$RELEASE_BASE_URL/$artifact $RELEASE_BASE_URL/$(checksum_path "$artifact")"
}

plan() {
	local os arch artifact
	read -r os arch < <(resolve_platform "${INPUT_OS:-$(uname -s)}" "${INPUT_ARCH:-$(uname -m)}")
	artifact=$(artifact_name "$os" "$arch")
	read -r artifact_url checksum_url < <(build_urls "$os" "$arch")
	local bin
	bin=$(binary_name "$os")
	local install_dir
	install_dir=$(install_dir_for_os "$os")

	printf 'platform=%s\n' "$os"
	printf 'arch=%s\n' "$arch"
	printf 'artifact=%s\n' "$artifact"
	printf 'artifact_url=%s\n' "$artifact_url"
	printf 'checksum_url=%s\n' "$checksum_url"
	printf 'binary=%s\n' "$bin"
	printf 'install_path=%s\n' "$install_dir/$bin"
}

extract_expected_checksum() {
	local manifest="$1"
	local artifact="$2"
	local artifact_base
	artifact_base=$(basename "$artifact")
	while read -r expected path; do
		if [[ "$path" == "$artifact_base" || "${path##*/}" == "$artifact_base" ]]; then
			echo "$expected"
			return
		fi
	done < "$manifest"
}

verify_checksum() {
	local artifact="$1"
	local manifest="$2"
	if [[ ! -f "$artifact" ]]; then
		echo "artifact not found: $artifact" >&2
		exit 1
	fi
	if [[ ! -f "$manifest" ]]; then
		echo "checksum manifest not found: $manifest" >&2
		exit 1
	fi

	local expected
	expected=$(extract_expected_checksum "$manifest" "$(basename "$artifact")")
	if [[ -z "$expected" ]]; then
		echo "checksum not found for artifact: $(basename "$artifact")" >&2
		exit 1
	fi

	local actual
	if command -v sha256sum >/dev/null 2>&1; then
		actual=$(sha256sum "$artifact" | awk '{print $1}')
	elif command -v shasum >/dev/null 2>&1; then
		actual=$(shasum -a 256 "$artifact" | awk '{print $1}')
	else
		echo "sha256sum utility not found" >&2
		exit 1
	fi

	if [[ "$actual" != "$expected" ]]; then
		echo "checksum mismatch for $artifact" >&2
		exit 1
	fi
}

download() {
	local url="$1"
	local output_path="$2"
	if ! curl -fsSL -o "$output_path" "$url"; then
		echo "download failed: $url" >&2
		exit 1
	fi
}

install_release() {
	local os arch
	read -r os arch < <(resolve_platform "${INPUT_OS:-$(uname -s)}" "${INPUT_ARCH:-$(uname -m)}")
	local artifact
	artifact=$(artifact_name "$os" "$arch")
	local artifact_url checksum_url
	read -r artifact_url checksum_url < <(build_urls "$os" "$arch")
	tmpdir=$(mktemp -d)
	trap '[[ -n "${tmpdir:-}" ]] && rm -rf "${tmpdir:-}"' EXIT

	local artifact_path="$tmpdir/$artifact"
	local checksum_path="$tmpdir/$(checksum_path "$artifact")"
	download "$artifact_url" "$artifact_path"
	download "$checksum_url" "$checksum_path"
	verify_checksum "$artifact_path" "$checksum_path"

	local extract_dir="$tmpdir/extract"
	mkdir -p "$extract_dir"
	if [[ "$os" == "windows" ]]; then
		unzip -q "$artifact_path" -d "$extract_dir"
	else
		tar -xzf "$artifact_path" -C "$extract_dir"
	fi

	local bin_name
	bin_name=$(binary_name "$os")
	local binary
	binary=$(find "$extract_dir" -type f -name "$bin_name" | head -n1)
	if [[ -z "$binary" ]]; then
		echo "expected binary not found in artifact: $bin_name" >&2
		exit 1
	fi

	local target_dir
	target_dir=$(install_dir_for_os "$os")
	mkdir -p "$target_dir"
	mv "$binary" "$target_dir/$bin_name"
	chmod +x "$target_dir/$bin_name"
	echo "installed $bin_name to $target_dir"
}

main() {
	case "$ACTION" in
	plan)
		plan
		;;
	test-checksum)
		if [[ -z "$CHECKSUM_ARTIFACT" || -z "$CHECKSUM_MANIFEST" ]]; then
			usage
		fi
		verify_checksum "$CHECKSUM_ARTIFACT" "$CHECKSUM_MANIFEST"
		;;
	install)
		install_release
		;;
	esac
}

main
