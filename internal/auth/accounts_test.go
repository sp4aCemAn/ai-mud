package auth

import (
	"strings"
	"testing"

	"github.com/sp4aceman/ai-mud/internal/storage"
)

// Memory-mode accounts (nil DB): the shape every DB-backed test path
// exercises mirrors what these assert.

func TestAutoAccountMintsNamesAndPasswords(t *testing.T) {
	a := NewAccounts(nil)

	acc, password, err := a.AutoAccount("ssh", "SHA256:abc")
	if err != nil {
		t.Fatalf("AutoAccount: %v", err)
	}
	if len(acc.Name) < 3 {
		t.Fatalf("auto name too short: %q", acc.Name)
	}
	if len(password) != passwordLen {
		t.Fatalf("generated password must be %d chars, got %d", passwordLen, len(password))
	}
	for _, r := range password {
		if !strings.ContainsRune(string(passwordSymbols), r) {
			t.Fatalf("password contains non-symbol %q", r)
		}
	}
	if len(acc.Credentials) != 1 || acc.Credentials[0].ID != "SHA256:abc" {
		t.Fatalf("credential must be attached at mint: %+v", acc.Credentials)
	}

	got, err := a.LookupByCredential("ssh", "SHA256:abc")
	if err != nil || got.Name != acc.Name {
		t.Fatalf("credential lookup after mint: %+v err=%v", got, err)
	}
}

func TestIdentifyAutoCreatesAndRecalls(t *testing.T) {
	a := NewAccounts(nil)

	id, err := a.Identify("SHA256:newkey")
	if err != nil {
		t.Fatalf("Identify: %v", err)
	}
	if !id.AccountFresh {
		t.Fatal("first sighting of a key must mint a fresh account")
	}
	if !id.Verified {
		t.Fatal("key-backed accounts are verified by the handshake itself")
	}
	if id.NewPassword == "" {
		t.Fatal("fresh accounts must carry the one-time password (fallback credential, revealed only via [v])")
	}

	again, err := a.Identify("SHA256:newkey")
	if err != nil {
		t.Fatalf("Identify again: %v", err)
	}
	if again.AccountFresh || again.NewPassword != "" {
		t.Fatalf("second sighting must be a recall, not a mint: %+v", again)
	}
	if again.User.Name != id.User.Name {
		t.Fatalf("same key should resolve the same account: %q vs %q", id.User.Name, again.User.Name)
	}
}

func TestGuestIdentity(t *testing.T) {
	a := NewAccounts(nil)
	id, err := a.Identify("")
	if err != nil {
		t.Fatalf("Identify guest: %v", err)
	}
	if id.User.Name != "guest" {
		t.Fatalf("anonymous connections stay guests, got %q", id.User.Name)
	}
}

func TestLoginPasswordFlow(t *testing.T) {
	a := NewAccounts(nil)

	acc, password, err := a.AutoAccount("ssh", "SHA256:seed")
	if err != nil {
		t.Fatalf("seed account: %v", err)
	}

	if _, err := a.Login(acc.Name, "not-it", "ssh", "SHA256:other"); err == nil {
		t.Fatal("wrong password must fail")
	}
	if _, err := a.Login("no-such-user", "whatever", "ssh", ""); err == nil {
		t.Fatal("unknown account must fail")
	}

	// correct password from another machine: attaches the new key
	other, err := a.Login(acc.Name, password, "ssh", "SHA256:newmachine")
	if err != nil {
		t.Fatalf("login with right password: %v", err)
	}
	if !a.hasCredential(other, "ssh", "SHA256:newmachine") {
		t.Fatal("login from a fresh machine should attach that key")
	}

	// and the attached key now identifies the same account
	got, err := a.LookupByCredential("ssh", "SHA256:newmachine")
	if err != nil || got.Name != acc.Name {
		t.Fatalf("attached key should resolve: %+v err=%v", got, err)
	}
}

func TestSetPasswordAndAck(t *testing.T) {
	a := NewAccounts(nil)

	acc, _, err := a.AutoAccount("ssh", "SHA256:abc")
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	if acc.PasswordAcked {
		t.Fatal("fresh accounts are unverified")
	}

	// setting your own counts as the ack + replaces the hash
	if err := a.SetPassword(acc.Name, "mine-own-!12"); err != nil {
		t.Fatalf("SetPassword: %v", err)
	}
	if verified, _ := a.Verified(acc.Name); !verified {
		t.Fatal("SetPassword must mark the account verified")
	}
	if _, err := a.Login(acc.Name, "mine-own-!12", "ssh", ""); err != nil {
		t.Fatalf("old generated password must stop working, new one must work: %v", err)
	}
}

func TestRenameAccount(t *testing.T) {
	a := NewAccounts(nil)

	acc, _, err := a.AutoAccount("ssh", "SHA256:abc")
	if err != nil {
		t.Fatalf("mint: %v", err)
	}

	// taken name conflicts
	other, _, _ := a.AutoAccount("ssh", "SHA256:def")
	if err := a.RenameAccount(acc.Name, other.Name); err == nil {
		t.Fatal("rename onto a taken name must fail")
	}

	// free rename reindexes the credential
	if err := a.RenameAccount(acc.Name, "grim-fir-55"); err != nil {
		t.Fatalf("rename: %v", err)
	}
	got, err := a.LookupByCredential("ssh", "SHA256:abc")
	if err != nil || got.Name != "grim-fir-55" {
		t.Fatalf("credential must follow the rename: %+v err=%v", got, err)
	}
	if _, err := a.Account(acc.Name); err != storage.ErrNotFound {
		t.Fatalf("old name should be gone, got %v", err)
	}
}

func TestLoginStealsKeyBoundToAnotherAccount(t *testing.T) {
	a := NewAccounts(nil)

	seed, _, err := a.AutoAccount("ssh", "SHA256:bound-to-seed")
	if err != nil {
		t.Fatalf("seed auto-account: %v", err)
	}

	// a named account created keyless: no key attached yet
	acc, namedPw, err := a.CreateNamed("kil-named-01")
	if err != nil {
		t.Fatalf("CreateNamed: %v", err)
	}

	// login with the named account's password FROM the device whose
	// fingerprint is bound to the seed account: the key binding must
	// move (steal) and the login must succeed
	got, err := a.Login(acc.Name, namedPw, "ssh", "SHA256:bound-to-seed")
	if err != nil {
		t.Fatalf("steal login: %v", err)
	}
	if got.Name != acc.Name {
		t.Fatalf("steal login returned wrong account: %s", got.Name)
	}
	if owner, err := a.LookupByCredential("ssh", "SHA256:bound-to-seed"); err != nil || owner.Name != acc.Name {
		t.Fatalf("fingerprint should now resolve to %s, got %+v err=%v", acc.Name, owner, err)
	}
	if holder, err := a.Account(seed.Name); err == nil {
		for _, c := range holder.Credentials {
			if c.ID == "SHA256:bound-to-seed" {
				t.Fatalf("old account still holds the stolen key: %+v", holder.Credentials)
			}
		}
	}
	holder, err := a.Account(acc.Name)
	if err != nil || !a.hasCredential(holder, "ssh", "SHA256:bound-to-seed") {
		t.Fatalf("steal target should hold the key: %+v err=%v", holder, err)
	}
}
