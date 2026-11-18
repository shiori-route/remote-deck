// Package api serves the loopback-only, versioned task API.
package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"remotedeck/internal/auth"
	"remotedeck/internal/job"
	"remotedeck/internal/store"
	"remotedeck/internal/task"
)

type Handler struct {
	Store        *store.Store
	LogDirectory string
	Logger       *slog.Logger
	Auth         auth.Verifier
	Sessions     *auth.Sessions
}

func (h Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	host := r.Host
	if parsed, _, err := net.SplitHostPort(host); err == nil {
		host = parsed
	}
	_, origin := r.Header["Origin"]
	fetchSite := r.Header.Get("Sec-Fetch-Site")
	if host != "127.0.0.1" || (origin && !h.Sessions.SameOrigin(r)) || (fetchSite != "" && fetchSite != "same-origin") {
		apiError(w, 403, "forbidden", "only trusted local clients are supported")
		return
	}
	if r.URL.Path == "/api/v1/session" {
		h.session(w, r)
		return
	}
	if !h.authorize(w, r) {
		return
	}
	path := r.URL.Path
	switch {
	case path == "/api/v1/jobs":
		switch r.Method {
		case http.MethodPost:
			h.submit(w, r)
		case http.MethodGet:
			h.list(w, r)
		default:
			methodError(w, "GET, POST")
		}
	case path == "/api/v1/task-types":
		if r.Method != http.MethodGet {
			methodError(w, "GET")
			return
		}
		writeJSON(w, 200, map[string]any{"data": task.Types()})
	case strings.HasPrefix(path, "/api/v1/jobs/"):
		parts := strings.Split(strings.TrimPrefix(path, "/api/v1/jobs/"), "/")
		if len(parts) > 2 || !job.ValidID(parts[0]) || (len(parts) == 2 && parts[1] != "logs") {
			apiError(w, 404, "not_found", "route or job not found")
			return
		}
		if r.Method != http.MethodGet {
			methodError(w, "GET")
			return
		}
		j, err := h.Store.Get(r.Context(), parts[0])
		if err != nil {
			h.storeError(w, err)
			return
		}
		if len(parts) == 2 {
			h.logs(w, r, j)
		} else {
			writeJSON(w, 200, map[string]any{"data": j})
		}
	default:
		apiError(w, 404, "not_found", "route not found")
	}
}

func (h Handler) submit(w http.ResponseWriter, r *http.Request) {
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		apiError(w, 415, "unsupported_media_type", "use application/json")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 16<<10))
	if err != nil {
		var max *http.MaxBytesError
		if errors.As(err, &max) {
			apiError(w, 413, "body_too_large", "body exceeds 16 KiB")
		} else {
			apiError(w, 400, "invalid_request", "cannot read body")
		}
		return
	}
	name, params, err := submission(body)
	if err != nil {
		apiError(w, 400, "invalid_request", "expected task_type and parameters with no extra or duplicate fields")
		return
	}
	spec, err := task.Resolve(name, params)
	if err != nil {
		apiError(w, 400, "invalid_task", err.Error())
		return
	}
	j, err := h.Store.Submit(r.Context(), name, params, int(spec.Timeout.Seconds()))
	if err != nil {
		h.storeError(w, err)
		return
	}
	w.Header().Set("Location", "/api/v1/jobs/"+j.ID)
	writeJSON(w, 201, map[string]any{"data": j})
}

// Decode the two exact keys, rejecting duplicates and trailing JSON documents.
func submission(body []byte) (string, json.RawMessage, error) {
	d := json.NewDecoder(bytes.NewReader(body))
	tok, err := d.Token()
	if err != nil || tok != json.Delim('{') {
		return "", nil, errors.New("expected object")
	}
	fields := map[string]json.RawMessage{}
	for d.More() {
		tok, err = d.Token()
		if err != nil {
			return "", nil, err
		}
		key, ok := tok.(string)
		if !ok || (key != "task_type" && key != "parameters") || fields[key] != nil {
			return "", nil, errors.New("invalid field")
		}
		var raw json.RawMessage
		if err = d.Decode(&raw); err != nil {
			return "", nil, err
		}
		fields[key] = raw
	}
	if _, err = d.Token(); err != nil {
		return "", nil, err
	}
	if _, err = d.Token(); err != io.EOF {
		return "", nil, errors.New("trailing JSON")
	}
	var name string
	if len(fields) != 2 || json.Unmarshal(fields["task_type"], &name) != nil {
		return "", nil, errors.New("missing or invalid fields")
	}
	return name, fields["parameters"], nil
}

