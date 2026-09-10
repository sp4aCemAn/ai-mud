# Test map — where every test lives and what it guards

Go unit/integration tests sit inside their packages (Go requires the
same package to reach unexported state); this folder collects the
end-to-end scripts and the index. If a name here loses its file, grep
the path — nothing is generated.

## End-to-end: `tests/smoke/`

| Script | What it drives |
|---|---|
| `ui_smoke.exp` | Full smoke against a running server on :2525 — both landing routes (Join World → auth narration → GameScreen, and the New Character wizard end-to-end incl. password reveal), movement (RELATIVE assert: waits out the resize-regen burst, captures settled spawn coords, then asserts `j` moves one step), pack open/close. Terminal pinned to `stty_init rows 30 columns 100`. Runs green on a fresh container; spacing repeated runs ~40s avoids the spawn-tile race. |
| `auth_smoke.exp` | The auth loop end-to-end, 4 paths: (1) anonymous wizard → account mint → 25-char one-time password reveal (captured from the reveal pane, not the raw stream) → world entry; quit → log back in with name + captured password; (3) a real SSH key connect → auto-mint + `Play as` recall; (4) password login from the keyed device PULLS the fingerprint from the auto-account (steal), and the next keyed connect recalls the NAMED account. Needs the throwaway key at `/tmp/opencode/mudkey`-style path in the script; keyless sections pass through the press-enter guest gate. |

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

## Go tests (in-package, committed where the code lives)

| File | Package | Covers |
|---|---|---|
| `internal/game/player_test.go` | game | join idempotency per fingerprint, spawn placement, world-bound clamps, unknown-player moves, stale reaper, Leave |
| `internal/game/world_test.go` | game | terrain generation: main-region reachability over 30 seeds (the no-dead-ends guarantee), water blocking + events, fight lifecycle (bump→fight→kill→loot), death→respawn with purse split, store purchase + close, tick regen |
| `internal/auth/auth_test.go` | auth | templated Provider seam (lookup/create/idempotency), fingerprint derivation, JSON stop-gap store (atomic file writes, key lookup) |
| `internal/auth/accounts_test.go` | auth | memory-mode accounts: auto-account minting, password check, credential conflicts; **`TestLoginStealsKeyBoundToAnotherAccount`** — password login from a device whose fp is bound elsewhere moves the binding (old account stripped, index re-pointed) |
| `internal/storage/storage_test.go` | storage | integration vs real containers (gate `STORAGE_INTEGRATION=1`): generations table round-trip, account CRUD incl. credential conflicts + rename reindexing, same-owner re-attach idempotency, the fp-steal (old account stripped, index re-pointed), unverified reaper list |
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
