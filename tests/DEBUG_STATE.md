# DEBUG STATE — session outcome (perf hunt SOLVED + auth hardening)

> This replaces the stale "slow updates / stalled pipeline" handoff. The
> hunt ended 2026-09-10; root cause found, fixed, and verified over a
> real SSH session into the container. Keep the Infrastructure gotchas
> at the bottom — they are still load-bearing.

## Outcome

### The 2-second frame bug — FIXED
- **Root cause**: `GameScreen.apply(r game.Result)` was a **value
  receiver** (`internal/ui/game.go`). In `keyInput` (and every other
  caller) `g.apply(...)` mutated a *discarded copy*. Keystroke results
  (movement, fights, shops, events) never reached the visible model;
  the screen only ever refreshed when the 2s `stateTick` re-pulled
  fresh server state — hence exactly one visible frame per ~2s.
- **Fix**: pointer receiver on `apply`, with a comment explaining the
  trap. Every other suspect was exonerated with evidence (see below).

### Verification
- Key→frame latency over SSH into the container: **24–60 ms**
  (previously 996–2001 ms, stair-stepping exactly with `stateTick`).
  Probe: `tests/tmp/latency.exp` (guest gate → Join World → typed keys,
  measures first matching frame).
- expect tests/smoke/ui_smoke.exp — green (movement, inventory, wizard).
- expect tests/smoke/auth_smoke.exp — green, now 4 paths.
- go test ./... -count=1 — all packages pass.

### How the hunt got there (evidence, now stale but reusable)
- `tests/perf_repro/mini` + `routerflow` cloned the stack and flowed
  perfectly → the wish/bubbletea/router/Goto/Init shape was never the bug.
- `tests/perf_repro/bigflow`: same stack with a 30-row altscreen frame
  + FPS(30) + local key counter → keys rendered in ≤1 ms. Exonerated
  output size, altscreen, FPS.
- `DBGVHASH` (frame hash probes in GameScreen.View) showed the View ran
  *instantly* on every key but returned an identical hash — the frame
  content was stale, not delayed. That pointed at apply().
- Debug instrumentation was fully removed after the finding; grep for
  `DBG` in internal/ returns nothing.

## Also fixed this session (auth)

1. **Accounts silently not persisting.** `NewRouterDirectGame` minted
   into a throwaway `auth.NewAccounts(nil)` (memory), discarding the
   docdb-backed `*auth.Accounts`. Wizard-created accounts vanished on
   reconnect. The diag hook now takes the real accounts; production
   wiring restored to `NewRouterWithAccounts` + `logging.Middleware()`.
   `NewRouterDirectGame` remains as a documented test hook.
2. **Keyed SSH logins never reached the app.** A bare "none" accept
   (`NoClientAuth: true`) ends the handshake before keys are offered —
   every keyed connect arrived as "connected as guest". The older
   `auto-account` smoke match was vacuous (it matched `guest`).
   Fix: `NoClientAuthCallback` answers the none-probe with
   `PartialSuccessError{Next: ...}` re-advertising two lanes in ONE
   auth sequence:
   - publickey (accept-all; key stashed in charm's
     `gliderlabs/ssh.PublicKey` permissions extension so
     `s.PublicKey()` works), and
   - keyboard-interactive (press-enter guest lane).
   Requires x/crypto ≥0.35 APIs; module is on v0.53.
3. **Fingerprint steal on password login.** `AttachCredential` used to
   ErrConflict when the SSH key already belonged to another account,
   so password-login from a previously-keyed device failed with
   "no such account". Now a proven password login MOVES the binding:
   docdb `AttachCredential` strips the credential from the old account
   inside the same tx and re-points the `cred:` index; the memory
   mirror does the same (`stripCredentialLocked`).
   - Unit: `internal/auth` `TestLoginStealsKeyBoundToAnotherAccount`.
   - Integration: `internal/storage` (STORAGE_INTEGRATION=1) covers
     same-owner re-attach idempotency + the steal strip/re-point.
   - e2e: PATH 4 of the auth smoke (keyed login pulls the fp, next
     keyed connect recalls the named account).

