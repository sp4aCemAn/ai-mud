package storage

import (
	"context"
	"os"
	"testing"
	"time"
)

// Integration tests against the real containers. Skipped unless
// STORAGE_INTEGRATION=1 and the databases are reachable on localhost
// (`docker compose up postgres docdb` exposes 5432/5433):
//
//	STORAGE_INTEGRATION=1 go test ./internal/storage -v
func integrationEnabled(t *testing.T) {
	t.Helper()
	if os.Getenv("STORAGE_INTEGRATION") != "1" {
		t.Skip("set STORAGE_INTEGRATION=1 to run against real databases")
	}
}

func testCfg() Config {
	cfg := ConfigFromEnv()
	cfg.MaxRetries = 2
	cfg.RetryWait = time.Second
	return cfg
}

// purge deletes fixture rows so tests stay idempotent across runs —
// CreateAccount is insert-only (ErrConflict), never an upsert.
func purge(t *testing.T, d *Document, keys ...string) {
	t.Helper()
	for _, k := range keys {
		if _, err := d.pool.Exec(context.Background(),
			`DELETE FROM documents WHERE key = $1`, k); err != nil {
			t.Fatalf("purge %s: %v", k, err)
		}
	}
}

var (
	acctGrim  = "account:grim-thistle-91"
	acctDup   = "account:grim-thistle-92"
	acctIcy   = "account:icy-fir-374"
	acctReap  = "account:reap-target-01"
	credTest1 = "cred:ssh:SHA256:testkey123"
	credLap2  = "cred:ssh:SHA256:laptop2"
)

func TestIntegrationRelational(t *testing.T) {
	integrationEnabled(t)
	cfg := testCfg()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	r, err := connectPostgres(ctx, cfg.PostgresDSN, cfg)
	if err != nil {
		t.Fatalf("connect postgres: %v", err)
	}
	defer r.Close()

	g, err := r.SaveGeneration(ctx, Generation{
		Kind: "test", Model: "stub-model", Prompt: "describe a tavern", Output: "a dimly lit room",
	})
	if err != nil {
		t.Fatalf("SaveGeneration: %v", err)
	}
	if g.ID == 0 || g.CreatedAt.IsZero() {
		t.Fatalf("returned generation missing id/created_at: %+v", g)
	}

	rec, err := r.RecentGenerations(ctx, 5)
	if err != nil || len(rec) == 0 {
		t.Fatalf("RecentGenerations: %v", err)
	}
	if rec[0].ID != g.ID {
		t.Fatalf("newest-first ordering broken: first id %d, saved %d", rec[0].ID, g.ID)
	}
}

