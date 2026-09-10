package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Document is the second PostgreSQL container used AS a document store:
// rows are `key → jsonb`, so documents keep whatever shape they need
// without schema migrations. Backed by the same driver/engine as the
// relational side — one dependency, two data models.
type Document struct {
	pool *pgxpool.Pool
}

// connectDocument opens the pool for the document backend and migrates.
func connectDocument(ctx context.Context, dsn string, cfg Config) (*Document, error) {
	var pool *pgxpool.Pool
	err := retry(ctx, "docdb", cfg.MaxRetries, cfg.RetryWait, func() error {
		p, err := pgxpool.New(ctx, dsn)
		if err != nil {
			return err
		}
		if err := p.Ping(ctx); err != nil {
			p.Close()
			return err
		}
		pool = p
		return nil
	})
	if err != nil {
		return nil, wrap("docdb", err)
	}

	d := &Document{pool: pool}
	if err := d.migrate(ctx); err != nil {
		d.Close()
		return nil, err
	}
	return d, nil
}

// migrate creates the single documents table. Idempotent — the whole
// point of the document backend is that user payloads never need one.
func (d *Document) migrate(ctx context.Context) error {
	_, err := d.pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS documents (
			key         TEXT PRIMARY KEY,
			doc         JSONB NOT NULL,
			created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
			updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
		);
	`)
	if err != nil {
		return fmt.Errorf("storage: docdb migrate: %w", err)
	}
	return nil
}

func (d *Document) Ping(ctx context.Context) error {
	if d == nil || d.pool == nil {
		return ErrNotAvailable
	}
	return d.pool.Ping(ctx)
}

// --- accounts ---------------------------------------------------------------
//
// Layout inside the single jsonb-documents table:
//
//	account:<name>   → the Account document
//	cred:<type>:<id> → {"account": "<name>"} — the credential index

func accountKey(name string) string     { return "account:" + name }
func credentialKey(t, id string) string { return "cred:" + t + ":" + id }

// docWriter is satisfied by both *pgxpool.Pool and *pgx.Tx, letting
// multi-document operations run on a transaction while single-document
// ones use the pool.
type docWriter interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

func marshalDoc(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("storage: encode document: %w", err)
	}
	return string(b), nil
}

func decodeDoc(raw string, v any) error {
	if err := json.Unmarshal([]byte(raw), v); err != nil {
		return fmt.Errorf("storage: decode document: %w", err)
	}
	return nil
}

func upsertDoc(ctx context.Context, qx docWriter, key, doc string) error {
	_, err := qx.Exec(ctx, `
		INSERT INTO documents (key, doc, updated_at) VALUES ($1, $2, now())
		ON CONFLICT (key) DO UPDATE SET doc = $2, updated_at = now()
	`, key, doc)
	if err != nil {
		return fmt.Errorf("storage: save %s: %w", key, err)
	}
	return nil
}

func deleteDoc(ctx context.Context, qx docWriter, key string) error {
	if _, err := qx.Exec(ctx, `DELETE FROM documents WHERE key = $1`, key); err != nil {
		return fmt.Errorf("storage: delete %s: %w", key, err)
	}
	return nil
}

func fetchDoc(ctx context.Context, qx docQuerier, key string, v any) error {
	var raw string
	err := qx.QueryRow(ctx, `SELECT doc::text FROM documents WHERE key = $1`, key).Scan(&raw)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("storage: fetch %s: %w", key, err)
	}
	return decodeDoc(raw, v)
}

// docQuerier is the read half of docWriter's pool/tx pair.
type docQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// avail guards every public operation against a nil backend.
func (d *Document) avail() error {
	if d == nil || d.pool == nil {
		return ErrNotAvailable
	}
	return nil
}

// CreateAccount persists the account and its credential index in one
// transaction. ErrConflict when the name is taken or any credential is
// already mapped to a different account.
func (d *Document) CreateAccount(ctx context.Context, a Account) error {
	if err := d.avail(); err != nil {
		return err
	}
	if a.CreatedAt.IsZero() {
		a.CreatedAt = time.Now().UTC()
	}
	if a.Credentials == nil {
		a.Credentials = []Credential{}
	}

	adoc, err := marshalDoc(a)
	if err != nil {
		return err
	}

	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("storage: create account: %w", err)
	}
	defer tx.Rollback(ctx)

	// all conflict probes and writes must see the same snapshot inside
	exists, err := existsDoc(ctx, tx, accountKey(a.Name))
	if err != nil {
		return fmt.Errorf("storage: create account: %w", err)
	}
	if exists {
		return ErrConflict
	}
	for _, c := range a.Credentials {
		var idx struct {
			Account string
		}
		err := fetchDoc(ctx, tx, credentialKey(c.Type, c.ID), &idx)
		switch {
		case errors.Is(err, ErrNotFound):
			// free — good
		case err != nil:
			return fmt.Errorf("storage: create account: %w", err)
		default:
			return ErrConflict // credential owned by another account
		}
		if err := upsertDoc(ctx, tx, credentialKey(c.Type, c.ID), marshalRef(a.Name)); err != nil {
			return fmt.Errorf("storage: create account: %w", err)
		}
	}
	if err := upsertDoc(ctx, tx, accountKey(a.Name), adoc); err != nil {
		return fmt.Errorf("storage: create account: %w", err)
	}
	err = tx.Commit(ctx)
	if err != nil {
		return fmt.Errorf("storage: create account: %w", err)
	}
	return nil
}

// marshalRef is the credential-index payload: which account owns it.
func marshalRef(name string) string {
	b, _ := json.Marshal(struct {
		Account string `json:"account"`
	}{Account: name})
	return string(b)
}

func existsDoc(ctx context.Context, qx docQuerier, key string) (bool, error) {
	var ok bool
	err := qx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM documents WHERE key = $1)`, key).Scan(&ok)
	if err != nil {
		return false, fmt.Errorf("storage: exists %s: %w", key, err)
	}
	return ok, nil
}

