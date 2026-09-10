package auth

import (
	"context"
	"crypto/hmac"
	crand "crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"math/rand/v2"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/argon2"

	"github.com/sp4aceman/ai-mud/internal/storage"
)

// Accounts is the real auth service: named player accounts with one
// or more credentials (the SSH fingerprint today), passwords for
// cross-device login, and the verify-now flow (ack the generated
// password or set your own to become verified).
//
// Backing store is the document store when provided; with a nil DB
// everything lives in memory — tests and `go run` without databases.
type Accounts struct {
	mu      sync.Mutex
	DB      storage.DocumentStore
	mem     map[string]storage.Account // name → account (memory mode)
	memCred map[string]string          // "type:id" → account name (memory mode)
}

func NewAccounts(db storage.DocumentStore) *Accounts {
	return &Accounts{DB: db, mem: map[string]storage.Account{}, memCred: map[string]string{}}
}

// Identity is who is connected, enriched with account state.
// NewPassword is the freshly generated one shown exactly once (never
// persisted again); non-empty only right after account creation.
type Identity struct {
	Fingerprint  string // "" = anonymous connection
	User         User
	Verified     bool   // the account was password-acked
	NewPassword  string // one-shot reveal at account creation
	AccountFresh bool   // the connect flow just created this account
}

// Identify runs the canonical connect sequence: lookup by credential,
// auto-account on a miss (keyed connections only — guests stay guests).
func (a *Accounts) Identify(fp string) (Identity, error) {
	if fp == "" {
		return Identity{Fingerprint: "", User: User{ID: "guest", Name: "guest"}}, nil
	}

	acc, err := a.LookupByCredential("ssh", fp)
	switch {
	case errors.Is(err, storage.ErrNotFound):
		acc, password, err := a.AutoAccount("ssh", fp) // fixed below
		if err != nil {
			return Identity{}, err
		}
		return a.identityFor(acc, password, true), nil
	case err != nil:
		return Identity{}, err
	}
	return a.identityFor(acc, "", false), nil
}

// identityFor derives the UI identity from an account. Verification is
// derived, never stored: the SSH key itself is primary (pubkey auth
// proven at connect == verified); the acked/set password is the
// fallback credential's route to the same status — either-or.
func (a *Accounts) identityFor(acc storage.Account, newPassword string, fresh bool) Identity {
	return Identity{
		Fingerprint:  fpOf(acc),
		User:         User{ID: acc.Name, Name: acc.Name},
		Verified:     acc.PasswordAcked || fpOf(acc) != "",
		NewPassword:  newPassword,
		AccountFresh: fresh,
	}
}

func fpOf(acc storage.Account) string {
	for _, c := range acc.Credentials {
		if c.Type == "ssh" {
			return c.ID
		}
	}
	return ""
}

// LookupByCredential finds the account owning a credential.
func (a *Accounts) LookupByCredential(credType, id string) (storage.Account, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.DB != nil {
		return a.DB.AccountByCredential(context.Background(), credType, id)
	}
	name, ok := a.memCred[credType+":"+id]
	if !ok {
		return storage.Account{}, storage.ErrNotFound
	}
	acc, ok := a.mem[name]
	if !ok {
		return storage.Account{}, storage.ErrNotFound
	}
	return acc, nil
}

// AutoAccount mints a fresh account with an auto-generated callsign
// and a generated password. The plaintext password is returned for the
// one-shot reveal; only the hash is stored.
func (a *Accounts) AutoAccount(credType, fpID string) (storage.Account, string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	acc := storage.Account{
		Credentials: []storage.Credential{},
	}
	if fpID != "" {
		acc.Credentials = append(acc.Credentials, storage.Credential{Type: credType, ID: fpID, Added: time.Now().UTC()})
	}
	password := genPassword()
	acc.PasswordHash = hashPassword(password)

	for tries := 0; tries < 64; tries++ {
		acc.Name = genName()
		if err := a.saveAccount(acc); err == nil {
			return acc, password, nil
		} else if !errors.Is(err, storage.ErrConflict) {
			return storage.Account{}, "", err
		}
	}
	return storage.Account{}, "", fmt.Errorf("auth: no free names after 64 tries")
}