func TestIntegrationDocument(t *testing.T) {
	integrationEnabled(t)
	cfg := testCfg()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	d, err := connectDocument(ctx, cfg.DocumentDSN, cfg)
	if err != nil {
		t.Fatalf("connect docdb: %v", err)
	}
	defer d.Close()

	purge(t, d, acctGrim, acctDup, acctIcy, acctReap, credTest1, credLap2, "cred:ssh:SHA256:reap-target-01")

	// --- create + lookups ---
	a := Account{
		Name:          "grim-thistle-91",
		PasswordAcked: false,
		Credentials:   []Credential{{Type: "ssh", ID: "SHA256:testkey123", Added: time.Now().UTC()}},
	}
	if err := d.CreateAccount(ctx, a); err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}

	got, err := d.AccountByName(ctx, a.Name)
	if err != nil || got.Name != a.Name {
		t.Fatalf("AccountByName: %+v err=%v", got, err)
	}
	if got.CreatedAt.IsZero() || got.Credentials[0].ID != "SHA256:testkey123" {
		t.Fatalf("stored account malformed: %+v", got)
	}

	cred, err := d.AccountByCredential(ctx, "ssh", "SHA256:testkey123")
	if err != nil || cred.Name != a.Name {
		t.Fatalf("AccountByCredential: %+v err=%v", cred, err)
	}

	if _, err := d.AccountByCredential(ctx, "ssh", "SHA256:missing"); err != ErrNotFound {
		t.Fatalf("missing credential should be ErrNotFound, got %v", err)
	}
	if free, err := d.NameFree(ctx, "grim-thistle-91"); err != nil || free {
		t.Fatalf("NameFree on taken name: free=%v err=%v", free, err)
	}

	// --- conflicts ---
	if err := d.CreateAccount(ctx, a); err != ErrConflict {
		t.Fatalf("duplicate name should be ErrConflict, got %v", err)
	}
	dup := a
	dup.Name = "grim-thistle-92"
	if err := d.CreateAccount(ctx, dup); err != ErrConflict {
		t.Fatalf("re-used credential should be ErrConflict, got %v", err)
	}

	// --- password lifecycle (verified == PasswordAcked) ---
	if err := d.AckPassword(ctx, a.Name); err != nil {
		t.Fatalf("AckPassword: %v", err)
	}
	if got, _ := d.AccountByName(ctx, a.Name); !got.PasswordAcked {
		t.Fatal("AckPassword did not persist")
	}
	if err := d.SetPasswordHash(ctx, a.Name, "$argon2id$test-hash"); err != nil {
		t.Fatalf("SetPasswordHash: %v", err)
	}
	if got, _ := d.AccountByName(ctx, a.Name); got.PasswordHash != "$argon2id$test-hash" {
		t.Fatalf("hash not persisted: %q", got.PasswordHash)
	}

	// --- attach a second credential ---
	if err := d.AttachCredential(ctx, a.Name, Credential{Type: "ssh", ID: "SHA256:laptop2", Added: time.Now().UTC()}); err != nil {
		t.Fatalf("AttachCredential: %v", err)
	}
	cred, err = d.AccountByCredential(ctx, "ssh", "SHA256:laptop2")
	if err != nil || cred.Name != a.Name {
		t.Fatalf("attached credential lookup: %+v err=%v", cred, err)
	}
	if err := d.AttachCredential(ctx, a.Name, Credential{Type: "ssh", ID: "SHA256:laptop2"}); err != ErrConflict {
		t.Fatalf("conflicting attach should be ErrConflict, got %v", err)
	}

	// --- rename rekeys account + all indexes ---
	if err := d.RenameAccount(ctx, a.Name, "icy-fir-374"); err != nil {
		t.Fatalf("RenameAccount: %v", err)
	}
	if _, err := d.AccountByName(ctx, a.Name); err != ErrNotFound {
		t.Fatalf("old name should be gone, got %v", err)
	}
	renamed, err := d.AccountByName(ctx, "icy-fir-374")
	if err != nil || len(renamed.Credentials) != 2 {
		t.Fatalf("renamed account: %+v err=%v", renamed, err)
	}
	for _, c := range renamed.Credentials {
		cred, err := d.AccountByCredential(ctx, c.Type, c.ID)
		if err != nil || cred.Name != "icy-fir-374" {
			t.Fatalf("index not rekeyed: %s err=%v", c.ID, err)
		}
	}
	if err := d.RenameAccount(ctx, "icy-fir-374", "grim-thistle-91"); err != nil {
		// free again after the first rename, must succeed
		t.Fatalf("rename to reclaimed name: %v", err)
	}
}

func TestIntegrationUnverifiedReaperList(t *testing.T) {
	integrationEnabled(t)
	cfg := testCfg()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	d, err := connectDocument(ctx, cfg.DocumentDSN, cfg)
	if err != nil {
		t.Fatalf("connect docdb: %v", err)
	}
	defer d.Close()

	purge(t, d, acctReap)

	old := time.Now().UTC().Add(-48 * time.Hour)
	a := Account{Name: "reap-target-01", CreatedAt: old}
	if err := d.CreateAccount(ctx, a); err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}

	got, err := d.ListUnverifiedBefore(ctx, time.Now().UTC())
	if err != nil {
		t.Fatalf("ListUnverifiedBefore: %v", err)
	}
	found := false
	for _, acc := range got {
		if acc.Name == "reap-target-01" {
			found = true
		}
	}
	if !found {
		t.Fatalf("old unverified account not listed: %+v", got)
	}
}

func TestIntegrationBothBackendsAreSeparateContainers(t *testing.T) {
	integrationEnabled(t)
	cfg := testCfg()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	s, err := Connect(ctx, cfg)
	if err != nil {
		t.Fatalf("Connect both: %v", err)
	}
	defer s.Close()

	if err := s.Relational.Ping(ctx); err != nil {
		t.Fatalf("relational ping: %v", err)
	}
	if err := s.Document.Ping(ctx); err != nil {
		t.Fatalf("document ping: %v", err)
	}
}
