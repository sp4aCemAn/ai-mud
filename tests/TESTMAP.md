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

## Stored API-level smokes: `tests/smokes/`

| File | What it drives |
|---|---|
| `suite_test.go` | `TestSmokeSuite` — registered scenarios against the tool-call surface of the HTTP API (`/api/world/*`): world summary sanity, tool-spawn a hostile group and read it back (name/count/level + bounds), announce, despawn round-trip. |
| `smoketest/` (framework) | `Env` client: targets a live deployed server when `GAME_SMOKE_URL` is set (contract mode — smoke what's deployed), otherwise spins an in-process standalone stack (game + httpapi, memory accounts). `GAME_SMOKE_LIVE_ONLY=1` forbids the standalone path. This is the home of future *stored* smokes — whenever a feature lands, register a smoke here rather than writing another ad-hoc expect script. SSH-level smokes stay in `tests/smoke/*.exp`. |

Run it:

    go test ./tests/smokes -count=1                                # standalone, no server needed
    GAME_SMOKE_LIVE_ONLY=1 GAME_SMOKE_URL=http://127.0.0.1:8081 go test ./tests/smokes -count=1

## Go tests (in-package, committed where the code lives)

| File | Package | Covers |
|---|---|---|
| `internal/game/player_test.go` | game | join idempotency per fingerprint, spawn placement, world-bound clamps, unknown-player moves, stale reaper, Leave |
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
