// Package auth holds the dashboard's login machinery: password hashing, sessions
// kept in memory, and a limiter for failed logins.
package auth

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/wisnuvb/ronda/internal/i18n"
)

const (
	hashScheme     = "pbkdf2-sha256"
	hashIter       = 310_000
	maxHashIter    = 5_000_000
	MinPasswordLen = 12
)

var b64 = base64.RawStdEncoding

// HashPassword returns "pbkdf2-sha256$iterations$salt$hash".
func HashPassword(pw string) (string, error) { return HashPasswordCost(pw, hashIter) }

// HashPasswordCost is HashPassword with an explicit iteration count. Production
// code uses HashPassword; a low count exists so tests do not spend seconds hashing.
func HashPasswordCost(pw string, iter int) (string, error) {
	if len([]rune(pw)) < MinPasswordLen {
		return "", errors.New(i18n.T("Password must be at least %d characters", MinPasswordLen))
	}
	if iter < 1 || iter > maxHashIter {
		return "", errors.New(i18n.T("Invalid iteration count"))
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key, err := pbkdf2.Key(sha256.New, pw, salt, iter, 32)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s$%d$%s$%s", hashScheme, iter, b64.EncodeToString(salt), b64.EncodeToString(key)), nil
}

// Decoy returns a valid hash that costs exactly as much to verify as encoded, for
// verifying against when the user name is wrong so that the response time does not
// reveal whether it was right.
func Decoy(encoded string) (string, error) {
	p, err := parse(encoded)
	if err != nil {
		return "", err
	}
	return HashPasswordCost("kata-sandi-pengecoh-0000", p.iter)
}

type parsed struct {
	iter int
	salt []byte
	key  []byte
}

func parse(encoded string) (parsed, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 4 || parts[0] != hashScheme {
		return parsed{}, errors.New(i18n.T("Unknown hash format; create one with: ronda hash-password"))
	}
	iter, err := strconv.Atoi(parts[1])
	if err != nil || iter < 1 || iter > maxHashIter {
		return parsed{}, errors.New(i18n.T("Invalid hash iteration count"))
	}
	salt, err1 := b64.DecodeString(parts[2])
	key, err2 := b64.DecodeString(parts[3])
	if err1 != nil || err2 != nil || len(salt) == 0 || len(key) == 0 {
		return parsed{}, errors.New(i18n.T("Corrupt hash"))
	}
	return parsed{iter, salt, key}, nil
}

// ValidHash reports whether encoded is a hash VerifyPassword can use.
func ValidHash(encoded string) error {
	_, err := parse(encoded)
	return err
}

// VerifyPassword checks pw against encoded in constant time.
func VerifyPassword(encoded, pw string) bool {
	p, err := parse(encoded)
	if err != nil {
		return false
	}
	key, err := pbkdf2.Key(sha256.New, pw, p.salt, p.iter, len(p.key))
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare(key, p.key) == 1
}

// ---- sessions ----

type Session struct {
	User    string
	CSRF    string // must be echoed in X-CSRF-Token on state-changing requests
	Created time.Time
	Last    time.Time
}

// Sessions are in memory, so a restart signs everyone out. The map is keyed by
// the SHA-256 of the token, so a memory dump does not yield usable cookies.
type Sessions struct {
	idle   time.Duration // sign out after this long without use
	maxAge time.Duration // sign out this long after login regardless
	now    func() time.Time

	mu sync.Mutex
	m  map[[32]byte]*Session
}

func NewSessions(idle, maxAge time.Duration) *Sessions {
	return &Sessions{idle: idle, maxAge: maxAge, now: time.Now, m: map[[32]byte]*Session{}}
}

func randHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err) // the system's random source failing is not recoverable
	}
	return hex.EncodeToString(b)
}

func key(token string) [32]byte { return sha256.Sum256([]byte(token)) }

func (s *Sessions) expiredLocked(se *Session, now time.Time) bool {
	return now.Sub(se.Last) > s.idle || now.Sub(se.Created) > s.maxAge
}

// Create starts a session and returns its cookie token.
func (s *Sessions) Create(user string) (string, Session) {
	token := randHex(32)
	now := s.now()
	se := &Session{User: user, CSRF: randHex(16), Created: now, Last: now}

	s.mu.Lock()
	defer s.mu.Unlock()
	for k, v := range s.m { // opportunistic cleanup keeps the map bounded
		if s.expiredLocked(v, now) {
			delete(s.m, k)
		}
	}
	s.m[key(token)] = se
	return token, *se
}

// Get returns the session for token and marks it used.
func (s *Sessions) Get(token string) (Session, bool) {
	if token == "" {
		return Session{}, false
	}
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	k := key(token)
	se := s.m[k]
	if se == nil {
		return Session{}, false
	}
	if s.expiredLocked(se, now) {
		delete(s.m, k)
		return Session{}, false
	}
	se.Last = now
	return *se, true
}

// RevokeOthers signs out every session except the one holding keep, and says how many it ended.
func (s *Sessions) RevokeOthers(keep string) int {
	keepKey := key(keep)
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for k := range s.m {
		if k != keepKey {
			delete(s.m, k)
			n++
		}
	}
	return n
}

func (s *Sessions) Delete(token string) {
	s.mu.Lock()
	delete(s.m, key(token))
	s.mu.Unlock()
}

// ---- failed-login limiter ----

// Limiter blocks a client after too many failures within a window.
type Limiter struct {
	max    int
	window time.Duration
	now    func() time.Time

	mu   sync.Mutex
	fail map[string][]time.Time
}

func NewLimiter(max int, window time.Duration) *Limiter {
	return &Limiter{max: max, window: window, now: time.Now, fail: map[string][]time.Time{}}
}

func (l *Limiter) recentLocked(key string, now time.Time) []time.Time {
	var keep []time.Time
	for _, t := range l.fail[key] {
		if now.Sub(t) < l.window {
			keep = append(keep, t)
		}
	}
	if len(keep) == 0 {
		delete(l.fail, key)
	} else {
		l.fail[key] = keep
	}
	return keep
}

// Blocked reports whether key must wait, and for how long.
func (l *Limiter) Blocked(key string) (bool, time.Duration) {
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	recent := l.recentLocked(key, now)
	if len(recent) < l.max {
		return false, 0
	}
	return true, l.window - now.Sub(recent[0])
}

func (l *Limiter) Fail(key string) {
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.fail) > 10000 { // an address-spraying attacker must not grow memory without bound
		for k := range l.fail {
			l.recentLocked(k, now)
		}
	}
	l.recentLocked(key, now)
	l.fail[key] = append(l.fail[key], now)
}

func (l *Limiter) Reset(key string) {
	l.mu.Lock()
	delete(l.fail, key)
	l.mu.Unlock()
}
