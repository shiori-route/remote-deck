# Architecture baseline

Status: proposed first milestone; implementation status lives in `progress.md`.

## Components and directories

One Linux Go process owns an HTTP API and one sequential worker. SQLite is the
durable queue, not a second in-memory queue. The worker polls for queued jobs;
submission does not depend on a notification being delivered.

```text
cmd/remotedeck/       configuration, startup, signals, HTTP lifecycle
internal/job/        job model and state invariants
internal/store/      SQLite schema and transactional operations
internal/task/       predefined task definitions and validation
internal/runner/     process groups, timeouts, bounded log persistence
internal/worker/     durable claim → execute → complete loop
internal/api/        versioned HTTP handlers
docs/                contracts, decisions, risks, progress and reviews
```

Use `net/http`, `database/sql`, `os/exec` and `log/slog`. A SQLite driver is the
only runtime library dependency (`mattn/go-sqlite3`, approved). Avoid a
generic repository layer. Worker and API use concrete, small storage operations.

## State and delivery semantics

Allowed first-milestone transitions: `queued → running`, then
`running → succeeded | failed | cancelled | timed_out`.
`cancelled` represents shutdown cancellation initially; no public cancel endpoint.
Terminal states cannot be updated or automatically requeued. Each transition and
the matching job update commit in one transaction. `attempt` starts at 0 and
becomes 1 on claim. Retries will require a separate design.

Submission is durable only after the transaction commits. An HTTP disconnect
after commit can leave the caller unsure whether submission succeeded. There is
no idempotency key yet; resubmitting creates a new job.

The claim is persisted before starting a process. A crash between claim and spawn
can therefore produce a failed job that never ran. A crash after spawn can leave
unknown external effects. Startup marks leftover `running` jobs `failed` with an
interruption explanation, never relaunching them. Queued jobs remain eligible.
This is at most one launch attempt per job in normal single-service operation,
not exactly-once execution or guaranteed completion.

Hold a Linux advisory lock on the data directory for the entire service lifetime
to reject a second instance. Do not use saved PIDs as evidence of process ownership.
A hard service crash may leave descendants running; process groups only address
cooperative shutdown and timeouts. Before supporting general commands, add and
test stronger systemd/cgroup supervision. For the first allowlisted `uname` task,
there are no intended long-running descendants.

## Initial schema contract

Store timestamps as UTC RFC3339Nano strings; ordering uses a monotonic SQLite row
sequence instead of timestamp text. IDs are 128-bit random lowercase hex strings.

`jobs`:

| Column | Type / invariant |
| --- | --- |
| sequence | INTEGER PRIMARY KEY AUTOINCREMENT; FIFO and pagination cursor |
| id | TEXT UNIQUE NOT NULL |
| task_type | TEXT NOT NULL; validated provider name |
| parameters | TEXT NOT NULL; canonical JSON object |
| state | TEXT NOT NULL; checked lifecycle enum |
| created_at | TEXT NOT NULL |
| started_at, completed_at | nullable TEXT; constrained by state |
| attempt | INTEGER NOT NULL; 0 queued, 1 after claim |
| exit_code | nullable INTEGER; unavailable for unstarted or interrupted jobs |
| error | TEXT NOT NULL; empty until an error occurs |
| timeout_seconds | INTEGER NOT NULL CHECK > 0; provider-owned |
| log_name | TEXT NOT NULL; derived from job ID, never client-supplied |

`job_events`: integer primary key, job ID foreign key, nullable previous state,
new state, timestamp, explanation. Insert an initial queued event on submission.
Indexes: `jobs(state, sequence)` for FIFO claims and `job_events(job_id, id)`.
Use versioned transactional schema migrations (`PRAGMA user_version`), foreign
keys, WAL, FULL synchronous mode and a bounded busy timeout. Initially one SQL
connection serializes short transactions. Atomically claim the oldest queued job
and insert its event. Status updates use an expected-state predicate.

## Logs and bounds

Use one combined stdout/stderr file per job under a private data directory.
Preserve raw bytes; ordering between the two streams is unspecified. Logs are
persisted independently of HTTP clients. Enforce a 4 MiB per-job cap, drain and
discard excess bytes, and fail the job explicitly if its output was truncated.
Sync and close the file before publishing the terminal state; persistence errors
must prevent a successful result. A crash can lose the last buffered/filesystem
output; there is no transaction spanning SQLite and the log file.

Read logs in bounded byte chunks (up to 64 KiB) with an offset. Byte offsets may
split UTF-8; clients must decode incrementally. Missing logs before worker start
are empty; missing logs after completion indicate a storage error.

Bound request bodies, history pages, HTTP timeouts, active execution and queued
jobs (100 queued initially). Disk usage still grows with retained history;
retention and quotas are subsequent work. Stop execution on terminal persistence
failure instead of proceeding with unrecorded outcomes.
