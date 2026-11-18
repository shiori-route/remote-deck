# API contract

Base path `/api/v1`. JSON responses use `{"data": ...}`; errors use
`{"error":{"code":"...","message":"..."}}`. Logs are the only binary response.
Implemented and integration-tested, with a local runnable service. See progress
for review status. Remote access and browser UI are not yet supported.

## Authentication

All routes require exactly one `Authorization: Bearer <token>` header, including
job history, details, logs and task-type listing. The auth scheme is case-insensitive.
The service generates a 256-bit random token, encoded as 64 lowercase hex
characters, in `<data-dir>/auth.token` (0600). It is reused on restart.
Missing, malformed, duplicate or incorrect credentials return `401` with
`WWW-Authenticate: Bearer realm="remotedeck"` and error code `unauthorized`.
No request body or job/log data is read before successful authentication.
Tokens in URL queries, request bodies or cookies are not accepted.
Host/Origin checks still apply, and can return 403 before credential checking.
This is one operator credential with full API access, not a multi-user role system.

## Submit a job

`POST /jobs`, `Content-Type: application/json`.

```json
{"task_type":"system-info","parameters":{}}
```

Only this task and an empty parameters object are accepted initially. Reject
unknown/duplicate fields, non-object or missing parameters, multiple JSON documents and
invalid JSON. Maximum request body: 16 KiB. The server chooses timeout (10 s),
executable (`/usr/bin/uname`), arguments (`-a`), environment and working directory.
Client-provided executable, paths or environment are never accepted.

Return `201 Created`, `Location: /api/v1/jobs/{id}` and `{"data":<job>}` only
after the job and initial event commit. The returned snapshot has state `queued`;
the worker may already have advanced by the next query. No idempotency guarantee.

## Query

- `GET /jobs/{id}`: `200` with `{"data":<job>}`; `404` for absent/invalid IDs.
- `GET /jobs?after=0&limit=20`: ascending creation sequence; limit 1–100.
  Response `{"data":[<job>],"next_after":42}`; use the last returned sequence
  as the next cursor, including when a later page is empty. An empty page keeps
  the supplied cursor. Invalid or negative query values return `400`.
- `GET /task-types`: `200`, descriptions of available tasks and fixed timeout.

Job fields: `id`, `sequence`, `task_type`, `parameters` (JSON object), `state`,
`created_at`, `started_at`, `completed_at`, `attempt`, `exit_code`, `error`,
`timeout_seconds`. Nullable fields encode as JSON null. Filesystem paths are
internal and are not returned.

## Logs

`GET /jobs/{id}/logs?offset=0&limit=65536` returns `application/octet-stream`.
Offset is a nonnegative byte offset; limit is 1–65536. `X-Next-Offset` gives the
offset plus bytes returned; `X-Job-State` is a status snapshot. Empty queued logs
return `200` with an empty body. Offset beyond current size returns `416`.
No assumption that a short read means the running job has completed.
Clients should poll status until terminal, then read to end. Preserve and decode
raw bytes incrementally. Responses must disable caching and MIME sniffing.

## Errors and access

`400` invalid input; `401` missing/invalid credentials; `403` forbidden origin/host;
`404` unknown job/route; `405` unsupported method;
`413` body too large; `415` unsupported media type; `429` queue full;
`500` internal/persistence failure (without internal paths or SQL details).
The service listens only on IPv4 loopback initially. Reject browser-originated
requests carrying Origin or nonempty Sec-Fetch-Site headers; no CORS allowance.
The Host header must be `127.0.0.1` with an optional port to reject DNS rebinding.
Unknown or duplicate query keys are rejected on history and log endpoints.
These browser guards supplement bearer authentication. Browser sessions, CSRF
protection for a UI and remote TLS access require a separate design.

## Deferred

Cancel, retry, SSE streaming and artifact endpoints are intentionally absent in
this milestone. Their semantics will be specified before implementation.
