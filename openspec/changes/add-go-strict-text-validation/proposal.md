## Why

Python rejects strict structured-output schemas that violate OpenAI's strict
JSON schema policy before opening an upstream connection. Go currently forwards
those invalid `text.format` / `response_format` schemas, which can surface as
upstream stream failures instead of deterministic client errors.

## What Changes

- Port strict text-format schema validation to Go using the same schema walker
  as strict function tools.
- Apply it to Responses `text.format` payloads.
- Apply it to Chat Completions `response_format.type = "json_schema"` payloads
  before upstream forwarding.

## Impact

- Affects strict structured-output requests on Responses and Chat Completions.
- No database schema changes.
