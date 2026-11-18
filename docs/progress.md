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
- Commit: recorded in the next accepted step to avoid a self-referential hash.
- Known limitations: storage requires a private parent directory and external
  instance ownership; service startup will enforce these in step 5. No runnable
  server exists yet. Reconciliation does not prove old processes have stopped.
- Next step: bounded process execution after acceptance.
