package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"remotedeck/internal/auth"
)

func TestBrowserSessionCSRFAndLogout(t *testing.T) {
	h := testHandler(t)
	h.Sessions = auth.NewSessions("http://127.0.0.1")
	login := func(origin string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "http://127.0.0.1/api/v1/session", nil)
		r.Header.Set("Authorization", "Bearer "+testToken)
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	for _, origin := range []string{"", "null", "http://127.0.0.1:1234", "http://attacker.test"} {
		if w := login(origin); w.Code != 403 {
			t.Fatalf("cross-origin login %d", w.Code)
		}
	}
	w := login("http://127.0.0.1")
	if w.Code != 201 {
		t.Fatalf("login %d %s", w.Code, w.Body)
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatal("missing session cookie")
	}
	cookie := cookies[0]
	if !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode || cookie.Path != "/api/v1" || cookie.Domain != "" || cookie.MaxAge != 28800 {
		t.Fatalf("cookie properties: %+v", cookie)
	}
	var session struct {
		Data auth.Session `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(w.Body.String(), cookie.Value) || strings.Contains(w.Body.String(), testToken) {
		t.Fatal("credential in JSON")
	}
	do := func(method, path, csrf, origin string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "http://127.0.0.1"+path, strings.NewReader(validSubmission))
		r.AddCookie(cookie)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Sec-Fetch-Site", "same-origin")
		if csrf != "" {
			r.Header.Set("X-CSRF-Token", csrf)
		}
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if w = do("GET", "/api/v1/session", "", ""); w.Code != 200 {
		t.Fatal("cannot resume session")
	}
	if w = do("GET", "/api/v1/jobs", "", ""); w.Code != 200 {
		t.Fatal("cookie read rejected")
	}
	for _, pair := range [][2]string{{"", "http://127.0.0.1"}, {"invalid", "http://127.0.0.1"}, {session.Data.CSRFToken, ""}, {session.Data.CSRFToken, "http://127.0.0.1:9999"}} {
		if w = do("POST", "/api/v1/jobs", pair[0], pair[1]); w.Code != 403 {
			t.Fatalf("CSRF bypass: %d", w.Code)
		}
	}
	if w = do("POST", "/api/v1/jobs", session.Data.CSRFToken, "http://127.0.0.1"); w.Code != 201 {
		t.Fatalf("cookie submit %d %s", w.Code, w.Body)
	}
	job := responseJob(t, w)
	if w = do("GET", "/api/v1/jobs/"+job.ID+"/logs", "", ""); w.Code != 200 {
		t.Fatal("cookie logs rejected")
	}
	r := httptest.NewRequest("GET", "http://127.0.0.1/api/v1/jobs", nil)
	r.AddCookie(cookie)
	r.Header.Set("Authorization", "invalid")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal("invalid explicit auth fell back to cookie")
	}
	if w = do("DELETE", "/api/v1/session", "", "http://127.0.0.1"); w.Code != 403 {
		t.Fatal("logout skipped CSRF")
	}
	if w = do("DELETE", "/api/v1/session", session.Data.CSRFToken, "http://127.0.0.1"); w.Code != 204 || w.Result().Cookies()[0].MaxAge != -1 {
		t.Fatal("logout failed")
	}
	if w = do("GET", "/api/v1/jobs", "", ""); w.Code != 401 {
		t.Fatal("old cookie remained authorized")
	}
	if w = request(h, "GET", "/api/v1/jobs", ""); w.Code != 200 {
		t.Fatal("bearer compatibility broken")
	}
}