func (d *Document) AccountByName(ctx context.Context, name string) (Account, error) {
	if err := d.avail(); err != nil {
		return Account{}, err
	}
	var a Account
	if err := fetchDoc(ctx, d.pool, accountKey(name), &a); err != nil {
		return Account{}, err
	}
	return a, nil
}

func (d *Document) AccountByCredential(ctx context.Context, credType, id string) (Account, error) {
	if err := d.avail(); err != nil {
		return Account{}, err
	}
	var idx struct {
		Account string `json:"account"`
	}
	if err := fetchDoc(ctx, d.pool, credentialKey(credType, id), &idx); err != nil {
		return Account{}, err
	}
	var a Account
	if err := fetchDoc(ctx, d.pool, accountKey(idx.Account), &a); err != nil {
		if errors.Is(err, ErrNotFound) {
			// dangling index — treat as unregistered
			return Account{}, ErrNotFound
		}
		return Account{}, err
	}
	return a, nil
}

func (d *Document) NameFree(ctx context.Context, name string) (bool, error) {
	if err := d.avail(); err != nil {
		return false, err
	}
	exists, err := existsDoc(ctx, d.pool, accountKey(name))
	if err != nil {
		return false, err
	}
	return !exists, nil
}

// RenameAccount rekeys the account document and every credential index
// pointing at it, in one transaction. ErrConflict if the new name is
// taken.
func (d *Document) RenameAccount(ctx context.Context, oldName, newName string) error {
	if err := d.avail(); err != nil {
		return err
	}

	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("storage: rename account: %w", err)
	}
	defer tx.Rollback(ctx)

	var a Account
	if err := fetchDoc(ctx, tx, accountKey(oldName), &a); err != nil {
		return fmt.Errorf("storage: rename account: %w", err)
	}
	exists, err := existsDoc(ctx, tx, accountKey(newName))
	if err != nil {
		return fmt.Errorf("storage: rename account: %w", err)
	}
	if exists {
		return ErrConflict
	}

	old, new := oldName, newName
	a.Name = newName
	adoc, err := marshalDoc(a)
	if err != nil {
		return fmt.Errorf("storage: rename account: %w", err)
	}
	if err := upsertDoc(ctx, tx, accountKey(new), adoc); err != nil {
		return fmt.Errorf("storage: rename account: %w", err)
	}
	for _, c := range a.Credentials {
		if err := upsertDoc(ctx, tx, credentialKey(c.Type, c.ID), marshalRef(new)); err != nil {
			return fmt.Errorf("storage: rename account: %w", err)
		}
	}
	if err := deleteDoc(ctx, tx, accountKey(old)); err != nil {
		return fmt.Errorf("storage: rename account: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("storage: rename account: %w", err)
	}
	return nil
}