// CreateNamed registers the wizard's chosen callsign with a generated
// password (revealed to the caller exactly once).
func (a *Accounts) CreateNamed(name string) (storage.Account, string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	password := genPassword()
	acc := storage.Account{
		Name:         name,
		PasswordHash: hashPassword(password),
		Credentials:  []storage.Credential{},
	}
	if err := a.saveAccount(acc); err != nil {
		return storage.Account{}, "", err
	}
	return acc, password, nil
}

// Login authenticates by name + password, then attaches this
// connection's credential when new (multi-device support).
func (a *Accounts) Login(name, password, credType, fpID string) (storage.Account, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	acc, err := a.loadAccount(name)
	if err != nil {
		return storage.Account{}, fmt.Errorf("auth: no such account")
	}
	if !checkPassword(acc.PasswordHash, password) {
		return storage.Account{}, errors.New("auth: wrong password")
	}
	if fpID != "" && !a.hasCredential(acc, credType, fpID) {
		if err := a.attachCredentialLocked(name, credType, fpID); err != nil {
			return storage.Account{}, err
		}
	}
	return a.loadAccount(name)
}

// --- storage-mode-agnostic helpers -------------------------------------------
//
// Every operation dispatches to the real DB when present, then keeps
// the memory mirrors in sync (they are the source of truth when the
// DB is nil).

func (a *Accounts) saveAccount(acc storage.Account) error {
	if a.DB != nil {
		if err := a.DB.CreateAccount(context.Background(), acc); err != nil {
			return err
		}
	}
	a.mem[acc.Name] = acc
	for _, c := range acc.Credentials {
		a.memCred[c.Type+":"+c.ID] = acc.Name
	}
	return nil
}

func (a *Accounts) loadAccount(name string) (storage.Account, error) {
	if a.DB != nil {
		return a.DB.AccountByName(context.Background(), name)
	}
	acc, ok := a.mem[name]
	if !ok {
		return storage.Account{}, storage.ErrNotFound
	}
	return acc, nil
}

func (a *Accounts) hasCredential(acc storage.Account, credType, id string) bool {
	for _, c := range acc.Credentials {
		if c.Type == credType && c.ID == id {
			return true
		}
	}
	return false
}

// RenameAccount changes the account name; conflicts fail cleanly.
func (a *Accounts) RenameAccount(oldName, newName string) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.DB != nil {
		if err := a.DB.RenameAccount(context.Background(), oldName, newName); err != nil {
			return err
		}
	}
	// memory mirror — taken-name conflicts fail here too
	if _, exists := a.mem[newName]; exists {
		return storage.ErrConflict
	}
	acc, ok := a.mem[oldName]
	if !ok {
		return storage.ErrNotFound
	}
	delete(a.mem, oldName)
	for k, n := range a.memCred {
		if n == oldName {
			a.memCred[k] = newName
		}
	}
	acc.Name = newName
	a.mem[newName] = acc
	return nil
}

// SetPassword replaces the hash and marks the account verified
// (typing your own is the ack).
func (a *Accounts) SetPassword(name, password string) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	acc, err := a.loadAccount(name)
	if err != nil {
		return err
	}
	if a.DB != nil {
		if err := a.DB.SetPasswordHash(context.Background(), name, hashPassword(password)); err != nil {
			return err
		}
	}
	acc.PasswordHash = hashPassword(password)
	acc.PasswordAcked = true
	if a.DB != nil {
		if err := a.DB.AckPassword(context.Background(), name); err != nil {
			return err
		}
	}
	a.mem[name] = acc
	return nil
}

