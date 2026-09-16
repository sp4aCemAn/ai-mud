#!/usr/bin/env bash
# The developer loop: format, vet, build, unit tests, and integration
# tests (STORAGE_INTEGRATION against the live containers when they're
# up). This is the TESTMAP.md chart's verification stack, scriptified:
#
#     go test ./... -count=1
#     STORAGE_INTEGRATION=1 go test ./internal/storage -count=1
#     GAME_SMOKE_LIVE_ONLY=1 GAME_SMOKE_URL=... go test ./tests/smokes
set -euo pipefail

cd "$(dirname "$0")/.."
say() { printf '\n\033[1;36m==>\033[0m %s\n' "$1"; }

say "gofmt (unreadable diffs are CI failures too)"
unformatted=$(gofmt -l internal cmd tests 2>/dev/null || true)
if [ -n "$unformatted" ]; then
    echo "dirty:"; echo "$unformatted"
    gofmt -w $unformatted
    echo "(formatted in place — re-commit)"
fi

say "vet"
go vet ./...

say "build"
go build ./...

say "unit tests (go test ./... -count=1)"
go test ./... -count=1 2>&1 | grep -v "no test files" || {
    echo "unit suite failed"; exit 1
}

if curl -sf -m 2 http://127.0.0.1:8081/../ >/dev/null 2>&1 || true; then
    : # (the world DBs' availability is what matters below)
fi
if docker ps --format '{{.Names}}' 2>/dev/null | grep -q build-postgres-1; then
    say "storage integration vs the live containers"
    STORAGE_INTEGRATION=1 go test ./internal/storage -count=1
fi
if curl -sf -m 2 http://127.0.0.1:8081/healthz >/dev/null 2>&1; then
    say "deployed contract suite (the live stack)"
    GAME_SMOKE_LIVE_ONLY=1 GAME_SMOKE_URL=http://127.0.0.1:8081 \
        go test ./tests/smokes -count=1
else
    say "smokes standalone (no live stack detected)"
    go test ./tests/smokes -count=1
fi

say "green."
