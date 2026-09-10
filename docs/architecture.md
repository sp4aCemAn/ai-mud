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

### 3. Game server — `internal/game` (first gameplay loop)

Owns all world state and the simulation. Key design decision: **it is transport-agnostic.**

- `game.PlayerView` is the narrow interface the UI plays through (`Join`/`State`/`Move`) — implemented by the in-process `*Server` today, swap-able for a networked client or a fake in tests
- `game.Session` is the interface for *pushed* events (`ID`, `Write`, `Close`) — an SSH connection, a websocket, and the AI harness all look identical to the world (screens don't consume pushed events yet)
- Players are keyed by SSH key fingerprint ("" = the shared anonymous guest). `Join` is idempotent (reconnect reuses the body); the tick loop **reaps players silent for 30s** — the stand-in for disconnect detection
- The pre-landgen world is a flat bounded field (`WorldW x WorldH`); `Move` clamps to bounds
- `Run` is the tick loop (500ms default). All simulation — movement validation today, combat, spawns, AI-generated events later — hangs off `Server.tick`
- The world is one bounded ASCII field (40×12) with blurred-noise terrain — blank-space water is impassable. Enemy groups and the merchant render as dots on the largest connected land region (`internal/game/world.go`), so nothing is unreachable
- Turn-based combat with no dead ends: bump an enemy dot to open a duel ([a]ttack / [c]ast / [f]lee); death respawns at spawn with half the purse; a fleeing pack closes or it bites. Kills pay coins/XP (+ the occasional potion), XP levels the body up
- `Old Marren`, the merchant NPC, opens a store overlay when walked into: potions, draughts, sword (+atk), shroud (+def)
- The tick loop (500ms) also regenerates players (1 HP / 4s, 1 mana / 8s) and respawns dead enemy groups after 60s. `W_SEED` pins world generation for reproducible smoke tests

### 4. AI harness — `internal/harness` + `configs/harness.yaml` (skeleton)

The "game master": prompts an LLM to hallucinate/steer the world and track events.

- **Config file** (`configs/harness.yaml`, path overridable via `$HARNESS_CONFIG`): enabled flag, LLM server URL (defaults to `http://localhost:11434/v1`), model, API key, timeout, sampling params, and the GM persona seed (`system_prompt`)
- **Client** (`internal/harness/client.go`): talks to any **OpenAI-compatible** server — `GET /v1/models`, `POST /v1/responses`, `POST /v1/chat/completions`, `POST /v1/completions`, `POST /v1/embeddings` — with unit tests against a stubbed server
- **Loop** (`Run`): startup pings the endpoint (warns and idles if unreachable — never takes the server down), then the game-master loop is TBD
- **In Docker**: compose mounts `../configs:/configs:ro` so config edits don't require an image rebuild

Design constraint the skeleton sets up: the harness will emit events into the game server through the same interfaces players use, rather than mutating world state directly.

### 5. Storage — `internal/storage` (wired: main connects before the errgroup)

Two **PostgreSQL** containers, one engine, two data models:

| Backend | Compose service | Model | Holds | Interface |
|---|---|---|---|---|
| Relational | `postgres` (:5432) | schema + SQL | AI generation records (`generations` table), world records later | `RelationalStore` — `SaveGeneration` / `RecentGenerations` |
| Document | `docdb` (:5433) | **jsonb documents** | auth/account docs (`documents` table) — payload changes never need a migration | `DocumentStore` — `SaveUser` / `UserByKey` |

- One engine + one driver (`pgx/v5`) for both: the relational side is classic schema'd data; the document side stores whole documents as `jsonb` keyed by identity (the SSH fingerprint today), upserted via `ON CONFLICT`
- `storage.Connect` (in main, before other components) retries while containers boot, runs both migrations, and **fails startup if unreachable** — infrastructure that exists must work, unlike optional components such as the harness
- Consumers program against `RelationalStore`/`DocumentStore`; the AI harness will write generation records via the former, and auth will migrate off the JSON stop-gap onto the latter
- DSNs: `POSTGRES_DSN` / `DOCUMENT_DSN` env (compose sets service-name hosts; local defaults are `localhost:5432` / `localhost:5433`)
- Integration tests run against the real containers: `STORAGE_INTEGRATION=1 go test ./internal/storage`

## Process model — `cmd/app/server/main.go`

One OS process, components as goroutines:

- `signal.NotifyContext` turns SIGINT/SIGTERM into context cancellation
- `errgroup.WithContext` runs the four components (SSH, HTTP, game loop, harness); **any component that returns an error cancels the context**, tearing down the others
- Each server (SSH, HTTP) installs a goroutine that waits on `ctx.Done()` and calls its own graceful `Shutdown` with a 5s timeout; `ListenAndServe` then returns `ErrServerClosed`, which is treated as success
- The game server is constructed in main and passed into the SSH server — screens play through it via `game.PlayerView`

When adding a component: write `Run(ctx, cfg) error`, add it as another `g.Go(...)` in main.

## Config

Server ports and the host-key path are hardcoded defaults (`Config` structs with `DefaultConfig()` constructors) — currently `:2525` (SSH) and `:8081` (HTTP), temporary until 2222/8080 free up on this machine; ready to be env/flag-overridden later.

The harness is file-configured: `configs/harness.yaml`, path overridable via `$HARNESS_CONFIG`, with `$HARNESS_BASE_URL` / `$HARNESS_MODEL` env overrides (compose uses those to aim the containerized harness at the host's LM Studio via `host.docker.internal`).

## Layout conventions

- `cmd/` — binaries only; `cmd/app/unit_test` is a scratch playground, not shipped
- `internal/` — all real code, importable only within this module
- `configs/` — runtime config files; mounted read-only into the container at `/configs`
- `data/` — local runtime data (JSON user store), gitignored
- `build/` — Dockerfiles and compose files
- `unit_test/` — older scratch files kept for reference