func (d *Document) SetPasswordHash(ctx context.Context, name, hash string) error {
	if err := d.avail(); err != nil {
		return err
	}
	var a Account
	if err := fetchDoc(ctx, d.pool, accountKey(name), &a); err != nil {
		return err
	}
	a.PasswordHash = hash
	adoc, err := marshalDoc(a)
	if err != nil {
		return fmt.Errorf("storage: set password: %w", err)
	}
	return upsertDoc(ctx, d.pool, accountKey(name), adoc)
}

// AckPassword marks the account verified — copied the generated
// password or set their own.
func (d *Document) AckPassword(ctx context.Context, name string) error {
	if err := d.avail(); err != nil {
		return err
	}
	var a Account
	if err := fetchDoc(ctx, d.pool, accountKey(name), &a); err != nil {
		return err
	}
	a.PasswordAcked = true
	adoc, err := marshalDoc(a)
	if err != nil {
		return fmt.Errorf("storage: ack password: %w", err)
	}
	return upsertDoc(ctx, d.pool, accountKey(name), adoc)
}

func (d *Document) AttachCredential(ctx context.Context, name string, c Credential) error {
	if err := d.avail(); err != nil {
		return err
	}

	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("storage: attach credential: %w", err)
	}
	defer tx.Rollback(ctx)

	var idx struct {
		Account string `json:"account"`
	}
	err = fetchDoc(ctx, tx, credentialKey(c.Type, c.ID), &idx)
	switch {
	case errors.Is(err, ErrNotFound):
		// unregistered — the attach path's whole purpose
	case err != nil:
		return fmt.Errorf("storage: attach credential: %w", err)
	default:
		return ErrConflict // key already belongs to someone
	}

	var a Account
	if err := fetchDoc(ctx, tx, accountKey(name), &a); err != nil {
		return fmt.Errorf("storage: attach credential: %w", err)
	}
	if a.Credentials == nil {
		a.Credentials = []Credential{}
	}
	a.Credentials = append(a.Credentials, c)
	adoc, err := marshalDoc(a)
	if err != nil {
		return fmt.Errorf("storage: attach credential: %w", err)
	}
	if err := upsertDoc(ctx, tx, credentialKey(c.Type, c.ID), marshalRef(name)); err != nil {
		return fmt.Errorf("storage: attach credential: %w", err)
	}
	if err := upsertDoc(ctx, tx, accountKey(name), adoc); err != nil {
		return fmt.Errorf("storage: attach credential: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("storage: attach credential: %w", err)
	}
	return nil
}

// ListUnverifiedBefore returns accounts that (a) are unverified and
// (b) were created before t — the reaper's candidate set. Scans the
// account docs; fine at this scale, gets a real query/index later.
func (d *Document) ListUnverifiedBefore(ctx context.Context, t time.Time) ([]Account, error) {
	if err := d.avail(); err != nil {
		return nil, err
	}
	rows, err := d.pool.Query(ctx,
		`SELECT doc::text FROM documents WHERE key LIKE 'account:%' AND (doc->>'password_acked' = 'false') AND (doc->>'created_at') < $1`,
		t.Format(time.RFC3339Nano))
	if err != nil {
		return nil, fmt.Errorf("storage: list unverified: %w", err)
	}
	defer rows.Close()

	out := []Account{}
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, fmt.Errorf("storage: list unverified: %w", err)
		}
		var a Account
		if err := decodeDoc(raw, &a); err != nil {
			return nil, fmt.Errorf("storage: list unverified: %w", err)
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("storage: list unverified: %w", err)
	}
	return out, nil
}

func (d *Document) Close() {
	if d != nil && d.pool != nil {
		d.pool.Close()
	}
}
