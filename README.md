# ai-mud

A multiplayer dungeon crawl played over **SSH**, where an **AI game master** observes the world, narrates it, and *builds it* — raising villages, seeding enemy bands on the frontier — while players explore. Written in Go as a learning project.

## Status

**Playable AI-steered world.** Players connect over SSH (or use the HTTP tool surface), land on a world with authored towns, and walk an **infinite chunked plane** — exploring beyond the served map grows it one chunk at a time, and the game master's frontier queue turns exploration into new content. Two local LLMs drive it: a creative persona (LM Studio) and a tool-call interpreter (Cactus).

| Component      | State                                                              |
| -------------- | ------------------------------------------------------------------ |
| SSH compositor | Working — wish + bubbletea, screen-router FSM (landing / auth / wizard / game) |
| Auth           | Working — keyless (one prompt) + SSH-key auto-mint, password reveal/recall, key-steal flows |
| World          | **Infinite plane** — 32×32 chunks generated from splitmix hash noise; growth keeps absolute coords fixed; beach + spawn-ring guarantees on every boot |
| Towns          | Nested rooms — a village `world_objects` row is its own gate (`町` door); enter/exit by walking; in-town NPCs (talks, quests, the innkeeper's stay, the store keep's counter) |
| Stores         | **One shared registry** — global item list + namespaced town-local items (`<town>:<thing>`); `"raise_village"` arrivals come with a real roster (innkeep / storekeep / villager) and a shop wired to the registry |
| Combat         | Working — bump→fight→kill→loot, XP/coins/levels, regen, respawns; quest bounties from NPC talk lines |
| Storage        | Working — relational Postgres (worlds, world_objects, generations ledger, narr_revisions commit chain) + jsonb docdb (accounts) |
| HTTP API       | Tool surface — `/api/world/*` CRUD for placements, terrain, enemies, announcements; the deployed-contract smoke suite runs against it |
| AI harness     | **Two-stage, live** — persona (LM Studio, lore-only) → interpreter (Cactus, native tool-calling) → rails → game verbs; frontier queue triggers turns on exploration, not the clock |

### Known issues

- Character creation still doesn't persist; reconnect joins the fingerprint identity.
- Multiplayer visibility is town-scoped (co-presence dots); the outside map still hides other players.
- The GM's build cadence leans on persona prompt nudges — sometimes a cycle narrates rather than builds (the rails decline what they can't ground).

## Running it

From a fresh machine:

```sh
build/install.sh          brew deps: docker, cactus, expect, golangci-lint
build/cactus.sh start     the tool-call interpreter engine (model downloads once)
build/deploy.sh           builds + boots the docker stack; waits for /healthz
```

Then:

```sh
ssh localhost -p 2525     # play (any key auto-mints an account; keyless guests too)
curl localhost:8081/healthz
```

The dev loop after any change:

```sh
build/dev.sh              # fmt → vet → build → unit tests → integration + deployed smokes
```

> Ports are temporary (2525 SSH / 8081 HTTP — 2222/8080 are occupied on this machine). The full ops doc — scripts, knobs, log locations, packaging — lives in `build/README.md`; the test map in `tests/TESTMAP.md`.

On first SSH connect the server generates a host key at `.ssh/ai-mud_host_key` (kept in a compose volume so clients' `known_hosts` survive rebuilds). Stop with Ctrl-C — all components shut down cleanly.

## The AI game master (how it works)

```
game.Server ──observe (world card, towns, frontier, events)──► harness
   ▲                                                             │
   │  persona turn (LM Studio :1234): decides the beat in LORE   │
   │  ── decision prose ──►  Cactus :1243 (interpreter):          │
   │                         decision → native tool_calls        │
   └── rails apply the verbs ── game verbs: announce, spawn_enemies,
       raise_village ── settlements + bands land at the FRONTIER
       (the dark edge of the explored map — never inside settled country)
```

- **The queue is exploration-driven**: a turn triggers when a player first enters an unseen chunk (the "frontier door"), not on a timer; a settled chunk (≥3 authored entities) goes quiet. Idle world = zero LLM spend.
- **The model never picks coordinates.** The rails resolve `frontier` anchors to real walkable tiles near the player's edge, cap band sizes (1–4 × level 1–3), and announce placements with coordinates (`(at x,y) a band of the gutter lantern stirs…`).
- **Two providers, two jobs**: the persona never sees a tool schema; the interpreter is a no-persona converter (Cloud handoff pinned off — everything local).
- **Every GM turn is audited** into the `generations` table (the model, prompt, and what the rails applied/declined).
- Removal/destructive verbs are deliberately **not on any model-facing tool card** — an admin/tool-ops concern, not the AI's.

## Project layout

```
cmd/app/server        entrypoint — starts all components via errgroup
internal/server/ssh   SSH compositor: wish + bubbletea middleware, :2525
internal/httpapi      tool surface + /healthz, :8081
internal/auth         identity, provider seam, accounts + credentials
internal/game         world state, infinite-plane terrain (terrain/grow),
                      towns (town.go), stores (store.go + item.go),
                      frontier population (frontier.go), GM seam (gm.go),
                      harness verbs, narration commits (talk.go)
internal/ui           screen router FSM + bubbles (camera viewport, overlays)
internal/harness      the two-stage game master loop (loop.go, actions.go)
internal/storage      two Postgres backends; narration commit chain
configs/              harness.yaml (persona/models/cadence) + gm_tools.json (verb card)
build/                compose + scripts (install / dev / deploy / cactus) + ops README
tests/                TESTMAP.md (what guards what), smoke suites, DEBUG_STATE
docs/                 architecture.md, user-loop.md, characters-proposal.md
```

## Architecture

```
                 ┌──────────────────────────────┐
 ssh :2525 ─────▶│ SSH compositor (wish)        │──┐  PlayerView / CombatView
 http :8081 ────▶│ HTTP API (chi, tool surface) │  ├─▶ ┌─────────────────────┐
                 └──────────────────────────────┘  │   │ game server         │
                 ┌──────────────────────────────┐  ├─▶ │ tick loop, infinite │
 LM Studio 1234▶│ harness   persona (lore)      │──┘   │ world, towns, GM    │
 cactus    1243▶│ (its own: tool_calls→verbs)   │      └─────────────────────┘
                 └──────────────────────────────┘
                                │
                    relational Postgres (world/object rows, narration
                    commits, generations ledger) · docdb (accounts)
```

Every client — SSH, HTTP tools, and the AI game master — attaches to the game server through the same interfaces (`game.PlayerView`, `game.CombatView`, the GM seam). World logic never depends on how a client is connected.

Docs:
- `docs/architecture.md` — components, process model, config conventions
- `docs/user-loop.md` — the connect → auth → character → world flow
- `tests/TESTMAP.md` — every test, smoke, and gotcha (the authoritative map)
- `build/README.md` — operating the stack: scripts, env knobs, log locations

## Credits & third-party components

- Local game-master inference is served with
  [Cactus](https://github.com/cactus-compute/cactus)
  (cactus-compute/cactus): `cactus serve` runs the gemma-4-E2B model
  family in Cactus's own quantized (CQ4) backend as an
  OpenAI-compatible server with **native function/tool calling** and
  `--no-cloud-handoff` (fully local, deterministic turns). Cactus is
  MIT-licensed.
- The persona model (`google/gemma-4-e4b`) rides LM Studio's
  OpenAI-compatible server; gemma is provided under the Google/Gemma
  model license terms by HuggingFace's `google/gemma-*` releases.
