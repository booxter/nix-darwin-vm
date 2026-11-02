#!/bin/sh
set -eu

IFS= read -r system
sudo -S -p '' -H /bin/sh -c '
    set -eu
    system=$1
    backup=/etc/nix/nix.conf.before-nix-darwin

    restoreConfiguration() {
        if [ ! -e /etc/nix/nix.conf ] && [ -f "$backup" ]; then
            mv "$backup" /etc/nix/nix.conf
        fi
    }

    # Bootstrap modifies the installer configuration to permit uploads.
    # Preserve it before nix-darwin checks and takes ownership of this file.
    if [ -e "$system/etc/nix/nix.conf" ] && [ ! -L /etc/nix/nix.conf ]; then
        mv /etc/nix/nix.conf "$backup"
        trap restoreConfiguration EXIT
    fi

    /nix/var/nix/profiles/default/bin/nix-env \
        --profile /nix/var/nix/profiles/system --set "$system"
    "$system/sw/bin/darwin-rebuild" activate
' sh "$system"
