package auth

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wisnuvb/ronda/i18n"
)

func TestHashAndVerify(t *testing.T) {
	h, err := HashPassword("kata-sandi-panjang-1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "pbkdf2-sha256$310000$") {
		t.Fatalf("hash = %q", h)
	}
	if !VerifyPassword(h, "kata-sandi-panjang-1") {
		t.Fatal("correct password rejected")
	}
	if VerifyPassword(h, "kata-sandi-panjang-2") || VerifyPassword(h, "") {
		t.Fatal("wrong password accepted")
	}
	if h2, _ := HashPassword("kata-sandi-panjang-1"); h2 == h {
		t.Fatal("two hashes of one password must differ (random salt)")
	}
	if err := ValidHash(h); err != nil {
		t.Fatal(err)
	}
}

func TestHashRejectsShortPassword(t *testing.T) {
	if _, err := HashPassword("pendek"); err == nil {
		t.Fatal("a short password must be refused")
	}
}

func TestMalformedHashesNeverVerify(t *testing.T) {
	for _, h := range []string{
		"", "abc", "pbkdf2-sha256$1$a$b", "pbkdf2-sha256$x$AAAA$AAAA",
		"md5$1000$AAAA$AAAA", "pbkdf2-sha256$99999999999$AAAA$AAAA", "pbkdf2-sha256$1000$$",
	} {
		if ValidHash(h) == nil {
			t.Errorf("ValidHash(%q) = nil", h)
		}
		if VerifyPassword(h, "apa-saja-1234567") {
			t.Errorf("VerifyPassword accepted %q", h)
		}
	}
}

func TestSessionLifecycle(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	s := NewSessions(time.Hour, 24*time.Hour)
	s.now = func() time.Time { return now }

	tok, se := s.Create("admin")
	if len(tok) < 64 || se.CSRF == "" || se.CSRF == tok {
		t.Fatalf("token=%q csrf=%q", tok, se.CSRF)
	}
	got, ok := s.Get(tok)
	if !ok || got.User != "admin" || got.CSRF != se.CSRF {
		t.Fatalf("get: %+v %v", got, ok)
	}
	if _, ok := s.Get("token-lain"); ok {
		t.Fatal("unknown token accepted")
	}
	if _, ok := s.Get(""); ok {
		t.Fatal("empty token accepted")
	}

	// Activity keeps the session alive past the idle limit...
	for i := 0; i < 5; i++ {
		now = now.Add(50 * time.Minute)
		if _, ok := s.Get(tok); !ok {
			t.Fatalf("session died while in use (step %d)", i)
		}
	}
	// ...but never past the absolute limit.
	now = now.Add(23 * time.Hour)
	if _, ok := s.Get(tok); ok {
		t.Fatal("session outlived maxAge")
	}
}

func TestSessionIdleExpiryAndLogout(t *testing.T) {
	now := time.Now()
	s := NewSessions(time.Hour, 24*time.Hour)
	s.now = func() time.Time { return now }

	tok, _ := s.Create("a")
	now = now.Add(61 * time.Minute)
	if _, ok := s.Get(tok); ok {
		t.Fatal("idle session accepted")
	}

	tok, _ = s.Create("a")
	s.Delete(tok)
	if _, ok := s.Get(tok); ok {
		t.Fatal("deleted session accepted")
	}
}

func TestSessionsCleanedOnCreate(t *testing.T) {
	now := time.Now()
	s := NewSessions(time.Minute, time.Hour)
	s.now = func() time.Time { return now }
	for i := 0; i < 50; i++ {
		s.Create("a")
	}
	now = now.Add(2 * time.Minute)
	s.Create("b")
	if len(s.m) != 1 {
		t.Fatalf("expired sessions kept: %d", len(s.m))
	}
}

func TestLimiter(t *testing.T) {
	now := time.Now()
	l := NewLimiter(3, 10*time.Minute)
	l.now = func() time.Time { return now }

	for i := 0; i < 2; i++ {
		l.Fail("1.2.3.4")
	}
	if b, _ := l.Blocked("1.2.3.4"); b {
		t.Fatal("blocked too early")
	}
	l.Fail("1.2.3.4")
	b, wait := l.Blocked("1.2.3.4")
	if !b || wait <= 0 || wait > 10*time.Minute {
		t.Fatalf("blocked=%v wait=%v", b, wait)
	}
	if b, _ := l.Blocked("5.6.7.8"); b {
		t.Fatal("another client must not be blocked")
	}

	now = now.Add(11 * time.Minute)
	if b, _ := l.Blocked("1.2.3.4"); b {
		t.Fatal("block must lapse after the window")
	}

	l.Fail("9.9.9.9")
	l.Fail("9.9.9.9")
	l.Fail("9.9.9.9")
	l.Reset("9.9.9.9")
	if b, _ := l.Blocked("9.9.9.9"); b {
		t.Fatal("Reset must clear the block")
	}
}

