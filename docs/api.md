# API contract (planned)

Base path `/api/v1`. JSON responses use `{"data": ...}`; errors use
`{"error":{"code":"...","message":"..."}}`. Logs are the only binary response.
No API is implemented at the design baseline.

## Submit a job

`POST /jobs`, `Content-Type: application/json`.

```json
{"task_type":"system-info","parameters":{}}
```

Only this task and an empty parameters object are accepted initially. Reject
unknown fields, non-object or missing parameters, multiple JSON documents and
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

`400` invalid input; `404` unknown job/route; `405` unsupported method;
`413` body too large; `415` unsupported media type; `429` queue full;
`500` internal/persistence failure (without internal paths or SQL details).
The service listens only on IPv4 loopback initially. Reject browser-originated
requests carrying Origin or nonempty Sec-Fetch-Site headers; no CORS allowance.
This mitigates browser access but is not authentication. Only trusted local
processes should have access. A protected browser UI requires a later auth design.

## Deferred

Cancel, retry, SSE streaming and artifact endpoints are intentionally absent in
this milestone. Their semantics will be specified before implementation.
