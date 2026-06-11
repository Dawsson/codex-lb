package proxy

import (
	"context"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/soju06/codex-lb/internal/apikeys"
	"github.com/soju06/codex-lb/internal/settings"
)

// ApiKeyData mirrors the subset of app.modules.api_keys.service.ApiKeyData
// used by the proxy request path.
type ApiKeyData struct {
	ID                            string
	Name                          string
	KeyPrefix                     string
	AllowedModels                 []string
	ApplyToCodexModel             bool
	EnforcedModel                 *string
	EnforcedReasoning             *string
	EnforcedServiceTier           *string
	TrafficClass                  string
	ExpiresAt                     *time.Time
	Limits                        []apikeys.LimitRule
	AccountAssignmentScopeEnabled bool
	AssignedAccountIDs            []string
}

// ValidateProxyAPIKey ports app.core.auth.dependencies.validate_proxy_api_key
// (and validate_proxy_api_key_authorization). It returns (nil, nil) when API
// key authentication is disabled and the request is local.
//
// Simplification: the Python implementation also allows unauthenticated
// remote access from CIDR ranges listed in
// proxy_unauthenticated_client_cidrs. That allowlist is not yet ported; with
// API key auth disabled, only loopback requests are permitted.
func ValidateProxyAPIKey(ctx context.Context, repo apikeys.Repository, settingsRepo settings.Repository, r *http.Request) (*ApiKeyData, error) {
	dashboardSettings, err := settingsRepo.Get(ctx)
	if err != nil {
		return nil, err
	}

	if !dashboardSettings.APIKeyAuthEnabled {
		if !isLocalRequest(r) {
			return nil, NewProxyAuthError("Proxy authentication must be configured before remote access is allowed")
		}
		return nil, nil
	}

	token := extractBearerToken(r.Header.Get("Authorization"))
	if token == "" {
		return nil, NewProxyAuthError("Missing API key in Authorization header")
	}

	return validateAPIKeyToken(ctx, repo, token)
}

// ValidateProxyAPIKeyRequired ports app.core.auth.dependencies.validate_usage_api_key:
// it always requires a valid bearer API key, regardless of
// api_key_auth_enabled.
func ValidateProxyAPIKeyRequired(ctx context.Context, repo apikeys.Repository, r *http.Request) (*ApiKeyData, error) {
	token := extractBearerToken(r.Header.Get("Authorization"))
	if token == "" {
		return nil, NewProxyAuthError("Missing API key in Authorization header")
	}
	return validateAPIKeyToken(ctx, repo, token)
}

func validateAPIKeyToken(ctx context.Context, repo apikeys.Repository, token string) (*ApiKeyData, error) {
	record, err := repo.GetByHash(ctx, apikeys.HashKey(token))
	if err != nil {
		return nil, err
	}
	if record == nil || !record.IsActive {
		return nil, NewProxyAuthError("Invalid API key")
	}
	if record.ExpiresAt.Valid {
		if expiresAt, err := parseSQLiteTimestamp(record.ExpiresAt.String); err == nil {
			if expiresAt.Before(time.Now().UTC()) {
				return nil, NewProxyAuthError("API key has expired")
			}
		}
	}
	return toAPIKeyData(record), nil
}

func toAPIKeyData(record *apikeys.KeyRecord) *ApiKeyData {
	data := &ApiKeyData{
		ID:                            record.ID,
		Name:                          record.Name,
		KeyPrefix:                     record.KeyPrefix,
		AllowedModels:                 record.AllowedModelsList(),
		ApplyToCodexModel:             record.ApplyToCodexModel,
		TrafficClass:                  record.TrafficClass,
		Limits:                        record.Limits,
		AccountAssignmentScopeEnabled: record.AccountAssignmentScopeEnabled,
		AssignedAccountIDs:            append([]string(nil), record.AssignedAccountIDs...),
	}
	if record.EnforcedModel.Valid {
		value := record.EnforcedModel.String
		data.EnforcedModel = &value
	}
	if record.EnforcedReasoningEffort.Valid {
		value := record.EnforcedReasoningEffort.String
		data.EnforcedReasoning = &value
	}
	if record.EnforcedServiceTier.Valid {
		value := record.EnforcedServiceTier.String
		data.EnforcedServiceTier = &value
	}
	if record.ExpiresAt.Valid {
		if expiresAt, err := parseSQLiteTimestamp(record.ExpiresAt.String); err == nil {
			data.ExpiresAt = &expiresAt
		}
	}
	return data
}

// parseSQLiteTimestamp parses a DATETIME column value as returned by the
// sqlite driver, which may be formatted as RFC3339 (e.g. "2026-06-10T22:26:37Z")
// or as "2006-01-02 15:04:05" depending on how the value was stored.
func parseSQLiteTimestamp(value string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, value); err == nil {
		return t, nil
	}
	return time.Parse("2006-01-02 15:04:05", value)
}

func extractBearerToken(authorization string) string {
	const prefix = "Bearer "
	if !strings.HasPrefix(authorization, prefix) {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(authorization, prefix))
}

// isLocalRequest reports whether the request's remote address is loopback.
func isLocalRequest(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return ip.IsLoopback()
}
