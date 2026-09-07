# ai-mud

A multiplayer dungeon crawl played over **SSH** (with a web interface planned), where an **AI game master** generates and steers the world D&D-style. Written in Go as a learning project.

## Status

**Early scaffolding — not playable yet.** The service skeleton runs: three long-running components start together, shut down cleanly, and the SSH compositor renders a placeholder UI. There is no world, no gameplay, and no AI yet.

| Component      | State                                                              |
| -------------- | ------------------------------------------------------------------ |
| SSH compositor | Working — wish + bubbletea, renders placeholder screen per session |
| HTTP API       | Skeleton — chi router with `/healthz` only                         |
| Game server    | Skeleton — session registry + tick loop, no world state            |
| AI harness | Skeleton — config file + OpenAI-compatible client, GM loop TBD |

### Known issues

- `internal/ui/window.go` has an unused `"fmt"` import that currently **breaks the build**. Delete line 4 to compile.
- SSH sessions are not yet wired to the game server; each connection just renders a static screen.
- `build/compose.yaml` builds `build/unit_test/Dockerfile`, not `build/gamemaster/Dockerfile`.

## Running it

Local:

```sh
go run ./cmd/app/server
```

Docker (from `build/`):

```sh
docker compose up --build
```

Then, from another terminal:

```sh
ssh localhost -p 2525     # game client (press q to quit)
curl localhost:8081/healthz
```

> Ports are temporary: 2525 (SSH) and 8081 (HTTP) are used because 2222/8080 are occupied by other services on this machine. Will move back / make configurable later.

On first SSH connect the server generates a host key at `.ssh/ai-mud_host_key`. Stop the server with Ctrl-C (graceful shutdown of all components).

## Project layout

```
cmd/app/server        entrypoint — starts all components via errgroup
cmd/app/unit_test     scratch playground for experiments (not shipped)
internal/server/ssh   SSH compositor: wish + bubbletea middleware, :2222
internal/httpapi      web API: chi router, :8080
internal/game         world state, tick loop, session registry
internal/ui           bubbletea models rendered over SSH
internal/util         error handling helpers
build/                Dockerfiles + compose files
```

## Architecture

```
                 ┌──────────────────────────────┐
 ssh :2222 ─────▶│ SSH compositor (wish)        │──┐
 http :8080 ────▶│ HTTP API (chi)               │  │  Session iface
                 └──────────────────────────────┘  ├─▶ ┌──────────────────┐
                 ┌──────────────────────────────┐  │   │ game server      │
 (future) ──────▶│ AI harness "game master"     │──┘   │ tick loop, world │
                 └──────────────────────────────┘      └──────────────────┘
```

Every player connection (SSH, web, or the AI acting as game master) attaches to the game server through the same `game.Session` interface, so world logic never depends on the transport. See `docs/architecture.md` for details.
