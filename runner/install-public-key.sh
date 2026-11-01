#!/bin/sh
set -eu

umask 077
mkdir -p "$HOME/.ssh"
chmod 700 "$HOME/.ssh"

key=$(cat)
touch "$HOME/.ssh/authorized_keys"
chmod 600 "$HOME/.ssh/authorized_keys"
if ! grep -qxF -- "$key" "$HOME/.ssh/authorized_keys"; then
    printf '%s\n' "$key" >> "$HOME/.ssh/authorized_keys"
fi
