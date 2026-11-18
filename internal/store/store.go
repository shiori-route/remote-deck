// Package store owns SQLite transactions. Open requires a private, trusted parent
// directory and external single-service ownership for restart reconciliation.
package store

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"remotedeck/internal/job"
)

var (
	ErrNotFound  = errors.New("job not found")
	ErrConflict  = errors.New("invalid job transition")
	ErrQueueFull = errors.New("queue full")
)

const QueueLimit = 100

//go:embed schema.sql
var schema string

type Store struct{ db *sql.DB }

func Open(path string) (*Store, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	u := url.URL{Scheme: "file", Path: abs}
	db, err := sql.Open("sqlite3", u.String()+"?_foreign_keys=on&_journal_mode=WAL&_synchronous=FULL&_busy_timeout=5000&_txlock=immediate")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	if err = s.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) migrate() error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var version int
	if err = tx.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	switch version {
	case 0:
		if _, err = tx.Exec(schema); err != nil {
			return fmt.Errorf("initialize schema: %w", err)
		}
	case 1:
	default:
		return fmt.Errorf("unsupported schema version %d", version)
	}
	return tx.Commit()
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) Submit(ctx context.Context, task string, params json.RawMessage, timeout int) (job.Job, error) {
	var obj map[string]json.RawMessage
	if task == "" || timeout < 1 || json.Unmarshal(params, &obj) != nil || obj == nil {
		return job.Job{}, errors.New("invalid job definition")
	}
	canonical, err := json.Marshal(obj)
	if err != nil {
		return job.Job{}, err
	}
	j := job.Job{ID: job.NewID(), TaskType: task, Parameters: canonical, State: job.Queued, CreatedAt: time.Now().UTC(), TimeoutSeconds: timeout}
	j.LogName = j.ID + ".log"
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return job.Job{}, err
	}
	defer tx.Rollback()
	var count int
	if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM jobs WHERE state='queued'").Scan(&count); err != nil {
		return job.Job{}, err
	}
	if count >= QueueLimit {
		return job.Job{}, ErrQueueFull
	}
	r, err := tx.ExecContext(ctx, `INSERT INTO jobs(id,task_type,parameters,state,created_at,attempt,timeout_seconds,log_name)
	 VALUES(?,?,?,'queued',?,0,?,?)`, j.ID, j.TaskType, string(j.Parameters), stamp(j.CreatedAt), timeout, j.LogName)
	if err != nil {
		return job.Job{}, err
	}
	j.Sequence, err = r.LastInsertId()
	if err != nil {
		return job.Job{}, err
	}
	if err = event(ctx, tx, j.ID, nil, job.Queued, j.CreatedAt, ""); err != nil {
		return job.Job{}, err
	}
	if err = tx.Commit(); err != nil {
		return job.Job{}, err
	}
	return j, nil
}

const columns = "sequence,id,task_type,parameters,state,created_at,started_at,completed_at,attempt,exit_code,error,timeout_seconds,log_name"

type scanner interface{ Scan(...any) error }

