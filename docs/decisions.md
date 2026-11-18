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

Authentication is deferred, so remote access is not supported. Reject network
binds and browser-originated requests. A future UI and remote-access milestone
must introduce an explicit authentication/CSRF/TLS design.
