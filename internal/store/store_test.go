package store

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"

	"remotedeck/internal/job"
)

func openTest(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "jobs.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	return s
}
func submitTest(t *testing.T, s *Store) job.Job {
	t.Helper()
	j, err := s.Submit(context.Background(), "system-info", []byte(`{}`), 10)
	if err != nil {
		t.Fatal(err)
	}
	return j
}
func events(t *testing.T, s *Store, id string) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow("SELECT count(*) FROM job_events WHERE job_id=?", id).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestLifecycleAndPersistence(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "jobs.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if s != nil {
			s.Close()
		}
	}()
	j := submitTest(t, s)
	if j.State != job.Queued || j.Attempt != 0 || events(t, s, j.ID) != 1 {
		t.Fatalf("submission %+v", j)
	}
	zero := 0
	if !errors.Is(s.Finish(ctx, j.ID, job.Succeeded, &zero, ""), ErrConflict) {
		t.Fatal("finished unclaimed job")
	}
	r, err := s.Claim(ctx)
	if err != nil || r.ID != j.ID || r.StartedAt == nil || r.Attempt != 1 {
		t.Fatalf("claim %+v %v", r, err)
	}
	if !errors.Is(s.Finish(ctx, j.ID, job.Queued, nil, ""), ErrConflict) {
		t.Fatal("requeued running job")
	}
	if !errors.Is(s.Finish(ctx, j.ID, job.Succeeded, nil, ""), ErrConflict) {
		t.Fatal("success without exit status")
	}
	if err = s.Finish(ctx, j.ID, job.Succeeded, &zero, ""); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(s.Finish(ctx, j.ID, job.Failed, nil, "bad"), ErrConflict) {
		t.Fatal("changed terminal state")
	}
	if events(t, s, j.ID) != 3 {
		t.Fatal("wrong event count")
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s = nil
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(ctx, j.ID)
	if err != nil || got.State != job.Succeeded || got.CompletedAt == nil || got.ExitCode == nil || *got.ExitCode != 0 {
		t.Fatalf("reopen %+v %v", got, err)
	}
	if _, err = s.Claim(ctx); !errors.Is(err, ErrNotFound) {
		t.Fatalf("empty queue %v", err)
	}
}

func TestRollbackWhenEventInsertFails(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	fail := func() {
		t.Helper()
		if _, e := s.db.Exec(`CREATE TRIGGER fail_event BEFORE INSERT ON job_events BEGIN SELECT RAISE(ABORT,'injected'); END;`); e != nil {
			t.Fatal(e)
		}
	}
	restore := func() {
		t.Helper()
		if _, e := s.db.Exec("DROP TRIGGER fail_event"); e != nil {
			t.Fatal(e)
		}
	}
	fail()
	if _, e := s.Submit(ctx, "system-info", []byte(`{}`), 10); e == nil {
		t.Fatal("expected fault")
	}
	list, e := s.List(ctx, 0, 100)
	if e != nil || len(list) != 0 {
		t.Fatalf("partial submission %v %v", list, e)
	}
	restore()
	j := submitTest(t, s)
	fail()
	if _, e = s.Claim(ctx); e == nil {
		t.Fatal("expected claim fault")
	}
	got, e := s.Get(ctx, j.ID)
	if e != nil || got.State != job.Queued {
		t.Fatalf("claim not rolled back %+v %v", got, e)
	}
	restore()
	if _, e = s.Claim(ctx); e != nil {
		t.Fatal(e)
	}
	fail()
	if e = s.Finish(ctx, j.ID, job.Failed, nil, "test"); e == nil {
		t.Fatal("expected finish fault")
	}
	got, e = s.Get(ctx, j.ID)
	if e != nil || got.State != job.Running || events(t, s, j.ID) != 2 {
		t.Fatalf("finish not rolled back %+v %v", got, e)
	}
	if e = s.Reconcile(ctx); e == nil {
		t.Fatal("expected reconcile fault")
	}
	got, e = s.Get(ctx, j.ID)
	if e != nil || got.State != job.Running {
		t.Fatalf("reconcile not rolled back %+v %v", got, e)
	}
}

func TestConcurrentSubmitClaimAndQueueLimit(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	var wg sync.WaitGroup
	for range QueueLimit {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.Submit(ctx, "system-info", []byte(`{}`), 10); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if _, err := s.Submit(ctx, "system-info", []byte(`{}`), 10); !errors.Is(err, ErrQueueFull) {
		t.Fatalf("queue limit %v", err)
	}
	ids := make(chan string, QueueLimit)
	for range QueueLimit {
		wg.Add(1)
		go func() {
			defer wg.Done()
			j, err := s.Claim(ctx)
			if err != nil {
				t.Error(err)
				return
			}
			ids <- j.ID
		}()
	}
	wg.Wait()
	close(ids)
	seen := map[string]bool{}
	for id := range ids {
		if seen[id] {
			t.Fatal("duplicate claim")
		}
		seen[id] = true
	}
	if len(seen) != QueueLimit {
		t.Fatal("lost claim")
	}
	page, err := s.List(ctx, 0, 20)
	if err != nil || len(page) != 20 {
		t.Fatalf("page %v %v", page, err)
	}
	next, err := s.List(ctx, page[19].Sequence, 20)
	if err != nil || len(next) != 20 || next[0].Sequence <= page[19].Sequence {
		t.Fatalf("next page %v %v", next, err)
	}
}

func TestFIFOReconcileAndSchemaVersion(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	first := submitTest(t, s)
	second := submitTest(t, s)
	got, e := s.Claim(ctx)
	if e != nil || got.ID != first.ID {
		t.Fatalf("FIFO %+v %v", got, e)
	}
	if e = s.Reconcile(ctx); e != nil {
		t.Fatal(e)
	}
	got, e = s.Get(ctx, first.ID)
	if e != nil || got.State != job.Failed || got.ExitCode != nil || got.Error == "" || events(t, s, first.ID) != 3 {
		t.Fatalf("reconcile %+v %v", got, e)
	}
	if e = s.Reconcile(ctx); e != nil || events(t, s, first.ID) != 3 {
		t.Fatalf("non-idempotent reconcile %v", e)
	}
	got, e = s.Claim(ctx)
	if e != nil || got.ID != second.ID {
		t.Fatalf("queued job lost %+v %v", got, e)
	}
	if _, e = s.db.Exec("PRAGMA user_version=99"); e != nil {
		t.Fatal(e)
	}
	if e = s.migrate(); e == nil {
		t.Fatal("accepted future schema")
	}
}
