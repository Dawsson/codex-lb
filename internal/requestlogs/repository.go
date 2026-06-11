package requestlogs

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/soju06/codex-lb/internal/db"
)

type Repository struct {
	store *db.Store
}

type Entry struct {
	RequestedAt          sql.NullString
	AccountID            sql.NullString
	PlanType             sql.NullString
	APIKeyID             sql.NullString
	APIKeyName           sql.NullString
	RequestID            string
	RequestKind          string
	Model                string
	Source               sql.NullString
	UserAgent            sql.NullString
	UserAgentGroup       sql.NullString
	Transport            sql.NullString
	ServiceTier          sql.NullString
	RequestedServiceTier sql.NullString
	ActualServiceTier    sql.NullString
	Status               string
	ErrorCode            sql.NullString
	ErrorMessage         sql.NullString
	FailurePhase         sql.NullString
	FailureDetail        sql.NullString
	FailureExceptionType sql.NullString
	UpstreamStatusCode   sql.NullInt64
	UpstreamErrorCode    sql.NullString
	BridgeStage          sql.NullString
	InputTokens          sql.NullInt64
	OutputTokens         sql.NullInt64
	CachedInputTokens    sql.NullInt64
	ReasoningTokens      sql.NullInt64
	ReasoningEffort      sql.NullString
	CostUSD              sql.NullFloat64
	LatencyMS            sql.NullInt64
	LatencyFirstTokenMS  sql.NullInt64
}

type Page struct {
	Entries []Entry
	Total   int64
}

type APIKeyOption struct {
	ID        string
	Name      string
	KeyPrefix sql.NullString
}

type ModelOption struct {
	Model           string
	ReasoningEffort sql.NullString
}

type StatusRow struct {
	Status    string
	ErrorCode sql.NullString
}

func NewRepository(store *db.Store) Repository {
	return Repository{store: store}
}

func (r Repository) FindLatestAccountIDForResponseID(ctx context.Context, responseID, apiKeyID, sessionID string) (string, error) {
	responseID = strings.TrimSpace(responseID)
	if responseID == "" {
		return "", nil
	}

	args := []any{responseID}
	conditions := []string{
		"request_id = ?",
		"status = 'success'",
		"account_id IS NOT NULL",
		"account_id != ''",
		"deleted_at IS NULL",
	}
	if apiKeyID != "" {
		conditions = append(conditions, "api_key_id = ?")
		args = append(args, apiKeyID)
	}

	lookup := func(extraConditions []string, extraArgs ...any) (string, error) {
		queryArgs := append([]any{}, args...)
		queryArgs = append(queryArgs, extraArgs...)
		queryConditions := append([]string{}, conditions...)
		queryConditions = append(queryConditions, extraConditions...)
		query := `
			SELECT account_id
			  FROM request_logs
			 WHERE ` + strings.Join(queryConditions, " AND ") + `
			 ORDER BY requested_at DESC, id DESC
			 LIMIT 1`
		var accountID string
		if err := r.store.DB().QueryRowContext(ctx, query, queryArgs...).Scan(&accountID); err != nil {
			if err == sql.ErrNoRows {
				return "", nil
			}
			return "", fmt.Errorf("find latest response owner: %w", err)
		}
		return strings.TrimSpace(accountID), nil
	}

	sessionID = strings.TrimSpace(sessionID)
	if sessionID != "" {
		sessionOwner, err := lookup([]string{"session_id = ?"}, sessionID)
		if err != nil || sessionOwner != "" {
			return sessionOwner, err
		}
	}
	return lookup(nil)
}

