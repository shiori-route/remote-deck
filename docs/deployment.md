# Deployment

No deployable service exists at the design baseline. Target: Debian Linux,
dedicated non-root account, local disk. Do not use network filesystems for the
SQLite/data directory or share it across hosts.

The initial executable will run in the foreground with an explicit data
directory and a loopback HTTP address. Startup must reject a second service
using the same data directory, prepare private storage, migrate the database,
reconcile interrupted jobs and only then accept traffic/start the worker.

Graceful shutdown: stop taking HTTP requests, cancel active execution and its
process group, persist its outcome, join the worker and close the database before
releasing the instance lock. Queue entries remain persisted for the next start.

Backups require stopping the service and copying the whole data directory,
including SQLite WAL sidecars and logs. Restoring a running-job row does not mean
its original process exists or is safe to rerun.

systemd units, HTTPS/Tailscale deployment and authenticated remote access will be
documented after the local vertical slice. systemd control-group cleanup will
need explicit crash tests; this document makes no crash containment guarantee.
