# Deployment

The local foreground service targets Debian Linux, a dedicated non-root account
and local disk. Public or private-network remote deployment is not supported yet.
Do not use network filesystems for the
SQLite/data directory or share it across hosts.

Build with Go 1.25+, GCC and Node.js 24/npm. Build frontend assets first:

```sh
npm --prefix web ci
npm --prefix web run check
npm --prefix web run build
go build -o ./bin/remotedeck ./cmd/remotedeck
```

The generated internal/webui/dist directory is ignored by Git but required by
Go embed, including for tests. The binary embeds those assets and needs no Node
runtime or external files to serve the UI. Run:

```sh
./bin/remotedeck -addr 127.0.0.1:8080 -data-dir ./data
```

The service rejects root execution, non-loopback addresses, symlink data paths,
directories not owned by the service user or not mode 0700, and unsafe existing
database/lock files. A missing directory is created with mode 0700. It holds a
Linux flock until workers and storage are closed. A second instance using that
directory fails startup. Do not delete or replace the lock file while running.

Startup loads or creates `auth.token` after taking the instance lock and before
opening SQLite. It then migrates SQLite, opens the loopback listener and
reconciles interrupted jobs before serving HTTP or starting the worker. Log
files, SQLite and its WAL/SHM sidecars are private to the service user.

Open the printed loopback address in a browser and log in with the private token.
Browser sessions expire after 8 hours and disappear on restart. CLI requests use
`Authorization: Bearer <token>`. Use the stdin-header
curl example in README; do not put credentials in URLs, shell history, command-line
arguments or logs. The token is not printed at startup. Existing data directories
without a token get a new one automatically; database contents are unchanged.

The token persists across restarts. For rotation, stop the service, replace
`auth.token` with a fresh cryptographically generated 64-character lowercase hex
token (optional final LF), owned by the same user with mode 0600, then restart.
Alternatively, securely archive the old token outside the data directory while
stopped, leaving the path absent so startup generates a new one. Old operator
credentials cease working after restart, which also revokes browser sessions.
Browser logout revokes one session online; operator rotation remains offline,
with no automatic operator-token expiry.
If token creation is interrupted or the file is malformed, startup fails rather
than overwriting it; repair/rotate it while stopped. Restore mode 0600 if only
permissions were changed. Backups contain the raw credential and need protection.

SIGINT/SIGTERM shutdown: stop taking HTTP requests, cancel active execution and its
process group, persist its outcome, join the worker and close the database before
releasing the instance lock. Queue entries remain persisted for the next start.
HTTP reads/writes and header/idle waits have bounded timeouts. If a worker fails
to claim or persist an outcome, the entire service shuts down with a nonzero exit.

Backups require stopping the service and copying the whole data directory,
including SQLite WAL sidecars and logs. Restoring a running-job row does not mean
its original process exists or is safe to rerun.

systemd units, HTTPS/Tailscale deployment and authenticated remote access will be
documented after the local vertical slice. systemd control-group cleanup will
need explicit crash tests; this document makes no crash containment guarantee.

Local validation includes real HTTP traffic across a graceful restart and a
SIGKILL helper test for startup database reconciliation and lock release. That
helper intentionally launches no job child; it does not validate crash cleanup
of descendants. The only enabled command is the short-lived fixed uname task.
