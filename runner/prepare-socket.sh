set -eu

socket=$1
directory=$(dirname "$socket")

mkdir -p "$directory"
chmod 700 "$directory"
if [ -S "$socket" ]; then
    rm "$socket"
fi
