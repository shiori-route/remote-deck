# Prioritized implementation roadmap

Each numbered step has its own tests, diff review, fixes and focused commit.
Do not proceed with unresolved Critical/High findings.

1. **Design baseline**: schema, contracts, risks, directory structure and scope.
2. **Persistent queue**: Go module, model, versioned schema, submission, atomic
   claims/transitions, restart reconciliation and tests including rollback and
   concurrent callers. The SQLite driver was approved on 2026-11-18.
3. **Bounded process execution**: fixed provider, private log storage, direct
   execution, process groups, deadlines, output cap and lifecycle tests.
4. **Worker integration**: serial durable polling, shutdown, failure handling,
   queue restart and real execution tests.
5. **HTTP vertical slice** (split into reviewed handlers and service composition): strict requests, submission/status/history/logs,
   safe startup and shutdown, instance lock, API integration tests, actual Debian
   smoke test, updated quick start and GitHub Actions. This completes milestone 1.
6. **Secure usable client**: single-operator bearer authentication, browser
   sessions/CSRF, responsive Svelte UI and explicit direct HTTPS implemented.
   Private-network deployment instructions and local TLS/browser tests are available;
   operator DNS, trusted certificates, VPN/firewall provisioning remain external.
7. **Supervised task integration**: first implement systemd/cgroup process
   ownership and pass crash-injection tests proving descendant cleanup and
   restart reconciliation. This is an acceptance gate before registering any
   general-purpose benchmark or Codex provider. Then add a benchmark provider with raw/structured results and an
   MP-SPDZ example; Codex provider after checking installed CLI capabilities,
   repository allowlists, execution profiles and isolated worktrees.
8. **Streaming and controls**: SSE byte-resume semantics, cancellation and retry
   contracts, artifact retrieval, slow-client and path-security tests.
9. **Reliability**: broaden fault injection, add concurrency and
   exclusive benchmarks, disk retention, scheduling metadata and metrics.
10. **Optional extensions**: only after stability: PWA, multi-host agents,
    dependencies, resource scheduling and additional AI providers.

First-milestone acceptance: an HTTP client submits `system-info`, observes a
persisted terminal status, reads actual command output, restarts the server and
reads the same result. Invalid commands/paths are rejected. Unit/integration
tests, `go test ./...`, `go test -race ./...` and `go vet ./...` must pass.
