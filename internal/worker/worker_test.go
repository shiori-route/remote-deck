package worker

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"remotedeck/internal/job"
	"remotedeck/internal/runner"
	"remotedeck/internal/store"
	"remotedeck/internal/task"
)

type executeFunc func(context.Context, string, task.Spec) runner.Result

func (f executeFunc) Run(ctx context.Context, id string, s task.Spec) runner.Result {
	return f(ctx, id, s)
}
func testStore(t *testing.T) (*store.Store, string) {
	t.Helper()
	dir := t.TempDir()
	s, e := store.Open(filepath.Join(dir, "jobs.db"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close() })
	return s, dir
}
func queued(t *testing.T, s *store.Store) job.Job {
	t.Helper()
	j, e := s.Submit(context.Background(), "system-info", []byte(`{}`), 10)
	if e != nil {
		t.Fatal(e)
	}
	return j
}
func logger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }
func terminal(t *testing.T, s *store.Store, id string) job.Job {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		j, e := s.Get(context.Background(), id)
		if e != nil {
			t.Fatal(e)
		}
		if j.State.Terminal() {
			return j
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("job did not finish")
	return job.Job{}
}

func TestRealExecutionAndRestart(t *testing.T) {
	s, dir := testStore(t)
	j := queued(t, s)
	// Reopen with a queued job, as after a service restart.
	if e := s.Close(); e != nil {
		t.Fatal(e)
	}
	s, e := store.Open(filepath.Join(dir, "jobs.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	if e = s.Reconcile(context.Background()); e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- (Worker{Store: s, Executor: runner.Runner{Directory: dir}, Logger: logger()}).Run(ctx) }()
	got := terminal(t, s, j.ID)
	cancel()
	if e = <-done; e != nil {
		t.Fatal(e)
	}
	if got.State != job.Succeeded || got.ExitCode == nil || *got.ExitCode != 0 {
		t.Fatalf("%+v", got)
	}
	b, e := os.ReadFile(filepath.Join(dir, j.LogName))
	if e != nil || !strings.Contains(string(b), "Linux") {
		t.Fatalf("output %q %v", b, e)
	}
}

func TestShutdownPersistsCancellationAndLeavesQueue(t *testing.T) {
	s, _ := testStore(t)
	first := queued(t, s)
	second := queued(t, s)
	started := make(chan struct{})
	execute := executeFunc(func(ctx context.Context, _ string, _ task.Spec) runner.Result {
		close(started)
		<-ctx.Done()
		return runner.Result{State: job.Cancelled, Error: "shutdown"}
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- (Worker{s, execute, logger()}).Run(ctx) }()
	<-started
	cancel()
	if e := <-done; e != nil {
		t.Fatal(e)
	}
	a, e := s.Get(context.Background(), first.ID)
	if e != nil || a.State != job.Cancelled {
		t.Fatalf("first %+v %v", a, e)
	}
	b, e := s.Get(context.Background(), second.ID)
	if e != nil || b.State != job.Queued {
		t.Fatalf("second %+v %v", b, e)
	}
}

func TestOutcomeFailureStopsBeforeNextJob(t *testing.T) {
	s, _ := testStore(t)
	queued(t, s)
	queued(t, s)
	var calls atomic.Int32
	execute := executeFunc(func(context.Context, string, task.Spec) runner.Result {
		calls.Add(1)
		if e := s.Close(); e != nil {
			t.Error(e)
		}
		code := 0
		return runner.Result{State: job.Succeeded, ExitCode: &code}
	})
	err := (Worker{s, execute, logger()}).Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "persist outcome") || calls.Load() != 1 {
		t.Fatalf("err=%v calls=%d", err, calls.Load())
	}
}

func TestSerialFIFOAndUnsupportedDefinition(t *testing.T) {
	s, _ := testStore(t)
	bad, e := s.Submit(context.Background(), "removed-provider", []byte(`{}`), 10)
	if e != nil {
		t.Fatal(e)
	}
	first := queued(t, s)
	second := queued(t, s)
	var order []string
	execute := executeFunc(func(_ context.Context, id string, _ task.Spec) runner.Result {
		order = append(order, id)
		code := 0
		return runner.Result{State: job.Succeeded, ExitCode: &code}
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- (Worker{s, execute, logger()}).Run(ctx) }()
	terminal(t, s, second.ID)
	cancel()
	if e = <-done; e != nil {
		t.Fatal(e)
	}
	if len(order) != 2 || order[0] != first.ID || order[1] != second.ID {
		t.Fatalf("execution order %v", order)
	}
	got, e := s.Get(context.Background(), bad.ID)
	if e != nil || got.State != job.Failed || got.ExitCode != nil {
		t.Fatalf("unsupported %+v %v", got, e)
	}
}
