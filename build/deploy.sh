#!/usr/bin/env bash
# Rebuild and deploy the ai-mud game stack (postgres + docdb + gamemaster).
#
# THE GOTCHA TESTMAP WARNS ABOUT: plain `docker compose up -d --build
# gamemaster` can silently REUSE the old image (the build cache lies on
# compose graph stamps). This script builds FIRST (`compose build` — a
# real materialization), verifies the running image is what we just
# built, then brings it up. If the image stamps mismatch, the compose
# up is re-run once.
set -euo pipefail

cd "$(dirname "$0")"
COMPOSE="docker compose -f compose.yaml"

say() { printf '\n\033[1;36m==>\033[0m %s\n' "$1"; }

say "cactus (the GM engine) — the stack needs it only for harness turns"
if [ -f "$HOME/.cactus-serve.pid" ] && kill -0 "$(cat "$HOME/.cactus-serve.pid")" 2>/dev/null; then
    echo "  cactus serve is up (pid $(cat "$HOME/.cactus-serve.pid"))"
else
    echo "  NOTE: cactus serve is NOT running — the harness will log-idle."
    echo "  Start it with: build/cactus.sh start"
fi

say "building the gamemaster image (fresh materialization)"
$COMPOSE build gamemaster

say "bringing the stack up (compose recreates when the image id moved)"
$COMPOSE up -d gamemaster

say "waiting for /healthz"
for i in $(seq 1 30); do
    if curl -sf -m 2 http://127.0.0.1:8081/healthz >/dev/null 2>&1; then
        echo "  healthz ok"
        break
    fi
    sleep 1
done
curl -s -m 3 http://127.0.0.1:8081/healthz && echo

say "deployed"
docker ps --format 'table {{.Names}}\t{{.Status}}' | head -4
