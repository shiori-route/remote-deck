package api

import (
	"net/http"
	"time"

	"remotedeck/internal/auth"
)

func unauthorized(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Bearer realm="remotedeck"`)
	apiError(w, http.StatusUnauthorized, "unauthorized", "valid credentials are required")
}

func (h Handler) authorize(w http.ResponseWriter, r *http.Request) bool {
	if _, present := r.Header["Authorization"]; present {
		if h.Auth.Allows(r.Header.Values("Authorization")) {
			return true
		}
		unauthorized(w)
		return false // Invalid explicit credentials never fall back to cookies.
	}
	entry, ok := h.Sessions.Get(auth.SessionID(r))
	if !ok {
		unauthorized(w)
		return false
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return h.checkCSRF(w, r, entry)
	}
	return true
}

func (h Handler) checkCSRF(w http.ResponseWriter, r *http.Request, entry auth.Session) bool {
	if !h.Sessions.SameOrigin(r) || !auth.ValidCSRF(entry, r.Header.Values("X-CSRF-Token")) {
		apiError(w, 403, "csrf_failed", "same-origin request and valid CSRF token required")
		return false
	}
	return true
}

func (h Handler) session(w http.ResponseWriter, r *http.Request) {
	if h.Sessions == nil {
		unauthorized(w)
		return
	}
	switch r.Method {
	case http.MethodPost:
		if !h.Sessions.SameOrigin(r) {
			apiError(w, 403, "forbidden", "same-origin login required")
			return
		}
		if !h.Auth.Allows(r.Header.Values("Authorization")) {
			unauthorized(w)
			return
		}
		id, entry, err := h.Sessions.Create(auth.SessionID(r))
		if err != nil {
			apiError(w, 429, "session_limit", "too many active sessions; sign out or wait for expiry")
			return
		}
		setSessionCookie(w, id, entry.ExpiresAt)
		writeJSON(w, 201, map[string]any{"data": entry})
	case http.MethodGet, http.MethodDelete:
		id := auth.SessionID(r)
		entry, ok := h.Sessions.Get(id)
		if !ok {
			unauthorized(w)
			return
		}
		if _, present := r.Header["Authorization"]; present {
			unauthorized(w)
			return
		}
		if r.Method == http.MethodGet {
			writeJSON(w, 200, map[string]any{"data": entry})
			return
		}
		if !h.checkCSRF(w, r, entry) {
			return
		}
		h.Sessions.Delete(id)
		setSessionCookie(w, "", time.Time{})
		w.WriteHeader(http.StatusNoContent)
	default:
		methodError(w, "GET, POST, DELETE")
	}
}

func setSessionCookie(w http.ResponseWriter, id string, expires time.Time) {
	cookie := &http.Cookie{Name: auth.SessionCookieName, Value: id, Path: "/api/v1", HttpOnly: true, SameSite: http.SameSiteStrictMode, Expires: expires, MaxAge: int(auth.SessionTTL.Seconds())}
	if id == "" {
		cookie.MaxAge = -1
	}
	// Secure cannot be required on the current HTTP-only loopback deployment.
	// Network listeners remain forbidden; HTTPS deployment must revisit this.
	http.SetCookie(w, cookie)
}
