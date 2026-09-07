# ai-mud

A multiplayer dungeon crawl played over **SSH** (with a web interface planned), where an **AI game master** generates and steers the world D&D-style. Written in Go as a learning project.

## Status

**Early scaffolding — not playable yet.** The service skeleton runs: components start together and shut down cleanly. The SSH compositor now has the first real user loop (landing → auth template → character creation), but there is no world, no persistence, and no gameplay yet.

| Component      | State                                                              |
| -------------- | ------------------------------------------------------------------ |
| SSH compositor | Working — wish + bubbletea, screen-router FSM (landing / auth / character creation / game) |
| Auth           | Template — `auth.Provider` seam; guest provider active, SSH-key-as-identity later |
| Gameplay       | First loop — movable dot on a bounded field, HP/level/mana panel, floating inventory window; players keyed by fingerprint |
| HTTP API       | Skeleton — chi router with `/healthz` only                         |
| Game server    | Player registry + tick loop + stale reaper; world gen next         |
| AI harness | Skeleton — config file + OpenAI-compatible client, GM loop TBD |

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
ssh localhost -p 2525     # game client (press q to quit)
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
internal/game         world state, tick loop, session registry
internal/ui           screen router FSM: landing, auth, character wizard
internal/harness      AI game master: config + OpenAI-compatible client
internal/util         error handling helpers
configs/              harness.yaml (LLM endpoint, model, GM persona)
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

Every player connection (SSH, web, or the AI acting as game master) attaches to the game server through the same `game.Session` interface, so world logic never depends on the transport.

Docs:
- `docs/architecture.md` — components, process model, config conventions
- `docs/user-loop.md` — the connect → auth → character → world flow, and where the next phases plug in