func TestDecoyCostsTheSameAsTheRealHash(t *testing.T) {
	real, _ := HashPasswordCost("kata-sandi-panjang-1", 1234)
	decoy, err := Decoy(real)
	if err != nil {
		t.Fatal(err)
	}
	pr, _ := parse(real)
	pd, _ := parse(decoy)
	if pd.iter != pr.iter {
		t.Fatalf("decoy iterations = %d, want %d: a different cost would leak whether the user exists", pd.iter, pr.iter)
	}
	if VerifyPassword(decoy, "kata-sandi-panjang-1") {
		t.Fatal("the decoy must not accept the real password")
	}
	if _, err := Decoy("bukan-hash"); err == nil {
		t.Fatal("decoy of a malformed hash must error")
	}
}

func TestRevokeOthersKeepsOnlyTheCaller(t *testing.T) {
	s := NewSessions(time.Hour, time.Hour)
	mine, _ := s.Create("a")
	other1, _ := s.Create("a")
	other2, _ := s.Create("a")
	if n := s.RevokeOthers(mine); n != 2 {
		t.Fatalf("revoked %d, want 2", n)
	}
	if _, ok := s.Get(mine); !ok {
		t.Fatal("the session that changed the password must stay signed in")
	}
	for _, tok := range []string{other1, other2} {
		if _, ok := s.Get(tok); ok {
			t.Fatal("another device must be signed out")
		}
	}
}

func TestOverrideIsBoundToTheConfigHashItReplaced(t *testing.T) {
	dir := t.TempDir()
	cfgHash, _ := HashPasswordCost("sandi-di-config-1", 1000)
	newHash, _ := HashPasswordCost("sandi-baru-dari-ui-9", 1000)

	if _, ok := LoadOverride(dir, cfgHash); ok {
		t.Fatal("nothing saved yet")
	}
	if err := SaveOverride(dir, cfgHash, newHash); err != nil {
		t.Fatal(err)
	}
	if got, ok := LoadOverride(dir, cfgHash); !ok || got != newHash {
		t.Fatalf("the saved hash applies while the config hash is unchanged: %v %v", got, ok)
	}
	if fi, _ := os.Stat(filepath.Join(dir, "auth.json")); fi.Mode().Perm() != 0o600 {
		t.Fatalf("the file must be private to the service, mode %v", fi.Mode())
	}

	// An administrator edits the config: the override must stop applying on its own.
	edited, _ := HashPasswordCost("sandi-yang-diatur-admin-2", 1000)
	if _, ok := LoadOverride(dir, edited); ok {
		t.Fatal("a new config hash means the console has the last word; the old override must be ignored")
	}
}

func TestOverrideRejectsGarbage(t *testing.T) {
	dir := t.TempDir()
	cfg, _ := HashPasswordCost("sandi-di-config-1", 1000)
	if err := SaveOverride(dir, cfg, "bukan-hash"); err == nil {
		t.Fatal("an invalid hash must not be stored")
	}
	if err := SaveOverride("", cfg, cfg); err == nil {
		t.Fatal("no directory, no save")
	}
	os.WriteFile(filepath.Join(dir, "auth.json"), []byte("{rusak"), 0o600)
	if _, ok := LoadOverride(dir, cfg); ok {
		t.Fatal("a corrupt file must be ignored, never trusted")
	}
	os.WriteFile(filepath.Join(dir, "auth.json"), []byte(`{"password_hash":"x","config_fingerprint":"y"}`), 0o600)
	if _, ok := LoadOverride(dir, cfg); ok {
		t.Fatal("a file with an invalid hash must be ignored")
	}
}

func TestPasswordErrorsAreTranslated(t *testing.T) {
	_, err := HashPassword("short")
	if err == nil || err.Error() != "Password must be at least 12 characters" {
		t.Fatalf("english: %v", err)
	}
	if err := i18n.SetLang("id"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = i18n.SetLang("en") })
	_, err = HashPassword("short")
	if err == nil || err.Error() != "kata sandi minimal 12 karakter" {
		t.Fatalf("indonesian: %v", err)
	}
	if err := ValidHash("not-a-hash"); err == nil || !strings.Contains(err.Error(), "format hash tidak dikenal") {
		t.Fatalf("indonesian hash error: %v", err)
	}
}
