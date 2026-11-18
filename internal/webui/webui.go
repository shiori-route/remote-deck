// Package webui serves the built local console embedded in the Go binary.
package webui

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

// Build assets with `npm ci && npm run build` in web before building Go.
//
//go:embed dist
var assets embed.FS

func Handler(api http.Handler, host string) http.Handler {
	files, err := fs.Sub(assets, "dist")
	if err != nil {
		panic(err)
	}
	static := http.FileServer(http.FS(files))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'self'; font-src 'self'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
		if r.Host != host {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(403)
			_, _ = w.Write([]byte(`{"error":{"code":"forbidden","message":"unexpected host"}}`))
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/") {
			api.ServeHTTP(w, r)
			return
		}
		if origins := r.Header.Values("Origin"); len(origins) > 0 && (len(origins) != 1 || origins[0] != "http://"+host) {
			http.Error(w, "forbidden", 403)
			return
		}
		if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "none" && site != "same-origin" {
			http.Error(w, "forbidden", 403)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", 405)
			return
		}
		if r.URL.Path != "/" && (!strings.HasPrefix(r.URL.Path, "/assets/") || strings.HasSuffix(r.URL.Path, "/")) {
			http.NotFound(w, r)
			return
		}
		static.ServeHTTP(w, r)
	})
}
