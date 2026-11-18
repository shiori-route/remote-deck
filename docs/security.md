# Security model

Status: local service with operator bearer authentication and browser sessions.
See progress for implementation and test evidence.

The service trusts its OS identity and anyone possessing its operator token.
Other local callers must authenticate. Bind only to `127.0.0.1`; reject
non-loopback bind configuration. Do not proxy this API to a network, including a
private network, until TLS and remote-access behavior are designed and tested.
Exact listener Host checks, cross-origin/Fetch Metadata rejection and strict JSON POSTs are additional
barriers in addition to bearer authentication.

## Operator token

Startup, while holding the instance lock, creates `auth.token` if absent using
32 cryptographically random bytes encoded as lowercase hex. The file is created
exclusively with mode 0600; file and directory are synced before startup proceeds.
An existing token must be a regular file owned by the service user with mode 0600,
one hard link, and exactly 64 hex characters (optional final LF). Symlinks, FIFOs,
directories, oversized/malformed contents and unsafe permissions fail startup.
Failed or partial creation is not silently overwritten on the next start.

Bearer requests are checked before application body/job/log access. The verifier
retains a SHA-256 digest and compares fixed-size digests
with `crypto/subtle.ConstantTimeCompare`. Credentials are never intentionally
logged or returned by the API. URL/body/operator-token cookies are not credential
sources; browser cookies instead hold a separate, revocable session identifier.
The bearer header format follows [RFC 6750 section 2.1](https://www.rfc-editor.org/rfc/rfc6750#section-2.1);
this local credential is not an OAuth server or a claim of full OAuth compliance.

The token has full API authority, no automatic expiry and no individual user
attribution or roles. It is loaded once at startup; changing the file does not
revoke a running service's in-memory credential. See deployment for rotation.
The raw credential is stored locally and travels over loopback HTTP; TLS is
required before extending access to a network. No attempt is made to isolate
credentials from processes running as the same OS user.

## Browser sessions and CSRF

Login requires the bearer header and exact configured Origin. It issues separate
random 256-bit session and CSRF tokens; only a hash of the session ID is used as
the map key. The bounded map permits 64 sessions, each with an absolute 8-hour
expiry. Logout deletes the entry; reauthentication rotates it; restart revokes all.
No browser sessions are persisted in SQLite.

Cookies are HttpOnly, SameSite=Strict, host-only and scoped to /api/v1. Secure is
not set for loopback HTTP and must be revisited for HTTPS deployment. Cookie writes
require exact Origin plus a matching CSRF header. Session-info GET restores the
CSRF token on reload. Operator tokens are cleared after login attempts and never
saved in localStorage/sessionStorage. Invalid explicit bearer headers cannot fall
back to cookies. Job data and log output are rendered as text, never HTML.

Host validation includes the listener port; cross-origin and same-site requests
are rejected. The console has restrictive CSP, frame-ancestors 'none', no-referrer,
nosniff and no-store. No third-party assets/scripts load. The CSRF approach follows
[OWASP's synchronizer-token guidance](https://cheatsheetseries.owasp.org/cheatsheets/Cross-Site_Request_Forgery_Prevention_Cheat_Sheet.html).

Cookies are not isolated by TCP port. Another untrusted service on 127.0.0.1 can
receive host cookies if a browser visits a matching path there; Origin/CSRF checks
do not fix that browser limitation. This local console assumes trusted loopback
services. Remote/multi-user deployment needs a dedicated HTTPS origin and must
revisit cookies, login rate limits and identity. Logout does not roll back requests
that were already authorized and in flight.

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

TLS termination, multi-user authorization and secrets management are not implemented.
Log reads require the same credentials as task results.
Errors returned to clients omit host paths, SQL details and environment values.

## Frontend build dependency audit

Svelte/Vite/TypeScript are pinned in web/package-lock.json; Node runs only during
build/check/browser tests. The Go service embeds static output and does not run
Vite or accept user-supplied glob patterns. On 2026-11-18, npm audit reported six
high entries inherited from picomatch <=4.0.7, with no patched release. The issue
is ReDoS on attacker-chosen extglobs; see
[GHSA-fc5q-39xc-vjpf](https://github.com/advisories/GHSA-fc5q-39xc-vjpf).
This remains an acknowledged build-tool risk: build only trusted source/config,
do not expose a Vite dev server, and update when a compatible fix is released.
Findings are not suppressed and the full audit is not reported as passing.