func (r Repository) List(ctx context.Context, filters Filters) (Page, error) {
	where, args := buildWhere(filters)
	joins := " LEFT JOIN api_keys ak ON ak.id = rl.api_key_id LEFT JOIN accounts acct ON acct.id = rl.account_id "
	totalQuery := "SELECT count(*) FROM request_logs rl" + joins + where
	var total int64
	if err := r.store.DB().QueryRowContext(ctx, totalQuery, args...).Scan(&total); err != nil {
		return Page{}, fmt.Errorf("count request logs: %w", err)
	}
	query := `
		SELECT rl.requested_at, rl.account_id, rl.plan_type, rl.api_key_id, ak.name,
		       rl.request_id, rl.request_kind, rl.model, rl.source, rl.useragent,
		       rl.useragent_group, rl.transport, rl.service_tier,
		       rl.requested_service_tier, rl.actual_service_tier, rl.status,
		       rl.error_code, rl.error_message, rl.failure_phase, rl.failure_detail,
		       rl.failure_exception_type, rl.upstream_status_code, rl.upstream_error_code,
		       rl.bridge_stage, rl.input_tokens, rl.output_tokens, rl.cached_input_tokens,
		       rl.reasoning_tokens, rl.reasoning_effort, rl.cost_usd, rl.latency_ms,
		       rl.latency_first_token_ms
		  FROM request_logs rl
		  ` + joins + where + `
		 ORDER BY rl.requested_at DESC, rl.id DESC
		 LIMIT ? OFFSET ?`
	args = append(args, filters.Limit, filters.Offset)
	rows, err := r.store.DB().QueryContext(ctx, query, args...)
	if err != nil {
		return Page{}, fmt.Errorf("list request logs: %w", err)
	}
	defer rows.Close()
	var entries []Entry
	for rows.Next() {
		var entry Entry
		if err := rows.Scan(
			&entry.RequestedAt, &entry.AccountID, &entry.PlanType, &entry.APIKeyID,
			&entry.APIKeyName, &entry.RequestID, &entry.RequestKind, &entry.Model,
			&entry.Source, &entry.UserAgent, &entry.UserAgentGroup, &entry.Transport,
			&entry.ServiceTier, &entry.RequestedServiceTier, &entry.ActualServiceTier,
			&entry.Status, &entry.ErrorCode, &entry.ErrorMessage, &entry.FailurePhase,
			&entry.FailureDetail, &entry.FailureExceptionType, &entry.UpstreamStatusCode,
			&entry.UpstreamErrorCode, &entry.BridgeStage, &entry.InputTokens,
			&entry.OutputTokens, &entry.CachedInputTokens, &entry.ReasoningTokens,
			&entry.ReasoningEffort, &entry.CostUSD, &entry.LatencyMS, &entry.LatencyFirstTokenMS,
		); err != nil {
			return Page{}, fmt.Errorf("scan request log: %w", err)
		}
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return Page{}, fmt.Errorf("iterate request logs: %w", err)
	}
	return Page{Entries: entries, Total: total}, nil
}

