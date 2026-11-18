# Security model

Status: design requirements; see progress for what is implemented.

The first milestone trusts the local OS user and other local processes. Bind
only to `127.0.0.1`; reject non-loopback bind configuration. Do not proxy this API
to a network, including a private network, before authentication is implemented.
Browser Origin/Fetch Metadata rejection and strict JSON POSTs are additional
barriers, not a substitute for identity and authorization.

The only initial task is `/usr/bin/uname -a`. Use absolute executable paths,
explicit arguments and `os/exec`, never a shell. Use a minimal environment
(`PATH=/usr/bin:/bin`, `LANG=C`) instead of inheriting credentials. The service
controls the working directory. A later provider must validate all parameters
and restrict repositories and paths before it is registered.

Use a private owned data directory (0700) and log/lock/database files accessible
only to the service account. Reject unsafe existing directory permissions and
symlink data roots. IDs must be exactly 32 lowercase hexadecimal characters;
derive log names internally. Filesystem protection assumes other processes with
the same OS identity are trusted. Run under a dedicated non-root account.

Limits: one executing job, 100 queued jobs, 16 KiB submission body, 4 MiB logs per
job, 64 KiB log reads, bounded pagination and HTTP timeouts. Total history size is
not capped; operators must monitor disk space until retention is implemented.

Process groups support timeouts and graceful service shutdown but are not a
sandbox: descendants can escape groups. Hard crashes can leave children running.
General-purpose commands and Codex require stronger isolation and supervision.
Never treat startup database reconciliation as proof of process termination.

No credentials, TLS termination, authorization, multi-user isolation or secrets
management exist yet. Avoid sensitive output; future log access needs the same
authorization as task results. Errors returned to clients must omit host paths,
SQL details and environment values.
