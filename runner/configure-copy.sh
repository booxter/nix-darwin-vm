#!/bin/sh
set -eu

# Once nix-darwin owns this file, upload access comes from the VM module.
if [ -L /etc/nix/nix.conf ]; then
    exit 0
fi

sudo -S -p '' /bin/sh -c '
    setting="extra-trusted-users = $1"
    if ! grep -qxF -- "$setting" /etc/nix/nix.conf; then
        printf "\n%s\n" "$setting" >> /etc/nix/nix.conf
        launchctl kickstart -k system/org.nixos.nix-daemon
    fi
' sh "$(id -un)"
