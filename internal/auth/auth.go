// Package auth is the templated authentication layer.
//
// The intended end state: a player's SSH public key IS their account.
// On connect, the key fingerprint is looked up in the user store; a
// known key logs that user in, an unknown key registers a new user.
// Later this moves to the SSH layer itself (wish's PublicKeyHandler)
// with a database backing the store. For now a GuestProvider treats
// every client as the same guest user, and users can be persisted to a
// JSON file as a stop-gap before a real database.
package auth

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"

	gossh "golang.org/x/crypto/ssh"
)

// User is an account. Keys holds the fingerprints authorized to log in
// as this user.
type User struct {
	ID   string   `json:"id"`
	Name string   `json:"name"`
	Keys []string `json:"keys,omitempty"` // "SHA256:..." fingerprints
}

// Identity lives in accounts.go (the enriched, account-aware version).

// ErrNotFound is returned by Provider.Lookup when no user has the key.
var ErrNotFound = errors.New("no user for this key")

// Provider is the seam real auth plugs into later.
type Provider interface {
	// Lookup returns the user owning this key fingerprint, or ErrNotFound.
	Lookup(fingerprint string) (*User, error)
	// Create registers a new user for this key fingerprint.
	// Implementations should be idempotent for already-known keys.
	Create(fingerprint string) (*User, error)
}

// Identify runs the canonical connect sequence: lookup, then create on
// miss. This is the exact sequence the real (SSH-layer) auth will use.
func Identify(p Provider, fingerprint string) (Identity, error) {
	u, err := p.Lookup(fingerprint)
	if errors.Is(err, ErrNotFound) {
		u, err = p.Create(fingerprint)
	}
	if err != nil {
		return Identity{}, err
	}
	return Identity{Fingerprint: fingerprint, User: *u}, nil
}

// Fingerprint derives the SHA256 fingerprint of the client's public key.
// Returns "" for a nil key (non-key auth methods).
func Fingerprint(pub gossh.PublicKey) string {
	if pub == nil {
		return ""
	}
	return gossh.FingerprintSHA256(pub)
}

// --- GuestProvider: the "not real" provider -------------------------------

// GuestProvider treats every client as the same guest user. Fingerprints
// are logged but never persisted. Swap for StoreProvider (or a database
// provider) when real auth lands.
type GuestProvider struct {
	guest User
}

func NewGuestProvider() *GuestProvider {
	return &GuestProvider{guest: User{ID: "guest", Name: "guest"}}
}

func (g *GuestProvider) Lookup(fingerprint string) (*User, error) {
	slog.Info("auth: guest lookup", "fingerprint", fingerprint)
	return nil, ErrNotFound
}

func (g *GuestProvider) Create(fingerprint string) (*User, error) {
	slog.Info("auth: guest create (not persisted)", "fingerprint", fingerprint)
	u := g.guest
	return &u, nil
}

// --- JSONUserStore + StoreProvider: the persistent stop-gap ----------------

// JSONUserStore persists users to a JSON file — the stand-in for the
// future database. Safe for concurrent use.
type JSONUserStore struct {
	path string

	mu    sync.RWMutex
	users []User
}

// OpenJSONUserStore loads (or initializes) the store at path.
func OpenJSONUserStore(path string) (*JSONUserStore, error) {
	s := &JSONUserStore{path: path}
	b, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		// first run — start empty, file is written on first Create
	case err != nil:
		return nil, fmt.Errorf("read user store: %w", err)
	default:
		if err := json.Unmarshal(b, &s.users); err != nil {
			return nil, fmt.Errorf("parse user store %s: %w", path, err)
		}
	}
	return s, nil
}

// ByKey finds the user whose Keys contain fingerprint.
func (s *JSONUserStore) ByKey(fingerprint string) (*User, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for i := range s.users {
		for _, k := range s.users[i].Keys {
			if k == fingerprint {
				u := s.users[i]
				return &u, true
			}
		}
	}
	return nil, false
}

// Create registers a fingerprint. Idempotent: if the key is already
// known its user is returned unchanged.
func (s *JSONUserStore) Create(fingerprint string) (*User, error) {
	if fingerprint == "" {
		return nil, errors.New("cannot register an empty fingerprint")
	}
	if u, ok := s.ByKey(fingerprint); ok {
		return u, nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	u := User{
		ID:   newID(),
		Name: "user-" + fingerprint[len(fingerprint)-6:], // temp display name
		Keys: []string{fingerprint},
	}
	s.users = append(s.users, u)
	if err := s.save(); err != nil {
		return nil, err
	}
	slog.Info("auth: user created", "id", u.ID, "name", u.Name, "fingerprint", fingerprint)
	return &u, nil
}

// save must be called with the write lock held.
func (s *JSONUserStore) save() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return fmt.Errorf("create data dir: %w", err)
	}
	b, err := json.MarshalIndent(s.users, "", "  ")
	if err != nil {
		return err
	}
	// write-then-rename so a crash never leaves a truncated file
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return fmt.Errorf("write user store: %w", err)
	}
	return os.Rename(tmp, s.path)
}

// StoreProvider is the real-ish Provider backed by the JSON store.
// Every fingerprint gets its own persisted user.
type StoreProvider struct {
	Store *JSONUserStore
}

func (p StoreProvider) Lookup(fingerprint string) (*User, error) {
	if u, ok := p.Store.ByKey(fingerprint); ok {
		return u, nil
	}
	return nil, ErrNotFound
}

func (p StoreProvider) Create(fingerprint string) (*User, error) {
	return p.Store.Create(fingerprint)
}

// newID returns a short random hex id.
func newID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand failing is abnormal; a counter fallback is fine here
		return fmt.Sprintf("u%d", os.Getpid())
	}
	return hex.EncodeToString(b)
}
