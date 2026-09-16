#!/usr/bin/env bash
# ai-mud dependency installer (macOS host).
#
# Installs everything the stack needs OUTSIDE the docker containers:
#   docker desktop engine (compose) + cactus (the local LLM engine the
#   game master runs through: OpenAI-compatible serve + native tool
#   calling) + expect (the SSH UX smokes) + golangci-lint (the lint CI
#   parity). The Go toolchain and module deps are handled by
#   `build/dev.sh` (or plain `go build ./...`) — Homebrew not required.
set -euo pipefail

say() { printf '\n\033[1;36m==>\033[0m %s\n' "$1"; }

if ! command -v brew >/dev/null 2>&1; then
    say "Homebrew is missing — install it first: https://brew.sh"
    exit 1
fi

say "Cactus (local GM inference: brew tap, MIT — see README credits)"
brew install cactus-compute/cactus/cactus || say "cactus install failed; check https://docs.cactuscompute.com"

say "expect (+tcl) for the SSH UX smokes"
brew install expect

say "golangci-lint (the linter.yaml CI pin is v2.13.1)"
brew install golangci-lint

# Docker Desktop: skip install if the engine is already present
if command -v docker >/dev/null 2>&1; then
    say "docker already present: $(docker --version)"
else
    say "installing docker (needs Docker Desktop app or colima on the PATH)"
    brew install --cask docker
fi

say "optional: pre-warm the default GM model (downloads the CQ4 bundle once)"
say "    cactus serve google/gemma-4-E2B-it --no-cloud-handoff --port 1243"
say "    (or let build/cactus.sh start|stop it — same flags, tracked pid)"

say "done — next: build/dev.sh (deps+vets), build/deploy.sh (the stack)"
