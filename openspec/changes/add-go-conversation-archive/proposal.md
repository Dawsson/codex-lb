# Add conversation archive endpoints to Go

## Why

`app/modules/conversation_archive` exposes archived conversation files and
records to the dashboard. This is a small, self-contained module suitable
as a final/low-priority phase.

## What Changes

- Port `app/modules/conversation_archive/service.py` and schemas.
- Implement:
  - `GET /api/conversation-archive/files`
  - `GET /api/conversation-archive/records`

## Impact

- Depends on `migrate-go-db-ownership` for any conversation archive tables.
- Lowest priority/risk phase; can land last.
