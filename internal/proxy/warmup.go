package proxy

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/soju06/codex-lb/internal/upstream"
)

const (
	warmupSkipIneligiblePrimary = "ineligible_primary_usage"
	warmupMaxConcurrentSubmits  = 5
)

type WarmupResponse struct {
	Mode          string                   `json:"mode"`
	TotalAccounts int                      `json:"total_accounts"`
	Submitted     []WarmupSubmittedAccount `json:"submitted"`
	Skipped       []WarmupSkippedAccount   `json:"skipped"`
	Failed        []WarmupFailedAccount    `json:"failed"`
}

type WarmupSubmittedAccount struct {
	AccountID string `json:"account_id"`
	RequestID string `json:"request_id"`
	Model     string `json:"model"`
}

type WarmupSkippedAccount struct {
	AccountID string `json:"account_id"`
	Reason    string `json:"reason"`
}

type WarmupFailedAccount struct {
	AccountID    string `json:"account_id"`
	ErrorCode    string `json:"error_code"`
	ErrorMessage string `json:"error_message"`
}

type warmupAccount struct {
	selection SelectionAccount
	token     string
}

type warmupSubmitResult struct {
	accountID    string
	requestID    string
	model        string
	success      bool
	errorCode    string
	errorMessage string
}

type warmupUpstreamFunc func(ctx context.Context, opts upstream.StreamOptions) (map[string]any, error)

func (s *Service) Warmup(ctx context.Context, r *http.Request, apiKey *ApiKeyData, mode string) (WarmupResponse, *OpenAIErrorEnvelope, int, error) {
	normalizedMode := normalizeWarmupMode(mode)
	if !validWarmupMode(normalizedMode) {
		envelope := OpenAIError("invalid_request_error", "Invalid warmup mode. Supported values: normal, strict, force.", "invalid_request_error")
		return WarmupResponse{}, &envelope, http.StatusBadRequest, nil
	}

	dashboardSettings, err := s.settingsRepo.Get(ctx)
	if err != nil {
		return WarmupResponse{}, nil, http.StatusInternalServerError, err
	}
	warmupModel := dashboardSettings.WarmupModel
	if apiKey != nil && apiKey.EnforcedModel != nil && *apiKey.EnforcedModel != "" {
		warmupModel = *apiKey.EnforcedModel
	}
	if err := ValidateModelAccess(apiKey, warmupModel); err != nil {
		if appErr, ok := err.(*AppError); ok {
			envelope := OpenAIError(appErr.Code, appErr.Message, appErr.ErrorType)
			return WarmupResponse{}, &envelope, appErr.StatusCode, nil
		}
		return WarmupResponse{}, nil, http.StatusInternalServerError, err
	}

	accounts, usage, err := s.loadBalancer.WarmupAccounts(ctx, warmupModel, apiKey)
	if err != nil {
		return WarmupResponse{}, nil, http.StatusInternalServerError, err
	}
	response := WarmupResponse{
		Mode:          normalizedMode,
		TotalAccounts: len(accounts),
		Submitted:     []WarmupSubmittedAccount{},
		Skipped:       []WarmupSkippedAccount{},
		Failed:        []WarmupFailedAccount{},
	}
	if len(accounts) == 0 {
		return response, nil, http.StatusOK, nil
	}

	accountsToSubmit := make([]warmupAccount, 0, len(accounts))
	for _, account := range accounts {
		if normalizedMode == "force" || warmupUsageEligible(usage[account.selection.ID]) {
			accountsToSubmit = append(accountsToSubmit, account)
			continue
		}
		if normalizedMode == "strict" {
			envelope := OpenAIError("invalid_request_error", "strict warmup requires every target account to be usage-eligible", "invalid_request_error")
			return WarmupResponse{}, &envelope, http.StatusBadRequest, nil
		}
		response.Skipped = append(response.Skipped, WarmupSkippedAccount{
			AccountID: account.selection.ID,
			Reason:    warmupSkipIneligiblePrimary,
		})
	}

	results := s.submitWarmups(ctx, r, accountsToSubmit, apiKey, warmupModel)
	for _, result := range results {
		if result.success {
			response.Submitted = append(response.Submitted, WarmupSubmittedAccount{
				AccountID: result.accountID,
				RequestID: result.requestID,
				Model:     result.model,
			})
			continue
		}
		response.Failed = append(response.Failed, WarmupFailedAccount{
			AccountID:    result.accountID,
			ErrorCode:    result.errorCode,
			ErrorMessage: result.errorMessage,
		})
	}

	return response, nil, http.StatusOK, nil
}

func (s *Service) submitWarmups(ctx context.Context, r *http.Request, accounts []warmupAccount, apiKey *ApiKeyData, model string) []warmupSubmitResult {
	if len(accounts) == 0 {
		return nil
	}
	results := make([]warmupSubmitResult, len(accounts))
	sem := make(chan struct{}, warmupMaxConcurrentSubmits)
	var wg sync.WaitGroup
	for idx, account := range accounts {
		idx, account := idx, account
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			results[idx] = s.submitWarmup(ctx, r, account, apiKey, model)
		}()
	}
	wg.Wait()
	return results
}