// AckPassword marks the account verified (generated password copied).
func (a *Accounts) AckPassword(name string) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.DB != nil {
		return a.DB.AckPassword(context.Background(), name)
	}
	acc, err := a.loadAccount(name)
	if err != nil {
		return err
	}
	acc.PasswordAcked = true
	a.mem[name] = acc
	return nil
}

// IdentityForAccount builds a session identity for an account — the
// login/wizard/verify flows' post-mutation identity source.
func (a *Accounts) IdentityForAccount(acc storage.Account) Identity {
	return a.identityFor(acc, "", false)
}

// Account fetches an account by name.
func (a *Accounts) Account(name string) (storage.Account, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.loadAccount(name)
}

// Verified reports the account's password-acked state.
func (a *Accounts) Verified(name string) (bool, error) {
	acc, err := a.loadAccount(name)
	if err != nil {
		return false, err
	}
	return acc.PasswordAcked, nil
}

// AttachCredential appends the credential to the account.
func (a *Accounts) AttachCredential(name, credType, fpID string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.attachCredentialLocked(name, credType, fpID)
}

// attachCredentialLocked is the lock-held variant — Login calls this
// directly; relocking here would deadlock.
func (a *Accounts) attachCredentialLocked(name, credType, fpID string) error {

	if a.DB != nil {
		return a.DB.AttachCredential(context.Background(), name, storage.Credential{Type: credType, ID: fpID, Added: time.Now().UTC()})
	}
	acc, err := a.loadAccount(name)
	if err != nil {
		return err
	}
	acc.Credentials = append(acc.Credentials, storage.Credential{Type: credType, ID: fpID, Added: time.Now().UTC()})
	a.mem[name] = acc
	a.memCred[credType+":"+fpID] = name
	return nil
}

// --- auto names ---------------------------------------------------------------

// MUD-tilt callsign: adjective-noun-suffix, e.g. "grim-thistle-91".
var nameAdjectives = []string{"grim", "iron", "sooty", "dread", "hoar", "splint", "hollow", "baleful", "cinder", "murk"}
var nameNouns = []string{"thistle", "fir", "raven", "ember", "shroud", "tooth", "bell", "pit", "pyre", "anchor"}

func genName() string {
	adj := nameAdjectives[rand.IntN(len(nameAdjectives))]
	noun := nameNouns[rand.IntN(len(nameNouns))]
	return fmt.Sprintf("%s-%s-%d", adj, noun, rand.IntN(90)+10)
}

// --- passwords ---------------------------------------------------------------

// passwordSymbols is a shell-safe symbol set (no quotes/backslash —
// copy-paste through SSH never mangles). Passwords are 25 chars.
//
// NOTE: crypto/rand is plenty for a dev game; revisit if this ever
// guards anything with actual value (review scheduled later).
var passwordSymbols = []rune(`!#$%&*+,-.:;=?@^`)

const passwordLen = 25

func genPassword() string {
	b := make([]rune, passwordLen)
	for i := range b {
		b[i] = passwordSymbols[rand.IntN(len(passwordSymbols))]
	}
	return string(b)
}

// hashPassword — argon2id with a random salt, stored as
// "argon2id$<salthex>$<hashhex>".
func hashPassword(pw string) string {
	salt := make([]byte, 16)
	_, _ = crand.Read(salt) // crypto/rand never fails on a live system
	h := argon2.IDKey([]byte(pw), salt, 1, 64*1024, 4, 32)
	return fmt.Sprintf("argon2id$%s$%s", hex.EncodeToString(salt), hex.EncodeToString(h))
}

func checkPassword(stored, pw string) bool {
	parts := strings.Split(stored, "$")
	if len(parts) != 3 || parts[0] != "argon2id" {
		return false
	}
	salt, err := hex.DecodeString(parts[1])
	if err != nil {
		return false
	}
	want, err := hex.DecodeString(parts[2])
	if err != nil {
		return false
	}
	got := argon2.IDKey([]byte(pw), salt, 1, 64*1024, 4, 32)
	return hmac.Equal(got, want)
}
