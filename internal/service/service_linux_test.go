package service

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"remotedeck/internal/auth"
	"remotedeck/internal/job"
)

func privateDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	return dir
}

func testLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }
func openService(t *testing.T, dir string) *Service {
	t.Helper()
	s, e := Open(Config{"127.0.0.1:0", dir}, testLogger())
	if e != nil {
		t.Fatal(e)
	}
	return s
}

func TestPrivateStorageAndInstanceLock(t *testing.T) {
	dir := privateDir(t)
	s := openService(t, dir)
	if other, e := Open(Config{"127.0.0.1:0", dir}, testLogger()); e == nil {
		other.Close()
		s.Close()
		t.Fatal("accepted second instance")
	}
	for _, name := range []string{"service.lock", "jobs.db", "jobs.db-wal", "jobs.db-shm", auth.TokenFile} {
		info, e := os.Stat(filepath.Join(dir, name))
		if e != nil || info.Mode().Perm() != 0600 {
			t.Fatalf("%s: %v %v", name, info, e)
		}
	}
	if e := s.Close(); e != nil {
		t.Fatal(e)
	}
	s = openService(t, dir)
	if e := s.Close(); e != nil {
		t.Fatal(e)
	}
}

func TestRejectUnsafeConfiguration(t *testing.T) {
	for _, addr := range []string{"0.0.0.0:8080", ":8080", "localhost:8080", "[::1]:8080", "127.0.0.1:http"} {
		if s, e := Open(Config{addr, privateDir(t)}, testLogger()); e == nil {
			s.Close()
			t.Fatalf("accepted %s", addr)
		}
	}
	dir := privateDir(t)
	if e := os.Chmod(dir, 0755); e != nil {
		t.Fatal(e)
	}
	if s, e := Open(Config{"127.0.0.1:0", dir}, testLogger()); e == nil {
		s.Close()
		t.Fatal("accepted shared directory")
	}
	base := privateDir(t)
	link := filepath.Join(base, "link")
	if e := os.Symlink(privateDir(t), link); e != nil {
		t.Fatal(e)
	}
	if s, e := Open(Config{"127.0.0.1:0", link}, testLogger()); e == nil {
		s.Close()
		t.Fatal("accepted symlink root")
	}
	for _, name := range []string{"service.lock", "jobs.db", "jobs.db-wal", auth.TokenFile} {
		dir = privateDir(t)
		target := filepath.Join(privateDir(t), "target")
		if e := os.WriteFile(target, []byte("preserve"), 0600); e != nil {
			t.Fatal(e)
		}
		if e := os.Symlink(target, filepath.Join(dir, name)); e != nil {
			t.Fatal(e)
		}
		if s, e := Open(Config{"127.0.0.1:0", dir}, testLogger()); e == nil {
			s.Close()
			t.Fatalf("accepted symlink %s", name)
		}
		b, _ := os.ReadFile(target)
		if string(b) != "preserve" {
			t.Fatal("changed link target")
		}
	}
}

