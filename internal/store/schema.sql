CREATE TABLE jobs (
 sequence INTEGER PRIMARY KEY AUTOINCREMENT,
 id TEXT UNIQUE NOT NULL,
 task_type TEXT NOT NULL,
 parameters TEXT NOT NULL CHECK(json_valid(parameters) AND json_type(parameters) = 'object'),
 state TEXT NOT NULL CHECK(state IN ('queued','running','succeeded','failed','cancelled','timed_out')),
 created_at TEXT NOT NULL,
 started_at TEXT,
 completed_at TEXT,
 attempt INTEGER NOT NULL CHECK(attempt IN (0,1)),
 exit_code INTEGER,
 error TEXT NOT NULL DEFAULT '',
 timeout_seconds INTEGER NOT NULL CHECK(timeout_seconds > 0),
 log_name TEXT NOT NULL,
 CHECK (
   (state = 'queued' AND attempt = 0 AND started_at IS NULL AND completed_at IS NULL AND exit_code IS NULL) OR
   (state = 'running' AND attempt = 1 AND started_at IS NOT NULL AND completed_at IS NULL AND exit_code IS NULL) OR
   (state IN ('succeeded','failed','cancelled','timed_out') AND attempt = 1 AND started_at IS NOT NULL AND completed_at IS NOT NULL)
 ),
 CHECK(state != 'succeeded' OR (exit_code IS NOT NULL AND exit_code = 0 AND error = ''))
);
CREATE INDEX jobs_queue ON jobs(state, sequence);
CREATE TABLE job_events (
 id INTEGER PRIMARY KEY,
 job_id TEXT NOT NULL REFERENCES jobs(id),
 from_state TEXT,
 to_state TEXT NOT NULL,
 occurred_at TEXT NOT NULL,
 explanation TEXT NOT NULL
);
CREATE INDEX job_events_job ON job_events(job_id, id);
PRAGMA user_version = 1;
