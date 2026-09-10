# DEBUG STATE — handoff for a fresh session (the "slow updates / stalled pipeline" hunt)

## Symptom we're chasing
Player reports the game is extremely sluggish: **~1 frame per 3 seconds**. Key presses
should render instantly. Everything else (build, tests, smokes) is green; the bug only
shows over a real SSH session into the container.

## Where the hunt stands (as of handoff)

### What we ruled out (with evidence)
- **The bubbletea/wish stack is healthy.** Standalone repro `tests/perf_repro/mini/main.go`
  (plain wish server, tick at 1s, altscreen+FPS options) ticks perfectly over SSH: t=0..5.
- **The router/value-receiver + Goto transition shape is fine.**
  `tests/perf_repro/routerflow/main.go` clones our two-screen router (landing → auth
  narration → game → 2s state ticks) with altscreen + logging middleware: narration ticks
  flow, transition fires, game state ticks fire at the 2s cadence. It has the same
  NoClientAuth callback + PublicKeyAuth-accept-all + logging middleware.
- **Altscreen/FPS options are not the cause** (repro uses them and works; bisect runs with
  them off behave the same).
- **Deadlock ruled out**: SIGQUIT goroutine dump shows no goroutine blocked in our code —
  every internal frame is a legit wait (select/chan-receive). The bubbletea eventLoop
  waits for messages; nothing arrived — see next section.
- **The composer/caches are not the RPC problem**: render cache keyed by world version,
  debug flatten, latest state acks all unit-tested green.

### Where the bug currently lives (strong suspicion)
- The REAL app freezes the message pipeline exactly when a **new screen's Init runs out of
  a GotoMsg transition**. Evidence: router instrumentation showed
  `WindowSizeMsg → KeyMsg → KeyMsg → GotoMsg(gotoid=1)` then — despite `authScreen.Init()`
  returning a non-nil cmd (logged "authscreen init: scheduling spinner tick") — **no
  TickMsg ever arrives**: the returned cmd is never executed.
- Starting *at* ScreenAuth directly (NewRouterDirectGame variant) ticks continuously —
  the Init path works; a mid-session Goto transition into a screen whose Init carries a
  cmd does not.
- Current failing probe: route A lands on GameScreen but the screen is never sized —
  capture shows `entering the world…` then the connection drops at quit. Cause: the
  `screenFor` prime (the `m.Update(WindowSizeMsg)` warm-up) is currently disabled in
  router.go under a "bisect: prime disabled" comment, so GameScreen reaches the screen
  with `width==0`, and view() returns the placeholder.

###_SUSPECT list, ranked
1. **The primed-size mechanism in `Router.screenFor`** — the returning of the prime cmd
   from *inside a message handler* seems to drop the whole Init cmd chain right after
   GotoMsg (both narration and later resize schedule). The DIRECT-GAME test (screen
   created in `NewRouterDirectGame`, initCmd stashed and returned from `Init()`) FLOWED
   PERFECTLY: resize → joins → 2s state ticks, no stall. The stall only appears through
   the GotoMsg handler return path.
2. `Router.Update` returning `r` (a value copy) after rebinding `r.screen = sc` while the
   new screen's Init cmd runs — bubbletea re-invokes Update on the model the previous
   Update returned; consider whether the current returned model and the returned cmd
   (batched) interplay safely. The repro does the same and works — so treat with care.
3. Repro missing pieces of the real app worth adding to the repro one at a time:
   `stateTick()` (2s) + `scheduleResize`, the security banner Widget, and the
   `apply(c.Result)` flow, `IdentityFingerprintless` (guest) until proven otherwise.

### Immediate state of the code (what is uncommitted / half-done)
- `internal/ui/router.go` — currently starts at **ScreenLanding** but `r.initCmd` is
  build-through `screenFor(ScreenLanding)`; NOTE the work-tree has the "prime disabled"
  comment in `screenFor` which is why the game screen boots unsized. `NewRouterDirectGame`
  still exists and pumps the router straight to ScreenGame (currently UNUSED in main
  wiring — main passes into `NewRouterWithAccounts` which lands on landing).
