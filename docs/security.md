# Security model

Status: local service with mandatory single-operator bearer authentication.
See progress for implementation and test evidence.

The service trusts its OS identity and anyone possessing its operator token.
Other local callers must authenticate. Bind only to `127.0.0.1`; reject
non-loopback bind configuration. Do not proxy this API to a network, including a
private network, until TLS and remote-access behavior are designed and tested.
Host validation (`127.0.0.1` only), browser Origin/Fetch Metadata rejection and strict JSON POSTs are additional
barriers in addition to bearer authentication.

## Operator token

Startup, while holding the instance lock, creates `auth.token` if absent using
32 cryptographically random bytes encoded as lowercase hex. The file is created
exclusively with mode 0600; file and directory are synced before startup proceeds.
An existing token must be a regular file owned by the service user with mode 0600,
one hard link, and exactly 64 hex characters (optional final LF). Symlinks, FIFOs,
directories, oversized/malformed contents and unsafe permissions fail startup.
Failed or partial creation is not silently overwritten on the next start.

Every API route checks one Authorization header before reading a body or accessing
jobs/logs. The verifier retains a SHA-256 digest and compares fixed-size digests
with `crypto/subtle.ConstantTimeCompare`. Credentials are never intentionally
logged or returned by the API. URL, body and cookie credentials are not supported.
The bearer header format follows [RFC 6750 section 2.1](https://www.rfc-editor.org/rfc/rfc6750#section-2.1);
this local credential is not an OAuth server or a claim of full OAuth compliance.

The token has full API authority, no automatic expiry and no individual user
attribution or roles. It is loaded once at startup; changing the file does not
revoke a running service's in-memory credential. See deployment for rotation.
The raw credential is stored locally and travels over loopback HTTP; TLS is
required before extending access to a network. No attempt is made to isolate
credentials from processes running as the same OS user.

The only initial task is `/usr/bin/uname -a`. Use absolute executable paths,
explicit arguments and `os/exec`, never a shell. Use a minimal environment
(`PATH=/usr/bin:/bin`, `LANG=C`) instead of inheriting credentials. The service
controls the working directory. A later provider must validate all parameters
and restrict repositories and paths before it is registered.
Tasks currently run as the service identity and use the data directory as cwd;
the fixed uname command does not read the token, but arbitrary future providers
must not assume that credentials are isolated merely because the environment is
minimal. Stronger process identity/filesystem separation needs its own design.

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

TLS termination, browser sessions, multi-user authorization and secrets management
are not implemented. Log reads require the same bearer credential as task results.
Errors returned to clients omit host paths, SQL details and environment values.