## Smoke ecology notes (flakes we learned to read)

- `ui_smoke.exp` movement is now **relative**: it waits out the
  resize-regen burst, captures the settled spawn coords, then asserts
  moving one tile is that coord ±1. The old hardcoded `49,14` broke
  whenever the world re-landed under a different `regenCount`.
- Still flaky if run immediately after another smoke leaves a guest on
  the spawn tile (<30s reap) or mid-regen — spacing runs ~40s+
  (or restarting the container first) keeps them green.
- **expect regex gotcha**: account names from the smoke are
  `Kil<clock>` — capital K. `[a-z0-9-]+` does NOT match `K` in Tcl (it
  does in some greps!) — use `[a-zA-Z0-9-]+` in expect patterns.
- **down-scrolling through expect**: the one-time password must be
  captured from its pane (the scoping in PATH 1), NOT from a `log_user
  1` replay of the whole stream — terrain rows happily match the
  password regex.

## Uncommitted at session end

- Perf fix (ui/game.go apply receiver).
- Auth: two-lane SSH auth (ssh/server.go), steal semantics
  (storage/document.go + auth/accounts.go), DirectGame accounts fix
  (ui/router.go), loginscreen focus fix.
- Render-cache + debounced resize + world `version` (combat.go,
  game.go, world.go).
- Smokes: guest-gate handling + PATH 4 steal in auth_smoke; relative
  movement in ui_smoke.
- `docs/characters-proposal.md` (account→characters refactor draft)
  and the in-chat world-persistence proposal (worlds/world_objects/
  object_state/world_snapshots — not yet file'd).
- KEPT as hooks/probes: `NewRouterDirectGame` (test hook),
  `tests/perf_repro/` (mini, routerflow, bigflow, authcopy), and
  `tests/tmp/` (scratch — safe to wipe; exp scripts reusable).

## Infrastructure gotchas (RECAP — still true)

- The temporary ports are **SSH :2525**, **HTTP :8081** (2222/8080 blocked).
- compose services: `gamemaster` + `postgres` (relational) + `docdb`
  (jsonb document); DSNs via env `POSTGRES_DSN` / `DOCUMENT_DSN`;
  storage tests gate `STORAGE_INTEGRATION=1`.
- `docker compose -f build/compose.yaml up -d --build gamemaster` DOES
  rebuild; **`docker compose start` does NOT** — a bisect run with
  `start` silently reused an old image and poisoned an afternoon.
  ALWAYS `up -d --build` when binaries changed.
- After container rebuilds: `ssh-keygen -R "[localhost]:2525"` or use
  `-o UserKnownHostsFile=/dev/null`.
- `docker compose stop gamemaster` still leaves Docker Desktop holding
  :2525/:8081 — a simultaneous `go run` fails with address-in-use.
  Stop the container AND use a different port for the local variant,
  or release the port first (kill the local listener).
- Auth model: accounts keyed by name in the docdb with a credential
  index (`cred:<type>:<id>` → account). Keyed SSH connections auto-mint
  accounts (`grim-thistle-77`-style); verify is either-or (pubkey or
  password-acked); password login from a keyed device now STEALS the
  fingerprint (see above).
- The world: dynamic dims (`worldState.ww/wh`) resized by
  `CombatView.Resize` from the terminal size, debounced 150ms (one
  regen per burst). `W_SEED` pins terrain creation; resize regenerates
  with `W_SEED + regenCount` (deterministic per n; compose pins
  `W_SEED=20260909`). NOTE: this regen re-spawns *existing* players —
  a known wart; fold into the characters/world-persistence refactor.
- Guest SSH gate: keyless connects now pass through one
  keyboard-interactive prompt ("press enter") before the landing —
  smokes already handle it; interactive users see it once.