- `internal/server/ssh/server.go` — wish middleware: `WithPublicKeyAuth(accept-all)` +
  `ServerConfigCallback(NoClientAuth)`; **`logging.Middleware()` was removed** for the
  bisect and not restored; **altscreen/FPS options are commented out** with a bisect note;
  `NewRouterDirectGame(identity, world)` still wired (waiting to be swapped back).
- `internal/ui/authscreen.go` — diagnos slog removed; narration intact.
- `internal/ui/game.go` — render cache (`terrainCache/terrainVersion`), debounced
  `scheduleResize`/`resizeDoneMsg`, `pendingResizeW/H` are all in place; `lastKeyAt`
  state-tick skip removed (it added false freshness).
- The smoke `tests/smoke/ui_smoke.exp` currently FAILS at "auth → game" exactly BECAUSE of
  the disabled prime above (the world screen renders `entering the world…`).
- Everything else is committed and green; `tests/bringup/auth_smoke.exp` covers the login
  loop; doc builds pass; per-package tests (go test ./...) pass, but the smoke needs the
  re-check.

### The squeaky wheel: config of container/bisect
- docker compose pins `W_SEED=20260909`, the **hostkey volume** avoids the
  known_hosts-hostkey churn; after container rebuilds, clear the ssh local view:
  `ssh-keygen -R "[localhost]:2525"` or test with `-o UserKnownHostsFile=/dev/null`.
- `docker compose -f build/compose.yaml up -d --build gamemaster` DOES rebuild; **`docker
  compose start` does NOT** — a bisect run with `start` silently reused an old image and
  poisoned a whole afternoon of结论s. ALWAYS use `up -d --build` when binaries changed.
- Go toolchain inside Docker bumped to `golang:1.25-alpine` (build/gamemaster/Dockerfile);
  host toolchain is Go 1.24.2 requirement (module), local runs build fine.
- `docker compose stop gamemaster` still leaves Docker Desktop holding :2525/:8081 — a
  simultaneous `go run` of the server will fail with address-in-use. Stop the container
  AND use a different port for the local variant, or down the compose.

### Next moves (in order, one variable at a time)
1. Re-enable the **prime** in `screenFor` but return its cmd **in the batched Init**
   (it is already batched: see if *only* the Goto handler changes and verify the *cmd* is
   reachable as `r.initCmd` style). Try: instead of `init = tea.Batch(init, prime)`,
   schedule the prime as an explicit `initCmd` on the screen model (like DirectGame did).
2. If that still stalls: switch the router's GotoMsg handler to **return
   `tea.Tick(0, ...)`-style deferred** transition (screen switch in the next message
   window) — repro2 works and has no Goto-inside-Update issue.
3. Add to `routerflow` repro the exact GameScreen Init pieces (stateTick + resize builtin)
   and `apply(Result)` — reproduce the freeze *in the repro* (fast local loop that avoids
   the full container), then fix the root cause there, then port it.
4. When fixed: re-run `go test ./... -count=1`, `expect tests/smoke/ui_smoke.exp`,
   `expect tests/smoke/auth_smoke.exp`, and have the user manually confirm key→frame
   latency in their own SSH session.
5. Remember to restore everything the debug probes changed: remove `NewRouterDirectGame`
   or keep it as a documented test hook (it is useful for future perf hunts) — user asked
   to keep tests in `tests/`, so `tests/perf_repro` can stay until the fix lands.

## Infrastructure gotchas (recap)
- The temporary ports are **SSH :2525**, **HTTP :8081** (2222/8080 blocked).
- compose services: `gamemaster` + `postgres` (relational) + `docdb` (jsonb document);
  DSNs wired via env `POSTGRES_DSN` / `DOCUMENT_DSN`; storage tests gate
  `STORAGE_INTEGRATION=1`.
- Auth model: accounts keyed by name in the docdb with a credential index
  (`cred:<type>:<id>` → account). Keyed SSH connections auto-mint accounts
  (`grim-thistle-77`-style names, 25-char symbol password hashed argon2id); verify is
  either-or (pubkey or password-acked). Smoke `tests/smoke/auth_smoke.exp` covers
  create-reveal-login + keyed recall, was fully green when last run.
- The world: dynamic dims (`worldState.ww/wh`) resized by `CombatView.Resize` from the
  terminal size, debounced 150ms. `W_SEED` pins world creation; resize regenerates with
  `W_SEED + regenCount` (deterministic per burst; compose pins `W_SEED=20260909`).
