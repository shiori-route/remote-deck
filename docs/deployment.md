# Deployment

The foreground service targets Debian Linux, a dedicated non-root account and
local disk. Default HTTP is local-only. Explicit direct HTTPS supports a single
operator over a private network/VPN; see the configuration below.
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

The service rejects root execution, non-loopback HTTP addresses, symlink data paths,
directories not owned by the service user or not mode 0700, and unsafe existing
database/lock files. A missing directory is created with mode 0700. It holds a
Linux flock until workers and storage are closed. A second instance using that
directory fails startup. Do not delete or replace the lock file while running.

Startup loads or creates `auth.token` after taking the instance lock and before
opening SQLite. TLS configuration is validated before touching storage. The
service then migrates SQLite, opens the configured listener and
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

systemd units and managed VPN/certificate provisioning remain future work.
systemd control-group cleanup will
need explicit crash tests; this document makes no crash containment guarantee.

Local validation includes real HTTP traffic across a graceful restart and a
SIGKILL helper test for startup database reconciliation and lock release. That
helper intentionally launches no job child; it does not validate crash cleanup
of descendants. The only enabled command is the short-lived fixed uname task.

## Direct HTTPS access

Use a dedicated hostname and a certificate whose SAN covers that hostname (or an
IP SAN for an IP origin). The browser and CLI must trust its issuer. Provision a
complete PEM certificate chain and unencrypted PEM private key separately; the
service does not obtain or renew certificates. Keep the key outside the data/task
directory, owned by the service account, mode 0600, with one link and no symlink.
Parent directories must be controlled by trusted administrators. Shared service
identity remains a trust boundary even when files live in separate directories.

Example (replace the private address, hostname and paths with your deployment):

```sh
./bin/remotedeck \
  -addr 192.168.1.10:8443 \
  -origin https://deck.example.com:8443 \
  -tls-cert /srv/remotedeck-tls/fullchain.pem \
  -tls-key /srv/remotedeck-tls/key.pem \
  -data-dir /srv/remotedeck/data
```

All three TLS options are required together. `-origin` is the exact URL origin:
lowercase hostname, optional non-default port, no trailing slash, credentials,
query or fragment. Omit `:443` for HTTPS's default port. The certificate must be
currently valid and cover the origin host. Bad options/key/certificate fail
before storage migration, job reconciliation or listener startup. The configured
origin may differ from the bind IP/port for private routing or TCP passthrough,
but HTTP Host and actual TLS must match it exactly. Neither forwarded headers
nor a TLS-terminating proxy are trusted. The listener supports IPv4 only.

Resolve the hostname to the server's private/VPN address on client devices.
Restrict ingress with the host/network firewall to intended clients; this step
does not install a VPN, modify DNS/firewalls or expose a port. Use the printed
`origin` URL on desktop or phone, verify a trusted connection, then enter the
operator token obtained through a trusted channel. Do not bypass certificate
warnings or use `curl -k`; private CAs can be supplied to curl via `--cacert`.
The existing stdin-header helper works with the HTTPS API URL.

The service uses TLS 1.2 or newer and opens no plaintext redirect listener.
HTTPS cookies are Secure/HttpOnly/SameSite=Strict with `__Host-` prefix, Path=/,
and no Domain. Use a dedicated host with no untrusted services on other ports:
cookies still have no port isolation. HTTP and HTTPS use different cookie names.
Do not change this dedicated host back to HTTP while HTTPS sessions are active.

Certificate/key contents are loaded once at startup. Renew before expiry and
restart to load the replacement, which revokes browser sessions and reconciles
interrupted jobs. Chain trust is verified by clients, not by the server's startup
check. No automated ACME, hot reload, HSTS policy, multi-user identity or public
Internet hardening is claimed. Login throttling is global (10 burst, 1/second);
a hostile caller can temporarily consume it, so network access controls matter.
Existing sessions and bearer API calls do not use that login bucket.

Validation uses real TLS sockets with a test CA and strict client certificate
verification, plus browser HTTP/HTTPS desktop/mobile flows. Browser fixtures use
an isolated self-signed certificate and allow it only in test contexts; Go tests
separately prove unknown issuers and obsolete TLS are rejected. No real external
client, DNS, VPN or production certificate was exercised in this milestone.
