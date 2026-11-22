set -eu
umask 077

guest_path=$1
mapping=$2
generation=$3
temp_dir=$(/usr/bin/getconf DARWIN_USER_TEMP_DIR)
socket_dir=${temp_dir%/}/vmf-$mapping
current=$socket_dir/current
listener=$socket_dir/$generation

require_private_directory() {
    if [ -L "$1" ] || [ ! -O "$1" ] || [ "$(/usr/bin/stat -f %Lp "$1")" != 700 ]; then
        echo "guest forwarding directory is not private: $1" >&2
        exit 1
    fi
}

require_private_directory "$temp_dir"
if mkdir "$socket_dir" 2>/dev/null; then
    printf '%s' "$guest_path" > "$socket_dir/path"
fi
require_private_directory "$socket_dir"
if ! printf '%s' "$guest_path" | cmp -s - "$socket_dir/path"; then
    echo "guest forwarding directory belongs to a different path: $socket_dir" >&2
    exit 1
fi

# Leave the configured path in place. Only the link inside our private
# directory changes when a new master takes over.
mkdir -p "$(dirname "$guest_path")"
if [ ! -e "$guest_path" ] && [ ! -L "$guest_path" ]; then
    ln -s "$current" "$guest_path"
fi
if [ "$(readlink "$guest_path")" != "$current" ]; then
    echo "guest forwarding path already in use: $guest_path" >&2
    exit 1
fi

# Each sshd binds a fresh listener. A disconnected sshd may still be alive.
previous=$(readlink "$current" || true)
ln -s "$listener" "$listener.link"
mv -f "$listener.link" "$current"
if [ "${previous%/*}" = "$socket_dir" ] && [ -S "$previous" ]; then
    rm "$previous"
fi
printf '%s' "$listener"
