# shellcheck shell=bash
#
# asroot.sh — run commands as root on the host.
#
#   V370_ROOT=sudo    (default) use sudo
#   V370_ROOT=docker  use a throwaway privileged container that enters the
#                      host's namespaces (nsenter -t 1). For hosts where sudo
#                      needs a password but the user is in the docker group
#                      (which is root-equivalent anyway). Nothing is installed.
#
# as_root CMD...            run CMD as root, in the foreground
# as_root_bg PIDFILE CMD... start CMD as root in the background; the root-side
#                           PID is written to PIDFILE (a host path)
# as_root_bg_out PIDFILE OUT CMD...  same, with CMD's stdout written to OUT
# as_root_kill SIG PIDFILE  send SIG to the process started by as_root_bg*

V370_ROOT="${V370_ROOT:-sudo}"
V370_ROOT_IMAGE="${V370_ROOT_IMAGE:-debian:bookworm-slim}"

as_root() {
    case "$V370_ROOT" in
        sudo)
            sudo "$@" ;;
        docker)
            docker run --rm -i --privileged --pid=host --net=host "$V370_ROOT_IMAGE" \
                nsenter -t 1 -m -u -i -n -p -- "$@" ;;
        *)
            echo "Error: V370_ROOT must be 'sudo' or 'docker'" >&2; return 2 ;;
    esac
}

# as_root_bg_out PIDFILE OUTFILE CMD... like as_root_bg, stdout → OUTFILE
# (opened by the root shell, so it works even if CMD drops privileges)
as_root_bg_out() {
    local pidfile outfile
    pidfile="$(realpath -m "$1")"; outfile="$(realpath -m "$2")"; shift 2
    _as_root_bg "$pidfile" "$outfile" "$@"
}

as_root_bg() {
    local pidfile
    pidfile="$(realpath -m "$1")"; shift
    _as_root_bg "$pidfile" /dev/stdout "$@"
}

_as_root_bg() {
    local pidfile="$1" outfile="$2"; shift 2
    rm -f "$pidfile"
    # Paths must be absolute: in docker mode the command runs with cwd "/".
    # shellcheck disable=SC2016
    as_root sh -c 'p="$1"; o="$2"; shift 2; echo $$ > "$p"; exec "$@" > "$o"' sh "$pidfile" "$outfile" "$@" &
    local i
    for i in $(seq 1 50); do
        [ -s "$pidfile" ] && return 0
        sleep 0.1
    done
    echo "Error: background root command did not start: $*" >&2
    return 1
}

as_root_kill() {
    local sig="$1" pidfile
    pidfile="$(realpath -m "$2")"
    [ -s "$pidfile" ] || return 0
    as_root kill "-$sig" "$(cat "$pidfile")" 2>/dev/null || true
}
