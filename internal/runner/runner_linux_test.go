package runner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"remotedeck/internal/job"
	"remotedeck/internal/task"
)

func TestHelperProcess(t *testing.T) {
	args := os.Args
	if len(args) < 3 || args[len(args)-2] != "--" {
		return
	}
	switch args[len(args)-1] {
	case "output":
		os.Stdout.Write([]byte{'o', 'u', 't', 0xff})
		fmt.Fprint(os.Stderr, "err")
		os.Exit(0)
	case "fail":
		fmt.Fprint(os.Stderr, "failure detail")
		os.Exit(7)
	case "flood":
		_, _ = io.CopyN(os.Stdout, strings.NewReader(strings.Repeat("x", MaxLogBytes+1)), MaxLogBytes+1)
		os.Exit(0)
	case "env":
		fmt.Fprint(os.Stdout, os.Getenv("REMOTEDECK_TEST_SECRET")+":"+os.Getenv("LANG"))
		os.Exit(0)
	case "sleep":
		time.Sleep(30 * time.Second)
		os.Exit(0)
	case "descendant", "orphan":
		exe, _ := os.Executable()
		child := exec.Command(exe, "-test.run=^TestHelperProcess$", "--", "sleep")
		child.Stdout = os.Stdout
		child.Stderr = os.Stderr
		if err := child.Start(); err != nil {
			os.Exit(8)
		}
		fmt.Fprintln(os.Stdout, child.Process.Pid)
		if args[len(args)-1] == "orphan" {
			os.Exit(0)
		}
		time.Sleep(30 * time.Second)
		os.Exit(0)
	}
	os.Exit(9)
}

func helper(t *testing.T, mode string) task.Spec {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return task.Spec{Executable: exe, Args: []string{"-test.run=^TestHelperProcess$", "--", mode}, Timeout: 5 * time.Second}
}
func logBytes(t *testing.T, dir, id string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, id+".log"))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestExecutionResultsAndLogs(t *testing.T) {
	for _, tc := range []struct {
		mode  string
		state job.State
		code  int
	}{{"output", job.Succeeded, 0}, {"fail", job.Failed, 7}, {"flood", job.Failed, 0}, {"env", job.Succeeded, 0}} {
		t.Run(tc.mode, func(t *testing.T) {
			t.Setenv("REMOTEDECK_TEST_SECRET", "must-not-leak")
			dir := t.TempDir()
			id := job.NewID()
			r := Runner{Directory: dir}.Run(context.Background(), id, helper(t, tc.mode))
			if r.State != tc.state || r.ExitCode == nil || *r.ExitCode != tc.code {
				t.Fatalf("%+v", r)
			}
			b := logBytes(t, dir, id)
			switch tc.mode {
			case "output":
				if !bytes.Contains(b, []byte{'o', 'u', 't', 0xff}) || !bytes.Contains(b, []byte("err")) {
					t.Fatalf("lost raw output %q", b)
				}
			case "flood":
				if len(b) != MaxLogBytes || !strings.Contains(r.Error, "truncated") {
					t.Fatalf("cap length=%d result=%+v", len(b), r)
				}
			case "env":
				if string(b) != ":C" {
					t.Fatalf("environment leak %q", b)
				}
			}
			info, err := os.Stat(filepath.Join(dir, id+".log"))
			if err != nil || info.Mode().Perm() != 0600 {
				t.Fatalf("log permissions %v %v", info, err)
			}
		})
	}
}

func TestDeadlineCancellationAndDescendantCleanup(t *testing.T) {
	for _, mode := range []string{"deadline", "cancel", "orphan"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			id := job.NewID()
			spec := helper(t, "descendant")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			want := job.TimedOut
			switch mode {
			case "deadline":
				spec.Timeout = 500 * time.Millisecond
			case "cancel":
				want = job.Cancelled
			case "orphan":
				spec = helper(t, "orphan")
				want = job.Succeeded
			}
			done := make(chan Result, 1)
			go func() { done <- Runner{Directory: dir}.Run(ctx, id, spec) }()
			// Wait for evidence that the descendant exists before cancelling.
			var pid int
			deadline := time.Now().Add(3 * time.Second)
			for time.Now().Before(deadline) {
				b, _ := os.ReadFile(filepath.Join(dir, id+".log"))
				pid, _ = strconv.Atoi(strings.TrimSpace(string(b)))
				if pid > 0 {
					break
				}
				time.Sleep(5 * time.Millisecond)
			}
			if pid <= 0 {
				cancel()
				<-done
				t.Fatal("helper did not publish descendant PID")
			}
			if mode == "cancel" {
				cancel()
			}
			select {
			case r := <-done:
				if r.State != want {
					t.Fatalf("result %+v want %s", r, want)
				}
			case <-time.After(7 * time.Second):
				t.Fatal("runner did not stop")
			}
			deadline = time.Now().Add(time.Second)
			for time.Now().Before(deadline) {
				b, e := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
				if errors.Is(e, os.ErrNotExist) {
					return
				}
				if e == nil {
					parts := strings.SplitN(string(b), ") ", 2)
					if len(parts) == 2 && strings.HasPrefix(parts[1], "Z ") {
						return
					}
				}
				time.Sleep(5 * time.Millisecond)
			}
			t.Fatalf("descendant %d remains live", pid)
		})
	}
}

func TestUnsafePathsAndStartFailure(t *testing.T) {
	dir := t.TempDir()
	r := Runner{Directory: dir}
	spec := helper(t, "output")
	if got := r.Run(context.Background(), "../escape", spec); got.State != job.Failed {
		t.Fatalf("unsafe ID %+v", got)
	}
	id := job.NewID()
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(target, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir, id+".log")); err != nil {
		t.Fatal(err)
	}
	if got := r.Run(context.Background(), id, spec); got.State != job.Failed {
		t.Fatalf("followed symlink %+v", got)
	}
	b, _ := os.ReadFile(target)
	if string(b) != "keep" {
		t.Fatal("overwrote existing file")
	}
	spec.Executable = "/nonexistent/remotedeck-test"
	if got := r.Run(context.Background(), job.NewID(), spec); got.State != job.Failed || got.ExitCode != nil {
		t.Fatalf("start failure %+v", got)
	}
}

type brokenWriter struct{}

func TestPreCancelledCreatesEmptyLogWithoutStarting(t *testing.T) {
	dir := t.TempDir()
	id := job.NewID()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	// A non-existent executable would fail if the process were started.
	spec := task.Spec{Executable: "/nonexistent/remotedeck-test", Timeout: time.Second}
	got := (Runner{Directory: dir}).Run(ctx, id, spec)
	if got.State != job.Cancelled || got.ExitCode != nil {
		t.Fatalf("result %+v", got)
	}
	if b := logBytes(t, dir, id); len(b) != 0 {
		t.Fatalf("unexpected output %q", b)
	}
}

func (brokenWriter) Write([]byte) (int, error) { return 0, errors.New("injected disk error") }
func TestLogFailureStillDrains(t *testing.T) {
	w := limitedWriter{dst: brokenWriter{}, remaining: 10}
	for range 2 {
		if n, e := w.Write([]byte("hello")); n != 5 || e != nil {
			t.Fatalf("stopped draining %d %v", n, e)
		}
	}
	if w.err == nil {
		t.Fatal("lost persistence error")
	}
}