func (r Repository) AccountIDs(ctx context.Context, filters Filters) ([]string, error) {
	where, args := buildWhere(filters)
	rows, err := r.store.DB().QueryContext(ctx, "SELECT DISTINCT rl.account_id FROM request_logs rl LEFT JOIN api_keys ak ON ak.id = rl.api_key_id LEFT JOIN accounts acct ON acct.id = rl.account_id "+where+" AND rl.account_id IS NOT NULL ORDER BY rl.account_id", args...)
	if err != nil {
		return nil, fmt.Errorf("list request log account options: %w", err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan account option: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (r Repository) ModelOptions(ctx context.Context, filters Filters) ([]ModelOption, error) {
	where, args := buildWhere(filters)
	rows, err := r.store.DB().QueryContext(ctx, "SELECT DISTINCT rl.model, rl.reasoning_effort FROM request_logs rl LEFT JOIN api_keys ak ON ak.id = rl.api_key_id LEFT JOIN accounts acct ON acct.id = rl.account_id "+where+" ORDER BY rl.model, rl.reasoning_effort", args...)
	if err != nil {
		return nil, fmt.Errorf("list request log model options: %w", err)
	}
	defer rows.Close()
	var options []ModelOption
	for rows.Next() {
		var option ModelOption
		if err := rows.Scan(&option.Model, &option.ReasoningEffort); err != nil {
			return nil, fmt.Errorf("scan model option: %w", err)
		}
		options = append(options, option)
	}
	return options, rows.Err()
}

func (r Repository) Statuses(ctx context.Context, filters Filters) ([]StatusRow, error) {
	where, args := buildWhere(filters)
	rows, err := r.store.DB().QueryContext(ctx, "SELECT DISTINCT rl.status, rl.error_code FROM request_logs rl LEFT JOIN api_keys ak ON ak.id = rl.api_key_id LEFT JOIN accounts acct ON acct.id = rl.account_id "+where+" ORDER BY rl.status, rl.error_code", args...)
	if err != nil {
		return nil, fmt.Errorf("list request log status options: %w", err)
	}
	defer rows.Close()
	var statuses []StatusRow
	for rows.Next() {
		var row StatusRow
		if err := rows.Scan(&row.Status, &row.ErrorCode); err != nil {
			return nil, fmt.Errorf("scan status option: %w", err)
		}
		statuses = append(statuses, row)
	}
	return statuses, rows.Err()
}

func (r Repository) APIKeys(ctx context.Context, filters Filters) ([]APIKeyOption, error) {
	where, args := buildWhere(filters)
	rows, err := r.store.DB().QueryContext(ctx, "SELECT DISTINCT ak.id, ak.name, ak.key_prefix FROM request_logs rl JOIN api_keys ak ON ak.id = rl.api_key_id LEFT JOIN accounts acct ON acct.id = rl.account_id "+where+" ORDER BY ak.name", args...)
	if err != nil {
		return nil, fmt.Errorf("list request log api key options: %w", err)
	}
	defer rows.Close()
	var options []APIKeyOption
	for rows.Next() {
		var option APIKeyOption
		if err := rows.Scan(&option.ID, &option.Name, &option.KeyPrefix); err != nil {
			return nil, fmt.Errorf("scan api key option: %w", err)
		}
		options = append(options, option)
	}
	return options, rows.Err()
}

// CostMetrics summarizes request_logs entries since a point in time, mirroring
// the totals computed by app.modules.usage.builders._cost_summary_from_logs
// and _usage_metrics.
type CostMetrics struct {
	Requests          int64
	Errors            int64
	TotalCostUSD      float64
	TotalTokens       int64
	CachedInputTokens int64
	TopErrorCode      *string
}

// AggregateCostMetrics computes cost and usage metrics for request_logs
// entries recorded at or after since (a SQLite-formatted timestamp).
func (r Repository) AggregateCostMetrics(ctx context.Context, since string) (CostMetrics, error) {
	var metrics CostMetrics
	err := r.store.DB().QueryRowContext(ctx, `
		SELECT count(*),
		       coalesce(sum(case when status != 'success' then 1 else 0 end), 0),
		       coalesce(sum(coalesce(cost_usd, 0)), 0),
		       coalesce(sum(coalesce(input_tokens, 0) + coalesce(output_tokens, reasoning_tokens, 0)), 0),
		       coalesce(sum(min(coalesce(cached_input_tokens, 0), coalesce(input_tokens, coalesce(cached_input_tokens, 0)))), 0)
		  FROM request_logs
		 WHERE deleted_at IS NULL AND requested_at >= ?
	`, since).Scan(&metrics.Requests, &metrics.Errors, &metrics.TotalCostUSD, &metrics.TotalTokens, &metrics.CachedInputTokens)
	if err != nil {
		return CostMetrics{}, fmt.Errorf("aggregate request log cost metrics: %w", err)
	}

	if metrics.Errors > 0 {
		var topError sql.NullString
		err := r.store.DB().QueryRowContext(ctx, `
			SELECT error_code
			  FROM request_logs
			 WHERE deleted_at IS NULL AND requested_at >= ? AND status != 'success' AND error_code IS NOT NULL AND error_code != ''
			 GROUP BY error_code
			 ORDER BY count(*) DESC, error_code ASC
			 LIMIT 1
		`, since).Scan(&topError)
		if err != nil && err != sql.ErrNoRows {
			return CostMetrics{}, fmt.Errorf("top error code: %w", err)
		}
		if topError.Valid {
			metrics.TopErrorCode = &topError.String
		}
	}

	return metrics, nil
}

func buildWhere(filters Filters) (string, []any) {
	clauses := []string{"rl.deleted_at IS NULL", "rl.request_kind NOT IN ('warmup', 'limit_warmup')"}
	args := []any{}
	if filters.Since != "" {
		clauses = append(clauses, "rl.requested_at >= ?")
		args = append(args, filters.Since)
	}
	if filters.Until != "" {
		clauses = append(clauses, "rl.requested_at <= ?")
		args = append(args, filters.Until)
	}
	if filters.Search != "" {
		clauses = append(clauses, `(
			rl.account_id LIKE ? OR acct.email LIKE ? OR rl.request_id LIKE ? OR
			rl.model LIKE ? OR rl.reasoning_effort LIKE ? OR rl.source LIKE ? OR
			rl.status LIKE ? OR rl.error_code LIKE ? OR rl.error_message LIKE ? OR
			rl.api_key_id LIKE ? OR ak.name LIKE ? OR rl.requested_at LIKE ? OR
			CAST(coalesce(rl.input_tokens, 0) AS TEXT) LIKE ? OR
			CAST(coalesce(rl.output_tokens, 0) AS TEXT) LIKE ? OR
			CAST(coalesce(rl.reasoning_tokens, 0) AS TEXT) LIKE ? OR
			CAST(coalesce(rl.cached_input_tokens, 0) AS TEXT) LIKE ? OR
			CAST(coalesce(rl.latency_ms, 0) AS TEXT) LIKE ?
		)`)
		value := "%" + filters.Search + "%"
		args = append(args, value, value, value, value, value, value, value, value, value, value, value, value, value, value, value, value, value)
	}
	addIn(&clauses, &args, "rl.account_id", filters.AccountIDs)
	addIn(&clauses, &args, "rl.api_key_id", filters.APIKeyIDs)
	appendStatusFilter(&clauses, &args, filters.StatusFilter)
	if len(filters.ModelOptions) > 0 {
		var optionClauses []string
		for _, option := range filters.ModelOptions {
			model, effort := parseModelOption(option)
			if model == "" {
				continue
			}
			if effort == "" {
				optionClauses = append(optionClauses, "(rl.model = ? AND rl.reasoning_effort IS NULL)")
				args = append(args, model)
			} else {
				optionClauses = append(optionClauses, "(rl.model = ? AND rl.reasoning_effort = ?)")
				args = append(args, model, effort)
			}
		}
		if len(optionClauses) > 0 {
			clauses = append(clauses, "("+strings.Join(optionClauses, " OR ")+")")
		}
	}
	return " WHERE " + strings.Join(clauses, " AND "), args
}

func addIn(clauses *[]string, args *[]any, column string, values []string) {
	if len(values) == 0 {
		return
	}
	placeholders := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		placeholders = append(placeholders, "?")
		*args = append(*args, value)
	}
	if len(placeholders) > 0 {
		*clauses = append(*clauses, column+" IN ("+strings.Join(placeholders, ",")+")")
	}
}

func appendStatusFilter(clauses *[]string, args *[]any, filter StatusFilter) {
	statusClauses := make([]string, 0, 3)
	if filter.IncludeSuccess {
		statusClauses = append(statusClauses, "rl.status = 'success'")
	}
	if len(filter.ErrorCodesIn) > 0 {
		placeholders := make([]string, 0, len(filter.ErrorCodesIn))
		for _, code := range filter.ErrorCodesIn {
			placeholders = append(placeholders, "?")
			*args = append(*args, code)
		}
		statusClauses = append(statusClauses, "(rl.status = 'error' AND rl.error_code IN ("+strings.Join(placeholders, ",")+"))")
	}
	if filter.IncludeErrorOther {
		if len(filter.ErrorCodesExcluding) > 0 {
			placeholders := make([]string, 0, len(filter.ErrorCodesExcluding))
			for _, code := range filter.ErrorCodesExcluding {
				placeholders = append(placeholders, "?")
				*args = append(*args, code)
			}
			statusClauses = append(statusClauses, "(rl.status = 'error' AND (rl.error_code IS NULL OR rl.error_code NOT IN ("+strings.Join(placeholders, ",")+")))")
		} else {
			statusClauses = append(statusClauses, "rl.status = 'error'")
		}
	}
	if len(statusClauses) == 0 {
		return
	}
	*clauses = append(*clauses, "("+strings.Join(statusClauses, " OR ")+")")
}

func parseModelOption(value string) (string, string) {
	model, effort, ok := strings.Cut(value, ":::")
	if !ok {
		return strings.TrimSpace(value), ""
	}
	return strings.TrimSpace(model), strings.TrimSpace(effort)
}
