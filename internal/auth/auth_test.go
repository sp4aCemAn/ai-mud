package auth

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"path/filepath"
	"testing"

	"golang.org/x/crypto/ssh"
)

func TestGuestProviderSequence(t *testing.T) {
	g := NewGuestProvider()

	if _, err := g.Lookup("SHA256:abc"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Lookup should miss, got %v", err)
	}
	u, err := g.Create("SHA256:abc")
	if err != nil || u.Name != "guest" {
		t.Fatalf("Create: u=%+v err=%v", u, err)
	}

	id, err := Identify(g, "SHA256:abc")
	if err != nil || id.User.Name != "guest" || id.Fingerprint != "SHA256:abc" {
		t.Fatalf("Identify: %+v err=%v", id, err)
	}
}

func TestFingerprintNilKey(t *testing.T) {
	if got := Fingerprint(nil); got != "" {
		t.Fatalf("nil key should fingerprint to empty, got %q", got)
	}
}

func TestFingerprintRealKey(t *testing.T) {
	// generate a throwaway ed25519 key like a connecting client would have
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatalf("signer: %v", err)
	}
	fp := Fingerprint(signer.PublicKey())
	if fp == "" || len(fp) < 8 {
		t.Fatalf("fingerprint looks wrong: %q", fp)
	}
}

func TestJSONUserStoreRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "users.json")

	s, err := OpenJSONUserStore(path)
	if err != nil {
		t.Fatalf("open empty store: %v", err)
	}
	if _, ok := s.ByKey("SHA256:nope"); ok {
		t.Fatal("empty store should not find keys")
	}

	u1, err := s.Create("SHA256:aaa111")
	if err != nil || u1.ID == "" {
		t.Fatalf("create: %+v err=%v", u1, err)
	}

	// idempotent
	u1b, err := s.Create("SHA256:aaa111")
	if err != nil || u1b.ID != u1.ID {
		t.Fatalf("re-create should be idempotent: %+v vs %+v", u1b, u1)
	}

	// fresh store instance reads the same file (persistence works)
	s2, err := OpenJSONUserStore(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	got, ok := s2.ByKey("SHA256:aaa111")
	if !ok || got.ID != u1.ID {
		t.Fatalf("persisted lookup failed: ok=%v got=%+v want id=%s", ok, got, u1.ID)
	}
}

func TestStoreProviderThroughIdentify(t *testing.T) {
	p := StoreProvider{Store: mustStore(t)}

	id1, err := Identify(p, "SHA256:xyz999")
	if err != nil || id1.User.Name == "" {
		t.Fatalf("first connect (creates user): %+v err=%v", id1, err)
	}
	id2, err := Identify(p, "SHA256:xyz999")
	if err != nil {
		t.Fatalf("second connect: %v", err)
	}
	if id1.User.ID != id2.User.ID {
		t.Fatalf("same key should resolve to same user: %s vs %s", id1.User.ID, id2.User.ID)
	}
}

func TestJSONUserStoreRejectsEmptyFingerprint(t *testing.T) {
	s := mustStore(t)
	if _, err := s.Create(""); err == nil {
		t.Fatal("empty fingerprint should be rejected")
	}
}

func mustStore(t *testing.T) *JSONUserStore {
	t.Helper()
	s, err := OpenJSONUserStore(filepath.Join(t.TempDir(), "users.json"))
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	return s
}
