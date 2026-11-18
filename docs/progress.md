# Development progress

## Repository baseline

- Commit: `75fb437` (Initial commit).
- Tracked files: `.gitignore`, `LICENSE`; local `AGENTS.md` supplies development rules.
- Debian 13 / x86-64; GCC available; no Go toolchain initially on PATH.
- No pre-existing application, tests or user edits.

## Step 1 — Design baseline (accepted)

- Scope: README, architecture/schema, API, security, deployment, decisions, roadmap.
- Acceptance: internally consistent first vertical slice; explicit risks and
  delivery semantics; planned functionality distinguished from implemented work.
- Validation: all 8 Markdown documents passed local-link and trailing-whitespace
  checks; Go 1.27.4 downloaded to `/tmp/remotedeck-toolchain`, archive SHA-256
  checked against the official download manifest, executable verified. No Go
  tests run: this step contains no application code.
- Review verdict: PASS after independent review and re-review. Report:
  [step 1 review](reviews/step-01-design.md).
- Important resolution: made tested systemd/cgroup supervision a prerequisite
  for general benchmark/Codex providers, fixing the original roadmap ordering.
- Commit: `ace0150` — `docs: establish reviewed MVP architecture and API contracts`.
- At acceptance, executable behavior was unimplemented and driver approval pending.
- Follow-up bookkeeping commit: `e8f0913` records the design commit hash.

## Step 2 — Persistent queue (accepted)

- Scope: `go.mod`, `go.sum`, `internal/job`, `internal/store`, synchronized docs.
- SQLite driver approved by user on 2026-11-18; pinned to v1.14.53.
- Acceptance: transactional submit/claim/finish with events; FIFO claims;
  concurrent callers and queue cap; rollback on event failures; restart
  reconciliation without retry; persistence across reopen; migration version guard.
- Tests: `go test ./...`, `go test -race ./...`, `go vet ./...` PASS with
  Go 1.27.4/CGO on Debian. Local documentation links and diff checks PASS.
- Review: independent PASS against `e8f0913`; [report](reviews/step-02-store.md).
- Initial test failure: SQLite strings could not scan directly into RawMessage;
  fixed by scanning a string and converting explicitly; all checks rerun.
- Commit: `067f7b9` — transactional SQLite job queue and lifecycle.
- Known limitations: storage requires a private parent directory and external
  instance ownership; service startup will enforce these in step 5. No runnable
  server exists yet. Reconciliation does not prove old processes have stopped.
- Next step: bounded process execution after acceptance.

## Step 3 — Bounded process execution (accepted)

- Scope: `internal/task`, `internal/runner`; fixed system-info task, explicit
  argv/environment, process group cleanup, deadlines, bounded raw logs.
- Acceptance: real success/nonzero/start failure; byte-preserving combined output;
  4 MiB cap; environment isolation; timeout/cancellation/descendant cleanup;
  unsafe IDs/symlink logs rejected; storage failures retained while draining.
- Tests: full `go test ./...`, `go test -race ./...`, `go vet ./...` PASS.
- Review: independent PASS after fix/re-review against `067f7b9`;
  [report](reviews/step-03-runner.md). Pre-cancelled jobs now persist an empty log
  without launching a process; new regression test passed independently uncached.
- Self-review also added parent-directory fsync for new log durability.
- Commit: `2291961` — allowlisted execution and bounded persistent logs.
- Risk: process groups do not contain descendants that deliberately escape;
  abrupt service death is still outside the containment guarantee.
- Next step: integrate the sequential worker after acceptance.

## Step 4 — Sequential worker (accepted)

- Scope: `internal/worker`, progress and review documentation.
- Acceptance: FIFO serial execution, real uname output after queue reopen,
  shutdown outcome persistence with queued work retained, unsupported provider
  rejection, stop immediately on result persistence failure.
- Checks: full ordinary/race/vet suite PASS with Go 1.27.4/CGO on Debian.
- Review: independent PASS against `2291961`; [report](reviews/step-04-worker.md).
- Commit: `6367a49` — durable queue connected to sequential worker.
- Known limits: no HTTP service yet; restart tests cover queued metadata, not
  hard-crash containment. A failed/removed provider may have no execution log.
- Next step: HTTP API, safe service startup, smoke test and CI.

## Step 5a — HTTP handlers (accepted)

- Step 5 split into handlers and service composition to keep reviews focused.
- Scope: `internal/api`, API/security/progress documentation.
- Acceptance: strict submission, query pagination, fixed task list, bounded raw
  logs with resume offsets, consistent errors, path/browser protections,
  concurrent submissions and real execution through the HTTP handler.
- Full ordinary tests, race and vet PASS. A test-only unused import was fixed
  before the suite passed; no runtime behavior was changed for that correction.
- Review: independent PASS against `6367a49`; [report](reviews/step-05a-api.md).
- Known limits: no standalone server yet; no authentication, UI or SSE.
- Next step: safe service composition, CLI, CI and actual HTTP smoke/restart test.
