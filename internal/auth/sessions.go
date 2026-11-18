package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"net/http"
	"sync"
	"time"
)

const SessionCookieName = "remotedeck_session"
const SessionTTL = 8 * time.Hour
const MaxSessions = 64

var ErrSessionCapacity = errors.New("session capacity reached")

type Session struct {
	CSRFToken string    `json:"csrf_token"`
	ExpiresAt time.Time `json:"expires_at"`
}

// Sessions is bounded and process-local. Restart revokes all browser sessions.
type Sessions struct {
	mu      sync.Mutex
	entries map[[sha256.Size]byte]Session
	origin  string
	now     func() time.Time
}

func NewSessions(origin string) *Sessions {
	return &Sessions{entries: make(map[[sha256.Size]byte]Session), origin: origin, now: time.Now}
}

func (s *Sessions) SameOrigin(r *http.Request) bool {
	values := r.Header.Values("Origin")
	return s != nil && len(values) == 1 && values[0] == s.origin && "http://"+r.Host == s.origin
}

func (s *Sessions) Create(previous string) (string, Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	for id, entry := range s.entries {
		if !now.Before(entry.ExpiresAt) {
			delete(s.entries, id)
		}
	}
	// Reauthentication rotates the current session instead of consuming a slot.
	if validToken(previous) {
		delete(s.entries, sha256.Sum256([]byte(previous)))
	}
	if len(s.entries) >= MaxSessions {
		return "", Session{}, ErrSessionCapacity
	}
	id := randomToken()
	entry := Session{CSRFToken: randomToken(), ExpiresAt: now.Add(SessionTTL).UTC()}
	s.entries[sha256.Sum256([]byte(id))] = entry
	return id, entry, nil
}

func (s *Sessions) Get(id string) (Session, bool) {
	if s == nil || !validToken(id) {
		return Session{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := sha256.Sum256([]byte(id))
	entry, ok := s.entries[key]
	if !ok {
		return Session{}, false
	}
	if !s.now().Before(entry.ExpiresAt) {
		delete(s.entries, key)
		return Session{}, false
	}
	return entry, true
}

func (s *Sessions) Delete(id string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.entries, sha256.Sum256([]byte(id)))
}

func ValidCSRF(entry Session, headers []string) bool {
	if len(headers) != 1 || !validToken(headers[0]) {
		return false
	}
	got := sha256.Sum256([]byte(headers[0]))
	want := sha256.Sum256([]byte(entry.CSRFToken))
	return subtle.ConstantTimeCompare(got[:], want[:]) == 1
}

func SessionID(r *http.Request) string {
	id := ""
	for _, cookie := range r.Cookies() {
		if cookie.Name == SessionCookieName {
			if id != "" || !validToken(cookie.Value) {
				return ""
			}
			id = cookie.Value
		}
	}
	return id
}

func randomToken() string {
	var data [32]byte
	_, _ = rand.Read(data[:])
	return hex.EncodeToString(data[:])
}
