# API contract

Base path `/api/v1`. JSON responses use `{"data": ...}`; errors use
`{"error":{"code":"...","message":"..."}}`. Logs are the only binary response.
Implemented and integration-tested. See progress for review status. The browser
console is served at `/`, using loopback HTTP by default or explicitly configured
direct HTTPS for private-network/VPN access.

## Authentication

Job routes require one `Authorization: Bearer <token>` header or a valid browser
session cookie, including history, details, logs and task types. The auth scheme
is case-insensitive. Invalid explicit Authorization never falls back to cookies.
The service generates a 256-bit random token, encoded as 64 lowercase hex
characters, in `<data-dir>/auth.token` (0600). It is reused on restart.
Missing, malformed, duplicate or incorrect credentials return `401` with
`WWW-Authenticate: Bearer realm="remotedeck"` and error code `unauthorized`.
No request body or job/log data is read before successful authentication.
Operator tokens in URL queries, request bodies or cookies are not accepted.
Host/Origin checks still apply, and can return 403 before credential checking.
This is one operator credential with full API access, not a multi-user role system.

## Browser sessions

- `POST /session`: exact configured Origin and operator bearer header required;
  no body. Returns 201 with `data.csrf_token` and `data.expires_at`. Sets an opaque
  `remotedeck_session` cookie: HttpOnly, SameSite=Strict, Path=/api/v1, no Domain,
  Max-Age=28800 on loopback HTTP. HTTPS instead uses
  `__Host-remotedeck_session`, Secure, HttpOnly, SameSite=Strict, Path=/, no Domain,
  with the same lifetime. Only the cookie for the actual transport is accepted.
  Login attempts share a process-wide bucket: burst 10, refill one per second;
  exhaustion returns 429 `login_rate_limit` and `Retry-After: 1`.
- `GET /session`: valid session cookie required. Returns its CSRF token and expiry
  for reload/resume; never returns the session ID or operator token.
- `DELETE /session`: valid cookie, exact Origin and X-CSRF-Token required. Revokes
  the server entry, expires the cookie and returns 204.

Cookie-authenticated writes require the exact configured origin, including the
port in the configured HTTPS origin (or actual HTTP listener), and one matching
X-CSRF-Token header. Missing/invalid CSRF
returns 403 `csrf_failed`; origin guard failures return 403 `forbidden`. Bearer
CLI requests do not require CSRF. Reauthentication rotates the current session.
Sessions have an absolute 8-hour lifetime, are capped at 64 (429 when full), and
live only in memory. Restart revokes all sessions. Expired entries are reclaimed
on access/login; no cleanup goroutine is needed.

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
`413` body too large; `415` unsupported media type; `429` queue/session/login limit;
`500` internal/persistence failure (without internal paths or SQL details).
HTTP listens only on 127.0.0.1; TLS enables explicit IPv4 network binding. Host
and actual transport must match the configured HTTPS origin, or the actual HTTP
listener. Origin, when present, must exactly match that origin. Forwarded and
X-Forwarded-* headers do not change this decision.
API Fetch Metadata, when present, must be `same-origin`; cross-site and same-site
requests are rejected, and no CORS allowance is added.
Unknown or duplicate query keys are rejected on history and log endpoints.
The public shell/assets contain no job data or credentials. They also permit
direct navigation (`Sec-Fetch-Site: none`). No HTTP redirect listener is opened.

## Deferred

Cancel, retry, SSE streaming and artifact endpoints are intentionally absent in
this milestone. Their semantics will be specified before implementation.
