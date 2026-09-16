# Test map — where every test lives and what it guards

Go unit/integration tests sit inside their packages (Go requires the
same package to reach unexported state); this folder collects the
end-to-end scripts and the index. If a name here loses its file, grep
the path — nothing is generated.

## CI workflows: `.github/workflows/`

| File | Trigger | Purpose |
|---|---|---|
| `linter.yaml` | push/PR to main | golangci-lint (binary `v2.13.1` — first go1.25-built line was `v2.4.0`; the old `v2.3.1` pin failed with "Go version go1.24 < targeted 1.25.0"). Actions pinned to commit SHAs. |
| `ci.yml` | every push/PR | `build → vet → go test` unit job + a `storage-integration` job with real `postgres:17-alpine` service containers on both ports (schema migrations proven on a clean DB every push). |
| `smoke.yml` | push to main, nightly, manual | builds + boots the compose stack, waits for `/healthz`, runs the stored smoke suite in deployed-contract mode (`GAME_SMOKE_LIVE_ONLY=1`), uploads `/api/world` dump + gamemaster logs on failure. |
| `smoke-expect.yml` | push to main, manual | expect-based UX smokes (`ui_smoke.exp`, `auth_smoke.exp` — retry-once for the spawn-tile race); needs `expect` on the runner. |
| `harness-mock.yml` | every push/PR | boots the real server on real DBs with the mock LLM (`tests/harness_mock` — the no-LLM degradation contract) + the full tool-contract suite. |
| `nightly.yml` | cron 04:00, manual | `go test -race ./...` (with storage integration), `govulncheck`, and a live persisted-world round-trip: SQL-insert world + objects → restart → assert `/api/world` replay. |

Supply-chain: all `uses:` pins are commit SHAs with the release tag as a comment; `.github/dependabot.yml` bumps the pins (and gomod/docker files) weekly.

## End-to-end: `tests/smoke/`

| Script | What it drives |
|---|---|
| `ui_smoke.exp` | Full smoke against a running server on :2525 — both landing routes (Join World → auth narration → GameScreen, and the New Character wizard end-to-end incl. password reveal), movement (RELATIVE assert: waits out the resize-regen burst, captures settled spawn coords, then asserts `j` moves one step), pack open/close. Terminal pinned to `stty_init rows 30 columns 100`. Runs green on a fresh container; spacing repeated runs ~40s avoids the spawn-tile race. |
| `auth_smoke.exp` | The auth loop end-to-end, 4 paths: (1) anonymous wizard → account mint → 25-char one-time password reveal (captured from the reveal pane, not the raw stream) → world entry; quit → log back in with name + captured password; (3) a real SSH key connect → auto-mint + `Play as` recall; (4) password login from the keyed device PULLS the fingerprint from the auto-account (steal), and the next keyed connect recalls the NAMED account. The keyed section mints its own throwaway key at `/tmp/ai_mud_smoke_key` if missing (nothing to install manually); keyless sections pass through the press-enter guest gate. |

Run it:

    expect tests/smoke/ui_smoke.exp
    expect tests/smoke/auth_smoke.exp

Helpers the smokes rely on:

