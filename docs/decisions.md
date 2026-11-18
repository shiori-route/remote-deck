# Decisions

## D001 — Single service and durable polling queue

Use one Go process and SQLite. A single sequential worker polls the database.
This keeps submission durable without introducing a broker or separate agent.
Polling adds a small scheduling delay; throughput is not the first priority.

## D002 — Native SQLite through database/sql

SQLite is required by the project. Approved driver: `github.com/mattn/go-sqlite3`
(user approved 2026-11-18; pinned in go.mod). It requires CGO and a C compiler at build time; the bundled
SQLite implementation avoids requiring a separately installed SQLite runtime.
Alternative: `modernc.org/sqlite` removes CGO but adds more Go dependencies.
Source: [driver documentation](https://github.com/mattn/go-sqlite3).

## D003 — Fixed task first

Start with `system-info` (`/usr/bin/uname -a`). It proves submission, execution,
status and log retrieval without exposing arbitrary commands. Task profiles,
parameter schemas and repository allowlists expand only with concrete providers.

## D004 — File logs and explicit uncertain outcomes

Keep combined stdout/stderr in bounded per-job files. This decouples output from
browser speed and keeps SQLite transactions small. Cross-stream ordering is
unspecified; logs and database outcomes cannot be atomically committed together.
Interrupted running jobs fail and are not retried automatically. Strong process
ownership with systemd/cgroups is required before unattended general workloads.

## D005 — Loopback-only milestone

The first milestone deferred authentication. Mandatory bearer authentication was
added in D006. D007 permits the same-origin console with sessions/CSRF; loopback
binding and rejection of other origins remain until remote TLS is designed.

## D006 — One local operator bearer token

Use a server-generated 256-bit token in a private `auth.token` file and require
it on every API route. Reuse it across restarts, reject unsafe/malformed files,
and compare fixed-size digests in constant time. No external dependency, database
migration, JWT, password database or user-management service is needed for one
operator. This changes previously anonymous API clients: they must send the header.

The token grants full authority and is revoked by offline rotation plus restart.
It is not a browser login/session solution or a multi-user authorization scheme.
Keep loopback binding. D007 subsequently adds browser sessions; HTTPS remains future work.

## D007 — Local browser sessions and an embedded Svelte console

Add in-memory opaque sessions, HttpOnly/SameSite cookies, absolute 8-hour expiry
and per-session CSRF tokens. The login uses the existing operator credential;
CLI bearer calls remain supported. Session count is bounded, restart revokes all
sessions, and no database migration is required. Keep exact origin/host guards.

Use Svelte/TypeScript and Vite to build static assets, then embed them in Go.
Node is a build/test dependency only. Generated output is ignored, so a fresh
checkout must build web assets before Go tests/build; CI does that explicitly.
The UI polls bounded existing API endpoints and uses incremental byte decoding.
General commands, SSE, user accounts and remote TLS remain separate steps.

The current HTTP loopback cookie is not Secure and cookies are not port-isolated.
This assumes trusted local services; it must not be generalized to a remote or
multi-user deployment without a dedicated HTTPS-origin design. An unpatched
picomatch issue in trusted-input build tooling is recorded in security.md.
