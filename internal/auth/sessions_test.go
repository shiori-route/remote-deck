package auth

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestSessionLifecycleBoundsAndExpiry(t *testing.T) {
	s := NewSessions("http://127.0.0.1:8080")
	now := time.Now()
	s.now = func() time.Time { return now }
	id, entry, err := s.Create("")
	if err != nil || !validToken(id) || !validToken(entry.CSRFToken) {
		t.Fatal("invalid session")
	}
	if _, ok := s.Get(id); !ok {
		t.Fatal("session missing")
	}
	if !ValidCSRF(entry, []string{entry.CSRFToken}) || ValidCSRF(entry, []string{id}) || ValidCSRF(entry, []string{entry.CSRFToken, entry.CSRFToken}) {
		t.Fatal("CSRF validation")
	}
	replacement, _, err := s.Create(id)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Get(id); ok {
		t.Fatal("old session survived rotation")
	}
	s.Delete(replacement)
	if _, ok := s.Get(replacement); ok {
		t.Fatal("logout did not revoke session")
	}
	for range MaxSessions {
		if _, _, err = s.Create(""); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err = s.Create(""); !errors.Is(err, ErrSessionCapacity) {
		t.Fatalf("unbounded sessions: %v", err)
	}
	now = now.Add(SessionTTL)
	if _, _, err = s.Create(""); err != nil {
		t.Fatal("expired sessions did not free capacity")
	}
	id, _, err = s.Create("")
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(SessionTTL)
	if _, ok := s.Get(id); ok {
		t.Fatal("expired session allowed")
	}
	if _, ok := NewSessions("http://127.0.0.1:8080").Get(id); ok {
		t.Fatal("session survived new process store")
	}
}

func TestConcurrentSessions(t *testing.T) {
	s := NewSessions("http://127.0.0.1:8080")
	var wg sync.WaitGroup
	for range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id, _, err := s.Create("")
			if err != nil {
				t.Error(err)
				return
			}
			if _, ok := s.Get(id); !ok {
				t.Error("session missing")
			}
			s.Delete(id)
		}()
	}
	wg.Wait()
}

func TestOriginAndDuplicateCookie(t *testing.T) {
	s := NewSessions("http://127.0.0.1:8080")
	r := httptest.NewRequest("POST", "http://127.0.0.1:8080/api/v1/session", nil)
	for _, origin := range []string{"", "null", "http://localhost:8080", "http://127.0.0.1:9999", "https://127.0.0.1:8080"} {
		r.Header.Set("Origin", origin)
		if s.SameOrigin(r) {
			t.Fatalf("accepted origin %q", origin)
		}
	}
	r.Header.Set("Origin", "http://127.0.0.1:8080")
	if !s.SameOrigin(r) {
		t.Fatal("rejected exact origin")
	}
	r.Header.Add("Origin", "http://127.0.0.1:8080")
	if s.SameOrigin(r) {
		t.Fatal("duplicate origin accepted")
	}
	id, _, _ := s.Create("")
	r.AddCookie(&http.Cookie{Name: SessionCookieName, Value: id})
	if SessionID(r) != id {
		t.Fatal("cookie unreadable")
	}
	r.AddCookie(&http.Cookie{Name: SessionCookieName, Value: id})
	if SessionID(r) != "" {
		t.Fatal("duplicate session cookie accepted")
	}
}
