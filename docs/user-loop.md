# The user loop: connect → auth → character → (world)

How a player goes from a raw TCP connection to sitting in front of the
game, and exactly where the next phases slot in. Code references are to
the current tree; "future" callouts mark the seams that are already
prepared.

## 1. The full path

```
ssh client
   │  tcp :2525
   ▼
┌───────────────────────────────────────────────────────────────┐
│ wish server (internal/server/ssh)                             │
│                                                               │
│   wish crypto/ssh handshake                                   │
│      │                                                        │
│      ▼                                                        │
│   teaSession(provider, world, s)   ← connect sequence         │
│      │  1. auth.Fingerprint(s.PublicKey())                    │
│      │  2. auth.Identify(provider, fp)   → auth.Identity      │
│      ▼                                                        │
│   ui.NewRouter(identity, world)    ← one tea program/session  │
└───────────────────────────────────────────────────────────────┘
      │
      ▼
┌───────────────────────────────────────────────────────────────┐
│ Screen FSM (internal/ui)                                      │
│                                                               │
│   ┌─────────┐  [1] Join World  ┌──────────┐                  │
│   │ Landing │ ───────────────▶ │ Auth     │ ──┐              │
│   │         │  [2] New Char    └──────────┘   │              │
│   │         │ ───────────────▶ ┌───────────┐  ▼              │
│   └─────────┘   (wizard)       │ Character │ ┌────────────┐  │
│   [3] Log in ──▶ login sc.     │ wizard    │ │ GameScreen │  │
│                                └─────┬─────┘ └────▲───────┘  │
│                                      └────────────┘          │
└───────────────────────────────────────────────────────────────┘
```

All three entrances end in the playable GameScreen:

- **Join World** — guest narration → the world (anonymous body)
- **New Character** — mint a named account: anonymous sessions get the
  25-char one-time password reveal (their only credential); keyed
  sessions skip it (the SSH handshake already verified them —
  either-or: pubkey OR password)
- **Log in** — name + password for returning bodies; on success the
  connecting machine's key is attached to the account (multi-device)

## 2. Connect sequence — where auth is initialized

`internal/server/ssh/server.go`, `Run()`:

1. **The account service is constructed in `main`** —
   `auth.NewAccounts(store.Document)` (`internal/auth/accounts.go`),
   backed by the docdb (nil DB → memory mode). Nothing downstream
   knows which backing it got.
2. The bubbletea middleware is given a closure over the accounts
   service and the game server:
   `bm.Middleware(func(s ssh.Session) … { return teaSession(accts, world, s) })`
3. For every new SSH session, `teaSession` runs the connect sequence:

```go
fp := auth.Fingerprint(s.PublicKey())          // "" if no key was offered
identity, err := accts.Identify(fp)            // lookup → auto-account on miss
return ui.NewRouterWithAccounts(identity, world, accts), nil
```

`Accounts.Identify` is the canonical sequence:

- `LookupByCredential("ssh", fp)` — is this key known?
- miss on a keyed connection → `AutoAccount("ssh", fp)` — mint a
  named account (`grim-thistle-91` style callsign, generated password
  hashed with argon2id — plaintext shown once via identity.NewPassword)
- result: `Identity{Fingerprint, User, Verified, NewPassword, AccountFresh}`

**Verification is either-or**: possession of a public key IS verified
(passed in the handshake itself); an acked/typed password is the
fallback credential's route to the same status. Keyed fresh accounts
therefore never nag — there is no reveal step, no quit guard; the
password stays a never-revealed fallback recoverable via [v] in-game.

The wishes server also allows `none` (`NoClientAuth` set via
`ServerConfigCallback`) so anonymous connections stay supported
alongside pubkey auth — the password screen kick whenever the client
advertises a password auth method.

### The three entrances

| Entry | Credential at play |
|---|---|
| Connect with a known key | fingerprint → account (`Play as <name>`) |
| Join World guest (no key) | none — anonymous body exists in session only |
| Log in (name + password) | password; machine's key attaches on success |

## 3. The screen FSM — how navigation works

`internal/ui/router.go`. One `Router` per SSH session holds the active
screen and the player's `auth.Identity`.

- Screens are plain bubbletea models. **Screens never import each
  other** — a screen asks for a transition by returning a cmd that
  emits `GotoMsg{ID: ScreenX}`.
- The router owns the screen map (`screenFor`) and swaps the active
  screen when a `GotoMsg` arrives.
- On every transition the router **runs the new screen's `Init()`** and
  returns its cmd. This matters: startup cmds (the auth spinner's first
  tick, the text-input cursor blink) are scheduled there. (This was a
  real bug — a transition that returned `nil` silently froze the auth
  screen live while unit tests, which fed ticks manually, kept passing.)
- `tea.WindowSizeMsg` is stored on the router and replayed into any
  newly created screen, so screens built after the initial resize still
  render at the right size.

Screens today:

