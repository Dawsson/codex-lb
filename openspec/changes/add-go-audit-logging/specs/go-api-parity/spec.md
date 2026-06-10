## ADDED Requirements

### Requirement: Go records audit log entries for dashboard mutations

The Go API SHALL write an `audit_logs` row for every dashboard mutation
endpoint (accounts, API keys, firewall, settings, sticky sessions, quota
planner), recording the action, actor IP, request ID, and a details payload.

#### Scenario: Account pause is audited

- **WHEN** an operator calls `POST /api/accounts/{accountID}/pause`
- **THEN** the Go API records an audit log entry with action `account.pause`,
  the account id in details, the request's actor IP, and request ID.

### Requirement: Go exposes audit log listing

The Go API SHALL expose `GET /api/audit-logs` supporting `action`, `limit`,
and `offset` query parameters, matching the Python response schema.

#### Scenario: Filter audit logs by action

- **WHEN** a client requests `GET /api/audit-logs?action=account.pause`
- **THEN** the response contains only audit log entries whose action is
  `account.pause`, ordered most-recent first.
