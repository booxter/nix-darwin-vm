#!/bin/sh
set -eu

IFS= read -r directory
trap 'rm -rf "$directory"' EXIT

sudo -S -p '' -H /bin/sh "$directory/install" --daemon --yes --no-channel-add
