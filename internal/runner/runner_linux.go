// Package runner executes approved tasks and persists bounded combined output.
// It requires a private service-owned directory; process groups are not a sandbox.
package runner

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
	"unsafe"

	"remotedeck/internal/job"
	"remotedeck/internal/task"
)

const MaxLogBytes = 4 << 20

type Result struct {
	State    job.State
	ExitCode *int
	Error    string
}
type Runner struct{ Directory string }

// Run only accepts server-resolved specs. Directory is also the working directory.
func (r Runner) Run(ctx context.Context, id string, spec task.Spec) Result {
	if !job.ValidID(id) || !filepath.IsAbs(spec.Executable) || spec.Timeout <= 0 {
		return failure("invalid execution specification")
	}
	f, err := os.OpenFile(filepath.Join(r.Directory, id+".log"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return failure("cannot create task log")
	}
	output := &limitedWriter{dst: f, remaining: MaxLogBytes}
	result := r.execute(ctx, spec, output)
	syncErr := f.Sync()
	closeErr := f.Close()
	// A durable new file also requires persisting its directory entry.
	dirErr := syncDirectory(r.Directory)
	if output.err != nil || syncErr != nil || closeErr != nil || dirErr != nil {
		result.State = job.Failed
		result.Error = "task log persistence failed"
	} else if output.truncated {
		result.State = job.Failed
		result.Error = "task output exceeded 4 MiB; log truncated"
	}
	return result
}

func syncDirectory(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	return errors.Join(dir.Sync(), dir.Close())
}

func (r Runner) execute(parent context.Context, spec task.Spec, output io.Writer) Result {
	if parent.Err() != nil {
		return Result{State: job.Cancelled, Error: "service shutting down"}
	}
	ctx, cancel := context.WithTimeout(parent, spec.Timeout)
	defer cancel()
	cmd := exec.Command(spec.Executable, spec.Args...)
	cmd.Dir = r.Directory
	cmd.Env = []string{"PATH=/usr/bin:/bin", "LANG=C"}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Stdout = output
	cmd.Stderr = output         // same comparable writer: os/exec uses one copy goroutine
	cmd.WaitDelay = time.Second // bound escaped descendants holding an output pipe
	if err := cmd.Start(); err != nil {
		return failure("task process could not start")
	}
	// Observe exit without reaping: the leader reserves its PID until group
	// cleanup finishes, so a reused PID cannot cause us to signal another job.
	exited := make(chan error, 1)
	go func() { exited <- waitWithoutReaping(cmd.Process.Pid) }()
	var observeErr error
	var reason error
	select {
	case observeErr = <-exited:
	case <-ctx.Done():
		reason = ctx.Err()
		if err := killGroup(cmd.Process.Pid); err != nil {
			_ = cmd.Process.Kill()
			observeErr = err
		}
		observeErr = errors.Join(observeErr, <-exited)
	}
	cleanupErr := killGroup(cmd.Process.Pid)
	waitErr := cmd.Wait()
	result := Result{State: job.Succeeded}
	if cmd.ProcessState != nil {
		code := cmd.ProcessState.ExitCode()
		result.ExitCode = &code
	}
	switch {
	case observeErr != nil || cleanupErr != nil:
		result.State = job.Failed
		result.Error = "process group cleanup failed"
	case errors.Is(reason, context.DeadlineExceeded):
		result.State = job.TimedOut
		result.Error = "task deadline exceeded"
	case reason != nil:
		result.State = job.Cancelled
		result.Error = "service shutting down"
	case waitErr != nil:
		result.State = job.Failed
		result.Error = "task process failed"
	}
	return result
}

func killGroup(pid int) error {
	err := syscall.Kill(-pid, syscall.SIGKILL)
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}

// Linux waitid(WNOWAIT) leaves the exited leader waitable for exec.Cmd.Wait.
func waitWithoutReaping(pid int) error {
	var info [128]byte // Linux siginfo_t; contents are not needed.
	for {
		_, _, errno := syscall.Syscall6(syscall.SYS_WAITID, 1, uintptr(pid), uintptr(unsafe.Pointer(&info[0])), syscall.WEXITED|syscall.WNOWAIT, 0, 0)
		if errno == syscall.EINTR {
			continue
		}
		if errno != 0 {
			return errno
		}
		return nil
	}
}

type limitedWriter struct {
	dst       io.Writer
	remaining int
	truncated bool
	err       error
}

func (w *limitedWriter) Write(p []byte) (int, error) {
	n := len(p)
	if len(p) > w.remaining {
		p = p[:w.remaining]
		w.truncated = true
	}
	if w.err == nil && len(p) > 0 {
		written, err := w.dst.Write(p)
		w.remaining -= written
		w.err = err
		if written != len(p) && err == nil {
			w.err = io.ErrShortWrite
		}
	}
	// Continue draining after the cap or a storage failure; never let output
	// pressure leave the child blocked or grow memory without bound.
	return n, nil
}
func failure(message string) Result { return Result{State: job.Failed, Error: message} }
