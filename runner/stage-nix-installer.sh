#!/bin/sh
set -eu

umask 077
directory=$(mktemp -d "${TMPDIR:-/tmp}/nix-darwin-vm.XXXXXXXX")
trap 'rm -rf "$directory"' EXIT

cat > "$directory/installer.tar.xz"
# Preserve the store files' permissions despite the private staging umask.
tar -xpJf "$directory/installer.tar.xz" -C "$directory" --strip-components=1
rm "$directory/installer.tar.xz"

printf '%s\n' "$directory"
trap - EXIT
