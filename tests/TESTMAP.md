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

## Stored API-level smokes: `tests/smokes/`

| File | What it drives |
|---|---|
| `suite_test.go` | `TestSmokeSuite` — registered scenarios against the tool-call surface of the HTTP API (`/api/world/*`): world summary sanity, tool-spawn a hostile group and read it back (name/count/level + bounds), announce, despawn round-trip; Part 3 authored placements: place readback (`place_object_readback`), terrain `POST /terrain` glyph rows + bad-payload 400 (`terrain_edit_applies`), `PATCH` update (`patch_update_replays`), `DELETE` remove + idempotent second removal (`remove_object_roundtrip`). |
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
| `internal/auth/auth_test.go` | auth | templated Provider seam (lookup/create/idempotency), fingerprint derivation, JSON stop-gap store (atomic file writes, key lookup) |
| `internal/auth/accounts_test.go` | auth | memory-mode accounts: auto-account minting, password check, credential conflicts; **`TestLoginStealsKeyBoundToAnotherAccount`** — password login from a device whose fp is bound elsewhere moves the binding (old account stripped, index re-pointed) |
| `internal/storage/storage_test.go` | storage | integration vs real containers (gate `STORAGE_INTEGRATION=1`): generations table round-trip, account CRUD incl. credential conflicts + rename reindexing, same-owner re-attach idempotency, the fp-steal (old account stripped, index re-pointed), unverified reaper list |
| `internal/storage/world_test.go` | storage | integration (`STORAGE_INTEGRATION=1`): world-persistence Part 1 — `worlds` CRUD (name conflict, one-active invariant, cascade deletes), `world_objects` insert/edit/list-by-kind with jsonb payloads, `object_state` upsert + cascade on object delete, `world_snapshots` (save two, latest-wins, delete-rolls-back) |
| `internal/harness/client_test.go` | harness | OpenAI-compatible client against a stubbed server |
| `internal/ui/ui_test.go` | ui | the full user loop: router FSM navigation, route-A auth narration → GameScreen, wizard walk (name validation, class select, confirm, reveal step), movement incl. overlay focus isolation, view render smoke |

Integration runs:

    STORAGE_INTEGRATION=1 go test ./internal/storage -count=1
    go test ./... -count=1

## SSH surface cheat-sheet (what the servers actually require)

- `wish.WithPublicKeyAuth(accept-all)` + `NoClientAuthCallback` returning
  `PartialSuccessError{Next: {PublicKeyCallback, KeyboardInteractiveCallback}}`:
  keyed clients authenticate via publickey without prompting; keyless
  clients need one empty keyboard-interactive reply (smokes send `"\r"`).
- `x/crypto` ≥0.35 for `NoClientAuthCallback`/`PartialSuccessError`
  (module is on v0.53; charm ssh v0.0.0-2025… overlays its pub key
  handler AFTER the ServerConfigCallback config — the callback wins).