func scan(row scanner) (job.Job, error) {
	var j job.Job
	var created, parameters string
	var start, done sql.NullString
	err := row.Scan(&j.Sequence, &j.ID, &j.TaskType, &parameters, &j.State, &created, &start, &done, &j.Attempt, &j.ExitCode, &j.Error, &j.TimeoutSeconds, &j.LogName)
	if errors.Is(err, sql.ErrNoRows) {
		return j, ErrNotFound
	}
	if err != nil {
		return j, err
	}
	j.Parameters = json.RawMessage(parameters)
	j.CreatedAt, err = time.Parse(time.RFC3339Nano, created)
	if err != nil {
		return j, err
	}
	for _, p := range []struct {
		src sql.NullString
		dst **time.Time
	}{{start, &j.StartedAt}, {done, &j.CompletedAt}} {
		if p.src.Valid {
			t, e := time.Parse(time.RFC3339Nano, p.src.String)
			if e != nil {
				return j, e
			}
			*p.dst = &t
		}
	}
	return j, nil
}
func (s *Store) Get(ctx context.Context, id string) (job.Job, error) {
	if !job.ValidID(id) {
		return job.Job{}, ErrNotFound
	}
	return scan(s.db.QueryRowContext(ctx, "SELECT "+columns+" FROM jobs WHERE id=?", id))
}
func (s *Store) List(ctx context.Context, after int64, limit int) ([]job.Job, error) {
	if after < 0 || limit < 1 || limit > 100 {
		return nil, errors.New("invalid page")
	}
	rows, err := s.db.QueryContext(ctx, "SELECT "+columns+" FROM jobs WHERE sequence>? ORDER BY sequence LIMIT ?", after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	jobs := make([]job.Job, 0)
	for rows.Next() {
		j, e := scan(rows)
		if e != nil {
			return nil, e
		}
		jobs = append(jobs, j)
	}
	return jobs, rows.Err()
}

// Claim commits running before the caller can launch a process.
func (s *Store) Claim(ctx context.Context) (job.Job, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return job.Job{}, err
	}
	defer tx.Rollback()
	j, err := scan(tx.QueryRowContext(ctx, "SELECT "+columns+" FROM jobs WHERE state='queued' ORDER BY sequence LIMIT 1"))
	if err != nil {
		return job.Job{}, err
	}
	now := time.Now().UTC()
	r, err := tx.ExecContext(ctx, "UPDATE jobs SET state='running',started_at=?,attempt=1 WHERE id=? AND state='queued'", stamp(now), j.ID)
	if err = changed(r, err); err != nil {
		return job.Job{}, err
	}
	if err = event(ctx, tx, j.ID, job.Queued, job.Running, now, ""); err != nil {
		return job.Job{}, err
	}
	if err = tx.Commit(); err != nil {
		return job.Job{}, err
	}
	j.State = job.Running
	j.StartedAt = &now
	j.Attempt = 1
	return j, nil
}

func (s *Store) Finish(ctx context.Context, id string, state job.State, exit *int, message string) error {
	if !job.CanTransition(job.Running, state) || (state == job.Succeeded && (exit == nil || *exit != 0 || message != "")) {
		return ErrConflict
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now().UTC()
	r, err := tx.ExecContext(ctx, "UPDATE jobs SET state=?,completed_at=?,exit_code=?,error=? WHERE id=? AND state='running'", state, stamp(now), exit, message, id)
	if err = changed(r, err); err != nil {
		return err
	}
	if err = event(ctx, tx, id, job.Running, state, now, message); err != nil {
		return err
	}
	return tx.Commit()
}

// Reconcile is startup-only, with exclusive service ownership. It records an
// unknown outcome; it does not establish that old processes have terminated.
func (s *Store) Reconcile(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := stamp(time.Now().UTC())
	message := "service interrupted; execution outcome unknown; not retried"
	_, err = tx.ExecContext(ctx, `INSERT INTO job_events(job_id,from_state,to_state,occurred_at,explanation)
	 SELECT id,'running','failed',?,? FROM jobs WHERE state='running'`, now, message)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "UPDATE jobs SET state='failed',completed_at=?,error=? WHERE state='running'", now, message)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func stamp(t time.Time) string { return t.Format(time.RFC3339Nano) }
func event(ctx context.Context, tx *sql.Tx, id string, from any, to job.State, t time.Time, message string) error {
	_, err := tx.ExecContext(ctx, "INSERT INTO job_events(job_id,from_state,to_state,occurred_at,explanation) VALUES(?,?,?,?,?)", id, from, to, stamp(t), message)
	return err
}
func changed(r sql.Result, err error) error {
	if err != nil {
		return err
	}
	n, err := r.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrConflict
	}
	return nil
}
