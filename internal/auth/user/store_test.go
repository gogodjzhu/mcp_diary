package user

import (
	"path/filepath"
	"testing"
	"time"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "users.json"), []byte("test-secret"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return s
}

func TestEmailRegistrationAndLogin(t *testing.T) {
	s := openTestStore(t)
	now := time.Now()

	if err := s.PutPendingCode(PurposeRegister, "Alice@Example.com", "123456", now); err != nil {
		t.Fatalf("PutPendingCode: %v", err)
	}
	if err := s.RedeemCode(PurposeRegister, "alice@example.com", "000000", now); err != ErrInvalidCode {
		t.Fatalf("wrong code: %v, want ErrInvalidCode", err)
	}
	if err := s.RedeemCode(PurposeRegister, "alice@example.com", "123456", now); err != nil {
		t.Fatalf("RedeemCode: %v", err)
	}

	u, err := s.CreateEmailUser("alice@example.com", "secret123", "Alice", now)
	if err != nil {
		t.Fatalf("CreateEmailUser: %v", err)
	}
	if !u.ID.Valid() || !u.EmailVerified || u.EmailRequired {
		t.Fatalf("user = %+v", u)
	}

	if _, err := s.CreateEmailUser("alice@example.com", "secret123", "", now); err != ErrEmailTaken {
		t.Fatalf("duplicate: %v, want ErrEmailTaken", err)
	}
	if _, err := s.AuthenticateEmail("alice@example.com", "wrong9999", now); err != ErrInvalidPassword {
		t.Fatalf("bad password: %v, want ErrInvalidPassword", err)
	}
	got, err := s.AuthenticateEmail("Alice@Example.com", "secret123", now)
	if err != nil || got.ID != u.ID {
		t.Fatalf("login = %+v, %v", got, err)
	}
}

func TestCodeLockoutAndExpiry(t *testing.T) {
	s := openTestStore(t)
	now := time.Now()
	if err := s.PutPendingCode(PurposeReset, "a@b.co", "111111", now); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < maxCodeAttempt-1; i++ {
		if err := s.RedeemCode(PurposeReset, "a@b.co", "000000", now); err != ErrInvalidCode {
			t.Fatalf("attempt %d: %v", i, err)
		}
	}
	if err := s.RedeemCode(PurposeReset, "a@b.co", "000000", now); err != ErrInvalidCode {
		t.Fatalf("last wrong attempt: %v", err)
	}
	if err := s.RedeemCode(PurposeReset, "a@b.co", "111111", now); err != ErrNoPendingCode {
		t.Fatalf("after lockout: %v, want ErrNoPendingCode", err)
	}

	if err := s.PutPendingCode(PurposeReset, "a@b.co", "222222", now); err != nil {
		t.Fatal(err)
	}
	later := now.Add(codeTTL + time.Second)
	if err := s.RedeemCode(PurposeReset, "a@b.co", "222222", later); err != ErrCodeExpired {
		t.Fatalf("expired: %v, want ErrCodeExpired", err)
	}
}

func TestUnlinkKeepsLastLoginMethod(t *testing.T) {
	s := openTestStore(t)
	now := time.Now()
	u, err := s.CreateEmailUser("a@b.co", "secret123", "", now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.LinkIdentity(u.ID, ProviderGoogle, "sub-1", Profile{Email: "a@gmail.com"}, now); err != nil {
		t.Fatal(err)
	}
	if err := s.UnlinkIdentity(u.ID, ProviderGoogle, now); err != nil {
		t.Fatalf("unlink google: %v", err)
	}
	if err := s.UnlinkIdentity(u.ID, ProviderEmail, now); err != ErrLastLoginMethod {
		t.Fatalf("unlink last: %v, want ErrLastLoginMethod", err)
	}
}

func TestSweepRemovesAbandonedUsers(t *testing.T) {
	s := openTestStore(t)
	past := time.Now().Add(-10 * 24 * time.Hour)
	u, err := s.CreateOAuthUser(ProviderGoogle, "sub-1", Profile{Email: "a@gmail.com"}, past)
	if err != nil {
		t.Fatal(err)
	}
	removed, err := s.Sweep(time.Now(), 7*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 1 || removed[0] != u.ID {
		t.Fatalf("removed = %v, want [%s]", removed, u.ID)
	}
	if _, err := s.Get(u.ID); err != ErrNotFound {
		t.Fatalf("get after sweep: %v", err)
	}
}

func TestStoreSurvivesReopen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "users.json")
	s, err := Open(path, []byte("test-secret"))
	if err != nil {
		t.Fatal(err)
	}
	u, err := s.CreateEmailUser("a@b.co", "secret123", "A", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path, []byte("test-secret"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := reopened.Get(u.ID)
	if err != nil || got.PrimaryEmail != "a@b.co" {
		t.Fatalf("reopened = %+v, %v", got, err)
	}
}

func TestPasswordRules(t *testing.T) {
	if err := CheckPasswordStrength("short1"); err != ErrPasswordTooShort {
		t.Fatalf("short: %v", err)
	}
	if err := CheckPasswordStrength("longpassword"); err != ErrWeakPassword {
		t.Fatalf("no digit: %v", err)
	}
	hash, err := HashPassword("secret123")
	if err != nil {
		t.Fatal(err)
	}
	if !VerifyPassword(hash, "secret123") {
		t.Fatal("matching password rejected")
	}
	if VerifyPassword(hash, "secret124") {
		t.Fatal("wrong password accepted")
	}
	if VerifyPassword("garbage", "secret123") {
		t.Fatal("malformed hash accepted")
	}
}
