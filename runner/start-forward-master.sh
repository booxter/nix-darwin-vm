# The runner is the sole writer of stdin. If it dies, terminate the master's
# private process group, including connections a guest client is keeping open.
exec 3<&0
(
    if ! IFS= read -r _; then
        kill -TERM 0
    fi
) <&3 >/dev/null 2>&1 &

exec "$@" </dev/null 3<&-