func TestWorkerFailureClosesServiceAndReleasesLock(t *testing.T) {
	dir := privateDir(t)
	s := openService(t, dir)
	if err := s.db.Close(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := s.Run(ctx); err == nil || !strings.Contains(err.Error(), "claim job") {
		t.Fatalf("worker failure not surfaced: %v", err)
	}
	next := openService(t, dir)
	if err := next.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestInvalidTokenBlocksStartupAndReleasesLock(t *testing.T) {
	dir := privateDir(t)
	path := filepath.Join(dir, auth.TokenFile)
	if err := os.WriteFile(path, []byte("incomplete-token"), 0600); err != nil {
		t.Fatal(err)
	}
	if s, err := Open(Config{"127.0.0.1:0", dir}, testLogger()); err == nil {
		s.Close()
		t.Fatal("started with invalid credentials")
	} else if strings.Contains(err.Error(), "incomplete-token") {
		t.Fatal("startup error leaked credentials")
	}
	if _, err := os.Stat(filepath.Join(dir, "jobs.db")); !os.IsNotExist(err) {
		t.Fatal("database opened before token validation")
	}
	// An operator repair while stopped must not be blocked by a leaked lock.
	if err := os.WriteFile(path, []byte(strings.Repeat("ab", 32)+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	s := openService(t, dir)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestActualHTTPServiceAndRestart(t *testing.T) {
	dir := privateDir(t)
	s := openService(t, dir)
	data, err := os.ReadFile(filepath.Join(dir, auth.TokenFile))
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Timeout: 3 * time.Second, Transport: bearerTransport{token: strings.TrimSuffix(string(data), "\n")}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()
	base := "http://" + s.Address()
	res, e := client.Post(base+"/api/v1/jobs", "application/json", strings.NewReader(`{"task_type":"system-info","parameters":{}}`))
	if e != nil {
		t.Fatal(e)
	}
	var envelope struct {
		Data job.Job `json:"data"`
	}
	e = json.NewDecoder(res.Body).Decode(&envelope)
	res.Body.Close()
	if e != nil || res.StatusCode != 201 {
		t.Fatalf("submit %d %v", res.StatusCode, e)
	}
	id := envelope.Data.ID
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		res, e = client.Get(base + "/api/v1/jobs/" + id)
		if e != nil {
			t.Fatal(e)
		}
		e = json.NewDecoder(res.Body).Decode(&envelope)
		res.Body.Close()
		if e != nil {
			t.Fatal(e)
		}
		if envelope.Data.State.Terminal() {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if envelope.Data.State != job.Succeeded {
		t.Fatalf("job %+v", envelope.Data)
	}
	cancel()
	if e = <-done; e != nil {
		t.Fatal(e)
	}
	s = openService(t, dir)
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	go func() { done <- s.Run(ctx) }()
	base = "http://" + s.Address()
	res, e = client.Get(base + "/api/v1/jobs/" + id + "/logs")
	if e != nil {
		t.Fatal(e)
	}
	b, e := io.ReadAll(res.Body)
	res.Body.Close()
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if e != nil || res.StatusCode != 200 || !strings.Contains(string(b), "Linux") {
		t.Fatalf("restart logs %d %q %v", res.StatusCode, b, e)
	}
}

type bearerTransport struct{ token string }

func (tr bearerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+tr.token)
	return http.DefaultTransport.RoundTrip(r)
}

// This helper dies abruptly while holding the service lock and a running row.
// It launches no job child: the test proves metadata reconciliation and lock
// release after death, not descendant containment.
func TestCrashHelper(t *testing.T) {
	if len(os.Args) < 3 || os.Args[len(os.Args)-2] != "--crash-helper" {
		return
	}
	dir := os.Args[len(os.Args)-1]
	s, e := Open(Config{"127.0.0.1:0", dir}, testLogger())
	if e != nil {
		os.Exit(2)
	}
	j, e := s.db.Submit(context.Background(), "system-info", []byte(`{}`), 10)
	if e != nil {
		os.Exit(3)
	}
	if _, e = s.db.Claim(context.Background()); e != nil {
		os.Exit(4)
	}
	if _, e = s.db.Submit(context.Background(), "system-info", []byte(`{}`), 10); e != nil {
		os.Exit(5)
	}
	if e = os.WriteFile(filepath.Join(dir, "ready"), []byte(j.ID), 0600); e != nil {
		os.Exit(6)
	}
	time.Sleep(30 * time.Second)
	os.Exit(7)
}

func TestAbruptDeathReconcilesWithoutRetry(t *testing.T) {
	dir := privateDir(t)
	exe, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	cmd := exec.Command(exe, "-test.run=^TestCrashHelper$", "--", "--crash-helper", dir)
	if e = cmd.Start(); e != nil {
		t.Fatal(e)
	}
	waited := false
	defer func() {
		if !waited {
			cmd.Process.Kill()
			cmd.Wait()
		}
	}()
	var id []byte
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		id, e = os.ReadFile(filepath.Join(dir, "ready"))
		if e == nil && job.ValidID(string(id)) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !job.ValidID(string(id)) {
		t.Fatal("crash helper not ready")
	}
	if e = cmd.Process.Kill(); e != nil {
		t.Fatal(e)
	}
	if e = cmd.Wait(); e == nil {
		t.Fatal("helper unexpectedly succeeded")
	}
	waited = true
	s := openService(t, dir)
	defer s.Close()
	j, e := s.db.Get(context.Background(), string(id))
	if e != nil || j.State != job.Failed || j.ExitCode != nil || !strings.Contains(j.Error, "unknown") {
		t.Fatalf("interrupted job %+v %v", j, e)
	}
	list, e := s.db.List(context.Background(), 0, 100)
	if e != nil || len(list) != 2 || list[1].State != job.Queued {
		t.Fatalf("queue lost %v %v", list, e)
	}
}