func (h Handler) list(w http.ResponseWriter, r *http.Request) {
	q, err := query(r.URL.RawQuery, "after", "limit")
	if err != nil {
		apiError(w, 400, "invalid_query", "invalid pagination")
		return
	}
	after, err := number(q, "after", 0, 0, 1<<63-1)
	if err != nil {
		apiError(w, 400, "invalid_query", "invalid after cursor")
		return
	}
	limit, err := number(q, "limit", 20, 1, 100)
	if err != nil {
		apiError(w, 400, "invalid_query", "limit must be 1–100")
		return
	}
	jobs, err := h.Store.List(r.Context(), after, int(limit))
	if err != nil {
		h.storeError(w, err)
		return
	}
	next := after
	if len(jobs) > 0 {
		next = jobs[len(jobs)-1].Sequence
	}
	writeJSON(w, 200, map[string]any{"data": jobs, "next_after": next})
}

func (h Handler) logs(w http.ResponseWriter, r *http.Request, j job.Job) {
	q, err := query(r.URL.RawQuery, "offset", "limit")
	if err != nil {
		apiError(w, 400, "invalid_query", "invalid log query")
		return
	}
	offset, err := number(q, "offset", 0, 0, 1<<63-1)
	if err != nil {
		apiError(w, 400, "invalid_query", "invalid offset")
		return
	}
	limit, err := number(q, "limit", 65536, 1, 65536)
	if err != nil {
		apiError(w, 400, "invalid_query", "limit must be 1–65536")
		return
	}
	// Never use an untrusted database/client path. Nonblocking open prevents a
	// corrupted FIFO from hanging a request; nofollow rejects symlink logs.
	f, err := os.OpenFile(filepath.Join(h.LogDirectory, j.ID+".log"), os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	var data []byte
	if errors.Is(err, os.ErrNotExist) && !j.State.Terminal() {
		if offset != 0 {
			apiError(w, 416, "invalid_offset", "offset exceeds log size")
			return
		}
	} else {
		if err != nil {
			h.storeError(w, err)
			return
		}
		defer f.Close()
		info, err := f.Stat()
		if err != nil {
			h.storeError(w, err)
			return
		}
		if !info.Mode().IsRegular() {
			h.storeError(w, errors.New("log is not regular"))
			return
		}
		if offset > info.Size() {
			apiError(w, 416, "invalid_offset", "offset exceeds log size")
			return
		}
		data = make([]byte, int(limit))
		n, err := f.ReadAt(data, offset)
		if err != nil && err != io.EOF {
			h.storeError(w, err)
			return
		}
		data = data[:n]
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("X-Next-Offset", strconv.FormatInt(offset+int64(len(data)), 10))
	w.Header().Set("X-Job-State", string(j.State))
	w.WriteHeader(200)
	_, _ = w.Write(data)
}

func query(raw string, allowed ...string) (url.Values, error) {
	q, err := url.ParseQuery(raw)
	if err != nil {
		return nil, err
	}
	for k, v := range q {
		found := false
		for _, a := range allowed {
			if k == a {
				found = true
			}
		}
		if !found || len(v) != 1 {
			return nil, errors.New("invalid query key")
		}
	}
	return q, nil
}
func number(q url.Values, key string, defaultValue, min, max int64) (int64, error) {
	v, ok := q[key]
	if !ok {
		return defaultValue, nil
	}
	n, err := strconv.ParseInt(v[0], 10, 64)
	if err != nil || n < min || n > max {
		return 0, errors.New("invalid number")
	}
	return n, nil
}
func (h Handler) storeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		apiError(w, 404, "not_found", "job not found")
	case errors.Is(err, store.ErrQueueFull):
		apiError(w, 429, "queue_full", "job queue is full")
	default:
		h.Logger.Error("request failed", "error", err)
		apiError(w, 500, "internal_error", "request could not be completed")
	}
}
func methodError(w http.ResponseWriter, allow string) {
	w.Header().Set("Allow", allow)
	apiError(w, 405, "method_not_allowed", "unsupported method")
}
func apiError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
