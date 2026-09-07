# ai-mud

A multiplayer dungeon crawl played over **SSH** (with a web interface planned), where an **AI game master** generates and steers the world D&D-style. Written in Go as a learning project.

## Status

**Playable prototype.** The service runs as four long-running components that start together and shut down cleanly. A player can connect over SSH, land on the title screen, join the world (through the templated auth flow), move a character dot around a flat field, and open floating windows (inventory). There is no generated world yet — rooms/terrain and character persistence are the next phases.

| Component      | State                                                              |
| -------------- | ------------------------------------------------------------------ |
| SSH compositor | Working — wish + bubbletea, screen-router FSM (landing / auth / character creation / game) |
| Auth           | Template — `auth.Provider` seam; guest provider active, SSH-key-as-identity later |
| Gameplay       | First loop — movable dot on a bounded field, HP/level/mana panel, floating inventory window; players keyed by fingerprint |
| HTTP API       | Skeleton — chi router with `/healthz` only                         |
| Game server    | Player registry + tick loop + stale reaper; world gen next         |
| AI harness     | Skeleton — config file + OpenAI-compatible client, GM loop TBD     |

### Known issues

- Character creation still dead-ends (no persistence); reconnect always joins as the fingerprint identity, overwriting the character.
- Multi-player awareness: players share the world registry but can't see each other yet (no broadcast to screens).

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
ssh localhost -p 2525     # game client (q on menus, ctrl+c anywhere)
curl localhost:8081/healthz
```

> Ports are temporary: 2525 (SSH) and 8081 (HTTP) are used because 2222/8080 are occupied by other services on this machine. Will move back / make configurable later.

On first SSH connect the server generates a host key at `.ssh/ai-mud_host_key`. Stop the server with Ctrl-C (graceful shutdown of all components).

## Project layout

```
cmd/app/server        entrypoint — starts all components via errgroup
cmd/app/unit_test     scratch playground for experiments (not shipped)
internal/server/ssh   SSH compositor: wish + bubbletea middleware, :2525
internal/httpapi      web API: chi router, :8081
internal/auth         templated auth: Identity, Provider seam, JSON user store
internal/game         world state, tick loop, player registry (fingerprint-keyed)
internal/ui           screen router FSM + reusable kit (Panel/Bar/Overlay)
internal/harness      AI game master: config + OpenAI-compatible client
internal/util         error handling helpers
configs/              harness.yaml (LLM endpoint, model, GM persona)
build/                Dockerfiles + compose files
```

## Architecture

```
                 ┌──────────────────────────────┐
 ssh :2525 ─────▶│ SSH compositor (wish)        │──┐  PlayerView (in-proc for now)
 http :8081 ────▶│ HTTP API (chi)               │  ├─▶ ┌──────────────────┐
                 └──────────────────────────────┘  │   │ game server      │
                 ┌──────────────────────────────┐  │   │ tick loop, world │
 (future) ──────▶│ AI harness "game master"     │──┘   └──────────────────┘
                 └──────────────────────────────┘
```

Every player connection (SSH, web, or the AI acting as game master) will attach to the game server through the same interfaces (`game.PlayerView` for play, `game.Session` for pushed events — the transport wiring is still in-process). World logic never depends on how a client is connected.

Docs:
- `docs/architecture.md` — components, process model, config conventions
- `docs/user-loop.md` — the connect → auth → character → world flow, and where the next phases plug in
