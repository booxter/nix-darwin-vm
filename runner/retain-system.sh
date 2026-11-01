#!/bin/sh
set -eu

IFS= read -r system
umask 077
mkdir -p "$HOME/.local/state/nix-darwin-vm"
/nix/var/nix/profiles/default/bin/nix-env \
    --profile "$HOME/.local/state/nix-darwin-vm/system" --set "$system"