- keyless connects hit one keyboard-interactive prompt ("press enter to
  continue" → "user name") before the landing — both smokes handle it.
- The container must be rebuilt with
  `docker compose -f build/compose.yaml up -d --build gamemaster`
  (`compose start` silently reuses the old image — smoke poisons).
- expect patterns must use `[a-zA-Z0-9-]+`: account names like
  `Kil1789…` start with a capital `K`, and Tcl's `[a-z0-9-]` won't match.

## Perf probes: `tests/perf_repro/`

| Dir | What it proved |
|---|---|
| `mini/` | Standalone wish+bubbletea tick server — the stack flows fine over SSH. |
| `routerflow/` | Clone of the router shape (landing → auth → game, 2s state ticks) with altscreen + FPS — flowed perfectly: the router/Goto/Init plumbing was exonerated in the 2s-frame hunt. |
| `bigflow/` | Same stack with a full-size altscreen frame + FPS(30) + key-driven counter — keys rendered ≤1 ms over SSH, exonerating output size/altscreen/FPS. Env toggles: `ALTSC=1 FPS=30 BIG=1`. The 2s bug was later pinned to `GameScreen.apply`'s value receiver (pointer receiver now); this repro shows exactly the shape that would have caught it. |
| `logs/`, `authcopy/` | Capture logs from the hunt. |

`tests/tmp/` — scratch debug artifacts (server logs, one-off expect
probes: `latency.exp` measures key→frame over SSH; `retuser_all.exp`/
`ret_steal.exp` were the working drafts of the steal smoke). Safe to
wipe; nothing product depends on it.

## World persistence (Part 2: loader)

- `internal/game/persist.go`: `NewServerWorld(WorldSpec)` boots terrain
  from the record's seed and replays authored `world_objects`
  (enemy_groups placed with payload stats, the first village/merchant
  row takes the merchant/keeper dot, kind=edit rows patch terrain
  glyphs post-regen). Resize regenerates terrain from the SAME record
  seed and replays content — players are no longer repositioned unless
  their tile drowned; the render version never rewinds (caches key on
  it).
- `cmd/app/server/main.go`: boot prefers the persisted world row —
  `ActiveWorld` → load → replace the seed-env world; no active world →
  classic `W_SEED` flow.
- Verified live: a row in `worlds` + two `world_objects` came up as the
  served world after restart (`/api/world` shows `spawnX=49 enemyCount=1
  version=3`), and the deploy contract suite + both SSH smokes stayed
  green.
- Tests: `internal/game/persist_test.go` (content replay, merchant
  placement, resize round-trips).

## World persistence (Part 3: tool surface)

- `internal/game/tools.go`: the place/edit/remove verbs —
  `PlaceObject` / `TerrainEdit` / `UpdateObject` / `RemoveObject` /
  `Objects(kinds…)`. A row is written through `WorldStore` FIRST
  (source of truth) then applied live (`applyObject`); fields live in
  the Server (`objects` list, `wstore`, negative `nextEphemeral` ids in
  memory mode so patch/remove stay addressable without SQL).
- Guarantees baked in: anchors re-validated at tool time (in-bounds,
  walkable; edits skip the anchor check and carry `{x,y,g}` deltas);
  an anchor-less placement lands on a region spot and STILL records its
  real coords on the row (`row.HomeX/HomeY` set after landing).
  Removal is idempotent: an already-gone row doesn't block dropping
  the live form, and a second call is a clean not-found. `Resize`/boot
  replay the tool rows like any authored content.
- `events.go` reconciliation: `SpawnEnemyGroup` writes its row too
  (payload `{count,level}`), and `DespawnEnemy` drops the authored row
  with the live group — restarts/resize replays keep tool content.
- HTTP (all under `/api/world`, in `internal/httpapi/tools.go`):
  `GET/POST /objects`, `PATCH/DELETE /objects/{id}`,
  `POST /terrain {name, tiles:[{x,y,g}]}`.
- `cmd/app/server/main.go`: `AttachWorldStore(store.Relational, w.ID)`
  after the loader — memory mode (no DB) keeps tools live-only.
- Verified live (committed-smoke envs): real-DB write-through with
  correct home coords, restart round-trip (tool rows replay), SQL-side
  delete then tool DELETE reconciles without a restart.
- Tests: `internal/game/tools_test.go` (fake store: place/apply,
  terrain edit replay after resize, update+remove round-trip with
  no-double-free, bad-spec rejections, seed-flow stays memory-only) and
  the API smokes in `tests/smokes/` (see below).

## Camera (viewport windowing)

- `internal/ui/game.go`: the field pane is a **camera window** into the
  world, not the whole world. `camX/camY` is the window's top-left; it
  pans only when the player walks out of a `camPad=3` dead-zone next to
  a viewport edge (lazy — never hard-centered, minimal repaint churn),
  clamped to `[0, W-vw] × [0, H-vh]`. Worlds smaller than the pane drop
  back to today's full-block render (cam pinned at 0,0).
- The terrain cache keys on **(version, camX, camY, paneW, paneH)** —
  a pan or pane change re-cuts the slice, a plain move only splices
  `@` on top (same per-frame cost as before). Enemy dots `x` and the
  merchant `$` now bake into the cached slice (WorldView's `Dots`,
  previously produced but never composited).
- Resize decoupling: the world only **grows** to fit a bigger pane
  (`c.Resize(max(field, curW))`); a smaller pane keeps the world and
  pans. Persisted worlds (98×25 live right now) render windowed on
  small terminals instead of being regenerated to fit.
- Tests: `TestCameraPansWithPlayer` (pan on leaving the dead zone,
  wall-clamp ride, lazy mid-pane non-pan, ride-back on the west wall),
  `TestCameraSmallWorldStaysPut` (fully visible world never pans),
  `TestCameraFieldRendersDots` (enemy dot visible in the slice).

## Infinite world (chunked plane)

- `internal/game/terrain.go`: the world is now an **unbounded plane of
  chunks** (`chunkSize=32`). Every tile is a pure function of
  (world seed, absolute coords) — splitmix64 hash noise + bilinear
  coarse lattice (one value per 2×2 tiles) + the same Gaussian blur
  passes and `heightGlyph` thresholds the finite board used. No order
  dependence: chunk N looks identical whenever it's generated, so
  absorbs never take seams apart.
- `internal/game/grow.go`: `absorbInto(nx, ny, dx, dy)` grows the served rect
  in whole chunks per direction. Absolute coordinates NEVER shift (the
  rect moves around the world); preserved tiles are copied verbatim;
  authored terrain edits re-apply on top; the region recomputes to ABS
  (local + origin). The world never shrinks. **Beach guarantee**: a
  landing beach (±2 around the walker's row/col) is forced to land
  through the ENTIRE fresh band along the step direction — chunk-gen
  ocean wall at the seam can no longer pin a walker. **Spawn ring**:
  `landSpawnRing()` runs at EVERY boot path (seed-env, persisted,
  resize-grown) and forces the spawn's 4-neighborhood walkable — the
  centroid pick can land on a 1-tile tongue fenced by lake, which
  blocked the first step of the SSH movement smoke deterministically.
- `internal/game/inf_roam_test.go`: `TestAbsorbBeach` pins the seam
  beach for seeds {1, 42, 20260909, 7727}; natural water inside the
  old rect stays legal terrain (oceans can still exist — roams can be
  legitimately blocked inside a rect, just not seam-pinned).
- `internal/game/player.go`: movement no longer clamps to edges —
  walking past the rect calls `absorbInto` first, then walks (water
  still blocks). `TestMoveExploresPastOldBounds` pins roam behavior.
- `internal/game/combat.go`: `worldState` gained `wx0/wy0` (ABS of
  `tiles[0][0]`), `seed`, `chunks` (genChunk memo), `flat` (debug).
  `World` snapshots gained `OriginX/OriginY`; `WorldSummary` still
  reports the current span.
- `internal/game/persist.go`: `spawnWorldBase(seed,…)` boots any world
  through `genBoard` — the base rect is a slice of the same plane, so
  persisted and seed-env worlds share one generator. `genTerrain` and
  the resize terrain ladder are retired; `Resize` only ever GROWS the
  rect (`regenWorld` keeps player positions; content replays via
  edits). `applyTerrainEdit` is origin-aware.
- `internal/ui/game.go`: `panCamera` works in ABSOLUTE coords now
  (clamp `[OriginX, OriginX+W-vw]`); `worldGlyph` translates absolute
  → rect-local rows — **and no longer fills `'·'` for `x < 0`** (that
  finite-era leftover guard painted a uniform forest wall over every
  negative-absolute column west of the seam; negative coords are legal
  now, only the rect-local bounds decide filler). **Row-width
  normalization** in `buildField`: wide glyphs paint two cells, so
  every row is padded to the pane's cell count — lipgloss's per-row
  centering no longer jitters the map when a wide glyph scrolls past.
- Verified live: deployed container boots the persisted world (120×60
  span served), `ui_smoke.exp` + deployed contract suite green.
- Tests: `TestMoveExploresPastOldBounds` (east+north roam, world grew),
  camera suite rides grew bounds, smoke suites green un-touched.

## Towns (slice 2: nested rooms)

- `internal/game/town.go`: towns are **nested coordinate spaces** built
  from one authored `village` `world_objects` row (seeded `townRows`
  registry at boot replay; the lazy `TownState` builds on first entry
  and stays warm). `buildTown`: walled rect clamped from the row's
  `radius` (never resized), a doorway pair on the west rim (Exit on the
  rim painted `=`, Entry one inside, both land-forced), the row's
  `data.npcs` roster projected to walkable `npc_town` dots.
- `internal/game/player.go`: `Player.townRef` (townID + return coords)
  — **per-player nesting**, no global swap: many players share a town's
  read-only grid, `lockedInteract` dispatches through the town walk and
  the world's `町` step enters only when a village row CLAIMS that tile
  (an unclaimed 町 stays walkable paint, slice 1 semantics intact).
- Exit: only by walking the doorway tile (walk-only; no key-quit).
  Death resets the ref (towns aren't respawn anchors). WorldView (and
  every Result payload via `currentWorld`) serves the TOWN rect for
  town players; co-present players composite as `player_town` dots
  (`+`), town NPCs as `npc_town` (`☺`).
- `WorldSummary(fp)` gained `inTown/townId/townName` (the `/api/world`
  readback scopes by `?fp`), the harness's "who's nested where" view.
- Tests: `town_test.go` (deterministic layout + roster projection,
  enter→exit round-trip restoring EXACT world coords, co-presence dot
  + anti-stacking, fight-pin blocks entering); `TestTownEnterExitRoundtripPlacedFromTool`
  guards the tool-path gap — **runtime-placed villages register and
  paint their 町 door intrinsically** (`paintGate`, re-landed at every
  boot replay too), so a village row is its own door without a second
  authoring edit; `TestGatesSurviveGrowth` pins doors surviving the
  absorb rebuild (the ghost: boot-painted doors vanished when the
  plane grew).

## Slice 3: NPC talks + quests (in `internal/game/talk.go`)

- Bumping a town NPC opens a **talk session** (`Server.talks` keyed by
  fingerprint; the Result channel mirrors fights/shops — `Result.Talk`
  plus the quest book). Lines resolve: **docdb** (`narr:<world>:<town>:<npc>`
  documents via `NarrStore`/`PutNarrDoc`/`NarrDocByKey`; main.go wires
  `store.Document`) → the row payload's per-NPC `convo` entries → the
  **canned pool** (`cannedConvo`, role-flavored — the never-blocking
  degradation layer).
- UI: `g.talk` mirrors the session; the overlay shows the line +
  `[enter] next (n/m) · esc walk away`; the last line may carry a
  quest → the grant lands on the close.
- **Bounties**: `Player.quests` tracks kill-targets; `questKill` hooks
  the combat kill site (progress `n/m` events, completion pays
  coins+xp with level progression through `checkLevel`); one copy per
  title, only matching target names progress.
- Tests: `talk_test.go` — canned fallback flow, payload convo quest
  grant, kill progress/complete with no stray-kill pollution, and
  docdb overrides the payload (fake store). `TestUIDrivesTownDoor`
  (ui) still drives the real door path through the full pane stack.

## Slice 4: the innkeeper's stay (`internal/game/stay.go`)

- Bumping an **innkeep** NPC opens the **standing menu** — no coin moves
  yet: the offer (`Stay.Offer`) shows `a bed takes 5 coins — [enter]
  sleep · [esc] decline`. **[enter]** buys the stay (**5 coins up
  front**), **[esc] declines — nothing was ever spent**. A player short
  on coin keeps nothing (the offer closes with the "you're short"
  line). Taking ANY step clears a standing offer.
- The bought stay runs the sleep sequence on the game tick — a staged
  line every ~2s (5 stages over ~10s, `stayLines` + the opener), then a
  full HP/mana restore. Movement is PINNED while asleep
  (`sleepingLocked`); `esc` cancels mid-sleep (`stay-cancel`) — the
  coins stay spent (the bed was taken), no partial heal.
- State mirrors through `Result.Stay` (`stayMirror`: the running sleep
  wins over the standing offer; the UI's `g.stay` overlay branches on
  `Offer`), cleared server-side on heal/cancel; `Leave` cleans both.
- Tests: `stay_test.go` — the full sequence through the real tick
  (charge → pinned movement → heal to max), the early-ESC path (no
  heal, movement unpinned), and `TestInnkeepBumpBuysTheStay` (the real
  bump dispatch: innkeep opens the standing menu not the talk; accept
  charges; decline never spends; a step dismisses the menu).

## Slice 5: the item registry + the town store (`internal/game/item.go`, `store.go`)

- **Item registry** (`item.go`): one `ItemSpec` shape (`id/name/desc/price/kind/effect/inv`),
  a **global list** (`globalItems` — the four wander-cart goods, ids like
  `potion:dark`), plus **town-local items** merged from the village row's
  `data.items`. The namespace rule is **no-collision**: local ids MUST
  be `<town>:<thing>`; a wrong prefix is rejected at tool time
  (`validateVillageData`, wired into `PlaceObject` + `UpdateObject` —
  a 400-equivalent error), so nothing ever shadows. `buildTown` merges
  local specs into the catalog and drops unknown refs with a WARN
  (a GM typo never blocks a town).
- **Unified public stores** (`store.go`): ONE `Store` type + ONE shared
  buy path (`buyFrom` → registry-resolved specs, the old `switch` index
  buy retired). The boolean per-player `w.shops` flag is retired —
  `Server.stores map[key]*Store` is keyed by the **store** (public area:
  many players browse the same counter concurrently; buys stay
  per-fingerprint) and `Server.storeOf[fp]` holds each player's one
  browsing ref. The world merchant taps the global list; the town
  storekeep bumps through `inTownInteract`'s role dispatch
  (innkeep → stay · loaded storekeep → counter · everyone else · talk).
  Stores are lazy-built once per counter; clearing = esc (`close`) or
  any step leaving the counter tile (same rule as the standing stay
  offer).
- Dot-broker: the storekeep **Dot carries `Wares []string`** — the dot
  IS the broker; buildTown lifts the row refs onto it and the open
  counters materialize from the touched dot (`tshopFor`).
- **Narration commits** (`internal/storage/world.go` `NarrCommits`):
  lore edits are APPEND-ONLY revisions in the relational
  `narr_revisions` table (`rev = tip+1`, `base_rev` links the chain;
  tip read returns the newest payload; nothing overwrites). The village
  row's authored `convo` seeds revision 1 on first talk (`seedNarr`),
  every later harness commit builds on top; restart replays the chain
  from the world record (nothing "goes hot"). main.go wires
  `storage.NewNarrCommits(store.Relational, w.ID)` — the docdb table
  stays free (accounts only).
- Tests: `tstore_test.go` (authored wares counter + local-item
  resolution, empty-counter falls to talk, two players browse the same
  public counter while buys stay per-fingerprint, namespace rejection
  at tool time); `world_test.go` purchase suite rides the refactor
  unchanged; storage `TestIntegrationNarrCommits` (real DB: tip read,
  chain ordering, base_rev link, world-cascade) — gated
  `STORAGE_INTEGRATION=1`; ui `TestUIStoreOverlayInTown` (the full pane
  stack, door oracle pattern: place village → door step → storekeep
  bump → the mounted overlay with global + local lines).
- Registered smokes: `town_store_authored_counter` (row surface:
  place village with `items`/`wares` and read it back) +
  `town_store_namespace_rejected` (unprefixed local item → 400).
  NOTE: the buy itself is SSH/UI-only — the HTTP surface has no play
  path; authoring/readback is what the deployed contract checks.
- **Polish pass (slice 5.5) gotchas charted**: a tool PATCH to a
  village row RETIRES its `town:*` counters (`registerTown` →
  `dropTownStores`;.RemoveObject sweeps too) — the stale counter never
  outlives the row. Death (`respawn`) clears the browsing ref and any
  standing stay offer. The pack's potion order is DERIVED from the
  registry (`PackPotions` — no hidden shadow list). Gear with an
  unresolvable effect is refused with the vendor voice and the coin
  returned (`tstore_test` pins — no silent no-op buys). The UI's
  shop mirror hardens: a Result with a nil Shop unmounts the overlay
  (`TestUIStoreOverlayUnmountsOnClose` — esc + no stale resurrection).
- **Door-squatting rule (bug hot-fixed)**: an enemy dot on a
  town-claimed 町 composites OVER the gate glyph — the door renders as
  the enemy `'x'` while its tile data stays honest 町 (stepping still
  enters, the map lies). Tool-time reject now: a hostile anchor on any
  claimed gate is a 400 (`PlaceObject` checks `tileAt == tileGate &&
  townAt != 0`); boot replay projects pre-dating rows off
  (`persist.go` enemy pass). Smokes: `door_tile_rejects_hostile_anchor`
  + in-package `TestDoorTileRejectsHostileAnchor`.

## Stored API-level smokes: `tests/smokes/`

| File | What it drives |
|---|---|
| `suite_test.go` | `TestSmokeSuite` — registered scenarios against the tool-call surface of the HTTP API (`/api/world/*`): world summary sanity, tool-spawn a hostile group and read it back (name/count/level + bounds), announce, despawn round-trip; Part 3 authored placements: place readback (`place_object_readback`), terrain `POST /terrain` glyph rows + bad-payload 400 (`terrain_edit_applies`), `PATCH` update (`patch_update_replays`), `DELETE` remove + idempotent second removal (`remove_object_roundtrip`); Slice 5: `town_store_authored_counter`, `town_store_namespace_rejected` (row-authoring surface only — the buy is SSH/UI-only). |
| `smoketest/` (framework) | `Env` client: targets a live deployed server when `GAME_SMOKE_URL` is set (contract mode — smoke what's deployed), otherwise spins an in-process standalone stack (game + httpapi, memory accounts). `GAME_SMOKE_LIVE_ONLY=1` forbids the standalone path. This is the home of future *stored* smokes — whenever a feature lands, register a smoke here rather than writing another ad-hoc expect script. SSH-level smokes stay in `tests/smoke/*.exp`. |

Run it:

    go test ./tests/smokes -count=1                                # standalone, no server needed
    GAME_SMOKE_LIVE_ONLY=1 GAME_SMOKE_URL=http://127.0.0.1:8081 go test ./tests/smokes -count=1

## Go tests (in-package, committed where the code lives)

| File | Package | Covers |
|---|---|---|
| `internal/game/player_test.go` | game | join idempotency per fingerprint, spawn placement, world-bound clamps, unknown-player moves, stale reaper, Leave |
| `internal/game/persist_test.go` | game | Part 2 loader: authored content replay (enemy stats, merchant dot from a hostile-terrain anchor, summary agreement), resize replay round-trips |
| `internal/game/tools_test.go` | game | Part 3 verbs against a fake store: place+apply with row write-through, first-friendly-takes-dot, terrain edit + post-resize glyph replay, update/remove round-trip (no double-free on re-run), bad-spec rejections (unknown kind, no name, out-of-bounds/water anchor), seed-flow stays memory-only with negative ephemeral ids |
| `internal/game/world_test.go` | game | terrain generation: main-region reachability over 30 seeds (the no-dead-ends guarantee), water blocking + events, fight lifecycle (bump→fight→kill→loot), death→respawn with purse split, store purchase + close, tick regen |
| `internal/game/tstore_test.go` | game | Slice 5: authored wares counter (global + namespaced local refs resolved on the bumped dot), empty counter falls to the talk pool, public-counter concurrency (two players browse one counter; buys stay per-fingerprint), tool-time namespace rejection |
| `internal/auth/auth_test.go` | auth | templated Provider seam (lookup/create/idempotency), fingerprint derivation, JSON stop-gap store (atomic file writes, key lookup) |
| `internal/auth/accounts_test.go` | auth | memory-mode accounts: auto-account minting, password check, credential conflicts; **`TestLoginStealsKeyBoundToAnotherAccount`** — password login from a device whose fp is bound elsewhere moves the binding (old account stripped, index re-pointed) |
| `internal/storage/storage_test.go` | storage | integration vs real containers (gate `STORAGE_INTEGRATION=1`): generations table round-trip, account CRUD incl. credential conflicts + rename reindexing, same-owner re-attach idempotency, the fp-steal (old account stripped, index re-pointed), unverified reaper list |
| `internal/storage/world_test.go` | storage | integration (`STORAGE_INTEGRATION=1`): world-persistence Part 1 — `worlds` CRUD (name conflict, one-active invariant, cascade deletes), `world_objects` insert/edit/list-by-kind with jsonb payloads, `object_state` upsert + cascade on object delete, `world_snapshots` (save two, latest-wins, delete-rolls-back) |
| `internal/harness/client_test.go` | harness | OpenAI-compatible client against a stubbed server |
| `internal/ui/ui_test.go` | ui | the full user loop: router FSM navigation, route-A auth narration → GameScreen, wizard walk (name validation, class select, confirm, reveal step), movement incl. overlay focus isolation, view render smoke |

Integration runs:

    STORAGE_INTEGRATION=1 go test ./internal/storage -count=1
    go test ./... -count=1

## Towns (slice 1: the gate glyph)

- `internal/game/world.go`: `tileGate = '町'` (machi, the town kanji)
  joins the terrain glyph vocabulary — walkable like everything that
  isn't water; `IsWideGlyph` (game-side, shared with the UI) reports
  the two-cell render width. Today it's paint-only: stepping on it
  walks, nothing reads it as a door yet.
- `internal/ui/game.go`: width-aware field math — `glyphCellWidth` +
  `cellsBefore` translate rune-indexed world tiles to display cells;
  the `@` splice is cell-anchored, and over a wide glyph the `@`
  claims both cells ("@ " two-cell replacement) so rows never shift.
- Tests: `TestGateGlyphWalkableAndPainted` (paint via a `kind=edit`
  row through `PlaceObject`, walk onto it, `@` composites with the
  two-cell claim, row display width stays aligned),
  `TestGlyphCellWidth` (the width table + `IsWideGlyph` agreement).
- Complexity bail-out documented per user note: the width handling is
  ~40 lines total confined to the splice; if more CJK glyphs ever
  arrive, generalize `IsWideGlyph` into a rune-width table

## SSH surface cheat-sheet (what the servers actually require)

- `wish.WithPublicKeyAuth(accept-all)` + `NoClientAuthCallback` returning
  `PartialSuccessError{Next: {PublicKeyCallback, KeyboardInteractiveCallback}}`:
  keyed clients authenticate via publickey without prompting; keyless
  clients need one empty keyboard-interactive reply (smokes send `"\r"`).
- `x/crypto` ≥0.35 for `NoClientAuthCallback`/`PartialSuccessError`
  (module is on v0.53; charm ssh v0.0.0-2025… overlays its pub key
  handler AFTER the ServerConfigCallback config — the callback wins).
