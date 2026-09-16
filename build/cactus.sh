#!/usr/bin/env bash
# Start / stop / status the local game-master inference engine.
#
#   cactus serve google/gemma-4-E2B-it --no-cloud-handoff --port 1243
#
# Runs on the HOST (never in a container — it wants the machine's own
# Metal/CPU kernels). Docker/Podman containers reach it via
# host.docker.internal / host.containers.internal on the configured
# port.
#
# CLOUD HANDOFF IS PINNED OFF: the GM stays local and deterministic —
# if a turn can't be handled the harness's no-op ladder answers, never
# a cloud route. Telemetry is off by default; no flag flips it here.
#
# Portable across macOS and Linux (POSIX shell + curl only):
#   macOS: brew install cactus-compute/cactus/cactus   (see build/install.sh)
#   Linux: see https://docs.cactuscompute.com (source build: `./setup` &
#   `cactus build --python` on Ubuntu/Debian needs python3.12 + cmake)
set -euo pipefail

PORT="${CACTUS_PORT:-1243}"
# The bind address: 127.0.0.1 is what the docker-desktop / podman
# desktop (macOS) gateways reach. On LINUX containers reaching the host
# bridge interface, use CACTUS_HOST=0.0.0.0 (podman's
# host.containers.internal resolves to the host's bridge gateway).
HOST="${CACTUS_HOST:-127.0.0.1}"
MODEL="${CACTUS_MODEL:-google/gemma-4-E2B-it}"
PIDFILE="${CACTUS_PIDFILE:-$HOME/.cactus-serve.pid}"
LOG="${CACTUS_LOG:-$HOME/.cactus-serve.log}"

# find the cactus binary (brew puts it in /opt/homebrew/bin on arm64,
# /usr/local/bin on x64 macs, $HOME/go/bin or $HOME/.local/bin elsewhere)
find_cactus() {
    if command -v cactus >/dev/null 2>&1; then
        command -v cactus
    elif [ -x /opt/homebrew/bin/cactus ]; then
        echo /opt/homebrew/bin/cactus
    elif [ -x /usr/local/bin/cactus ]; then
        echo /usr/local/bin/cactus
    else
        echo ""
    fi
}

running_pid() {
    # a pid we own that's alive
    if [ -f "$PIDFILE" ]; then
        local pid
        pid="$(cat "$PIDFILE" 2>/dev/null || true)"
        if [ -n "$pid" ] && kill -0 "$pid" 2>/dev/null; then
            echo "$pid"
        fi
    fi
}

case "${1:-}" in
  start)
    self="$(basename "$0")"
    if pid="$(running_pid)"; then
        if [ -n "$pid" ]; then
            echo "cactus serve already running (pid $pid)"
            exit 0
        fi
    fi
    CA="$(find_cactus)"
    if [ -z "$CA" ]; then
        echo "cactus binary not found on the PATH — install it first:"
        echo "  macOS: brew install cactus-compute/cactus/cactus"
        echo "  Linux: see https://docs.cactuscompute.com/latest/docs/quickstart/"
        exit 1
    fi
    echo "starting cactus serve: model=$MODEL bind=$HOST:$PORT (log: $LOG)"
    nohup "$CA" serve "$MODEL" --no-cloud-handoff \
        --host "$HOST" --port "$PORT" --no-access-log >> "$LOG" 2>&1 &
    echo $! > "$PIDFILE"
    # the bundle downloads on the first start (~2.5G) — poll the port
    # up to ten minutes for /v1/models before declaring victory
    echo -n "waiting for the engine"
    i=0
    while ! curl -sf -m 2 "http://127.0.0.1:${PORT}/v1/models" >/dev/null 2>&1; do
        sleep 3; i=$((i+1))
        if [ $i -gt 120 ]; then
            echo; echo "did not answer in 10 minutes — check $LOG"
            exit 1
        fi
        echo -n "."
    done
    echo " up (pid $(cat "$PIDFILE"))"
    $0 ping
    ;;
  stop)
    pid="$(running_pid)"
    if [ -n "$pid" ]; then
        kill "$pid" 2>/dev/null || true
        rm -f "$PIDFILE"
        echo "cactus serve stopped"
    elif command -v pgrep >/dev/null 2>&1 && pgrep -f "cactus serve" >/dev/null; then
        # started outside this script (brew services, bare nohup) — stop
        # it by name so the operator doesn't need our pidfile
        pkill -f "cactus serve" 2>/dev/null || true
        echo "cactus serve (untracked) stopped"
    else
        echo "not running"
    fi
    ;;
  status)
    pid="$(running_pid)"
    if [ -n "$pid" ]; then
        echo "running (pid $pid) on ${HOST}:${PORT}"
    else
        echo "not running"
    fi
    ;;
  models)
    curl -s -m 5 "http://127.0.0.1:${PORT}/v1/models"
    echo
    ;;
  ping)
    curl -s -m 60 -X POST "http://127.0.0.1:${PORT}/v1/chat/completions" \
      -H 'Content-Type: application/json' \
      -d '{"model":"gemma-4-e2b-it-cq4","messages":[{"role":"user","content":"reply with the single word ok"}],"max_tokens":5}' \
      | head -c 300
    echo
    ;;
  log)
    tail -n "${2:-40}" "$LOG" 2>/dev/null || echo "no log yet"
    ;;
  *)
    echo "usage: $0 start|stop|status|models|ping|log [n lines]"
    echo "  env:  CACTUS_PORT=$PORT  CACTUS_HOST=$HOST  CACTUS_MODEL=$MODEL"
    echo "        CACTUS_PIDFILE=$PIDFILE  CACTUS_LOG=$LOG"
    ;;
esac
