package api

import (
	"crypto/rand"
	"encoding/base64"
	"sync"
	"time"
)

// sessionTTL is how long a login stays usable without being exercised.
const sessionTTL = 7 * 24 * time.Hour

// sessionStore hands browsers a random token so the password is never stored in
// localStorage. Memory-only, so a restart logs everyone out.
type sessionStore struct {
	mu      sync.Mutex
	expires map[string]time.Time
	ttl     time.Duration
}

func newSessionStore(ttl time.Duration) *sessionStore {
	return &sessionStore{expires: make(map[string]time.Time), ttl: ttl}
}

// issue mints a token. Callers must verify the password first.
func (s *sessionStore) issue() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	token := base64.RawURLEncoding.EncodeToString(buf)

	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.purgeLocked(now)
	s.expires[token] = now.Add(s.ttl)
	return token, nil
}

// valid reports a live session and slides its expiry. No constant-time compare:
// a 256-bit random key is not guessable, so timing leaks nothing.
func (s *sessionStore) valid(token string) bool {
	if token == "" {
		return false
	}
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	exp, ok := s.expires[token]
	if !ok {
		return false
	}
	if now.After(exp) {
		delete(s.expires, token)
		return false
	}
	s.expires[token] = now.Add(s.ttl)
	return true
}

func (s *sessionStore) revoke(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.expires, token)
}

func (s *sessionStore) purgeLocked(now time.Time) {
	for token, exp := range s.expires {
		if now.After(exp) {
			delete(s.expires, token)
		}
	}
}
