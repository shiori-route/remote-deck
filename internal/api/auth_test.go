package api

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"remotedeck/internal/auth"
)

type unreadBody struct{ t *testing.T }

func (b unreadBody) Read([]byte) (int, error) {
	b.t.Error("unauthorized request body was read")
	return 0, io.EOF
}

func TestEveryRouteRequiresAuthenticationBeforeAccess(t *testing.T) {
	v, err := auth.New(testToken)
	if err != nil {
		t.Fatal(err)
	}
	// Nil storage makes any accidental unauthenticated database access fail.
	h := Handler{Auth: v}
	id := strings.Repeat("a", 32)
	for _, route := range []struct{ method, path string }{
		{"POST", "/api/v1/jobs"}, {"GET", "/api/v1/jobs"},
		{"GET", "/api/v1/jobs/" + id}, {"GET", "/api/v1/jobs/" + id + "/logs"},
		{"GET", "/api/v1/task-types"}, {"DELETE", "/api/v1/jobs"}, {"GET", "/missing"},
	} {
		for _, credentials := range [][]string{nil, {"Bearer " + strings.Repeat("f", 64)}, {"Bearer " + testToken, "Bearer " + testToken}} {
			r := httptest.NewRequest(route.method, "http://127.0.0.1"+route.path, unreadBody{t})
			r.Header["Authorization"] = credentials
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != 401 || !strings.Contains(w.Body.String(), `"code":"unauthorized"`) {
				t.Fatalf("%s %s: %d %s", route.method, route.path, w.Code, w.Body)
			}
			if w.Header().Get("WWW-Authenticate") != `Bearer realm="remotedeck"` || w.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("missing authentication response headers")
			}
			if strings.Contains(w.Body.String(), testToken) {
				t.Fatal("response leaked token")
			}
		}
	}
	for _, path := range []string{"/api/v1/task-types?access_token=" + testToken, "/api/v1/task-types"} {
		r := httptest.NewRequest("GET", "http://127.0.0.1"+path, nil)
		r.AddCookie(&http.Cookie{Name: "access_token", Value: testToken})
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 401 {
			t.Fatal("query/cookie authorized a request")
		}
	}
	r := httptest.NewRequest("GET", "http://127.0.0.1/api/v1/task-types", nil)
	r.Header.Set("Authorization", "Bearer "+testToken)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("valid authorization: %d", w.Code)
	}
	w = httptest.NewRecorder()
	(Handler{}).ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal("unconfigured handler allowed access")
	}
}

func TestValidTokenDoesNotBypassBrowserProtections(t *testing.T) {
	v, err := auth.New(testToken)
	if err != nil {
		t.Fatal(err)
	}
	for _, header := range []string{"Host", "Origin", "Sec-Fetch-Site"} {
		r := httptest.NewRequest("GET", "http://127.0.0.1/api/v1/task-types", nil)
		r.Header.Set("Authorization", "Bearer "+testToken)
		if header == "Host" {
			r.Host = "untrusted.example"
		} else {
			r.Header.Set(header, "untrusted")
		}
		w := httptest.NewRecorder()
		(Handler{Auth: v}).ServeHTTP(w, r)
		if w.Code != 403 {
			t.Fatalf("valid token bypassed %s", header)
		}
	}
}
