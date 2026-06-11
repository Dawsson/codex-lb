## ADDED Requirements

### Requirement: Go proxy API-key reservations use post-policy model

Go proxy paths that apply API-key model enforcement before upstream work MUST reserve API-key usage against the same post-policy model that will be sent upstream. Model-filtered API-key limits MUST block the request before account selection or upstream forwarding when the post-policy model has no remaining capacity.

This requirement applies to direct Go proxy paths for chat completions, HTTP Responses streaming, websocket Responses, compact Responses, media/file proxy work, image generation, and warmup submissions. HTTP bridge reservation forwarding and quota-planner warm-now `apiKeyId` reservation enforcement remain separate tracked gaps.

#### Scenario: Streaming reservation uses enforced model

- **GIVEN** an authenticated API key enforces model `gpt-5.5`
- **AND** the key has a model-filtered request limit for `gpt-5.5` with no remaining capacity
- **WHEN** a Go HTTP or websocket Responses request asks for `gpt-5.4`
- **THEN** the proxy rejects the request with `rate_limit_exceeded` before upstream forwarding
- **AND** it does not reserve usage against `gpt-5.4`
