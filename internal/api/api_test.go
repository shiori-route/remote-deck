package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"remotedeck/internal/job"
	"remotedeck/internal/runner"
	"remotedeck/internal/store"
	"remotedeck/internal/worker"
)

func testHandler(t *testing.T) Handler {
	t.Helper()
	dir := t.TempDir()
	s, e := store.Open(filepath.Join(dir, "jobs.db"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close() })
	return Handler{s, dir, slog.New(slog.NewTextHandler(io.Discard, nil))}
}
func request(h Handler, method, path, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "http://127.0.0.1"+path, strings.NewReader(body))
	if method == "POST" {
		r.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func responseJob(t *testing.T, w *httptest.ResponseRecorder) job.Job {
	t.Helper()
	var data struct {
		Data job.Job `json:"data"`
	}
	if e := json.Unmarshal(w.Body.Bytes(), &data); e != nil {
		t.Fatal(e)
	}
	return data.Data
}

const validSubmission = `{"task_type":"system-info","parameters":{}}`

func TestHTTPExecutionLogsAndReopen(t *testing.T) {
	h := testHandler(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- (worker.Worker{Store: h.Store, Executor: runner.Runner{Directory: h.LogDirectory}, Logger: h.Logger}).Run(ctx)
	}()
	w := request(h, "POST", "/api/v1/jobs", validSubmission)
	if w.Code != 201 {
		t.Fatalf("submit %d %s", w.Code, w.Body)
	}
	j := responseJob(t, w)
	if j.State != job.Queued || !job.ValidID(j.ID) || w.Header().Get("Location") != "/api/v1/jobs/"+j.ID {
		t.Fatalf("submission %+v", j)
	}
	path := "/api/v1/jobs/" + j.ID
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		w = request(h, "GET", path, "")
		j = responseJob(t, w)
		if j.State.Terminal() {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	if e := <-done; e != nil {
		t.Fatal(e)
	}
	if j.State != job.Succeeded {
		t.Fatalf("job %+v", j)
	}
	w = request(h, "GET", path+"/logs?limit=2", "")
	if w.Code != 200 || w.Body.Len() != 2 || w.Header().Get("X-Next-Offset") != "2" {
		t.Fatalf("first log %d %q", w.Code, w.Body)
	}
	first := append([]byte(nil), w.Body.Bytes()...)
	w = request(h, "GET", path+"/logs?offset=2", "")
	all := append(first, w.Body.Bytes()...)
	if !bytes.Contains(all, []byte("Linux")) || w.Header().Get("X-Job-State") != "succeeded" {
		t.Fatalf("output %q", all)
	}
	if w = request(h, "GET", path+"/logs?offset=999999", ""); w.Code != 416 {
		t.Fatalf("offset %d", w.Code)
	}
	if e := h.Store.Close(); e != nil {
		t.Fatal(e)
	}
	s, e := store.Open(filepath.Join(h.LogDirectory, "jobs.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	h.Store = s
	w = request(h, "GET", path, "")
	if w.Code != 200 || responseJob(t, w).State != job.Succeeded {
		t.Fatalf("reopen %d %s", w.Code, w.Body)
	}
	w = request(h, "GET", path+"/logs", "")
	if w.Code != 200 || !bytes.Equal(all, w.Body.Bytes()) {
		t.Fatal("logs changed across reopen")
	}
}

func TestStrictRequestsAndErrorEnvelope(t *testing.T) {
	h := testHandler(t)
	for _, tc := range []struct {
		method, path, body string
		status             int
	}{
		{"POST", "/api/v1/jobs", `{}`, 400},
		{"POST", "/api/v1/jobs", `null`, 400},
		{"POST", "/api/v1/jobs", validSubmission + `{}`, 400},
		{"POST", "/api/v1/jobs", `{"task_type":"sh","parameters":{}}`, 400},
		{"POST", "/api/v1/jobs", `{"task_type":"system-info","parameters":{"cwd":"/etc"}}`, 400},
		{"POST", "/api/v1/jobs", `{"task_type":"system-info","parameters":null}`, 400},
		{"POST", "/api/v1/jobs", `{"task_type":"system-info","parameters":{},"command":"sh"}`, 400},
		{"POST", "/api/v1/jobs", `{"task_type":"system-info","task_type":"system-info","parameters":{}}`, 400},
		{"POST", "/api/v1/jobs", strings.Repeat("x", (16<<10)+1), 413},
		{"GET", "/api/v1/jobs?limit=0", "", 400},
		{"GET", "/api/v1/jobs?after=-1", "", 400},
		{"GET", "/api/v1/jobs?after=1&after=2", "", 400},
		{"GET", "/api/v1/jobs?path=/etc", "", 400},
		{"GET", "/api/v1/jobs?after=999999999999999999999", "", 400},
		{"GET", "/api/v1/jobs/../../etc/passwd", "", 404},
		{"GET", "/missing", "", 404},
		{"DELETE", "/api/v1/jobs", "", 405},
	} {
		w := request(h, tc.method, tc.path, tc.body)
		if w.Code != tc.status {
			t.Errorf("%s %s status=%d body=%s", tc.method, tc.path, w.Code, w.Body)
		}
		var envelope struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		if e := json.Unmarshal(w.Body.Bytes(), &envelope); e != nil || envelope.Error.Code == "" {
			t.Errorf("invalid error %s", w.Body)
		}
	}
	r := httptest.NewRequest("POST", "http://127.0.0.1/api/v1/jobs", strings.NewReader(validSubmission))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 415 {
		t.Fatal("missing content type accepted")
	}
	for _, header := range []string{"Origin", "Sec-Fetch-Site", "Host"} {
		r = httptest.NewRequest("GET", "http://127.0.0.1/api/v1/jobs", nil)
		if header == "Host" {
			r.Host = "attacker.test"
		} else {
			r.Header.Set(header, "attacker")
		}
		w = httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 403 {
			t.Fatalf("accepted %s", header)
		}
	}
}

func TestPaginationConcurrencyAndCapacity(t *testing.T) {
	h := testHandler(t)
	var wg sync.WaitGroup
	for range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w := request(h, "POST", "/api/v1/jobs", validSubmission)
			if w.Code != 201 {
				t.Errorf("submit %d", w.Code)
			}
		}()
	}
	wg.Wait()
	w := request(h, "GET", "/api/v1/jobs?limit=10", "")
	var page struct {
		Data []job.Job `json:"data"`
		Next int64     `json:"next_after"`
	}
	if e := json.Unmarshal(w.Body.Bytes(), &page); e != nil || len(page.Data) != 10 || page.Next != page.Data[9].Sequence {
		t.Fatalf("page %s %v", w.Body, e)
	}
	for range store.QueueLimit - 32 {
		if w = request(h, "POST", "/api/v1/jobs", validSubmission); w.Code != 201 {
			t.Fatalf("fill %d", w.Code)
		}
	}
	if w = request(h, "POST", "/api/v1/jobs", validSubmission); w.Code != 429 {
		t.Fatalf("full %d", w.Code)
	}
	w = request(h, "GET", "/api/v1/jobs?after=999", "")
	if !strings.Contains(w.Body.String(), `"data":[]`) || !strings.Contains(w.Body.String(), `"next_after":999`) {
		t.Fatalf("empty page %s", w.Body)
	}
}

func TestLogBoundsAndPathProtection(t *testing.T) {
	h := testHandler(t)
	j := responseJob(t, request(h, "POST", "/api/v1/jobs", validSubmission))
	path := "/api/v1/jobs/" + j.ID + "/logs"
	w := request(h, "GET", path, "")
	if w.Code != 200 || w.Body.Len() != 0 {
		t.Fatal("queued logs not empty")
	}
	for _, q := range []string{"?offset=-1", "?limit=0", "?limit=65537", "?limit=1&limit=2"} {
		if w = request(h, "GET", path+q, ""); w.Code != 400 {
			t.Fatalf("accepted %s", q)
		}
	}
	if w = request(h, "GET", path+"?offset=1", ""); w.Code != 416 {
		t.Fatal("absent log offset accepted")
	}
	logPath := filepath.Join(h.LogDirectory, j.ID+".log")
	// Symlink to another readable file must never be served.
	if e := os.Symlink(filepath.Join(h.LogDirectory, "jobs.db"), logPath); e != nil {
		t.Fatal(e)
	}
	w = request(h, "GET", path, "")
	if w.Code != 500 || strings.Contains(w.Body.String(), h.LogDirectory) {
		t.Fatalf("unsafe log response %d %s", w.Code, w.Body)
	}
	if w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal("missing response protections")
	}
}
