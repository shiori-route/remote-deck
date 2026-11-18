# Development progress

Current status: the local HTTP vertical slice, operator bearer authentication,
browser sessions/CSRF and responsive Svelte console are implemented and tested.
The original slice passed independent reviews; later authentication/UI steps
received self-review. Protected remote HTTPS deployment is still future work.

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
- Commit: `906a6e9` — strict job API and resumable log reads.
- Known limits: no standalone server yet; no authentication, UI or SSE.
- Next step: safe service composition, CLI, CI and actual HTTP smoke/restart test.

## Step 5b — Runnable local service (accepted)

- Scope: `cmd/remotedeck`, `internal/service`, CI, gitignore and synchronized docs.
- Acceptance: loopback-only non-root startup; private storage; exclusive instance
  lock; shutdown joins server/worker; worker failures stop service; persisted
  results/logs remain readable after restart; interrupted rows are not retried.
- Full `go test ./...`, `go test -race ./...`, `go vet ./...` PASS on Debian 13,
  Go 1.27.4/CGO. Added worker-failure service test also passes ordinary/race/vet.
- Test harness corrections: temporary service directories explicitly chmod 0700;
  crash helper arguments follow `--` so Go's test flags do not reject them.
- Built actual binary and ran HTTP smoke test: submit system-info, succeed with
  exit 0, retrieve 112 bytes of output, reject a concurrent second instance,
  stop via SIGTERM, restart and read unchanged status/logs. PASS.
- SIGKILL helper test: running row becomes failed with unknown outcome, queued
  row retained, instance lock released. PASS; no task descendant was launched.
- CI configured for Go 1.25.x and 1.27.x; GitHub-hosted runs not executed here.
- Review: independent PASS against `906a6e9`; [report](reviews/step-05b-service.md).
- Commit: `6f97719` — loopback service with safe lifecycle and CI.
- M1 (Medium) fixed: crash-test readiness now requires a complete valid job ID,
  avoiding a race between ready-file creation and writing. Targeted race test
  repeated 5 times PASS; reviewer service tests uncached PASS.
- Known limits: no auth/UI/SSE/retries/artifacts or general commands; total history
  retention unbounded; no hard-crash or escaped-descendant containment guarantee.
- Next planned milestone after acceptance: authentication design and usable client.

## Final progress bookkeeping

- Scope: record the actual accepted service commit above after Git assigned it.
- Review: self-review PASS; commit subject/hash checked against Git and the
  one-file diff checked for whitespace. No code changes or new risks.
- Validation: documentation links checked; application checks were completed in
  step 5b and are not rerun for this documentation-only edit.

## Step 6a — Mandatory local API authentication

- Commit: `b55c7cf`.
- Baseline: `43152c2`; continued in the existing `dev` worktree after the workspace
  root moved to its parent. Earlier AGENTS.md instructions were revoked by the
  user; this step uses self-review, not an independent agent review.
- Scope: `internal/auth`, API/service integration and tests, synchronized docs.
- First startup generates a 256-bit token in `auth.token` (0600), persists it,
  and reuses it across restarts. Unsafe/malformed existing token files fail closed.
- All routes require one bearer header, including read-only endpoints and logs.
  Missing/invalid/duplicate credentials return 401 before application body/storage
  access. Zero-value verifier denies access. Existing Host/Origin guards remain.
- Runtime verifier keeps a digest; credential comparison is constant-time.
  No new dependencies or schema migration. Existing clients must add the header.
- Validation: full `go test ./...`, `go test -race ./...`, `go vet ./...` PASS
  with Go 1.27.4 on Debian. Auth/API/service tests cover invalid headers, unsafe
  files, stable tokens, rotation, startup failure cleanup and authorized execution.
- Real binary smoke PASS: upgraded a temporary data directory created by the
  pre-auth binary, preserved its existing job/log, rejected anonymous requests and
  wrong credentials, confirmed unauthorized submission had no effect, exercised
  README's stdin-header curl helper, restarted with the same token, rotated the
  token offline and rejected the old credential. Secrets absent from captured
  service/task logs in that scenario. No service left running.
- Self-review: PASS; [report](reviews/step-06a-auth.md). Token permission regression
  setup explicitly chmods the unsafe fixture so it works under restrictive umasks.
- Known limits: one full-authority token, no expiry/online revocation/user roles,
  no browser session or TLS, same-OS-user processes remain trusted. Tasks still
  share the service identity/data directory; future providers need isolation design.
- Next step: browser-session and CSRF design, then a usable client; keep loopback
  binding until protected remote access is designed and tested.

## Step 6b — Local browser sessions and Svelte console

- Baseline: `b55c7cf`; scope: bounded session store, session API and CSRF guards,
  embedded static serving, Svelte/TypeScript UI, browser tests, CI and docs.
- Browser login uses the operator token once, then an HttpOnly/SameSite cookie.
  Sessions expire after 8 hours, cap at 64, rotate on reauthentication, revoke on
  logout and all disappear on restart. Cookie writes require exact Origin/CSRF.
- UI: login/logout, system-info submission, paginated history, selected-job
  details, 1.5-second polling and incremental UTF-8 log reads with a display cap.
  No operator credential goes into localStorage/sessionStorage. CLI bearer stays
  supported. No network listener or runtime Node/Vite server was introduced.
- Validation: npm ci, npm run check (0 errors/warnings), npm run build and npm ls
  passed; full Go ordinary/race/vet suites and binary build passed on Debian.
- Chromium E2E: desktop (1365px) and narrow-screen (390px) PASS. Tests cover login
  failure/success, real uname output, reload session recovery, CSRF/origin refusal,
  21-job forward/backward pagination, literal HTML log display, split UTF-8
  boundaries, bounded output, logout replay
  refusal, no browser credential storage and no horizontal overflow. Screenshots
  inspected at `/tmp/remotedeck-desktop.png` and `/tmp/remotedeck-mobile.png`.
- Real service restart test now confirms browser cookies are revoked while bearer
  access and persisted job/log data remain available.
- Self-review: PASS for implementation; [report](reviews/step-06b-console.md).
  Initial Svelte reactivity/accessibility warnings fixed; TypeScript pinned to a
  compatible version; stale polling 401 cannot clear a newer login generation.
  Pagination controls stay disabled during refresh to prevent stale-cursor clicks.
- Dependency audit is NOT clean: six high build-tool entries inherit the unpatched
  picomatch advisory. Restricted to trusted build patterns here; details and
  limitations recorded in security.md. No findings were hidden or suppressed.
- CI now builds frontend assets before Go checks and runs browser tests on Go
  1.27.x. Remote GitHub Actions has not been executed in this session.
- Remaining limits: HTTP-only loopback cookies require trusted local services
  (cookies are not port-isolated), no remote access, no roles, SSE, cancellation
  UI or general task providers. Go build/test requires generated frontend assets.
- Next step: design protected HTTPS remote access before enabling network clients.
