#!/usr/bin/env bash
# Start / stop / status the local game-master inference engine.
#
#   cactus serve google/gemma-4-E2B-it --no-cloud-handoff --port 1243
#
# Runs on the HOST (the docker containers reach it via
# host.docker.internal:1243). Cloud handoff is PINNED OFF: the GM stays
# local and deterministic — if a turn can't be handled, the harness's
# no-op ladder answers, never a cloud route. Telemetry is off by
# default; no flag flips it here.
set -euo pipefail

PORT="${CACTUS_PORT:-1243}"
MODEL="${CACTUS_MODEL:-google/gemma-4-E2B-it}"
PIDFILE="$HOME/.cactus-serve.pid"
LOG="$HOME/.cactus-serve.log"

case "${1:-}" in
  start)
    if [ -f "$PIDFILE" ] && kill -0 "$(cat "$PIDFILE")" 2>/dev/null; then
      echo "cactus serve already running (pid $(cat "$PIDFILE"))"
      exit 0
    fi
    echo "starting cactus serve: model=$MODEL port=$PORT (log: $LOG)"
    nohup cactus serve "$MODEL" --no-cloud-handoff \
        --host 127.0.0.1 --port "$PORT" --no-access-log >> "$LOG" 2>&1 &
    echo $! > "$PIDFILE"
    sleep 2
    echo "started (pid $(cat "$PIDFILE"))"
    ;;
  stop)
    if [ -f "$PIDFILE" ]; then
      kill "$(cat "$PIDFILE")" 2>/dev/null || true
      rm -f "$PIDFILE"
      echo "cactus serve stopped"
    else
      echo "no pidfile — not launched by this script"
    fi
    ;;
  status)
    if [ -f "$PIDFILE" ] && kill -0 "$(cat "$PIDFILE")" 2>/dev/null; then
      echo "running (pid $(cat "$PIDFILE")) on port $PORT"
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
  *)
    echo "usage: $0 start|stop|status|models|ping"
    echo "  env: CACTUS_PORT=$PORT  CACTUS_MODEL=$MODEL"
    ;;
esac
