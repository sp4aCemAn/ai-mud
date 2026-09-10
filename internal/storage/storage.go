// Package storage holds the database layer: two PostgreSQL backends.
//
//   - Relational (`internal/storage/postgres.go`): schema + queries —
//     AI-generated content (the harness's output records), world
//     records, event history.
//   - Document (`internal/storage/document.go`): the second Postgres
//     container used as a document store via jsonb — schema-free
//     documents (auth/account data whose shape will evolve), no
//     migrations required.
//
// Both run the same engine so there is one driver and one ops surface,
// while each keeps the data model that suits its purpose. Components
// program against the two interfaces below, never against drivers.
package storage

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"strconv"
	"time"
)

// ErrNotAvailable is returned by store methods when the backend was
// never connected.
var ErrNotAvailable = errors.New("storage: backend not available")

// ErrNotFound is returned by lookups that find nothing.
var ErrNotFound = errors.New("storage: not found")

// ErrConflict is returned when a write collides with existing data —
// e.g. a new account name that is already taken.
var ErrConflict = errors.New("storage: conflict")

// Config is resolved from the environment with localhost defaults so
// `go run` against local containers works out of the box; compose sets
// the service-name DSNs (postgres:5432 / docdb:5432).
type Config struct {
	PostgresDSN string        // relational: postgres://aimud:aimud@postgres:5432/aimud?sslmode=disable
	DocumentDSN string        // document:  postgres://aimud:aimud@docdb:5432/aimud_docs?sslmode=disable
	MaxRetries  int           // connection attempts during startup
	RetryWait   time.Duration // between attempts
}

func ConfigFromEnv() Config {
	cfg := Config{
		PostgresDSN: envOr("POSTGRES_DSN", "postgres://aimud:aimud@localhost:5432/aimud?sslmode=disable"),
		DocumentDSN: envOr("DOCUMENT_DSN", "postgres://aimud:aimud@localhost:5433/aimud_docs?sslmode=disable"),
		MaxRetries:  15,
		RetryWait:   2 * time.Second,
	}
	if n, err := strconv.Atoi(os.Getenv("STORAGE_MAX_RETRIES")); err == nil && n > 0 {
		cfg.MaxRetries = n
	}
	return cfg
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// Store bundles both backends. Nil members mean "not connected".
type Store struct {
	Relational RelationalStore
	Document   DocumentStore
}

// Connect opens both backends, retrying while containers come up, and
// runs migrations. It blocks until ready; on exhausting retries it
// returns an error (which takes the whole server down — infrastructure
// that exists must work, unlike optional components such as the harness).
func Connect(ctx context.Context, cfg Config) (*Store, error) {
	pg, err := connectPostgres(ctx, cfg.PostgresDSN, cfg)
	if err != nil {
		return nil, err
	}
	doc, err := connectDocument(ctx, cfg.DocumentDSN, cfg)
	if err != nil {
		pg.Close()
		return nil, err
	}

	slog.Info("storage connected", "relational=ok", "document=ok")
	return &Store{Relational: pg, Document: doc}, nil
}

// Close releases both backends.
func (s *Store) Close() {
	if s.Relational != nil {
		s.Relational.Close()
	}
	if s.Document != nil {
		s.Document.Close()
	}
}

// retry runs fn until it succeeds, MaxRetries is hit, or ctx is done.
func retry(ctx context.Context, what string, attempts int, wait time.Duration, fn func() error) error {
	var err error
	for i := 1; i <= attempts; i++ {
		if err = fn(); err == nil {
			return nil
		}
		if i == attempts {
			break
		}
		slog.Warn("storage: backend not ready, retrying", "backend", what, "attempt", i, "err", err)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
	}
	return err
}

// --- value types shared across stores ---------------------------------------

// Generation is one AI-generated artifact (harness → relational store).
// Kind selects a bucket: "world-gen", "narration", "event", …
type Generation struct {
	ID        int64
	Kind      string
	Model     string
	Prompt    string
	Output    string
	CreatedAt time.Time
}

// Credential is one way a client can prove it owns an account. Type is
// the transport ("ssh" today, other kinds later); ID is the verifier
// (the SSH fingerprint today).
type Credential struct {
	Type  string    `json:"type"` // "ssh"
	ID    string    `json:"id"`   // "SHA256:..."
	Added time.Time `json:"added"`
}

// Account is a player account. An account is verified once the player
// has acked its password (copied the generated one, or set their own);
// name choices never gate verification, so a user who keeps their
// auto-generated callsign is never nagged about it. Verification is
// exactly PasswordAcked — there is no separate status flag.
type Account struct {
	Name          string         `json:"name"` // display + login name
	PasswordHash  string         `json:"password_hash,omitempty"`
	PasswordAcked bool           `json:"password_acked"`
	Credentials   []Credential   `json:"credentials"`
	CreatedAt     time.Time      `json:"created_at,omitzero"`
	Body          map[string]any `json:"body,omitempty"` // game state lives here later
}

// --- the consumer-facing interfaces ------------------------------------------

// RelationalStore is the SQL side (AI output + future world records).
type RelationalStore interface {
	Ping(ctx context.Context) error
	// SaveGeneration persists one AI-generated artifact and returns it
	// with its assigned ID.
	SaveGeneration(ctx context.Context, g Generation) (Generation, error)
	// RecentGenerations lists the latest artifacts, newest first.
	RecentGenerations(ctx context.Context, limit int) ([]Generation, error)
	Close()
}

// DocumentStore is the document side (player accounts). Accounts are
// keyed by name; a credential index (cred-type + ID → account name)
// makes credential lookups direct hits.
type DocumentStore interface {
	Ping(ctx context.Context) error
	// CreateAccount persists a new account and indexes its credentials.
	// ErrConflict if the name is taken or a credential is already
	// mapped to a different account.
	CreateAccount(ctx context.Context, a Account) error
	// AccountByName fetches the account; ErrNotFound when absent.
	AccountByName(ctx context.Context, name string) (Account, error)
	// AccountByCredential resolves a credential to its account, or
	// ErrNotFound when the credential is unregistered.
	AccountByCredential(ctx context.Context, credType, id string) (Account, error)
	// NameFree reports whether an account name is unused.
	NameFree(ctx context.Context, name string) (bool, error)
	// RenameAccount rekeys the account and reindexes its credentials.
	// ErrConflict if the new name is taken.
	RenameAccount(ctx context.Context, oldName, newName string) error
	// SetPasswordHash replaces the stored hash (acked untouched).
	SetPasswordHash(ctx context.Context, name, hash string) error
	// AckPassword marks the account verified: the player has copied
	// the generated password (or set their own).
	AckPassword(ctx context.Context, name string) error
	AttachCredential(ctx context.Context, name string, c Credential) error
	// ListUnverifiedBefore returns unverified accounts created before
	// t arbitrarily old (reaper input).
	ListUnverifiedBefore(ctx context.Context, t time.Time) ([]Account, error)
	Close()
}