func (s *Service) submitWarmup(ctx context.Context, r *http.Request, account warmupAccount, apiKey *ApiKeyData, model string) warmupSubmitResult {
	started := time.Now()
	requestID := uuid.NewString()
	headers := r.Header.Clone()
	headers.Del("X-Request-Id")
	headers.Del("Request-Id")
	headers.Set("X-Request-Id", requestID)
	payload := map[string]any{
		"model":        model,
		"instructions": "Warmup request.",
		"input":        "warmup",
		"store":        false,
	}
	result := warmupSubmitResult{
		accountID: account.selection.ID,
		requestID: requestID,
		model:     model,
	}

	reservation, err := s.reserveAPIKeyUsage(ctx, apiKey, model, payload)
	if err != nil {
		result.errorCode = "rate_limit_exceeded"
		result.errorMessage = err.Error()
		return result
	}
	upstreamResult, err := s.warmupUpstream(ctx, upstream.StreamOptions{
		BaseURL:        s.upstreamBaseURL,
		Payload:        payload,
		InboundHeaders: headers,
		AccessToken:    account.token,
		AccountID:      account.selection.ID,
		Transport:      upstream.TransportHTTP,
		Client:         s.upstreamClient,
	})
	latencyMS := time.Since(started).Milliseconds()
	status := "success"
	var envelope *OpenAIErrorEnvelope
	if err != nil {
		status = "error"
		envelopeValue := OpenAIError("upstream_error", err.Error(), "server_error")
		envelope = &envelopeValue
		result.errorCode = "upstream_error"
		result.errorMessage = err.Error()
		s.failAPIKeyReservation(reservation, model, upstreamResult, "")
	} else {
		if id, _ := upstreamResult["id"].(string); id != "" {
			result.requestID = id
		}
		result.success = true
		s.finalizeAPIKeyReservation(reservation, model, upstreamResult, "")
	}

	proxyAccount := &ProxyAccount{SelectionAccount: account.selection, AccessToken: account.token}
	s.logRequest(ctx, logRequestParams{
		requestID: result.requestID,
		model:     model,
		account:   proxyAccount,
		apiKey:    apiKey,
		status:    status,
		envelope:  envelope,
		result:    upstreamResult,
		latencyMS: latencyMS,
		userAgent: r.UserAgent(),
	})
	return result
}

func (s *Service) warmupUpstream(ctx context.Context, opts upstream.StreamOptions) (map[string]any, error) {
	call := s.warmupSubmitter
	if call == nil {
		call = collectUpstreamResponse
	}
	return call(ctx, opts)
}

func collectUpstreamResponse(ctx context.Context, opts upstream.StreamOptions) (map[string]any, error) {
	events, errs := upstream.OpenResponseStream(ctx, opts)
	var terminal map[string]any
	for event := range events {
		payload := ParseSSEDataJSON(event)
		if payload == nil {
			continue
		}
		eventType, _ := payload["type"].(string)
		if eventType == "response.completed" || eventType == "response.incomplete" {
			if response, ok := payload["response"].(map[string]any); ok {
				terminal = response
			}
		}
		if IsTerminalResponseEvent(payload) {
			break
		}
	}
	select {
	case err, ok := <-errs:
		if ok && err != nil {
			return terminal, err
		}
	default:
	}
	if terminal == nil {
		return nil, fmt.Errorf("upstream warmup ended without response")
	}
	return terminal, nil
}

func (lb *LoadBalancer) WarmupAccounts(ctx context.Context, model string, apiKey *ApiKeyData) ([]warmupAccount, map[string]*UsageEntry, error) {
	inputs, err := lb.loadSelectionInputs(ctx, model, nil)
	if err != nil {
		return nil, nil, err
	}
	targets := SelectableAccounts(inputs.Accounts)
	if apiKey != nil && apiKey.AccountAssignmentScopeEnabled {
		assigned := make(map[string]struct{}, len(apiKey.AssignedAccountIDs))
		for _, accountID := range apiKey.AssignedAccountIDs {
			if accountID != "" {
				assigned[accountID] = struct{}{}
			}
		}
		filtered := make([]SelectionAccount, 0, len(targets))
		for _, account := range targets {
			if _, ok := assigned[account.ID]; ok {
				filtered = append(filtered, account)
			}
		}
		targets = filtered
	}

	result := make([]warmupAccount, 0, len(targets))
	for _, target := range targets {
		record, err := lb.accountsRepo.GetProxyRecord(ctx, target.ID)
		if err != nil {
			return nil, nil, err
		}
		if record == nil {
			continue
		}
		token, err := lb.accountsRepo.DecryptAccessToken(lb.encryptor, *record)
		if err != nil {
			return nil, nil, err
		}
		result = append(result, warmupAccount{selection: target, token: token})
	}
	return result, inputs.LatestPrimary, nil
}

func warmupUsageEligible(entry *UsageEntry) bool {
	return entry != nil && entry.WindowMinutes != nil && *entry.WindowMinutes == 300 && entry.UsedPercent != nil && *entry.UsedPercent <= 0
}

func normalizeWarmupMode(mode string) string {
	if mode == "" {
		return "normal"
	}
	return strings.ToLower(strings.TrimSpace(mode))
}

func validWarmupMode(mode string) bool {
	return mode == "normal" || mode == "strict" || mode == "force"
}