| Screen          | File               | Role                                                                                                                                            |
| --------------- | ------------------ | ----------------------------------------------------------------------------------------------------------------------------------------------- |
| `Landing`       | `landing.go`       | ASCII logo, identity header, state-adaptive menu: Join World/**Play as <name>** / New Character / Log in / quit                                 |
| `authScreen`    | `authscreen.go`    | Narrates the connect-time auth steps, spinner-paced; `isNew` flag switches the copy between "account found" and "creating account"              |
| `loginScreen`   | `loginscreen.go`   | Name + password for returning bodies; success attaches the connecting machine's key and lands in the world                                      |
| `charWizard`    | `newchar.go`       | Name (validated, spaces allowed) → class → confirm; anonymous creations continue to the one-time-password reveal, keyed sessions enter directly |
| `GameScreen`    | `game.go`          | The playable view: world grid with the `@` dot, stats panel, floating windows, verify nudge + quit guard for password-only accounts             |
| `unimplemented` | `unimplemented.go` | Holding screen (no current caller); `esc` returns to Landing                                                                                    |

Key conventions: `ctrl+c` quits from anywhere (router force-quits); `q` quits on menus (deliberately *not* bound in-game — it will become a game key); `esc` always means "go back one level" (in-game: close the floating window); direct number keys select menu items.

## 4. The handoff to the game

Every entrance is live now:

```
Landing → Auth (guest narration) ──▶ GameScreen        (anonymous body)
Landing → Wizard → [reveal] ──▶ GameScreen             (named account)
Landing → Log in (name+password) ──▶ GameScreen        (returning body)
Landing → Play as <name> ──▶ GameScreen                (known key reconnect)
```

`identitySwapMsg` is how screens upgrade the session identity mid-flow
(login success, verification, rename) — the router then lands the fresh
screen server-side.

How the GameScreen is wired:

- **Identity**: players are keyed by SSH key fingerprint ("" = anonymous, the shared guest). The narrow seam the UI plays through is `game.PlayerView` (`internal/game/player.go`) — `Join`/`State`/`Move` — implemented by the in-process `*game.Server` today, swap-able for a networked client later.
- **Movement**: arrow keys / wasd / hjkl send deltas to the server; the server clamps to the pre-landgen field (`WorldW x WorldH`) and returns updated state. Input is authoritative-server, not UI-local.
- **Sync**: the screen polls state every 2s (`stateTick`). That poll keeps the server's `lastSeen` fresh — the tick loop reaps players silent for 30s, which is the stand-in for disconnect detection (idle-but-connected players stay alive because of the poll).
- **Stats**: the side panel renders name, level, HP and mana via the shared `kit.Bar` — the same bars party frames / enemy info / trade windows will reuse.
- **Floating windows**: `kit.Panel` + `kit.Overlay` (ANSI-aware compositing via `x/ansi`) draw the inventory as a centered floating window. `i` toggles, `esc` closes; keys don't leak through to movement while a window is open. Every future window (trade, NPC dialog, party) is a new `Panel` content — no new plumbing.
- **Rejoin/reset**: `Join` is idempotent per fingerprint — reconnecting reuses your body at spawn point; the reaper removes you 30s after your last poll.

What's still deferred: multi-player interaction (sessions aren't broadcast to yet), the game's AI game master (the merchant runs on a fixed script; enemies spawn from tables — both will eventually steer from the harness), self-serve password reset.

## 5. How this loop is tested

- **Unit (`internal/ui/ui_test.go`)**: models are driven directly —
  `drive()` feeds key presses and resolves transition cmds inline;
  `pump()` is a mini bubbletea runtime that executes every returned cmd
  until the message queue drains (needed for spinner/timer-paced screens
  — a naive driver silently drops the last transition cmd). The router
  is wired to a real in-process `*game.Server` so gameplay is exercised
  end to end. Covers: menu routing, route A → GameScreen, the full
  wizard walk, invalid-name rejection, wizard restart, reveal esc-exit paths, the keyed skip-reveal paths,
  landing content, movement (all three key sets), inventory open/close +
  movement gating through the overlay.
- **Auth (`internal/auth/auth_test.go`, `accounts_test.go`)**: guest
  sequence, fingerprint derivation (nil-safe), JSON store round-trip;
  memory-mode accounts: auto-account minting (25-symbol passwords),
  identify recall, login + credential attach, set-own-password ack,
  rename conflicts, guest identity.
- **Game logic (`internal/game/player_test.go`)**: idempotent join,
  movement clamping to world bounds, unknown-player moves, the stale
  reaper, explicit leave.
- **UI kit (`internal/ui/kit_test.go`)**: panel/bars, overlay centering,
  overlay-taller-than-background, and an ANSI-corruption regression test
  (a colored background spliced through must keep its sequences balanced
  and its visible width unchanged).
- **Live smoke (`tests/smoke/ui_smoke.exp`)**: walks both paths over a
  real SSH connection on `:2525` — landing, auth → game screen, a real
  movement (asserts the new `pos` render), inventory open/close, wizard
  incl. name-rejection, class select and the password-reveal step.
  Runs against the Docker container (compose pins `W_SEED` so the
  movement assert is stable).
