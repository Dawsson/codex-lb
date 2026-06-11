## Why

Dashboard overview `metrics.topError` should summarize actual failed request
logs. The Go dashboard query currently considers any non-null error code,
including blank codes and successful rows, which can disagree with Python.

## What Changes

- Filter Go dashboard top-error queries to non-success request logs.
- Ignore blank error codes.
- Add a focused repository test.

## Impact

- Affects `GET /api/dashboard/overview` metrics.
- No database schema changes.
