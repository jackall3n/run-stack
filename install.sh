#!/bin/sh
# Installs the latest run-stack release, or upgrades the one already there.
#
#   curl -fsSL https://raw.githubusercontent.com/jackall3n/run-stack/main/install.sh | sh
#
# RUN_STACK_INSTALL_DIR  where the binary goes (default: ~/.local/bin)
# RUN_STACK_VERSION      a release tag such as v0.1.0 (default: the latest)

set -eu

dir="${RUN_STACK_INSTALL_DIR:-$HOME/.local/bin}"
version="${RUN_STACK_VERSION:-latest}"

case "$(uname -s)" in
  Darwin) os=darwin ;;
  Linux) os=linux ;;
  *) echo "run-stack: unsupported system $(uname -s)" >&2; exit 1 ;;
esac

case "$(uname -m)" in
  arm64 | aarch64) arch=arm64 ;;
  x86_64 | amd64) arch=amd64 ;;
  *) echo "run-stack: unsupported processor $(uname -m)" >&2; exit 1 ;;
esac

if [ "$version" = latest ]; then
  base="${RUN_STACK_BASE_URL:-https://github.com/jackall3n/run-stack/releases/latest/download}"
else
  base="${RUN_STACK_BASE_URL:-https://github.com/jackall3n/run-stack/releases/download/$version}"
fi

archive="run-stack_${os}_${arch}.tar.gz"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

curl -fsSL "$base/$archive" -o "$work/$archive"
curl -fsSL "$base/checksums.txt" -o "$work/checksums.txt"

expected="$(awk -v name="$archive" '$2 == name { print $1 }' "$work/checksums.txt")"

if command -v sha256sum >/dev/null 2>&1; then
  actual="$(sha256sum "$work/$archive" | awk '{ print $1 }')"
else
  actual="$(shasum -a 256 "$work/$archive" | awk '{ print $1 }')"
fi

if [ -z "$expected" ] || [ "$expected" != "$actual" ]; then
  echo "run-stack: the download does not match its checksum, nothing installed" >&2
  exit 1
fi

tar -xzf "$work/$archive" -C "$work"
mkdir -p "$dir"
# Moved into place, not copied over: a running proxy keeps the old file until it restarts.
mv -f "$work/run-stack" "$dir/run-stack"

echo "installed run-stack $("$dir/run-stack" version) to $dir/run-stack"

case ":$PATH:" in
  *":$dir:"*) ;;
  *) echo "$dir is not on your PATH. Add it, for example: echo 'export PATH=\"$dir:\$PATH\"' >> ~/.zshrc" ;;
esac
