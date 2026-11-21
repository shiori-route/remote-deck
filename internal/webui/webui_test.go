package webui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestEmbeddedConsoleAndSecurityHeaders(t *testing.T) {
	h := Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(401) }), "http://127.0.0.1:8080")
	for _, tc := range []struct {
		method, path string
		status       int
	}{{"GET", "/", 200}, {"GET", "/api/v1/jobs", 401}, {"GET", "/auth.token", 404}, {"GET", "/assets/", 404}, {"POST", "/", 405}} {
		r := httptest.NewRequest(tc.method, "http://127.0.0.1:8080"+tc.path, nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Fatalf("%s %s: %d", tc.method, tc.path, w.Code)
		}
		if !strings.Contains(w.Header().Get("Content-Security-Policy"), "frame-ancestors 'none'") || w.Header().Get("Referrer-Policy") != "no-referrer" {
			t.Fatal("missing security headers")
		}
		if tc.path == "/" && tc.method == "GET" && !strings.Contains(w.Body.String(), "/assets/") {
			t.Fatal("missing frontend build")
		}
	}
	for _, host := range []string{"attacker.test", "127.0.0.1:9999"} {
		r := httptest.NewRequest("GET", "http://"+host+"/", nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 403 {
			t.Fatal("unsafe host accepted")
		}
	}
}
