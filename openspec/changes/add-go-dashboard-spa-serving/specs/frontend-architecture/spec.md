## ADDED Requirements

### Requirement: Go API optionally serves dashboard SPA assets

The Go API SHALL support optional serving of built dashboard SPA assets from an
operator-configured directory.

#### Scenario: Existing static asset is served
- **GIVEN** dashboard SPA serving is configured with a directory containing
  `assets/app.js`
- **WHEN** a client requests `GET /assets/app.js`
- **THEN** the Go API serves that static file from the configured directory

#### Scenario: Dashboard route falls back to index
- **GIVEN** dashboard SPA serving is configured with an `index.html`
- **WHEN** a client requests `GET /accounts`
- **THEN** the Go API serves `index.html`

#### Scenario: API 404 is not masked by SPA fallback
- **GIVEN** dashboard SPA serving is configured
- **WHEN** a client requests `GET /api/not-found`
- **THEN** the Go API returns a not-found response instead of `index.html`
