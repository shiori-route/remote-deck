// Package worker drives the durable queue with one synchronous execution slot.
package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"remotedeck/internal/job"
	"remotedeck/internal/runner"
	"remotedeck/internal/store"
	"remotedeck/internal/task"
)

type Executor interface {
	Run(context.Context, string, task.Spec) runner.Result
}

type Worker struct {
	Store    *store.Store
	Executor Executor
	Logger   *slog.Logger
}

// Run returns on cancellation or any storage failure. The caller must shut
// down the service on failure, and must not call Run concurrently.
func (w Worker) Run(ctx context.Context) error {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return nil
		}
		j, err := w.Store.Claim(ctx)
		if errors.Is(err, store.ErrNotFound) {
			select {
			case <-ctx.Done():
				return nil
			case <-ticker.C:
				continue
			}
		}
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("claim job: %w", err)
		}
		w.Logger.Info("job started", "job_id", j.ID, "task_type", j.TaskType)
		spec, resolveErr := task.Resolve(j.TaskType, j.Parameters)
		var result runner.Result
		if resolveErr != nil || int(spec.Timeout/time.Second) != j.TimeoutSeconds {
			result = runner.Result{State: job.Failed, Error: "persisted task definition is not supported"}
		} else {
			result = w.Executor.Run(ctx, j.ID, spec)
		}
		// Shutdown cancellation must not cancel the outcome transaction.
		finishCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err = w.Store.Finish(finishCtx, j.ID, result.State, result.ExitCode, result.Error)
		cancel()
		if err != nil {
			return fmt.Errorf("persist outcome for %s: %w", j.ID, err)
		}
		w.Logger.Info("job completed", "job_id", j.ID, "state", result.State, "exit_code", result.ExitCode)
	}
}
