## MODIFIED Requirements

### Requirement: Responses proxy service decomposition preserves public behavior

The Python Responses proxy service SHALL preserve its externally visible
request, streaming, retry, bridge, websocket, request-log, and API-key
settlement behavior when private implementation methods are decomposed into
domain-specific modules.

#### Scenario: Existing proxy callers keep using the facade
- **WHEN** application code imports and constructs `ProxyService` from
  `app.modules.proxy.service`
- **THEN** the public facade remains available
- **AND** moved private implementation helpers do not require route or caller
  changes outside the proxy module boundary

#### Scenario: Domain extraction preserves compatibility shims
- **WHEN** existing internal code imports moved private support or warmup names
  from their legacy private module paths
- **THEN** compatibility shims continue to expose those names until callers are
  migrated to the new private package layout
