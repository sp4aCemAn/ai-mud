# Architecture

Target shape of the system, and what exists today.

## Components

### 1. SSH compositor — `internal/server/ssh` (implemented)

The primary way players connect. Built on [wish](https://github.com/charmbracelet/wish), which wraps `crypto/ssh`, with the bubbletea middleware: **every SSH session becomes its own bubbletea program**, so each player gets a live TUI rendered over their connection.

- Listens on `:2525` (temporary — normally `:2222`), host key auto-generated at `.ssh/ai-mud_host_key` on first run
- Connect sequence (`teaSession`): read the client's public key fingerprint → `auth.Identify` (lookup-or-create through the `auth.Provider` seam) → hand the `auth.Identity` to the screen router
- Currently uses the `GuestProvider` (everyone is `guest`); real auth will swap in a store-backed provider and add wish's `PublicKeyHandler` — UI code doesn't change
- The full walkthrough of this flow (screens, transitions, and the future handoff into the game) is in `user-loop.md`

### 2. HTTP API — `internal/httpapi` (skeleton)

chi router on `:8081` (temporary — normally `:8080`, which is taken by a local searxng service). Currently only `/healthz`. This will back the web interface and admin/ops endpoints. The chi router (`NewRouter`) is exported so tests can hit it without opening a port.

### 3. Game server — `internal/game` (skeleton)

Owns all world state and the simulation. Key design decision: **it is transport-agnostic.**

- `game.Session` is the interface any connected client implements (`ID`, `Write`, `Close`) — an SSH connection, a websocket from the web UI, and the AI harness all look identical to the world
- `Attach`/`Detach` manage the session registry; `Broadcast` fans out to everyone
- `Run` is the tick loop (500ms default). All world simulation — movement, combat, spawns, AI-generated events — hangs off `Server.tick`
- World state (rooms, exits, entities, scheduled events) is not designed yet

### 4. AI harness — `internal/harness` + `configs/harness.yaml` (skeleton)

The "game master": prompts an LLM to hallucinate/steer the world and track events.

- **Config file** (`configs/harness.yaml`, path overridable via `$HARNESS_CONFIG`): enabled flag, LLM server URL (defaults to `http://localhost:11434/v1`), model, API key, timeout, sampling params, and the GM persona seed (`system_prompt`)
- **Client** (`internal/harness/client.go`): talks to any **OpenAI-compatible** server — `GET /v1/models`, `POST /v1/responses`, `POST /v1/chat/completions`, `POST /v1/completions`, `POST /v1/embeddings` — with unit tests against a stubbed server
- **Loop** (`Run`): startup pings the endpoint (warns and idles if unreachable — never takes the server down), then the game-master loop is TBD
- **In Docker**: compose mounts `../configs:/configs:ro` so config edits don't require an image rebuild

Design constraint the skeleton sets up: the harness will emit events into the game server through the same interfaces players use, rather than mutating world state directly.

## Process model — `cmd/app/server/main.go`

One OS process, components as goroutines:

- `signal.NotifyContext` turns SIGINT/SIGTERM into context cancellation
- `errgroup.WithContext` runs the three components; **any component that returns an error cancels the context**, tearing down the others
- Each server (SSH, HTTP) installs a goroutine that waits on `ctx.Done()` and calls its own graceful `Shutdown` with a 5s timeout; `ListenAndServe` then returns `ErrServerClosed`, which is treated as success

When adding a component: write `Run(ctx, cfg) error`, add it as another `g.Go(...)` in main.

## Config

Hardcoded defaults for now (ports `2222`/`8080`, host key path), expressed as `Config` structs with `DefaultConfig()` constructors — ready to be overridden by env vars/flags later.

## Layout conventions

- `cmd/` — binaries only; `cmd/app/unit_test` is a scratch playground, not shipped
- `internal/` — all real code, importable only within this module
- `build/` — Dockerfiles and compose files (note: `build/compose.yaml` currently points at `unit_test/Dockerfile`)
- `unit_test/` — older scratch files kept for reference
