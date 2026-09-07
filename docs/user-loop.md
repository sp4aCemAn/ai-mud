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
│   teaSession(provider, s)          ← connect sequence         │
│      │  1. auth.Fingerprint(s.PublicKey())                    │
│      │  2. auth.Identify(provider, fp)   → auth.Identity      │
│      ▼                                                        │
│   ui.NewRouter(identity)           ← one tea program/session  │
└───────────────────────────────────────────────────────────────┘
      │
      ▼
┌───────────────────────────────────────────────────────────────┐
│ Screen FSM (internal/ui)                                      │
│                                                               │
│   ┌─────────┐  [1] Join World   ┌───────────┐                 │
│   │ Landing │ ────────────────▶ │ Auth      │ ─┐              │
│   │         │  [2] New Char     └───────────┘  │              │
│   │         │ ────────────────▶ ┌───────────┐  │  ┌───────────┐│
│   └─────────┘   (wizard)        │ Character │ ─┴─▶│ unim-    ││
│                                 │ wizard    │     │ plemented ││
│                                 └───────────┘     └───────────┘│
└───────────────────────────────────────────────────────────────┘
                                       │ (future: replaced by GameScreen)
                                       ▼
                             game.Server.Attach(session)
```

## 2. Connect sequence — where auth is initialized

`internal/server/ssh/server.go`, `Run()`:

1. **Provider is constructed first.** Today: `auth.NewGuestProvider()`.
   This is the single line to change when real auth lands. Nothing
   downstream knows which provider it got.
2. The bubbletea middleware is given a closure over that provider:
   `bm.Middleware(func(s ssh.Session) … { return teaSession(provider, s) })`
3. For every new SSH session, `teaSession` runs the connect sequence:

```go
fp := auth.Fingerprint(s.PublicKey())          // "" if no key was offered
identity, err := auth.Identify(provider, fp)   // lookup → create on miss
return ui.NewRouter(identity), nil
```

`auth.Identify` (`internal/auth/auth.go`) is the canonical sequence the
real flow will use:

- `provider.Lookup(fp)` — is this key known?
- if `ErrNotFound` → `provider.Create(fp)` — register it
- result: `Identity{Fingerprint, User}`

With `GuestProvider` both calls are no-ops that log the fingerprint and
always return the shared `guest` user. With `StoreProvider` the same two
calls persist real users to `data/users.json` (the JSON stop-gap before
the database). **The UI cannot tell the difference** — that's the point
of the template.

### What changes when auth becomes real

| Today (template) | Real (later) |
|---|---|
| `GuestProvider`, any key accepted | A store/db-backed `Provider` |
| Auth happens inside `teaSession` (post-connect) | wish's `PublicKeyHandler` decides *at handshake* whether the key may connect at all |
| `data/users.json` | A database |
| Everyone logs in as `guest` | Key fingerprint → owned account |

`teaSession` keeps its role either way: derive fingerprint, resolve
identity, build the router. Unknown-key *rejection* moves behind the
SSH layer; unknown-key *registration* stays a game-side flow.

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

| Screen | File | Role |
|---|---|---|
| `Landing` | `landing.go` | ASCII logo, identity header, menu: Join World / New Character / quit |
| `authScreen` | `authscreen.go` | Narrates the connect-time auth steps, spinner-paced; `isNew` flag switches the copy between "account found" and "creating account" |
| `charWizard` | `newchar.go` | 3 steps: name (validated) → class → confirm; `y` logs the character and moves on, `n` restarts, `esc` backs out a step (to Landing from step 1) |
| `unimplemented` | `unimplemented.go` | Holding screen for world entry; `esc` returns to Landing |

Key conventions: `q`/`ctrl+c` quit from anywhere (router force-quits on
ctrl+c), `esc` always means "go back one level", direct number keys
select menu items.

## 4. The handoff to the game

Route A now ends in the **GameScreen** (`internal/ui/game.go`) — the playable view — while the character-wizard route still dead-ends on `unimplemented` until character persistence exists.

```
today:   Landing → Auth ──────▶ GameScreen      (join world, play)
         Landing → Wizard → unimplemented      (character attach later)

next:    Wizard confirm ──▶ GameScreen with the created character
         GameScreen shows real rooms ◀── world generation (next phase)
```

How the GameScreen is wired:

- **Identity**: players are keyed by SSH key fingerprint ("" = anonymous, the shared guest). The narrow seam the UI plays through is `game.PlayerView` (`internal/game/player.go`) — `Join`/`State`/`Move` — implemented by the in-process `*game.Server` today, swap-able for a networked client later.
- **Movement**: arrow keys / wasd / hjkl send deltas to the server; the server clamps to the pre-landgen field (`WorldW x WorldH`) and returns updated state. Input is authoritative-server, not UI-local.
- **Sync**: the screen polls state every 2s (`stateTick`). That poll keeps the server's `lastSeen` fresh — the tick loop reaps players silent for 30s, which is the stand-in for disconnect detection (idle-but-connected players stay alive because of the poll).
- **Stats**: the side panel renders name, level, HP and mana via the shared `kit.Bar` — the same bars party frames / enemy info / trade windows will reuse.
- **Floating windows**: `kit.Panel` + `kit.Overlay` (ANSI-aware compositing via `x/ansi`) draw the inventory as a centered floating window. `i` toggles, `esc` closes; keys don't leak through to movement while a window is open. Every future window (trade, NPC dialog, party) is a new `Panel` content — no new plumbing.
- **Stats/stats reset**: `Join` is idempotent per fingerprint — reconnecting reuses your body at spawn point; the reaper removes you 30s after your last poll.

What's still deferred: rooms/terrain (land gen), character persistence (wizard route), multi-player interaction (sessions aren't broadcast to yet), inventory contents.

## 5. How this loop is tested

- **Unit (`internal/ui/ui_test.go`)**: models are driven directly —
  `drive()` feeds key presses and resolves transition cmds inline;
  `pump()` is a mini bubbletea runtime that executes every returned cmd
  until the message queue drains (needed for spinner/timer-paced screens
  — a naive driver silently drops the last transition cmd). Covers: menu
  routing, the full wizard walk, invalid-name rejection, wizard restart,
  esc-exit paths, auth-screen progression, landing content.
- **Auth (`internal/auth/auth_test.go`)**: guest sequence, fingerprint
  derivation (nil-safe), JSON store round-trip + idempotent create,
  store-provider lookup-creates-once semantics.
- **Live smoke (`expect` script, not in repo)**: walks both paths over a
  real SSH connection on `:2525` — landing render, auth sequence →
  unimplemented, esc back, wizard incl. name-rejection and class select →
  unimplemented. Runs against the local binary and the Docker container.
