#!/bin/sh
set -eu

profile=/nix/var/nix/profiles/default
daemon=/Library/LaunchDaemons/org.nixos.nix-daemon.plist

if [ ! -e "$profile/bin/nix" ] && [ ! -e "$daemon" ]; then
    exit 0
fi

# SSH can become available before macOS mounts the existing Nix volume.
until [ -x "$profile/bin/nix" ] &&
    "$profile/bin/nix-store" --store daemon --query --hash "$profile" >/dev/null 2>&1; do
    sleep 1
done
