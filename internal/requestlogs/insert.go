package requestlogs

import (
	"context"
	"fmt"
)

type InsertParams struct {
	RequestID            string
	RequestKind          string
	Model                string
	AccountID            *string
	PlanType             *string
	APIKeyID             *string
	Status               string
	ErrorCode            *string
	ErrorMessage         *string
	InputTokens          *int64
	OutputTokens         *int64
	LatencyMS            *int64
	UserAgent            *string
	Transport            *string
	ServiceTier          *string
	RequestedServiceTier *string
	Source               *string
}

func (r Repository) Insert(ctx context.Context, params InsertParams) error {
	_, err := r.store.DB().ExecContext(ctx, `
		INSERT INTO request_logs (
			request_id, request_kind, model, account_id, plan_type, api_key_id,
			status, error_code, error_message, input_tokens, output_tokens,
			latency_ms, useragent, transport, service_tier, requested_service_tier,
			requested_at, source
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, datetime('now'), ?)
	`,
		params.RequestID,
		params.RequestKind,
		params.Model,
		nullableString(params.AccountID),
		nullableString(params.PlanType),
		nullableString(params.APIKeyID),
		params.Status,
		nullableString(params.ErrorCode),
		nullableString(params.ErrorMessage),
		nullableInt(params.InputTokens),
		nullableInt(params.OutputTokens),
		nullableInt(params.LatencyMS),
		nullableString(params.UserAgent),
		nullableString(params.Transport),
		nullableString(params.ServiceTier),
		nullableString(params.RequestedServiceTier),
		nullableString(params.Source),
	)
	if err != nil {
		return fmt.Errorf("insert request log: %w", err)
	}
	return nil
}

func nullableString(value *string) any {
	if value == nil {
		return nil
	}
	return *value
}

func nullableInt(value *int64) any {
	if value == nil {
		return nil
	}
	return *value
}
