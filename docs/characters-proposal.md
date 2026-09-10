# Proposal: Accounts, Characters & Credentials in ai-mud

Part of `docs/` (read alongside `architecture.md` and `user-loop.md`).

## 1. The problem

Everything gameplay-side today hangs off the *fingerprint* or the
*account name*, and the two are conflated:

- `auth.Identity.User.Name` IS both the account name and the in-world
  character name. Renaming or re-keying an account moves the character.
- `game.Server.players` is keyed by fingerprint; gameplay state
  (`Player`) exists only for the live connection and dies with it.
- Changing a password is safe (credentials are per-account), but there
  is no way to own more than one character per account.
- Guests (keyed or not) cannot pick their persona — they ARE the auto
  account.

## 2. Desired shape

```
Credential (key/password) ──< Account ──< Character
```

- **Account** = login identity + credentials (unchanged from today:
  `account:<name>` docs, `cred:<type>:<id>` index, passwords, acks).
- **Character** = named persona carrying gameplay state, owned by an
  account, NOT by a key. `char:<account>:<name>` records. An account
  owns many; one is marked "last played" for fast entry.
- Changing or stealing the password (`AttachCredential` steal, just
  landed) moves nothing: characters live on the account, and the
  account's docdb row is untouched by credential churn.
- A brand-new game session assigns another username to the same
  account by creating a character (the wizard's name step becomes
  "name this character").

## 3. What this changes

### storage (docdb)
- New doc kind `char:<account>:<name>`: gameplay snapshot (level, HP,
  coins, XP, atk/def, inventory, position, class) + timestamps.
- Account doc unchanged; character names are validated at creation
  (same name-rules the wizard already enforces), unique per account.
- No migration: existing accounts simply start with zero characters.

### auth
- `Identity` keeps `Fingerprint` + `User` (account), gains
  `AccountID` and loses the "name = character" reading.
- `Accounts.Login`/`Identify` return the account; the UI decides
  which character. No API break: `fpOf`, the credential index, acks
  and the prefetch steal are untouched.

### game server
- `Server.players` map key: fingerprint → **character ID
  (`account/name`)**. `lastSeen`, reaping, fights, shops already key
  off the player entry; only the key and its owner change.
- `PlayerView`/`CombatView` calls gain a character handle instead of
  the fingerprint from `Identity` (`g.fp` becomes the character id).
- Sessions with `staleAfter` reaped players reload from the character
  doc on re-entry (first time gameplay survives a disconnect).

### UI
- New **character select** screen after landing/auth success:
  pick existing character, create new, or (guests) skip straight to
  the wizard. `GotoMsg`-screens fit the existing router shape.
- Wizard: on success now creates a *character* under the account and
  reveals a password only when the account itself is new.
- `NewRouterDirectGame` becomes "direct to character select" in tests.

## 4. Phasing

1. **Storage first** — `Document.CreateCharacter/LoadCharacter/
   ListCharacters/DeleteCharacter` + integration tests (pattern-copy
   of the credential-index work just done).
2. **Auth passes-through** — `Identity` gains the account handle;
   nothing else moves. Smoke stays green.
3. **Server keys by character** — the one risky step: player maps,
   fights, shops, reaper. Ship behind the select screen returning a
   single auto-created character so behavior is unchanged for
   existing accounts.
4. **Character select screen** + wizard rewrite; smokes extended
   (one login loop covering: password change → characters intact;
   second character join/switch; stale character reload).

## 5. Risks / notes

- Fingerprint-keyed fights/shops maps in `combat.go` and event logs:
  same rename-hardening as `players` — mechanical but wide.
- Guest identities (no account) keep working exactly as today: guest
  gameplay state remains ephemeral and keyed by fingerprint.
- The smoke's keyed device now carries ONE base credential for many
  characters — the `auth_smoke.exp` PATH list above extends cheaply.
